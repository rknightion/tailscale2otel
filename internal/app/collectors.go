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
	"github.com/rknightion/tailscale2otel/internal/stream"
	"github.com/rknightion/tailscale2otel/internal/webhook"
)

func flowOptions(cfg *config.Config) flowlog.Options {
	return flowlog.Options{
		LogMode:      cfg.Collectors.Flowlogs.LogMode,
		IncludePorts: cfg.Cardinality.FlowIncludePorts,
		NodeDims:     cfg.Cardinality.FlowNodeDims,
		// collapse_external=true (the default) keeps unresolved/external addresses
		// bucketed as external/unknown; false preserves the raw IP on flow logs.
		KeepExternalAddrs: !cfg.Cardinality.CollapseExternal,
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
			c.Devices.CollectRoutes, c.Devices.CollectPosture), c.Devices.Interval.D())
	}
	if c.Users.Enabled {
		a.registry.Register(users.New(a.client, c.Users.Interval.D()), c.Users.Interval.D())
	}
	if c.Keys.Enabled {
		a.registry.Register(keys.New(a.client, c.Keys.Interval.D(), c.Keys.ExpiryWarn.D(), nil), c.Keys.Interval.D())
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
	if nm := c.NodeMetrics; nm.Enabled && len(nm.Targets) > 0 {
		a.registry.Register(nodemetrics.New(nodeMetricsOptions(nm)), nm.Interval.D())
	}
	if c.Flowlogs.Enabled && pollSource(c.Flowlogs.Source) {
		fc := flowlogs.New(a.client, a.flowProc, c.Flowlogs.Interval.D(), c.Flowlogs.Lag.D(), a.flowFeatureCheck())
		a.registry.RegisterWindow(fc, c.Flowlogs.Interval.D(), c.Flowlogs.InitialLookback.D(), c.Flowlogs.MaxWindow.D())
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
			Listen:      a.cfg.Streaming.Listen,
			Path:        a.cfg.Streaming.Path,
			Token:       a.cfg.Streaming.Token,
			Decompress:  a.cfg.Streaming.Decompress,
			TLSCertFile: a.cfg.Streaming.TLS.CertFile,
			TLSKeyFile:  a.cfg.Streaming.TLS.KeyFile,
		}, a.flowProc, a.auditProc, a.emitter, a.logger)
	}
	if a.cfg.Webhook.Enabled {
		a.webhookSrv = webhook.New(webhook.Options{
			Listen: a.cfg.Webhook.Listen,
			Path:   a.cfg.Webhook.Path,
			Secret: a.cfg.Webhook.Secret,
		}, a.emitter, a.logger)
	}
}
