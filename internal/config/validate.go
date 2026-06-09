package config

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"
)

// oneOf reports whether v equals one of the allowed values.
func oneOf(v string, allowed ...string) bool {
	return slices.Contains(allowed, v)
}

// Warnings returns non-fatal configuration advisories. They never block startup
// (Validate handles hard errors); the caller logs them at WARN. The goal is to
// steer operators toward the safer choice without removing flexibility.
func (c *Config) Warnings() []string {
	var w []string
	if c.Tailscale.Auth.Method == "apikey" {
		w = append(w, "tailscale.auth.method=apikey: a personal API key expires in <=90 days "+
			"and is tied to the user that created it (it stops working when that user is "+
			"suspended/removed). For an unattended exporter prefer an OAuth client "+
			"(method: oauth) — its scoped tokens are short-lived and not bound to a user.")
	}

	// Dual log-ingestion risk: the supported design is to pick ONE method per log
	// type (poll OR stream). When the stream receiver is enabled AND a log
	// collector still polls, the same flow/audit data can arrive via both paths
	// and be double-counted. Cross-source de-duplication is a best-effort FAILSAFE
	// (audit keys on event identity, flow on the connection tuple) — not a
	// guarantee — so flag the configuration rather than relying on it silently.
	if c.Streaming.Enabled {
		dualLogCollectors := []struct {
			name    string
			enabled bool
			source  string
		}{
			{"flowlogs", c.Collectors.Flowlogs.Enabled, c.Collectors.Flowlogs.Source},
			{"auditlogs", c.Collectors.Auditlogs.Enabled, c.Collectors.Auditlogs.Source},
		}
		for _, col := range dualLogCollectors {
			if col.enabled && pollsSource(col.source) {
				src := col.source
				if src == "" {
					src = "poll"
				}
				w = append(w, fmt.Sprintf("collectors.%s.source=%s with streaming.enabled=true: "+
					"this log type can be ingested by BOTH the poll collector and the stream "+
					"receiver and double-counted. Cross-source de-duplication is a best-effort "+
					"FAILSAFE, not a guarantee. Choose ONE method: set collectors.%s.source=stream "+
					"(rely on the receiver), or keep source=poll and set streaming.enabled=false.",
					col.name, src, col.name))
			}
		}
	}

	if c.Prometheus.Enabled && c.Prometheus.Auth.Token == "" && isWildcardListen(c.Prometheus.Listen) {
		w = append(w, "prometheus.enabled with no prometheus.auth.token on "+c.Prometheus.Listen+": "+
			"the /metrics page exposes every series (incl. device/flow identifiers) to anyone who can "+
			"reach the port. Set prometheus.auth.token, or bind prometheus.listen to a loopback/tailnet "+
			"address (e.g. 127.0.0.1:2112).")
	}

	// The admin status page (/ and /api/status.json) exposes internal state
	// (collectors, device names, the config shape). When it is served on a
	// wildcard (all-interfaces) bind with no admin.auth.token, anyone who can
	// reach the port can read it. Steer the operator toward a token or a
	// restricted (loopback/tailnet) bind. pprof is handled more strictly in
	// Validate (it errors rather than warns).
	if c.Admin.Enabled && c.Admin.LandingPage && c.Admin.Auth.Token == "" && isWildcardListen(c.Admin.Listen) {
		w = append(w, "admin.landing_page is served on "+c.Admin.Listen+" without admin.auth.token: "+
			"the status page exposes internal state (collectors, device names, config shape) to anyone "+
			"who can reach the port. Set admin.auth.token, or bind admin.listen to a loopback/tailnet "+
			"address (e.g. 127.0.0.1:9090).")
	}

	// An enabled ingestion receiver with no credential accepts UNAUTHENTICATED
	// input. The webhook receiver skips HMAC verification entirely when
	// webhook.secret is empty (internal/webhook: an empty Secret bypasses verify),
	// and the HEC streaming receiver disables token auth when streaming.token is
	// empty (internal/stream: an empty token authorizes every request). Either lets
	// anyone who can reach the port post forged events. A credential left empty —
	// whether unset in the file or via a mistyped TS2OTEL_* env var name — lands
	// here, so flag it rather than fail open quietly. (Unlike pprof, these are not
	// hard-errored: a trusted-network or
	// local-testing deployment behind an authenticating proxy is a legitimate use.)
	if c.Webhook.Enabled && c.Webhook.Secret == "" {
		w = append(w, "webhook.enabled=true with an empty webhook.secret: HMAC signature "+
			"verification is SKIPPED, so anyone who can reach "+c.Webhook.Listen+" can post "+
			"forged webhook events (and inflate metric cardinality via attacker-chosen event "+
			"types). Set webhook.secret (e.g. TS2OTEL_WEBHOOK__SECRET — check the env var name), "+
			"or only run the receiver behind an authenticating proxy on a trusted network.")
	}
	if c.Streaming.Enabled && c.Streaming.Token == "" {
		w = append(w, "streaming.enabled=true with an empty streaming.token: the HEC receiver "+
			"authenticates NO requests, so anyone who can reach "+c.Streaming.Listen+" can inject "+
			"arbitrary flow/audit records. Set streaming.token (e.g. TS2OTEL_STREAMING__TOKEN — "+
			"check the env var name), or only run the receiver behind an authenticating "+
			"proxy on a trusted network.")
	}

	// Grafana Cloud Profiles authenticates Pyroscope pushes with HTTP basic auth
	// (the user is the stack's profiles instance ID, the password an access
	// policy token). A grafana.net endpoint with no basic_auth_password set will
	// be rejected by the server, so steer the operator toward configuring it.
	if p := c.Profiling.Pyroscope; p.Enabled &&
		strings.Contains(p.ServerAddress, "grafana.net") && p.BasicAuthPassword == "" {
		w = append(w, "profiling.pyroscope.server_address points at Grafana Cloud (grafana.net) "+
			"but profiling.pyroscope.basic_auth_password is empty: Grafana Cloud Profiles "+
			"requires HTTP basic-auth credentials (basic_auth_user = profiles instance ID, "+
			"basic_auth_password = an access policy token with profiles:write).")
	}

	// Both-mode emits the raw AND rollup flow-metric families; summing them in
	// PromQL without filtering by metric name double-counts traffic.
	if c.Cardinality.Flow.MetricsMode == "both" {
		w = append(w, "cardinality.flow.metrics_mode=both: both the raw (tailscale.network.io/packets) and "+
			"rollup (tailscale.network.io.rollup/...) flow-metric families are emitted, so a PromQL query that "+
			"sums them without filtering by metric name double-counts traffic (and roughly doubles series cost). "+
			"Prefer flow.metrics_mode=rollup for bounded cardinality, or all for full per-connection detail.")
	}

	// Reverse DNS replaces the low-cardinality "external" bucket with per-host PTR
	// names. This only inflates flow-METRIC cardinality when node_dims is also on:
	// with node_dims=false the names stay on flow LOGS only (no metric cardinality
	// cost), so the advisory must NOT fire there. Operators who have sized
	// cardinality.metric_limit for the added series can set
	// enrichment.reverse_dns.acknowledge_cardinality=true to silence it.
	if c.Enrichment.ReverseDNS.Enabled && c.Cardinality.Flow.NodeDims &&
		!c.Enrichment.ReverseDNS.AcknowledgeCardinality {
		w = append(w, "enrichment.reverse_dns.enabled=true with cardinality.flow.node_dims=true: resolved "+
			"PTR names replace the \"external\" bucket in tailscale.src.node/tailscale.dst.node, so on flow "+
			"METRICS this can add roughly one series per external IP (bounded only by cardinality.metric_limit). "+
			"To keep the names on flow LOGS only, set cardinality.flow.node_dims=false; otherwise size "+
			"cardinality.metric_limit for the added cardinality and set "+
			"enrichment.reverse_dns.acknowledge_cardinality=true to acknowledge.")
	}

	if c.VersionChecks.Devices.Enabled && !c.Collectors.Devices.Enabled {
		w = append(w, "version_checks.devices.enabled=true but collectors.devices is disabled: per-device version-skew metrics need the devices collector and will not be emitted")
	}

	if c.Tracing.Enabled && c.Tracing.SamplerArg == 0 &&
		(c.Tracing.Sampler == "traceidratio" || c.Tracing.Sampler == "parentbased_traceidratio") {
		w = append(w, "tracing.enabled is true but tracing.sampler_arg is 0 with a ratio sampler — no spans will be recorded")
	}

	if c.Provider == "headscale" {
		// Features with no Headscale API; warn if the operator explicitly enabled them.
		type col struct {
			name    string
			enabled bool
		}
		unsupported := []col{
			{"flowlogs", c.Collectors.Flowlogs.Enabled}, {"auditlogs", c.Collectors.Auditlogs.Enabled},
			{"settings", c.Collectors.Settings.Enabled}, {"dns", c.Collectors.Dns.Enabled},
			{"contacts", c.Collectors.Contacts.Enabled}, {"webhooks", c.Collectors.Webhooks.Enabled},
			{"posture_integrations", c.Collectors.PostureIntegrations.Enabled},
			{"log_stream", c.Collectors.LogStream.Enabled}, {"services", c.Collectors.Services.Enabled},
		}
		for _, u := range unsupported {
			if u.enabled {
				w = append(w, fmt.Sprintf("collector %s is enabled but provider=headscale does not "+
					"support it; it will not run. Set collectors.%s.enabled=false to silence this.", u.name, u.name))
			}
		}
	}

	for _, name := range c.unknownEnv {
		w = append(w, fmt.Sprintf("environment variable %s does not match any configuration key "+
			"and was ignored — check the name for typos (keys use the %s prefix with %q as the "+
			"nesting delimiter, e.g. %sOTLP%sENDPOINT).", name, EnvPrefix, envNestDelim, EnvPrefix, envNestDelim))
	}

	return w
}

// isWildcardListen reports whether addr binds to all interfaces (so a non-tailnet
// host could reach it). An empty/unspecified host (":9090", "0.0.0.0:9090",
// "[::]:9090") is a wildcard; a loopback or specific address (e.g. tailnet IP)
// is not. A malformed address is treated as a wildcard so the advisory errs
// toward warning.
func isWildcardListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// pollsSource reports whether a window collector with the given source value
// runs the poller (as opposed to relying solely on the stream receiver). It
// mirrors app.pollSource; an empty source defaults to polling.
func pollsSource(s string) bool {
	return s == "" || s == "poll" || s == "both"
}

// Validate reports the first configuration error it finds, or nil if the
// Config is valid.
func (c *Config) Validate() error {
	provider := c.Provider
	if provider == "" {
		provider = "tailscale"
	}
	if !oneOf(provider, "tailscale", "headscale") {
		return fmt.Errorf("provider: must be \"tailscale\" or \"headscale\", got %q", provider)
	}
	if provider == "headscale" {
		if strings.TrimSpace(c.Headscale.URL) == "" {
			return fmt.Errorf("headscale.url: required when provider=headscale")
		}
		if c.Headscale.APIKey == "" {
			return fmt.Errorf("headscale.api_key: required when provider=headscale (set TS2OTEL_HEADSCALE__API_KEY)")
		}
	}

	if !oneOf(c.OTLP.Protocol, "grpc", "http", "stdout") {
		return fmt.Errorf("otlp.protocol %q invalid: must be one of grpc, http, stdout", c.OTLP.Protocol)
	}
	if c.Prometheus.Enabled && c.Admin.Enabled && c.Prometheus.Listen == c.Admin.Listen {
		return fmt.Errorf("prometheus.listen and admin.listen both bind %q: give the Prometheus endpoint its own listener", c.Prometheus.Listen)
	}
	if provider == "tailscale" {
		if !oneOf(c.Tailscale.Auth.Method, "oauth", "apikey") {
			return fmt.Errorf("tailscale.auth.method %q invalid: must be one of oauth, apikey", c.Tailscale.Auth.Method)
		}
		// Single tailscale: block and a tailnets: list are mutually exclusive.
		// Default() seeds tailscale.tailnet="-" (the "principal's default tailnet"
		// sentinel), so only treat an EXPLICIT non-sentinel name as a conflict.
		if len(c.Tailnets) > 0 && c.Tailscale.Tailnet != "" && c.Tailscale.Tailnet != "-" {
			return fmt.Errorf("tailscale.tailnet=%q and tailnets: are mutually exclusive — "+
				"use the single tailscale: block OR the tailnets: list, not both", c.Tailscale.Tailnet)
		}
		// Multi-tailnet: every entry needs a name + a valid auth method (creds are
		// never inherited from the top-level block).
		seenTailnet := map[string]bool{}
		for i, t := range c.Tailnets {
			if strings.TrimSpace(t.Name) == "" {
				return fmt.Errorf("tailnets[%d].name: required", i)
			}
			if seenTailnet[t.Name] {
				return fmt.Errorf("tailnets[%d].name %q: duplicate tailnet name", i, t.Name)
			}
			seenTailnet[t.Name] = true
			if !oneOf(t.Auth.Method, "oauth", "apikey") {
				return fmt.Errorf("tailnets[%d].auth.method %q invalid: must be one of oauth, apikey", i, t.Auth.Method)
			}
		}
		// Streaming/webhook receivers feed shared single-tailnet processors and a
		// single enrichment cache; multi-tailnet routing is a follow-up.
		if len(c.Tailnets) > 1 && (c.Streaming.Enabled || c.Webhook.Enabled) {
			return fmt.Errorf("streaming/webhook receivers require single-tailnet mode " +
				"(use the single tailscale: block, not a multi-entry tailnets: list)")
		}
	}
	if !oneOf(c.Checkpoint.Store, "memory", "file") {
		return fmt.Errorf("checkpoint.store %q invalid: must be one of memory, file", c.Checkpoint.Store)
	}

	// Source validation. Only the two log collectors have a source; an empty
	// value defaults to poll.
	logCollectors := []struct {
		name   string
		source string
	}{
		{"flowlogs", c.Collectors.Flowlogs.Source},
		{"auditlogs", c.Collectors.Auditlogs.Source},
	}
	for _, col := range logCollectors {
		if col.source != "" && !oneOf(col.source, "poll", "stream", "both") {
			return fmt.Errorf("collectors.%s.source %q invalid: must be one of poll, stream, both", col.name, col.source)
		}
	}

	if !oneOf(c.Collectors.Flowlogs.LogMode, "per_connection", "per_record", "off") {
		return fmt.Errorf("collectors.flowlogs.log_mode %q invalid: must be one of per_connection, per_record, off", c.Collectors.Flowlogs.LogMode)
	}

	if !oneOf(c.Cardinality.Flow.MetricsMode, "all", "rollup", "both") {
		return fmt.Errorf("cardinality.flow.metrics_mode %q invalid: must be one of all, rollup, both", c.Cardinality.Flow.MetricsMode)
	}
	if c.Cardinality.Flow.RollupTopN < 0 {
		return fmt.Errorf("cardinality.flow.rollup_top_n %d invalid: must be >= 0 (0 selects the default)", c.Cardinality.Flow.RollupTopN)
	}

	if c.Collectors.Devices.PostureLogMode != "" &&
		!oneOf(c.Collectors.Devices.PostureLogMode, "changes", "always", "off") {
		return fmt.Errorf("collectors.devices.posture_log_mode %q invalid: must be one of changes, always, off", c.Collectors.Devices.PostureLogMode)
	}

	if !oneOf(c.Streaming.Decompress, "auto", "gzip", "zstd", "none") {
		return fmt.Errorf("streaming.decompress %q invalid: must be one of auto, gzip, zstd, none", c.Streaming.Decompress)
	}

	// Auto-configuring the log-streaming sink needs an enabled receiver and the
	// externally reachable URL to register with Tailscale.
	if c.Streaming.AutoConfigure {
		if !c.Streaming.Enabled {
			return fmt.Errorf("streaming.auto_configure requires streaming.enabled: true")
		}
		if c.Streaming.PublicURL == "" {
			return fmt.Errorf("streaming.auto_configure requires streaming.public_url to be set")
		}
	}

	// Every static node-metrics target must have a URL when the scraper is
	// enabled; when dynamic discovery is enabled its fields are validated too.
	// Either static targets OR discovery is a valid way to have something to scrape.
	if nm := c.Collectors.NodeMetrics; nm.Enabled {
		for i, t := range nm.Targets {
			if t.URL == "" {
				return fmt.Errorf("collectors.node_metrics.targets[%d].url is required", i)
			}
		}
		if nm.MaxResponseBytes <= 0 {
			return fmt.Errorf("collectors.node_metrics.max_response_bytes must be > 0")
		}
		if nm.MaxSamples <= 0 {
			return fmt.Errorf("collectors.node_metrics.max_samples must be > 0")
		}
		// Passthrough metric-name filters are anchored regexes; compile them up
		// front so a bad pattern is a config error rather than a silent no-op.
		for i, p := range nm.MetricAllow {
			if _, err := regexp.Compile(fmt.Sprintf("^(?:%s)$", p)); err != nil {
				return fmt.Errorf("collectors.node_metrics.metric_allow[%d] %q: invalid regex: %w", i, p, err)
			}
		}
		for i, p := range nm.MetricDeny {
			if _, err := regexp.Compile(fmt.Sprintf("^(?:%s)$", p)); err != nil {
				return fmt.Errorf("collectors.node_metrics.metric_deny[%d] %q: invalid regex: %w", i, p, err)
			}
		}
		if d := nm.Discovery; d.Enabled {
			if !oneOf(d.Scheme, "http", "https") {
				return fmt.Errorf("collectors.node_metrics.discovery.scheme %q invalid: must be one of http, https", d.Scheme)
			}
			if d.Port < 1 || d.Port > 65535 {
				return fmt.Errorf("collectors.node_metrics.discovery.port %d invalid: must be 1-65535", d.Port)
			}
			if !oneOf(d.AddressOrder, "ipv4", "ipv6") {
				return fmt.Errorf("collectors.node_metrics.discovery.address_order %q invalid: must be one of ipv4, ipv6", d.AddressOrder)
			}
			if !oneOf(d.InstanceSource, "address", "name", "hostname") {
				return fmt.Errorf("collectors.node_metrics.discovery.instance_source %q invalid: must be one of address, name, hostname", d.InstanceSource)
			}
			if d.Interval.D() <= 0 {
				return fmt.Errorf("collectors.node_metrics.discovery.interval must be > 0")
			}
			if d.MaxTargets <= 0 {
				return fmt.Errorf("collectors.node_metrics.discovery.max_targets must be > 0")
			}
		}
	}

	// Reverse-DNS enrichment: when enabled, the resolver address (if set) must be
	// an IP or IP:port, and the cache bound must be positive.
	if rd := c.Enrichment.ReverseDNS; rd.Enabled {
		if rd.Server != "" {
			host := rd.Server
			if h, _, err := net.SplitHostPort(rd.Server); err == nil {
				host = h
			}
			if net.ParseIP(host) == nil {
				return fmt.Errorf("enrichment.reverse_dns.server %q invalid: must be an IP or IP:port", rd.Server)
			}
		}
		if rd.MaxEntries <= 0 {
			return fmt.Errorf("enrichment.reverse_dns.max_entries must be > 0 when reverse DNS is enabled")
		}
	}

	// Profiling is opt-in. The pprof handlers are mounted on the admin server, so
	// they need it enabled; the Pyroscope push agent needs a server to push to.
	if c.Profiling.Pprof.Enabled && !c.Admin.Enabled {
		return fmt.Errorf("profiling.pprof.enabled requires admin.enabled: true")
	}
	// pprof exposes process internals (heap/goroutine dumps can contain
	// in-memory secrets), so it must not be served unauthenticated: enabling it
	// requires a shared admin.auth.token. The status page itself only warns
	// (see Warnings); pprof is the stricter surface.
	if c.Profiling.Pprof.Enabled && c.Admin.Auth.Token == "" {
		return fmt.Errorf("profiling.pprof.enabled requires admin.auth.token to be set (pprof can expose in-memory secrets via heap dumps)")
	}
	if c.Profiling.Pyroscope.Enabled && c.Profiling.Pyroscope.ServerAddress == "" {
		return fmt.Errorf("profiling.pyroscope.enabled requires profiling.pyroscope.server_address")
	}

	if !oneOf(c.Tracing.Sampler, "always_on", "always_off", "traceidratio",
		"parentbased_always_on", "parentbased_traceidratio") {
		return fmt.Errorf("tracing.sampler %q invalid: must be one of always_on, always_off, traceidratio, parentbased_always_on, parentbased_traceidratio", c.Tracing.Sampler)
	}
	if c.Tracing.SamplerArg < 0 || c.Tracing.SamplerArg > 1 {
		return fmt.Errorf("tracing.sampler_arg %v invalid: must be in [0,1]", c.Tracing.SamplerArg)
	}

	if c.VersionChecks.Self.Enabled || c.VersionChecks.Devices.Enabled {
		if c.VersionChecks.CacheTTL.D() < 5*time.Minute {
			return fmt.Errorf("version_checks.cache_ttl must be >= 5m to avoid hammering the upstream release endpoints")
		}
		if c.VersionChecks.Timeout.D() <= 0 {
			return fmt.Errorf("version_checks.timeout must be > 0")
		}
	}
	if c.VersionChecks.Devices.Enabled && c.VersionChecks.Devices.OutdatedMinorThreshold < 1 {
		return fmt.Errorf("version_checks.devices.outdated_minor_threshold must be >= 1")
	}

	return nil
}
