package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/auditlogs"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/contacts"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/dns"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/flowlogs"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/logstream"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/pam"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/postureintegrations"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/services"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/settings"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/users"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/webhooks"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/dedup"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/rdns"
	"github.com/rknightion/tailscale2otel/v5/internal/stream"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v5/internal/webhook"
)

func flowOptions(cfg *config.Config) flowlog.Options {
	return flowlog.Options{
		LogMode: cfg.Collectors.Flowlogs.LogMode,
		// cardinality.flow.source_port / cardinality.flow.destination_port independently add source.port /
		// destination.port to the raw flow metric families (both default off).
		IncludeSourcePort:      cfg.Cardinality.Flow.SourcePort,
		IncludeDestinationPort: cfg.Cardinality.Flow.DestinationPort,
		NodeDims:               cfg.Cardinality.Flow.NodeDims,
		// cardinality.flow.identity_dims (default off) adds the per-flow user/tags/os
		// carried in the record's own srcNode/dstNodes blocks to flow METRICS. Flow
		// LOGS carry them regardless.
		IdentityDims: cfg.Cardinality.Flow.IdentityDims,
		// cardinality.flow.collapse_external=true (the default) buckets unresolved/external addresses
		// as external/unknown; false preserves the raw IP. This affects BOTH flow LOGS
		// and, when cardinality.flow.node_dims is true, the flow METRIC attrs tailscale.src.node /
		// tailscale.dst.node (srcNode/dstNode come from the processor's resolve()).
		KeepExternalAddrs:      !cfg.Cardinality.Flow.CollapseExternal,
		MaxLogRecordsPerWindow: cfg.Collectors.Flowlogs.MaxLogRecordsPerWindow,
		TrustedReporterNodeIDs: cfg.Collectors.Flowlogs.TrustedReporterNodeIDs,
		TrustedReporterTags:    cfg.Collectors.Flowlogs.TrustedReporterTags,
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
func rdnsOptions(cfg *config.Config, checkpointPath string) rdns.Options {
	rd := cfg.Enrichment.ReverseDNS
	return rdns.Options{
		Server:       rd.Server,
		Timeout:      rd.Timeout.D(),
		TTL:          rd.CacheTTL.D(),
		NegativeTTL:  rd.NegativeTTL.D(),
		StaleTTL:     rd.StaleTTL.D(),
		MaxEntries:   rd.MaxEntries,
		SnapshotPath: rdns.SnapshotPath(checkpointPath),
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

func aclSnapshotStatePath(evidencePath, tailnet string) string {
	ext := filepath.Ext(evidencePath)
	base := strings.TrimSuffix(filepath.Base(evidencePath), ext)
	sum := sha256.Sum256([]byte(tailnet))
	suffix := hex.EncodeToString(sum[:6])
	return filepath.Join(filepath.Dir(evidencePath), base+".acl-policy-"+suffix+ext)
}

func aclSnapshotStateStore(evidencePath, tailnet string) aclpolicy.SnapshotStateStore {
	if evidencePath == "" {
		return aclpolicy.NewMemorySnapshotStateStore()
	}
	return aclpolicy.NewFileSnapshotStateStore(aclSnapshotStatePath(evidencePath, tailnet))
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
	onIngest := ingestObserver(rt.emitter, cfg.SelfObservability.Enabled)
	onAccepted := acceptedEventObserver(rt.emitter, cfg.SelfObservability.Enabled)

	if c.Devices.Enabled && cp.Supports("devices") {
		postureChecks := make([]devices.PostureComplianceCheck, 0, len(c.Devices.PostureComplianceChecks))
		for _, check := range c.Devices.PostureComplianceChecks {
			postureChecks = append(postureChecks, devices.PostureComplianceCheck{
				Name:      check.Name,
				Attribute: check.Attribute,
				Equals:    check.Equals,
			})
		}
		devOpts := []devices.Option{
			devices.WithPerEntity(cfg.Cardinality.PerEntity.Device),
			devices.WithChangeLog(c.Devices.ChangeLogEnabled),
			devices.WithPostureLogMode(c.Devices.PostureLogMode),
			devices.WithExpiryLogMode(c.Devices.ExpiryLogMode),
			devices.WithAttributeNamespaces(c.Devices.AttributeNamespaces),
			devices.WithAttributeLimits(c.Devices.AttributeKeyLimit, c.Devices.AttributeValueLimit),
			devices.WithPostureComplianceChecks(postureChecks),
			devices.WithDeviceInvites(c.Devices.CollectDeviceInvites),
			devices.WithDerpRegionRollup(cfg.Cardinality.DerpRegionRollup),
			devices.WithTagRollup(c.Devices.CollectTagRollup, c.Devices.TagRollupLimit),
			devices.WithConnectivity(c.Devices.CollectConnectivity),
			devices.WithSubnetRouteRollup(cfg.Cardinality.SubnetRouteRollup),
			// Per-device posture/invite subrequests can fail individually while the
			// enclosing tick still reports a clean scrape. The coverage tally is what
			// makes that partial blindness visible (#421).
			devices.WithCoverage(rt.coverage),
			// Coverage and APIState are NOT the same signal, which is exactly how
			// this collector stayed dark: it had the coverage tally from #421 but
			// never recorded a tracker entry, so all three of its capability-matrix
			// rows read `unknown` while it succeeded every 60s (#524).
			devices.WithAPIState(rt.apiState),
		}
		if d.geoDB != nil {
			// Fleet geography (tailscale.devices.by_country), derived from each
			// device's public magicsock endpoint. Bounded by country count; the
			// per-device gauges are deliberately left untouched.
			devOpts = append(devOpts, devices.WithGeo(d.geoDB))
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
				devices.WithBlocksIncomingConnectionsData(false),
				// hsapi hard-codes both source fields false, so without these a
				// Headscale build would publish a confident "SSH enabled nowhere /
				// key expiry disabled nowhere" (#414). Absence, not a false zero.
				devices.WithSSHData(false),
				devices.WithKeyExpiryDisabledData(false))
		}
		rt.registry.Register(devices.New(cp.Client, rt.cache, c.Devices.Interval.D(),
			c.Devices.CollectRoutes, c.Devices.CollectPosture,
			append(devOpts, devices.WithSubrequestConcurrency(c.Devices.SubrequestConcurrency))...),
			c.Devices.Interval.D())
	}
	if c.Users.Enabled && cp.Supports("users") {
		userOpts := []users.Option{users.WithPerEntity(cfg.Cardinality.PerEntity.User)}
		// Headscale has no per-user device-count / currently-connected data, so
		// suppress those gauges rather than emit fabricated zeros for every user (#64).
		if cfg.Provider == "headscale" {
			userOpts = append(userOpts, users.WithActivityData(false))
		}
		// The ACL evaluator needs to know which login holds which tailnet role,
		// which only this collector reports. Without it every autogroup over
		// people is undecidable rather than silently false.
		if rt.policy != nil {
			userOpts = append(userOpts, users.WithDirectorySink(rt.policy))
		}
		userOpts = append(userOpts, users.WithAPIState(rt.apiState))
		rt.registry.Register(users.New(cp.Client, c.Users.Interval.D(), userOpts...), c.Users.Interval.D())
	}
	if c.Keys.Enabled && cp.Supports("keys") {
		rt.registry.Register(keys.New(cp.Client, c.Keys.Interval.D(), c.Keys.ExpiryWarn.D(), nil,
			keys.WithPerEntity(cfg.Cardinality.PerEntity.Key),
			keys.WithExpiryLogMode(c.Keys.ExpiryLogMode),
			keys.WithAPIState(rt.apiState)), c.Keys.Interval.D())
	}
	if c.Settings.Enabled && cp.Supports("settings") {
		rt.registry.Register(settings.New(rt.client, c.Settings.Interval.D(),
			settings.WithAPIState(rt.apiState),
			settings.WithSnapshot(c.Settings.SnapshotEnabled, cfg.OTLP.Limits.LogBodyBytes)), c.Settings.Interval.D())
	}
	if c.Acl.Enabled && cp.Supports("acl") {
		aclStore := d.evidenceStore
		if d.multi {
			aclStore = collector.Namespaced(d.evidenceStore, rt.name)
		}
		aclOpts := []acl.Option{
			acl.WithValidate(c.Acl.Validate),
			acl.WithAPIState(rt.apiState),
			acl.WithCheckpointStore(aclStore),
		}
		if rt.policy != nil {
			aclOpts = append(aclOpts, acl.WithPolicySink(rt.policy))
		}
		// The validator is deliberately NOT part of provider.ControlPlane: that
		// interface is shared with the Headscale adapter, which has no validate
		// endpoint. Wiring the concrete Tailscale client here keeps the dependency
		// optional, so a Headscale runtime (rt.client == nil) simply never
		// validates instead of needing a stub that errors (#428).
		if rt.client != nil {
			aclOpts = append(aclOpts, acl.WithValidator(rt.client))
		}
		if c.Acl.SnapshotEnabled {
			aclOpts = append(aclOpts, acl.WithPolicySnapshots(
				c.Acl.SnapshotHeartbeat.D(),
				cfg.OTLP.Limits.LogBodyBytes,
				aclSnapshotStateStore(d.evidencePath, rt.name),
			))
		}
		rt.registry.Register(acl.New(cp.Client, c.Acl.Interval.D(), nil, aclOpts...), c.Acl.Interval.D())
	}
	if c.Dns.Enabled && cp.Supports("dns") {
		rt.registry.Register(dns.New(rt.client, c.Dns.Interval.D(),
			dns.WithAPIState(rt.apiState),
			dns.WithSnapshot(c.Dns.SnapshotEnabled, cfg.OTLP.Limits.LogBodyBytes)), c.Dns.Interval.D())
	}
	if c.Contacts.Enabled && cp.Supports("contacts") {
		rt.registry.Register(contacts.New(rt.client, c.Contacts.Interval.D(),
			contacts.WithAPIState(rt.apiState)), c.Contacts.Interval.D())
	}
	if c.Webhooks.Enabled && cp.Supports("webhooks") {
		rt.registry.Register(webhooks.New(rt.client, c.Webhooks.Interval.D(),
			webhooks.WithPerEntity(cfg.Cardinality.PerEntity.Webhook),
			webhooks.WithDesiredEvents(c.Webhooks.DesiredEvents),
			webhooks.WithAPIState(rt.apiState),
			webhooks.WithSnapshot(c.Webhooks.SnapshotEnabled, cfg.OTLP.Limits.LogBodyBytes)), c.Webhooks.Interval.D())
	}
	if c.PostureIntegrations.Enabled && cp.Supports("posture_integrations") {
		rt.registry.Register(postureintegrations.New(rt.client, c.PostureIntegrations.Interval.D(),
			postureintegrations.WithAPIState(rt.apiState),
			postureintegrations.WithSnapshot(c.PostureIntegrations.SnapshotEnabled, cfg.OTLP.Limits.LogBodyBytes)), c.PostureIntegrations.Interval.D())
	}
	if c.LogStream.Enabled && cp.Supports("log_stream") {
		logStreamCollector := logstream.New(rt.client, c.LogStream.Interval.D(),
			logstream.WithAPIState(rt.apiState),
			logstream.WithProbeIntervals(
				c.LogStream.ConfigurationInterval.D(), c.LogStream.NetworkInterval.D()))
		rt.registry.Register(logStreamCollector, logStreamCollector.PollInterval())
	}
	if c.OAuthApps.Enabled && cp.Supports("oauth_apps") {
		rt.registry.Register(oauthapps.New(rt.client, c.OAuthApps.Interval.D(),
			oauthapps.WithAPIState(rt.apiState)), c.OAuthApps.Interval.D())
	}
	if c.Services.Enabled && cp.Supports("services") {
		rt.registry.Register(services.New(rt.client, c.Services.Interval.D(),
			services.WithPerEntity(cfg.Cardinality.PerEntity.Service),
			services.WithCollectHosts(c.Services.CollectHosts),
			services.WithSubrequestConcurrency(c.Services.SubrequestConcurrency),
			services.WithTagRollup(c.Services.CollectTagRollup, c.Services.TagRollupLimit),
			// Feed the service-VIP -> name map so flow logs resolve a service
			// VIP peer to its service name instead of "unknown" (#166).
			services.WithEnrichCache(rt.cache),
			services.WithAPIState(rt.apiState)), c.Services.Interval.D())
	}
	pamRuntime := (cfg.PAM.Tailnet == "" && d.primary) || cfg.PAM.Tailnet == rt.configuredName
	if c.PAM.Enabled && pamRuntime && d.pamClient != nil {
		// PAM is one process-global Border0 organization, not one API surface per
		// configured tailnet. Register both schedules on the configured runtime,
		// or the primary runtime when pam.tailnet is empty, so one durable session
		// cursor and one copy of every series are retained.
		rt.registry.Register(pam.New(d.pamClient, c.PAM.Interval.D(),
			pam.WithAPIState(rt.apiState),
			pam.WithSnapshot(c.PAM.SnapshotEnabled, c.PAM.SnapshotBodyBytes),
			pam.WithSnapshotHeartbeat(c.PAM.SnapshotHeartbeat.D())), c.PAM.Interval.D())
		rt.registry.Register(pam.NewSessions(d.pamClient, c.PAM.SessionsInterval.D(), d.store, d.evidenceStore,
			pam.WithSessionsAPIState(rt.apiState),
			pam.WithSessionLog(c.PAM.SessionLogEnabled, piiCategories(cfg.PIIFilter), d.addrSet)), c.PAM.SessionsInterval.D())
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
			nmOpts := nodeMetricsOptions(nm, cp.Client, rt.cache, d.addrSet, withComponent(logger, compNodeMetrics), d.tracer)
			// Set here rather than inside nodeMetricsOptions: that function maps
			// CONFIG onto Options and is tested as a pure config mapping, while the
			// tracker is a per-runtime object it has no business knowing about.
			nmOpts.APIState = rt.apiState
			rt.nodeMetrics = nodemetrics.New(nmOpts)
			rt.registry.Register(rt.nodeMetrics, nm.Interval.D())
		}
	}
	if c.Flowlogs.Enabled && cp.Supports("flowlogs") && pollSource(c.Flowlogs.Source) {
		fc := flowlogs.New(rt.client, rt.flowProc, c.Flowlogs.Interval.D(), c.Flowlogs.Lag.D(), flowFeatureCheck(rt.client), onIngest,
			flowlogs.WithAPIState(rt.apiState),
			flowlogs.WithAcceptedObserver(onAccepted),
			flowlogs.WithDedupCapacity(c.Flowlogs.DedupCapacity),
			flowlogs.WithReplay(
				c.Flowlogs.ReplayOverlap.D(),
				c.Flowlogs.ReplaySeenCapacity,
				replayStoreForRuntime(d.store, rt.name, d.multi),
			))
		rt.registry.RegisterWindow(fc, c.Flowlogs.Interval.D(), c.Flowlogs.InitialLookback.D(), c.Flowlogs.MaxWindow.D())
	} else if c.Flowlogs.Enabled && cp.Supports("flowlogs") {
		// Stream-only or objectstore-only: the poller isn't registered, so the
		// tailscale.feature.enabled health gauge it normally emits would be missing.
		// Register a lightweight probe that reports it independently of ingestion.
		fp := flowlogs.NewFeatureProbe(flowFeatureCheck(rt.client), c.Flowlogs.Interval.D(),
			flowlogs.WithFeatureProbeAPIState(rt.apiState))
		rt.registry.Register(fp, fp.DefaultInterval())
	}
	if c.Flowlogs.Enabled && objectStoreSource(c.Flowlogs.Source) {
		// Each runtime reads its OWN destination: the global block in legacy mode,
		// this tailnet's objectstore.flow entry with a tailnets: list. Nothing is
		// inherited across the two (#284).
		registerObjectStoreCollector(objectStoreFlowFeed, rt, d, onIngest, onAccepted)
	}
	if c.Auditlogs.Enabled && cp.Supports("auditlogs") && pollSource(c.Auditlogs.Source) {
		ac := auditlogs.New(rt.client, rt.auditProc, c.Auditlogs.Interval.D(), c.Auditlogs.Lag.D(), onIngest,
			auditlogs.WithAcceptedObserver(onAccepted),
			auditlogs.WithAPIState(rt.apiState),
			auditlogs.WithDedupCapacity(c.Auditlogs.DedupCapacity))
		rt.registry.RegisterWindow(ac, c.Auditlogs.Interval.D(), c.Auditlogs.InitialLookback.D(), c.Auditlogs.MaxWindow.D())
	}
	if c.Auditlogs.Enabled && objectStoreSource(c.Auditlogs.Source) {
		// Deliberately NOT gated on cp.Supports("auditlogs"): the capability set
		// describes the CONTROL PLANE's API surface, and reading an export bucket
		// makes no API call at all. A Headscale runtime with no configuration-log
		// endpoint can still be pointed at an S3 export.
		registerObjectStoreCollector(objectStoreAuditFeed, rt, d, onIngest, onAccepted)
	}
	if c.K8sAudit.Enabled {
		// Gated purely on Enabled, for two reasons. There is no Source field:
		// object storage is the only surface tsrecorder exposes, so there is no
		// poll/stream alternative to select between. And like the audit feed
		// above this is deliberately NOT gated on any cp.Supports() capability —
		// reading a recorder bucket makes no control-plane API call at all, so a
		// Headscale runtime can be pointed at one just as well.
		registerObjectStoreCollector(objectStoreK8sAuditFeed, rt, d, onIngest, onAccepted)
	}
}

// replayStoreForRuntime mirrors the scheduler's checkpoint key shape: legacy
// bare keys for a single tailnet, and tailnet-prefixed keys when runtimes share
// one checkpoint store.
func replayStoreForRuntime(store collector.CheckpointStore, tailnet string, multi bool) collector.CheckpointStore {
	if !multi {
		return store
	}
	return collector.Namespaced(store, tailnet)
}

// buildReceivers constructs the optional HTTP receivers and, when durability is
// enabled, returns closed replay routes for every configured runtime. Replay
// routes exist even when a listener is disabled so an operator can drain
// accepted backlog before changing receiver configuration.
func (a *App) buildReceivers() []ingressWALRoute {
	rt := a.runtimes[0]
	streamServers := make(map[*tailnetRuntime]*stream.Server, len(a.runtimes))
	webhookServers := make(map[*tailnetRuntime]*webhook.Server, len(a.runtimes))
	durableAppend := func(
		routeRT *tailnetRuntime,
		source, signal string,
	) func(context.Context, []byte, time.Time) error {
		tailnet := a.runtimeConfiguredName(routeRT)
		return func(ctx context.Context, body []byte, accepted time.Time) error {
			if a.ingressWAL == nil {
				return errIngressWALAppend
			}
			return a.ingressWAL.appender(tailnet, source, signal)(ctx, body, accepted)
		}
	}
	newStream := func(
		routeRT *tailnetRuntime,
		path, token, tokenFile string,
		acceptDurably bool,
	) *stream.Server {
		options := []stream.Option{
			stream.WithTracer(a.tracer),
			stream.WithRemoteParentPolicy(a.cfg.Tracing.RemoteParent),
		}
		if acceptDurably {
			options = append(options, stream.WithDurableAppend(durableAppend(
				routeRT,
				ingressWALSourceStream,
				ingressWALSignalHEC,
			)))
		}
		server := stream.New(
			stream.Options{
				Listen:        a.cfg.Streaming.Listen,
				Path:          path,
				Token:         token,
				TokenProvider: a.credReload.streamTokenProvider(tokenFile),
				Decompress:    a.cfg.Streaming.Decompress,
				TLSCertFile:   a.cfg.Streaming.TLS.CertFile,
				TLSKeyFile:    a.cfg.Streaming.TLS.KeyFile,
				MaxBodyBytes:  a.cfg.Streaming.MaxBodyBytes,
				// Aggregate admission control (#209): MaxBodyBytes bounds one body,
				// this bounds how many are buffered at once.
				MaxConcurrentRequests:         a.cfg.Streaming.MaxConcurrentRequests,
				PerRouteMaxConcurrentRequests: a.cfg.Streaming.PerRouteMaxConcurrentRequests,
				OnIngest: ingestObserver(
					routeRT.emitter,
					a.cfg.SelfObservability.Enabled,
				),
				OnAccepted: acceptedEventObserver(
					routeRT.emitter,
					a.cfg.SelfObservability.Enabled,
				),
			},
			routeRT.flowProc,
			routeRT.auditProc,
			routeRT.emitter,
			withComponent(a.logger, appcatalog.ComponentStream),
			options...,
		)
		streamServers[routeRT] = server
		return server
	}
	newWebhook := func(
		routeRT *tailnetRuntime,
		secret, secretFile string,
		acceptDurably bool,
	) *webhook.Server {
		options := []webhook.Option{
			webhook.WithTracer(a.tracer),
			webhook.WithRemoteParentPolicy(a.cfg.Tracing.RemoteParent),
		}
		if set := a.webhookDedupFor(routeRT); set != nil {
			options = append(options, webhook.WithDedup(set))
		}
		if acceptDurably {
			options = append(options, webhook.WithDurableAppend(durableAppend(
				routeRT,
				ingressWALSourceWebhook,
				ingressWALSignalWebhook,
			)))
		}
		wh := webhookOptions(a.cfg.Webhook)
		wh.EventStore = a.eventStore // shared /events explorer store (#300); nil when disabled
		wh.Secret = secret
		wh.SecretProvider = a.credReload.webhookSecretProvider(secretFile)
		wh.OnIngest = ingestObserver(routeRT.emitter, a.cfg.SelfObservability.Enabled)
		wh.OnAccepted = acceptedEventObserver(routeRT.emitter, a.cfg.SelfObservability.Enabled)
		server := webhook.New(
			wh,
			routeRT.emitter,
			withComponent(a.logger, appcatalog.ComponentWebhook),
			options...,
		)
		webhookServers[routeRT] = server
		return server
	}

	if a.cfg.Streaming.Enabled {
		if len(a.cfg.Streaming.Routes) == 0 {
			a.streamSrv = newStream(
				rt,
				a.cfg.Streaming.Path,
				a.cfg.Streaming.Token.Reveal(),
				a.cfg.Streaming.TokenFile,
				a.cfg.IngressWAL.Enabled,
			)
		} else {
			routes := make([]stream.Route, 0, len(a.cfg.Streaming.Routes))
			for _, route := range a.cfg.Streaming.Routes {
				routeRT := a.runtimeFor(route.Tailnet)
				if routeRT == nil { // Validate prevents this; keep startup fail-closed.
					continue
				}
				srv := newStream(
					routeRT,
					route.Path,
					route.Token.Reveal(),
					route.TokenFile,
					a.cfg.IngressWAL.Enabled,
				)
				routes = append(routes, stream.Route{Path: route.Path, Server: srv})
			}
			a.streamSrv = stream.NewRouter(routes)
		}
	}
	if a.cfg.Webhook.Enabled {
		if len(a.cfg.Webhook.Routes) == 0 {
			a.webhookSrv = newWebhook(
				rt,
				a.cfg.Webhook.Secret.Reveal(),
				a.cfg.Webhook.SecretFile,
				a.cfg.IngressWAL.Enabled,
			)
		} else {
			routes := make([]webhook.Route, 0, len(a.cfg.Webhook.Routes))
			for _, route := range a.cfg.Webhook.Routes {
				routeRT := a.runtimeFor(route.Tailnet)
				if routeRT == nil {
					continue
				}
				routes = append(routes, webhook.Route{
					Tailnet: route.Tailnet,
					Server: newWebhook(
						routeRT,
						route.Secret.Reveal(),
						route.SecretFile,
						a.cfg.IngressWAL.Enabled,
					),
				})
			}
			a.webhookSrv = webhook.NewRouter(routes)
		}
	}

	if !a.cfg.IngressWAL.Enabled {
		return nil
	}
	routes := make([]ingressWALRoute, 0, 2*len(a.runtimes))
	for _, routeRT := range a.runtimes {
		streamServer := streamServers[routeRT]
		if streamServer == nil {
			streamServer = newStream(routeRT, a.cfg.Streaming.Path, "", "", false)
		}
		webhookServer := webhookServers[routeRT]
		if webhookServer == nil {
			webhookServer = newWebhook(routeRT, "", "", false)
		}
		streamApply := streamServer
		webhookApply := webhookServer
		routes = append(routes,
			ingressWALRoute{
				tailnet: a.runtimeConfiguredName(routeRT),
				source:  ingressWALSourceStream,
				signal:  ingressWALSignalHEC,
				apply: func(
					ctx context.Context,
					body []byte,
					accepted time.Time,
				) (bool, error) {
					result, err := streamApply.ApplyDurable(ctx, body, accepted)
					return result.FlowsApplied, err
				},
				drain: func() {
					routeRT.flowProc.FlushRollup(routeRT.emitter)
				},
				flush: routeRT.forceFlush,
			},
			ingressWALRoute{
				tailnet: a.runtimeConfiguredName(routeRT),
				source:  ingressWALSourceWebhook,
				signal:  ingressWALSignalWebhook,
				apply: func(
					ctx context.Context,
					body []byte,
					accepted time.Time,
				) (bool, error) {
					return false, webhookApply.ApplyDurable(ctx, body, accepted)
				},
				flush: routeRT.forceFlush,
			},
		)
	}
	return routes
}

func (a *App) runtimeFor(name string) *tailnetRuntime {
	for _, rt := range a.runtimes {
		if a.runtimeConfiguredName(rt) == name {
			return rt
		}
	}
	return nil
}

func (a *App) webhookDedupFor(rt *tailnetRuntime) *dedup.Set {
	if a.webhookDedups != nil {
		return a.webhookDedups[a.runtimeConfiguredName(rt)]
	}
	return a.webhookDedup
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
		TLSCertFile:  c.TLS.CertFile,
		TLSKeyFile:   c.TLS.KeyFile,
		MaxBodyBytes: c.MaxBodyBytes,
		// Aggregate admission control (GHSA-9547-8jpc-48h6): the HMAC covers the
		// whole body, so buffering necessarily precedes authentication.
		// MaxBodyBytes bounds ONE body; this bounds their sum, so unauthenticated
		// senders cannot multiply the per-request allowance.
		MaxConcurrentRequests:         c.MaxConcurrentRequests,
		PerRouteMaxConcurrentRequests: c.PerRouteMaxConcurrentRequests,
	}
}
