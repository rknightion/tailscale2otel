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
// the object store is only ever used when explicitly asked for.
func objectStoreSource(s string) bool { return s == "objectstore" }

// newObjectStoreCollector builds the objectstore ingestion collector for one
// runtime.
//
// Credentials come from config when supplied and from the ambient chain
// otherwise — the environment, then web identity (IRSA), then an EC2 instance
// profile. Static credentials are the last resort by design: a container
// deployment should be using a role, and the config fields exist for the
// MinIO/Ceph case where there is no role to use.
func newObjectStoreCollector(
	rt *tailnetRuntime,
	d runtimeDeps,
	onIngest func(source, signal string, records, bytes int),
	onAccepted ingest.AcceptedObserver,
) (*objectstore.Collector, error) {
	os := d.cfg.Collectors.Flowlogs.ObjectStore

	var creds s3.Provider
	if os.AccessKeyID != "" || os.SecretAccessKey != "" {
		creds = s3.StaticProvider{Credentials: s3.Credentials{
			AccessKeyID:     os.AccessKeyID.Reveal(),
			SecretAccessKey: os.SecretAccessKey.Reveal(),
			SessionToken:    os.SessionToken.Reveal(),
		}}
	} else {
		creds = s3.AmbientProvider(nil, nil)
	}

	client, err := s3.New(s3.Config{
		Endpoint:          os.Endpoint,
		Region:            os.Region,
		Bucket:            os.Bucket,
		PathStyle:         os.PathStyle,
		AllowInsecureHTTP: os.AllowInsecureHTTP,
		Credentials:       creds,
	})
	if err != nil {
		return nil, fmt.Errorf("build object-store client: %w", err)
	}

	legacyNamespace := ""
	tailnetScope := d.cfg.Tailscale.Tailnet
	if d.multi {
		legacyNamespace = rt.name
		tailnetScope = rt.name
	}

	return objectstore.New(client, objectstore.NewFlowSignal(rt.flowProc), d.store, objectstore.Options{
		Prefix:          os.Prefix,
		Interval:        os.Interval.D(),
		Lookback:        os.Lookback.D(),
		InitialLookback: os.InitialLookback.D(),
		MaxObjects:      os.MaxObjects,
		Logger:          d.logger,
		OnIngest:        onIngest,
		OnAccepted:      onAccepted,
		Scope: objectstore.CheckpointScope{
			Tailnet:  tailnetScope,
			Provider: "s3",
			Signal:   "flow",
			Feed:     objectstore.FeedID(os.Endpoint, os.Bucket, os.Prefix),
		},
		LegacyCheckpointNamespace: legacyNamespace,
	})
}

// ensure the config type stays referenced from here, so a rename of the config
// field is a compile error in this file rather than a silently unwired option.
var _ = config.ObjectStoreConfig{}
