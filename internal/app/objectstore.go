package app

import (
	"fmt"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/ingest"
	"github.com/rknightion/tailscale2otel/v3/internal/s3"
)

// objectStoreSource reports whether flow logs are ingested from Tailscale's
// export bucket rather than from the API or the stream receiver.
//
// Unlike pollSource this is exact, not a default: an empty source means poll, so
// the object store is only ever used when explicitly asked for. Source selection
// is global per signal — a runtime cannot pick a different ingestion path (#284).
func objectStoreSource(s string) bool { return s == "objectstore" }

func objectStoreOptions(cfg config.ObjectStoreConfig) objectstore.Options {
	return objectstore.Options{
		Prefix: cfg.Prefix,
		// config resolves an unset layout to "partitioned" in FlowObjectStore, and
		// objectstore.New treats an empty Layout the same way, so a caller that
		// bypassed that seam still gets the safe default rather than a refusal.
		Layout:                     objectstore.Layout(cfg.Layout),
		Interval:                   cfg.Interval.D(),
		Lookback:                   cfg.Lookback.D(),
		InitialLookback:            cfg.InitialLookback.D(),
		MaxObjects:                 cfg.MaxObjects,
		MaxObjectWireBytes:         cfg.MaxObjectWireBytes,
		MaxObjectDecompressedBytes: cfg.MaxObjectDecompressedBytes,
		MaxObjectRecords:           cfg.MaxObjectRecords,
		MaxCycleWireBytes:          cfg.MaxCycleWireBytes,
		MaxCycleDecompressedBytes:  cfg.MaxCycleDecompressedBytes,
		MaxCycleRecords:            cfg.MaxCycleRecords,
	}
}

// objectStoreTailnetIdentity is the runtime's LITERAL configured tailnet name:
// the tailnets[] entry's name, or tailscale.tailnet in single mode — including
// the "-" placeholder even when New() resolved a display name from it.
//
// It is deliberately not rt.name (the resolved display/telemetry name). This
// value keys the durable checkpoint namespace, so taking the resolved name would
// silently move a runtime's state the first time a name lookup succeeded, or
// back again when it failed (#284, #453).
func objectStoreTailnetIdentity(rt *tailnetRuntime, cfg *config.Config) string {
	if rt.configuredName != "" {
		return rt.configuredName
	}
	return cfg.Tailscale.Tailnet
}

// objectStoreFeed describes one signal's object-store ingestion: where its
// destination comes from and what turns an object into emitted telemetry.
//
// It exists so the wiring below is written once for both signals. signal is the
// FROZEN checkpoint-namespace segment (#453) — changing a value orphans every
// deployment's durable state for that signal.
type objectStoreFeed struct {
	signal string
	// entryKey is the config path a destination is read from in multi-tailnet
	// mode, used only to make the "no destination" error name the right key.
	entryKey  string
	globalKey string
	resolve   func(*config.Config, string) (config.ObjectStoreConfig, bool)
	// processor adapts the runtime's shared processor for this signal. It is the
	// only place a signal's record type enters the object-store path.
	processor func(*tailnetRuntime) objectstore.SignalProcessor
}

var objectStoreFlowFeed = objectStoreFeed{
	signal:    "flow",
	entryKey:  "objectstore.flow",
	globalKey: "collectors.flowlogs.objectstore",
	resolve: func(c *config.Config, tailnet string) (config.ObjectStoreConfig, bool) {
		return c.FlowObjectStore(tailnet)
	},
	processor: func(rt *tailnetRuntime) objectstore.SignalProcessor {
		return objectstore.NewFlowSignal(rt.flowProc)
	},
}

var objectStoreAuditFeed = objectStoreFeed{
	signal:    "audit",
	entryKey:  "objectstore.audit",
	globalKey: "collectors.auditlogs.objectstore",
	resolve: func(c *config.Config, tailnet string) (config.ObjectStoreConfig, bool) {
		return c.AuditObjectStore(tailnet)
	},
	processor: func(rt *tailnetRuntime) objectstore.SignalProcessor {
		return objectstore.NewAuditSignal(rt.auditProc)
	},
}

// objectStoreDestinationFor resolves the one object-store destination this
// runtime reads for a signal, refusing rather than falling back when there is
// none.
//
// The dispatch is on the CONFIG shape (a tailnets: list) and not on d.multi (>1
// runtime), so a one-entry list still takes its destination from the entry —
// exactly as config.Validate demands. Every value the S3 client needs, including
// the credentials, comes from the returned struct, so no other tailnet's
// material is in reach while this runtime is being built.
func objectStoreDestinationFor(
	feed objectStoreFeed,
	rt *tailnetRuntime,
	d runtimeDeps,
) (config.ObjectStoreConfig, error) {
	tailnet := objectStoreTailnetIdentity(rt, d.cfg)
	dest, ok := feed.resolve(d.cfg, tailnet)
	if !ok {
		return config.ObjectStoreConfig{}, fmt.Errorf(
			"tailnet %q has no %s destination of its own; a destination is never "+
				"inherited from %s in multi-tailnet mode", tailnet, feed.entryKey, feed.globalKey)
	}
	return dest, nil
}

// objectStoreScope is the durable checkpoint identity for one runtime's feed of
// one signal. The namespace it produces is frozen (#453):
// objectstore/v1/<base64url-tailnet>/<provider>/<signal>/<feed-digest>/ — the
// feed digest is a one-way function of endpoint, bucket and prefix, so no
// operator-controlled value enters a checkpoint path.
//
// signal comes from the feed spec and must stay "flow" for the flow signal: it
// is the segment every existing deployment's flow state already lives under.
func objectStoreScope(signal, tailnet string, dest config.ObjectStoreConfig) objectstore.CheckpointScope {
	return objectstore.CheckpointScope{
		Tailnet:  tailnet,
		Provider: "s3",
		Signal:   signal,
		Feed:     objectstore.FeedID(dest.Endpoint, dest.Bucket, dest.Prefix),
	}
}

// registerObjectStoreCollector wires one runtime's object-store ingestion onto
// its registry, on the interval of ITS destination.
//
// A failure is logged, not fatal: the rest of the exporter is still useful, and
// a bucket that cannot be reached at startup is usually a credential or endpoint
// problem an operator fixes without a restart being the only recovery. Every
// structural fault — an incomplete or unusable destination, a tailnet with no
// destination of its own, an unreadable credential file — is already a
// config.Validate/Load error, so startup fails there instead of leaving one
// runtime silently mute.
func registerObjectStoreCollector(
	feed objectStoreFeed,
	rt *tailnetRuntime,
	d runtimeDeps,
	onIngest func(source, signal string, records, bytes int),
	onAccepted ingest.AcceptedObserver,
) {
	dest, err := objectStoreDestinationFor(feed, rt, d)
	if err == nil {
		var oc *objectstore.Collector
		oc, err = newObjectStoreCollector(feed, rt, d, dest, onIngest, onAccepted)
		if err == nil {
			rt.registry.Register(oc, dest.Interval.D())
			return
		}
	}
	d.logger.Error("objectstore ingestion is not running",
		"signal", feed.signal, "tailnet", objectStoreTailnetIdentity(rt, d.cfg), "error", err)
}

// newObjectStoreCollector builds the objectstore ingestion collector for one
// runtime, from the destination that runtime resolved.
//
// Credentials come from dest when supplied and from the ambient chain otherwise
// — the environment, then web identity (IRSA), then an EC2 instance profile.
// Static credentials are the last resort by design: a container deployment
// should be using a role, and the config fields exist for the MinIO/Ceph case
// where there is no role to use. dest is the ONLY place they are read from, and
// it holds one tailnet's material, so a credential cannot cross runtimes.
func newObjectStoreCollector(
	feed objectStoreFeed,
	rt *tailnetRuntime,
	d runtimeDeps,
	dest config.ObjectStoreConfig,
	onIngest func(source, signal string, records, bytes int),
	onAccepted ingest.AcceptedObserver,
) (*objectstore.Collector, error) {
	var creds s3.Provider
	if dest.AccessKeyID != "" || dest.SecretAccessKey != "" {
		creds = s3.StaticProvider{Credentials: s3.Credentials{
			AccessKeyID:     dest.AccessKeyID.Reveal(),
			SecretAccessKey: dest.SecretAccessKey.Reveal(),
			SessionToken:    dest.SessionToken.Reveal(),
		}}
	} else {
		creds = s3.AmbientProvider(nil, nil)
	}

	client, err := s3.New(s3.Config{
		Endpoint:          dest.Endpoint,
		Region:            dest.Region,
		Bucket:            dest.Bucket,
		PathStyle:         dest.PathStyle,
		AllowInsecureHTTP: dest.AllowInsecureHTTP,
		Credentials:       creds,
	})
	if err != nil {
		// config.Validate already rejects every structural fault s3.New can report
		// (missing endpoint/region/bucket, an unusable endpoint URL, plaintext
		// remote without the override), so reaching this is a wiring bug rather
		// than a misconfiguration.
		return nil, fmt.Errorf("build object-store client: %w", err)
	}

	opts := objectStoreOptions(dest)
	opts.Logger = d.logger
	opts.OnIngest = onIngest
	opts.OnAccepted = onAccepted
	opts.Scope = objectStoreScope(feed.signal, objectStoreTailnetIdentity(rt, d.cfg), dest)
	// The pre-v1 layout is flow-only; objectstore.New ignores this for any other
	// signal, so it is safe (and simpler) to supply the namespace unconditionally.
	if d.multi {
		opts.LegacyCheckpointNamespace = rt.name
	}

	return objectstore.New(client, feed.processor(rt), d.store, opts)
}

// ensure the config type stays referenced from here, so a rename of the config
// field is a compile error in this file rather than a silently unwired option.
var _ = config.ObjectStoreConfig{}
