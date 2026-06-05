package app

import (
	"context"

	"github.com/rknightion/tailscale2otel/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/internal/collector/auditlogs"
	"github.com/rknightion/tailscale2otel/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/internal/collector/dns"
	"github.com/rknightion/tailscale2otel/internal/collector/flowlogs"
	"github.com/rknightion/tailscale2otel/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/collector/settings"
	"github.com/rknightion/tailscale2otel/internal/collector/users"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/rdns"
	"github.com/rknightion/tailscale2otel/internal/stream"
	"github.com/rknightion/tailscale2otel/internal/webhook"
)

func flowOptions(cfg *config.Config) flowlog.Options {
	return flowlog.Options{
		LogMode: cfg.Collectors.Flowlogs.LogMode,
		// flow_include_ports is the legacy "both ports" toggle; OR it with the
		// independent flow_source_port / flow_destination_port so old configs keep
		// emitting both ports while new ones can drop either side.
		IncludeSourcePort:         cfg.Cardinality.FlowIncludePorts || cfg.Cardinality.FlowSourcePort,
		IncludeDestinationPort:    cfg.Cardinality.FlowIncludePorts || cfg.Cardinality.FlowDestinationPort,
		IncludeDestinationService: cfg.Cardinality.FlowDestinationService,
		NodeDims:                  cfg.Cardinality.FlowNodeDims,
		// collapse_external=true (the default) buckets unresolved/external addresses
		// as external/unknown; false preserves the raw IP. This affects BOTH flow LOGS
		// and, when flow_node_dims is true, the flow METRIC attrs tailscale.src.node /
		// tailscale.dst.node (srcNode/dstNode come from the processor's resolve()).
		KeepExternalAddrs:      !cfg.Cardinality.CollapseExternal,
		MaxLogRecordsPerWindow: cfg.Collectors.Flowlogs.MaxLogRecordsPerWindow,
		// Default "rollup": emit the bounded top-N *.rollup families instead of the
		// raw per-connection io/packets. FlushRollup (runRollupFlusher) drains the
		// accumulator on the OTLP export interval.
		FlowMetricsMode: cfg.Cardinality.FlowMetricsMode,
		RollupTopN:      cfg.Collectors.Flowlogs.FlowRollupTopN,
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
func (a *App) flowFeatureCheck() flowlogs.FeatureCheck {
	return func(ctx context.Context) (bool, error) {
		s, err := a.client.TailnetSettings(ctx)
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

// registerCollectors registers the enabled collectors on the registry based on
// a.cfg. Window collectors (flowlogs/auditlogs) only poll when their source
// includes "poll"; the shared processors are reused by the stream receiver.
func (a *App) registerCollectors() {
	c := &a.cfg.Collectors

	if c.Devices.Enabled {
		a.registry.Register(devices.New(a.client, a.cache, c.Devices.Interval.D(),
			c.Devices.CollectRoutes, c.Devices.CollectPosture,
			devices.WithPerEntity(a.cfg.Cardinality.DevicePerEntity),
			devices.WithPostureLogMode(c.Devices.PostureLogMode)), c.Devices.Interval.D())
	}
	if c.Users.Enabled {
		a.registry.Register(users.New(a.client, c.Users.Interval.D(),
			users.WithPerEntity(a.cfg.Cardinality.UserPerEntity)), c.Users.Interval.D())
	}
	if c.Keys.Enabled {
		a.registry.Register(keys.New(a.client, c.Keys.Interval.D(), c.Keys.ExpiryWarn.D(), nil,
			keys.WithPerEntity(a.cfg.Cardinality.KeyPerEntity)), c.Keys.Interval.D())
	}
	if c.Settings.Enabled {
		a.registry.Register(settings.New(a.client, c.Settings.Interval.D()), c.Settings.Interval.D())
	}
	if c.Acl.Enabled {
		a.registry.Register(acl.New(a.client, c.Acl.Interval.D(), nil), c.Acl.Interval.D())
	}
	if c.Dns.Enabled {
		a.registry.Register(dns.New(a.client, c.Dns.Interval.D()), c.Dns.Interval.D())
	}
	if nm := c.NodeMetrics; nm.Enabled && (len(nm.Targets) > 0 || nm.Discovery.Enabled) {
		// Keep a typed reference so the status page can surface discovered nodes.
		a.nodeMetrics = nodemetrics.New(nodeMetricsOptions(nm, a.client, a.logger))
		a.registry.Register(a.nodeMetrics, nm.Interval.D())
	}
	if c.Flowlogs.Enabled && pollSource(c.Flowlogs.Source) {
		fc := flowlogs.New(a.client, a.flowProc, c.Flowlogs.Interval.D(), c.Flowlogs.Lag.D(), a.flowFeatureCheck())
		a.registry.RegisterWindow(fc, c.Flowlogs.Interval.D(), c.Flowlogs.InitialLookback.D(), c.Flowlogs.MaxWindow.D())
	} else if c.Flowlogs.Enabled {
		// Stream-only (source: stream): the poller isn't registered, so the
		// tailscale.feature.enabled health gauge it normally emits would be missing.
		// Register a lightweight probe that reports it independently of ingestion.
		fp := flowlogs.NewFeatureProbe(a.flowFeatureCheck(), c.Flowlogs.Interval.D())
		a.registry.Register(fp, fp.DefaultInterval())
	}
	if c.Auditlogs.Enabled && pollSource(c.Auditlogs.Source) {
		ac := auditlogs.New(a.client, a.auditProc, c.Auditlogs.Interval.D(), c.Auditlogs.Lag.D())
		a.registry.RegisterWindow(ac, c.Auditlogs.Interval.D(), c.Auditlogs.InitialLookback.D(), c.Auditlogs.MaxWindow.D())
	}
}

// buildReceivers constructs the optional HTTP receivers (off by default).
func (a *App) buildReceivers() {
	if a.cfg.Streaming.Enabled {
		a.streamSrv = stream.New(stream.Options{
			Listen:       a.cfg.Streaming.Listen,
			Path:         a.cfg.Streaming.Path,
			Token:        a.cfg.Streaming.Token.Reveal(),
			Decompress:   a.cfg.Streaming.Decompress,
			TLSCertFile:  a.cfg.Streaming.TLS.CertFile,
			TLSKeyFile:   a.cfg.Streaming.TLS.KeyFile,
			MaxBodyBytes: a.cfg.Streaming.MaxBodyBytes,
		}, a.flowProc, a.auditProc, a.emitter, a.logger)
	}
	if a.cfg.Webhook.Enabled {
		var wopts []webhook.Option
		if a.webhookDedup != nil {
			wopts = append(wopts, webhook.WithDedup(a.webhookDedup))
		}
		a.webhookSrv = webhook.New(webhookOptions(a.cfg.Webhook), a.emitter, a.logger, wopts...)
	}
}

// webhookOptions maps the webhook config block to the receiver's Options. Kept as
// a pure function so the config->Options wiring (notably the replay Tolerance) is
// unit-tested rather than only exercised end-to-end.
func webhookOptions(c config.WebhookConfig) webhook.Options {
	return webhook.Options{
		Listen:    c.Listen,
		Path:      c.Path,
		Secret:    c.Secret.Reveal(),
		Tolerance: c.Tolerance.D(),
	}
}
