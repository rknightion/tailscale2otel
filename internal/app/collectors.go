package app

import (
	"context"

	"github.com/rknightion/tailscale2otel/v2/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/auditlogs"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/contacts"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/dns"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/flowlogs"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/logstream"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/postureintegrations"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/services"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/settings"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/users"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/webhooks"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/rdns"
	"github.com/rknightion/tailscale2otel/v2/internal/stream"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v2/internal/webhook"
)

func flowOptions(cfg *config.Config) flowlog.Options {
	return flowlog.Options{
		LogMode: cfg.Collectors.Flowlogs.LogMode,
		// cardinality.flow.source_port / cardinality.flow.destination_port independently add source.port /
		// destination.port to the raw flow metric families (both default off).
		IncludeSourcePort:         cfg.Cardinality.Flow.SourcePort,
		IncludeDestinationPort:    cfg.Cardinality.Flow.DestinationPort,
		IncludeDestinationService: cfg.Cardinality.Flow.DestinationService,
		NodeDims:                  cfg.Cardinality.Flow.NodeDims,
		// cardinality.flow.collapse_external=true (the default) buckets unresolved/external addresses
		// as external/unknown; false preserves the raw IP. This affects BOTH flow LOGS
		// and, when cardinality.flow.node_dims is true, the flow METRIC attrs tailscale.src.node /
		// tailscale.dst.node (srcNode/dstNode come from the processor's resolve()).
		KeepExternalAddrs:      !cfg.Cardinality.Flow.CollapseExternal,
		MaxLogRecordsPerWindow: cfg.Collectors.Flowlogs.MaxLogRecordsPerWindow,
		// Default "rollup": emit the bounded top-N *.rollup families instead of the
		// raw per-connection io/packets. FlushRollup (runRollupFlusher) drains the
		// accumulator on the OTLP export interval.
		FlowMetricsMode: cfg.Cardinality.Flow.MetricsMode,
		RollupTopN:      cfg.Cardinality.Flow.RollupTopN,
		// cardinality.flow.exit_node_attribution (default true) emits the bounded
		// tailscale.exit_node.io/packets counters attributing exit traffic to the
		// relaying node; independent of FlowMetricsMode.
		ExitNodeAttribution: cfg.Cardinality.Flow.ExitNodeAttribution,
	}
}

// rdnsOptions maps the reverse-DNS enrichment config into rdns.Cache options.
// Only called when enrichment.reverse_dns.enabled is true.
func rdnsOptions(cfg *config.Config) rdns.Options {
	rd := cfg.Enrichment.ReverseDNS
	return rdns.Options{
		Server:      rd.Server,
		Timeout:     rd.Timeout.D(),
		TTL:         rd.CacheTTL.D(),
		NegativeTTL: rd.NegativeTTL.D(),
		MaxEntries:  rd.MaxEntries,
	}
}

// flowFeatureCheck reports whether network flow logging is enabled on the
// tailnet, so the flowlogs collector can self-disable (emit feature.enabled=0
// and idle) instead of error-spamming when the feature is off or plan-gated.
func flowFeatureCheck(client *tsapi.Client) flowlogs.FeatureCheck {
	return func(ctx context.Context) (bool, error) {
		s, err := client.TailnetSettings(ctx)
		if err != nil {
			return false, err
		}
		return s.NetworkFlowLoggingOn, nil
	}
}

// pollSource reports whether a collector configured with the given source value
// should run as a poller (as opposed to relying solely on the stream receiver).
func pollSource(s string) bool {
	return s == "" || s == "poll" || s == "both"
}

// registerCollectors registers the enabled collectors on the runtime's registry.
// Each runtime has its own provider/client/emitter/cache (one per tailnet).
// Window collectors (flowlogs/auditlogs) only poll when their source includes
// "poll"; the runtime's processors are reused by the stream receiver (single
// tailnet only). a.client (settings/dns/contacts/etc.) are Tailscale-only APIs
// gated off for Headscale by the capability set, so rt.client is safe there.
func registerCollectors(rt *tailnetRuntime, d runtimeDeps) {
	cfg := d.cfg
	c := &cfg.Collectors
	cp := rt.cp
	logger := d.logger
	ingest := ingestObserver(rt.emitter, cfg.SelfObservability.Enabled)

	if c.Devices.Enabled && cp.Supports("devices") {
		devOpts := []devices.Option{
			devices.WithPerEntity(cfg.Cardinality.PerEntity.Device),
			devices.WithPostureLogMode(c.Devices.PostureLogMode),
			devices.WithAttributeNamespaces(c.Devices.AttributeNamespaces),
			devices.WithDeviceInvites(c.Devices.CollectDeviceInvites),
			devices.WithDerpRegionRollup(cfg.Cardinality.DerpRegionRollup),
			devices.WithTagRollup(c.Devices.CollectTagRollup, c.Devices.TagRollupLimit),
			devices.WithConnectivity(c.Devices.CollectConnectivity),
			devices.WithSubnetRouteRollup(cfg.Cardinality.SubnetRouteRollup),
		}
		if d.tsRelease != nil {
			devOpts = append(devOpts, devices.WithUpstreamLatest(
				d.tsRelease.Latest, cfg.VersionChecks.Devices.OutdatedMinorThreshold))
		}
		// Headscale provides no update-available or ephemeral signal, so suppress
		// those gauges instead of emitting a fabricated 0/false for every node (#64).
		if cfg.Provider == "headscale" {
			devOpts = append(devOpts,
				devices.WithUpdateAvailableData(false),
				devices.WithEphemeralData(false),
				// Headscale reports neither multipleConnections nor
				// blocksIncomingConnections; suppress rather than fabricate 0s.
				// The posture-identity gauge self-gates on wire presence.
				devices.WithMultipleConnectionsData(false),
				devices.WithBlocksIncomingConnectionsData(false))
		}
		rt.registry.Register(devices.New(cp.Client, rt.cache, c.Devices.Interval.D(),
			c.Devices.CollectRoutes, c.Devices.CollectPosture, devOpts...), c.Devices.Interval.D())
	}
	if c.Users.Enabled && cp.Supports("users") {
		userOpts := []users.Option{users.WithPerEntity(cfg.Cardinality.PerEntity.User)}
		// Headscale has no per-user device-count / currently-connected data, so
		// suppress those gauges rather than emit fabricated zeros for every user (#64).
		if cfg.Provider == "headscale" {
			userOpts = append(userOpts, users.WithActivityData(false))
		}
		rt.registry.Register(users.New(cp.Client, c.Users.Interval.D(), userOpts...), c.Users.Interval.D())
	}
	if c.Keys.Enabled && cp.Supports("keys") {
		rt.registry.Register(keys.New(cp.Client, c.Keys.Interval.D(), c.Keys.ExpiryWarn.D(), nil,
			keys.WithPerEntity(cfg.Cardinality.PerEntity.Key)), c.Keys.Interval.D())
	}
	if c.Settings.Enabled && cp.Supports("settings") {
		rt.registry.Register(settings.New(rt.client, c.Settings.Interval.D()), c.Settings.Interval.D())
	}
	if c.Acl.Enabled && cp.Supports("acl") {
		rt.registry.Register(acl.New(cp.Client, c.Acl.Interval.D(), nil), c.Acl.Interval.D())
	}
	if c.Dns.Enabled && cp.Supports("dns") {
		rt.registry.Register(dns.New(rt.client, c.Dns.Interval.D()), c.Dns.Interval.D())
	}
	if c.Contacts.Enabled && cp.Supports("contacts") {
		rt.registry.Register(contacts.New(rt.client, c.Contacts.Interval.D()), c.Contacts.Interval.D())
	}
	if c.Webhooks.Enabled && cp.Supports("webhooks") {
		rt.registry.Register(webhooks.New(rt.client, c.Webhooks.Interval.D(),
			webhooks.WithPerEntity(cfg.Cardinality.PerEntity.Webhook)), c.Webhooks.Interval.D())
	}
	if c.PostureIntegrations.Enabled && cp.Supports("posture_integrations") {
		rt.registry.Register(postureintegrations.New(rt.client, c.PostureIntegrations.Interval.D()), c.PostureIntegrations.Interval.D())
	}
	if c.LogStream.Enabled && cp.Supports("log_stream") {
		rt.registry.Register(logstream.New(rt.client, c.LogStream.Interval.D()), c.LogStream.Interval.D())
	}
	if c.OAuthApps.Enabled && cp.Supports("oauth_apps") {
		rt.registry.Register(oauthapps.New(rt.client, c.OAuthApps.Interval.D()), c.OAuthApps.Interval.D())
	}
	if c.Services.Enabled && cp.Supports("services") {
		rt.registry.Register(services.New(rt.client, c.Services.Interval.D(),
			services.WithPerEntity(cfg.Cardinality.PerEntity.Service),
			services.WithCollectHosts(c.Services.CollectHosts),
			// Feed the service-VIP -> name map so flow logs resolve a service
			// VIP peer to its service name instead of "unknown" (#166).
			services.WithEnrichCache(rt.cache)), c.Services.Interval.D())
	}
	if nm := c.NodeMetrics; nm.Enabled && cp.Supports("nodemetrics") {
		// Static node_metrics targets are process-global (a shared jump host, not a
		// per-tailnet resource). In multi-tailnet mode register them on the FIRST
		// runtime only, so N tailnets don't each scrape + re-emit the same target
		// N times (2x tailscaled load, duplicated/mis-attributed samples) — #59.
		// Per-tailnet discovery still runs on every runtime.
		if d.multi && !d.primary {
			nm.Targets = nil
		}
		if len(nm.Targets) > 0 || nm.Discovery.Enabled {
			// Keep a typed reference so the status page can surface discovered nodes.
			// Discovery uses the provider client's DevicesRich, so it works for both
			// backends (Headscale nodes also run tailscaled on :5252).
			rt.nodeMetrics = nodemetrics.New(nodeMetricsOptions(nm, cp.Client, rt.cache, withComponent(logger, compNodeMetrics)))
			rt.registry.Register(rt.nodeMetrics, nm.Interval.D())
		}
	}
	if c.Flowlogs.Enabled && cp.Supports("flowlogs") && pollSource(c.Flowlogs.Source) {
		fc := flowlogs.New(rt.client, rt.flowProc, c.Flowlogs.Interval.D(), c.Flowlogs.Lag.D(), flowFeatureCheck(rt.client), ingest)
		rt.registry.RegisterWindow(fc, c.Flowlogs.Interval.D(), c.Flowlogs.InitialLookback.D(), c.Flowlogs.MaxWindow.D())
	} else if c.Flowlogs.Enabled && cp.Supports("flowlogs") {
		// Stream-only (source: stream): the poller isn't registered, so the
		// tailscale.feature.enabled health gauge it normally emits would be missing.
		// Register a lightweight probe that reports it independently of ingestion.
		fp := flowlogs.NewFeatureProbe(flowFeatureCheck(rt.client), c.Flowlogs.Interval.D())
		rt.registry.Register(fp, fp.DefaultInterval())
	}
	if c.Auditlogs.Enabled && cp.Supports("auditlogs") && pollSource(c.Auditlogs.Source) {
		ac := auditlogs.New(rt.client, rt.auditProc, c.Auditlogs.Interval.D(), c.Auditlogs.Lag.D(), ingest)
		rt.registry.RegisterWindow(ac, c.Auditlogs.Interval.D(), c.Auditlogs.InitialLookback.D(), c.Auditlogs.MaxWindow.D())
	}
}

// buildReceivers constructs the optional HTTP receivers (off by default). The
// receivers feed the FIRST runtime's processors/emitter: config validation
// guarantees streaming/webhook are only enabled in single-tailnet mode, so
// runtimes[0] is the sole tailnet.
func (a *App) buildReceivers() {
	rt := a.runtimes[0]
	ingest := ingestObserver(rt.emitter, a.cfg.SelfObservability.Enabled)
	if a.cfg.Streaming.Enabled {
		a.streamSrv = stream.New(stream.Options{
			Listen:       a.cfg.Streaming.Listen,
			Path:         a.cfg.Streaming.Path,
			Token:        a.cfg.Streaming.Token.Reveal(),
			Decompress:   a.cfg.Streaming.Decompress,
			TLSCertFile:  a.cfg.Streaming.TLS.CertFile,
			TLSKeyFile:   a.cfg.Streaming.TLS.KeyFile,
			MaxBodyBytes: a.cfg.Streaming.MaxBodyBytes,
			// Aggregate admission control (#209): MaxBodyBytes bounds one body,
			// this bounds how many are buffered at once.
			MaxConcurrentRequests: a.cfg.Streaming.MaxConcurrentRequests,
			OnIngest:              ingest,
		}, rt.flowProc, rt.auditProc, rt.emitter, withComponent(a.logger, appcatalog.ComponentStream),
			stream.WithTracer(a.tracer))
	}
	if a.cfg.Webhook.Enabled {
		var wopts []webhook.Option
		if a.webhookDedup != nil {
			wopts = append(wopts, webhook.WithDedup(a.webhookDedup))
		}
		wopts = append(wopts, webhook.WithTracer(a.tracer))
		wh := webhookOptions(a.cfg.Webhook)
		wh.OnIngest = ingest
		a.webhookSrv = webhook.New(wh, rt.emitter, withComponent(a.logger, appcatalog.ComponentWebhook), wopts...)
	}
}

// webhookOptions maps the webhook config block to the receiver's Options. Kept as
// a pure function so the config->Options wiring (notably the replay Tolerance) is
// unit-tested rather than only exercised end-to-end.
func webhookOptions(c config.WebhookConfig) webhook.Options {
	return webhook.Options{
		Listen:       c.Listen,
		Path:         c.Path,
		Secret:       c.Secret.Reveal(),
		Tolerance:    c.Tolerance.D(),
		MaxBodyBytes: c.MaxBodyBytes,
	}
}
