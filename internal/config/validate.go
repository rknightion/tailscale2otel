package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/listenaddr"
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

	// With the tailnet distinguisher dropped, per-tailnet Prometheus series become
	// byte-identical and collapse on the pull path (the handler serves 200 via
	// first-wins rather than 500 — see #103). Flag the silent per-tailnet data loss.
	if c.Prometheus.Enabled && !c.PIIFilter.TailnetName && len(c.Tailnets) > 1 {
		w = append(w, "prometheus.enabled with pii_filter.tailnet_name=false in multi-tailnet mode: "+
			"the tailscale_tailnet label is the only per-tailnet distinguisher on /metrics, so with it "+
			"disabled the per-tailnet series are identical and collapse to one on the pull path (the "+
			"scrape still returns 200, but per-tailnet breakdowns are lost). Keep pii_filter.tailnet_name "+
			"enabled, or rely on the OTLP push path where each tailnet is a distinct target.")
	}

	if c.Prometheus.Enabled && c.Prometheus.Auth.Token == "" && isWildcardListen(c.Prometheus.Listen) {
		w = append(w, "prometheus.enabled with no prometheus.auth.token on "+c.Prometheus.Listen+": "+
			"the /metrics page exposes every series (incl. device/flow identifiers) to anyone who can "+
			"reach the port. Set prometheus.auth.token, or bind prometheus.listen to a loopback/tailnet "+
			"address (e.g. 127.0.0.1:2112).")
	}

	// The admin status page (/ and /api/status.json) exposes internal state
	// (collectors, device names, the config shape). On a network-reachable bind
	// with no admin.auth.token it no longer serves that to anyone who asks (#227):
	// the handler now REFUSES with 403. Warn so the operator knows the page is
	// dark and why, rather than discovering it via a 403 in a browser. Note this
	// fires on any non-loopback bind, not just a wildcard one — a tailnet address
	// is reachable by every peer on the tailnet. pprof is handled more strictly in
	// Validate (it errors rather than warns).
	if c.Admin.Enabled && c.Admin.LandingPage && c.Admin.Auth.Token == "" && !listenaddr.IsLoopback(c.Admin.Listen) {
		w = append(w, "admin.landing_page is served on the network-reachable bind "+c.Admin.Listen+
			" without admin.auth.token: the status page and its JSON APIs are REFUSED with HTTP 403 "+
			"(they would otherwise expose collectors, device names and the config shape to anyone who "+
			"can reach the port). /healthz and /readyz are unaffected. Set admin.auth.token, or bind "+
			"admin.listen to loopback (e.g. 127.0.0.1:9091).")
	}

	// An enabled ingestion receiver with no credential accepts UNAUTHENTICATED
	// input. The webhook receiver still skips HMAC verification entirely when
	// webhook.secret is empty (internal/webhook: an empty Secret bypasses verify),
	// so anyone who can reach the port can post forged events. A credential left
	// empty — whether unset in the file or via a mistyped TS2OTEL_* env var name —
	// lands here, so flag it rather than fail open quietly. (Unlike pprof, this is
	// not hard-errored: a trusted-network or local-testing deployment behind an
	// authenticating proxy is a legitimate use.)
	if c.Webhook.Enabled && c.Webhook.Secret == "" {
		w = append(w, "webhook.enabled=true with an empty webhook.secret: HMAC signature "+
			"verification is SKIPPED, so anyone who can reach "+c.Webhook.Listen+" can post "+
			"forged webhook events (and inflate metric cardinality via attacker-chosen event "+
			"types). Set webhook.secret (e.g. TS2OTEL_WEBHOOK__SECRET — check the env var name), "+
			"or only run the receiver behind an authenticating proxy on a trusted network.")
	}
	// The HEC streaming receiver no longer fails OPEN on an empty token (#228): on
	// a network-reachable bind it now REFUSES every request with 403 rather than
	// accepting forged flow/audit records. That turns a silent security hole into
	// a loud functional one, so the warning has to tell the operator ingestion is
	// broken, not merely unauthenticated. A loopback bind stays open — only the
	// local host can reach it — and is the supported credential-free setup.
	if c.Streaming.Enabled && c.Streaming.Token == "" {
		if listenaddr.IsLoopback(c.Streaming.Listen) {
			w = append(w, "streaming.enabled=true with an empty streaming.token on the loopback "+
				"bind "+c.Streaming.Listen+": the HEC receiver authenticates no requests. Only the "+
				"local host can reach it, so this is accepted, but any local process can inject "+
				"arbitrary flow/audit records. Set streaming.token (e.g. TS2OTEL_STREAMING__TOKEN) "+
				"if that matters.")
		} else {
			w = append(w, "streaming.enabled=true with an empty streaming.token on the "+
				"network-reachable bind "+c.Streaming.Listen+": the HEC receiver REFUSES every "+
				"request with HTTP 403, so NO logs will be ingested. Set streaming.token (e.g. "+
				"TS2OTEL_STREAMING__TOKEN — check the env var name), or bind streaming.listen to "+
				"loopback and put an authenticating proxy in front.")
		}
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
		// Streaming/webhook receivers have no Headscale equivalent (Headscale has no
		// log-stream/webhook API), so an enabled receiver just exposes a listener that
		// can never receive legitimate data — and, token-less, would accept forged
		// records (#117). auto_configure is silently skipped at runtime.
		if c.Streaming.Enabled {
			w = append(w, "streaming.enabled=true but provider=headscale has no log-stream API: the HEC "+
				"receiver can never receive legitimate data (and with no streaming.token would accept forged "+
				"records). Set streaming.enabled=false.")
		}
		if c.Streaming.AutoConfigure {
			w = append(w, "streaming.auto_configure=true but provider=headscale has no log-streaming sink "+
				"to register; it is silently skipped at runtime. Set streaming.auto_configure=false.")
		}
		if c.Webhook.Enabled {
			w = append(w, "webhook.enabled=true but provider=headscale has no webhook API: the receiver "+
				"can never receive legitimate events. Set webhook.enabled=false.")
		}
	}

	if lim := c.Cardinality.MetricLimit; lim > 0 {
		if warn, crit := c.Cardinality.WarningThreshold, c.Cardinality.CriticalThreshold; warn > lim || crit > lim {
			w = append(w, fmt.Sprintf("cardinality warning_threshold/critical_threshold (%d/%d) exceed metric_limit %d: "+
				"a metric's active-series count pins at metric_limit, so a threshold above it can never fire on the "+
				"status page. Lower the thresholds to <= metric_limit for them to be meaningful.",
				warn, crit, lim))
		}
	}

	if c.Collectors.NodeMetrics.Enabled && c.Cardinality.MetricLimit <= 0 {
		w = append(w, "collectors.node_metrics.enabled=true with cardinality.metric_limit unlimited "+
			"(<=0): scraped label VALUES are controlled by the scraped nodes, so a compromised or "+
			"misbehaving node can mint unbounded series (memory + backend cost). Set "+
			"cardinality.metric_limit (default 10000) so the SDK collapses the excess into "+
			"otel_metric_overflow.")
	}

	// (#52g) Advisory only: a tailnet with exactly one half of an OAuth credential
	// pair set is almost always a copy-paste slip (a wrong env var name for the
	// other half). Never fires on a both-empty block — rendered/checked-in configs
	// legitimately carry no credentials (they arrive via env at runtime).
	checkPartialOAuth := func(label string, a TailscaleAuth) {
		if a.Method != "oauth" {
			return
		}
		hasID, hasSecret := a.OAuth.ClientID != "", a.OAuth.ClientSecret != ""
		if hasID == hasSecret {
			return
		}
		have, missing := "client_id", "client_secret"
		if hasSecret {
			have, missing = "client_secret", "client_id"
		}
		w = append(w, fmt.Sprintf("%s has oauth.%s set but oauth.%s empty: an OAuth client needs "+
			"both — check the missing field's value / its TS2OTEL_* env var name (or leave both empty "+
			"to supply them at runtime).", label, have, missing))
	}
	if len(c.Tailnets) > 0 {
		for i, t := range c.Tailnets {
			checkPartialOAuth(fmt.Sprintf("tailnets[%d] (%s)", i, t.Name), t.Auth)
		}
	} else if c.Provider != "headscale" {
		checkPartialOAuth("tailscale.auth", c.Tailscale.Auth)
	}

	if c.configFileWarning != "" {
		w = append(w, c.configFileWarning)
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

// validateReceiverPath checks that a non-empty HTTP receiver path is a rooted
// absolute path with no whitespace, so it can be registered with
// http.ServeMux.HandleFunc without panicking or being misparsed as a
// method/host-scoped pattern. An empty path is accepted (the receiver fills in
// its own default).
func validateReceiverPath(field, p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%s %q invalid: must be a rooted absolute path beginning with \"/\" "+
			"(e.g. \"/tailscale/webhook\")", field, p)
	}
	if strings.ContainsAny(p, " \t") {
		return fmt.Errorf("%s %q invalid: must not contain whitespace", field, p)
	}
	return nil
}

// validateTLSFiles enforces the shared TLS-file contract for a listener block
// (admin.tls / prometheus.tls): cert_file and key_file must be set together
// (both-or-neither), and any set path must exist and be readable now — not
// discovered as an opaque http.Server.ListenAndServeTLS failure at startup.
// label is the config prefix (e.g. "admin") used in error messages.
func validateTLSFiles(label, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("%s.tls.cert_file and %s.tls.key_file must both be set or both be empty "+
			"(got cert_file=%q, key_file=%q)", label, label, certFile, keyFile)
	}
	for _, f := range [...]struct {
		field string
		path  string
	}{
		{"cert_file", certFile},
		{"key_file", keyFile},
	} {
		if f.path == "" {
			continue
		}
		fh, err := os.Open(f.path)
		if err != nil {
			return fmt.Errorf("%s.tls.%s %q: %w", label, f.field, f.path, err)
		}
		_ = fh.Close()
	}
	return nil
}

// validateWorkloadIdentity enforces the workload-identity auth contract when
// that method is selected: both client_id and id_token_file are required, and
// the token file must exist and be readable now — startup-time failure beats a
// first-poll failure (tsapi re-checks per exchange as defense-in-depth, since
// projected tokens rotate in place). label is the config prefix (e.g.
// "tailscale.auth") used in error messages.
func validateWorkloadIdentity(label string, a TailscaleAuth) error {
	if a.Method != "workload_identity" {
		return nil
	}
	if a.WorkloadIdentity.ClientID == "" {
		return fmt.Errorf("%s.workload_identity.client_id: required when %s.method is workload_identity", label, label)
	}
	if a.WorkloadIdentity.IDTokenFile == "" {
		return fmt.Errorf("%s.workload_identity.id_token_file: required when %s.method is workload_identity", label, label)
	}
	fh, err := os.Open(a.WorkloadIdentity.IDTokenFile)
	if err != nil {
		return fmt.Errorf("%s.workload_identity.id_token_file %q: %w", label, a.WorkloadIdentity.IDTokenFile, err)
	}
	_ = fh.Close()
	return nil
}

// Validate reports the first configuration error it finds, or nil if the
// Config is valid.
func (c *Config) Validate() error {
	// A "*_file" secret sibling whose paired value field was ALSO set (recorded
	// by resolveSecretFiles at Load) — report the first one, matching this
	// method's "first error found" contract.
	if len(c.secretFileConflicts) > 0 {
		return fmt.Errorf("%s: set only one, not both (value XOR file)", c.secretFileConflicts[0])
	}

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

	// log_level is documented (configuration.md) and framed as a validated enum,
	// so reject a value outside the set here rather than silently failing open to
	// info in cmd/tailscale2otel.parseLevel (the mismatch #106 flagged).
	if !oneOf(c.LogLevel, "debug", "info", "warn", "error") {
		return fmt.Errorf("log_level %q invalid: must be one of debug, info, warn, error", c.LogLevel)
	}

	if !oneOf(c.OTLP.Protocol, "grpc", "http", "stdout") {
		return fmt.Errorf("otlp.protocol %q invalid: must be one of grpc, http, stdout", c.OTLP.Protocol)
	}
	// The gRPC OTLP exporter (otlp*grpc.WithEndpoint) dials a host:port address,
	// NOT a URL: a scheme or path (e.g. "https://gw.example/otlp", which is the
	// correct shape for the http protocol) makes the gRPC dialer fail to connect.
	// Catch the mismatch at load time rather than as an opaque runtime dial error.
	// (http endpoints are full URLs; stdout ignores the endpoint entirely.)
	if c.OTLP.Protocol == "grpc" && c.OTLP.Endpoint != "" {
		if strings.Contains(c.OTLP.Endpoint, "://") || strings.Contains(c.OTLP.Endpoint, "/") {
			return fmt.Errorf("otlp.endpoint %q invalid for otlp.protocol=grpc: use a host:port "+
				"address with no scheme or path (e.g. otlp-gateway-prod-us-central-0.grafana.net:443); "+
				"a full URL is only valid for protocol=http", c.OTLP.Endpoint)
		}
	}
	// The http OTLP exporter dials a full URL. A scheme-less host:port (the grpc
	// shape) parses to an empty host and silently zeroes the endpoint at runtime
	// rather than failing at load; require an http/https scheme and a host so the
	// mismatch is caught here (#52b).
	if c.OTLP.Protocol == "http" {
		u, err := url.Parse(c.OTLP.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("otlp.endpoint %q invalid for otlp.protocol=http: use a full URL with "+
				"an http:// or https:// scheme and a host (e.g. "+
				"https://otlp-gateway-prod-us-central-0.grafana.net/otlp); a scheme-less host:port "+
				"is only valid for protocol=grpc", c.OTLP.Endpoint)
		}
	}
	if c.OTLP.MetricInterval.D() <= 0 {
		return fmt.Errorf("otlp.metric_interval must be > 0 (got %v); a zero or negative interval panics time.NewTicker at startup", c.OTLP.MetricInterval.D())
	}
	// Every enabled HTTP listener needs its own bind address. Two enabled servers
	// on the same address race on net.Listen: one binds, the other logs an ERROR
	// and the process keeps running with that surface silently dead. Check ALL
	// four listeners pairwise, not just admin/prometheus (#52f).
	listeners := []struct {
		name    string
		addr    string
		enabled bool
	}{
		{"admin.listen", c.Admin.Listen, c.Admin.Enabled},
		{"prometheus.listen", c.Prometheus.Listen, c.Prometheus.Enabled},
		{"streaming.listen", c.Streaming.Listen, c.Streaming.Enabled},
		{"webhook.listen", c.Webhook.Listen, c.Webhook.Enabled},
	}
	for i := range listeners {
		for j := i + 1; j < len(listeners); j++ {
			a, b := listeners[i], listeners[j]
			if a.enabled && b.enabled && a.addr != "" && a.addr == b.addr {
				return fmt.Errorf("%s and %s both bind %q: each enabled HTTP listener needs its own "+
					"address (only one wins the net.Listen race; the other dies silently)", a.name, b.name, a.addr)
			}
		}
	}
	// admin.tls / prometheus.tls: both-or-neither, and any configured file must
	// exist and be readable now rather than surfacing as an opaque
	// ListenAndServeTLS failure at startup.
	if err := validateTLSFiles("admin", c.Admin.TLS.CertFile, c.Admin.TLS.KeyFile); err != nil {
		return err
	}
	if err := validateTLSFiles("prometheus", c.Prometheus.TLS.CertFile, c.Prometheus.TLS.KeyFile); err != nil {
		return err
	}
	if provider == "tailscale" {
		if !oneOf(c.Tailscale.Auth.Method, "oauth", "apikey", "workload_identity") {
			return fmt.Errorf("tailscale.auth.method %q invalid: must be one of oauth, apikey, workload_identity", c.Tailscale.Auth.Method)
		}
		if err := validateWorkloadIdentity("tailscale.auth", c.Tailscale.Auth); err != nil {
			return err
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
			if !oneOf(t.Auth.Method, "oauth", "apikey", "workload_identity") {
				return fmt.Errorf("tailnets[%d].auth.method %q invalid: must be one of oauth, apikey, workload_identity", i, t.Auth.Method)
			}
			if err := validateWorkloadIdentity(fmt.Sprintf("tailnets[%d].auth", i), t.Auth); err != nil {
				return err
			}
		}
		// Single-tailnet mode needs a tailnet name. The default is the "-" sentinel
		// (the principal's default tailnet), so an empty value can only come from an
		// explicit tailscale.tailnet: "" (or TS2OTEL_TAILSCALE__TAILNET=""). Catch it
		// here with actionable guidance rather than letting tsapi.NewClient fail at
		// startup with the opaque "Tailnet is required".
		if len(c.Tailnets) == 0 && strings.TrimSpace(c.Tailscale.Tailnet) == "" {
			return fmt.Errorf("tailscale.tailnet: required — set your tailnet's name " +
				"(e.g. \"example.com\") or \"-\" for the auth principal's default tailnet")
		}
	}
	// Streaming/webhook receivers feed shared single-tailnet processors and a
	// single enrichment cache; multi-tailnet routing is a follow-up. Checked for
	// EVERY provider (not just tailscale — #117) so a headscale config can't slip
	// a multi-entry list past it either; headscale has no tailnets list so it's a
	// no-op there, and the unsupported-receiver advisory below covers headscale.
	if len(c.Tailnets) > 1 && (c.Streaming.Enabled || c.Webhook.Enabled) {
		return fmt.Errorf("streaming/webhook receivers require single-tailnet mode " +
			"(use the single tailscale: block, not a multi-entry tailnets: list)")
	}
	if !oneOf(c.Checkpoint.Store, "memory", "file") {
		return fmt.Errorf("checkpoint.store %q invalid: must be one of memory, file", c.Checkpoint.Store)
	}

	// Source + window-timing validation. Only the two log collectors have a
	// source; an empty value defaults to poll.
	logCollectors := []struct {
		name            string
		enabled         bool
		source          string
		lag             time.Duration
		initialLookback time.Duration
		maxWindow       time.Duration
		interval        time.Duration
	}{
		{"flowlogs", c.Collectors.Flowlogs.Enabled, c.Collectors.Flowlogs.Source,
			c.Collectors.Flowlogs.Lag.D(), c.Collectors.Flowlogs.InitialLookback.D(),
			c.Collectors.Flowlogs.MaxWindow.D(), c.Collectors.Flowlogs.Interval.D()},
		{"auditlogs", c.Collectors.Auditlogs.Enabled, c.Collectors.Auditlogs.Source,
			c.Collectors.Auditlogs.Lag.D(), c.Collectors.Auditlogs.InitialLookback.D(),
			c.Collectors.Auditlogs.MaxWindow.D(), c.Collectors.Auditlogs.Interval.D()},
	}
	for _, col := range logCollectors {
		if col.source != "" && !oneOf(col.source, "poll", "stream", "both") {
			return fmt.Errorf("collectors.%s.source %q invalid: must be one of poll, stream, both", col.name, col.source)
		}
		if !col.enabled {
			continue
		}
		// (a) A pure-stream collector needs an ingestion path that actually exists.
		if col.source == "stream" {
			if len(c.Tailnets) > 1 {
				return fmt.Errorf("collectors.%s.source=stream is not supported in multi-tailnet mode "+
					"(streaming receivers require single-tailnet mode); use source: poll", col.name)
			}
			if !c.Streaming.Enabled {
				return fmt.Errorf("collectors.%s.source=stream requires streaming.enabled: true — "+
					"otherwise there is no ingestion path and %s are silently never collected", col.name, col.name)
			}
		}
		// (c)/(d) Window timing applies only to the polling path.
		if pollsSource(col.source) {
			if col.initialLookback <= 0 {
				return fmt.Errorf("collectors.%s.initial_lookback must be > 0 (got %v): a zero or "+
					"negative cold-start lookback leaves the poll window's from >= to forever, so the "+
					"collector never polls and never bootstraps its checkpoint", col.name, col.initialLookback)
			}
			if col.lag < 0 {
				return fmt.Errorf("collectors.%s.lag must be >= 0 (got %v): a negative lag pushes the "+
					"window end into the future, permanently skipping records that arrive within it",
					col.name, col.lag)
			}
			// A positive max_window <= interval can never catch up: catch-up
			// advances at most max_window per tick, so a backlogged poller falls
			// further behind every tick without bound. A zero/negative max_window
			// is the intentional "no cap" sentinel and is exempt.
			if col.maxWindow > 0 && col.maxWindow <= col.interval {
				return fmt.Errorf("collectors.%s.max_window (%v) <= interval (%v): catch-up advances "+
					"at most max_window per tick, so with interval >= max_window the checkpoint falls "+
					"further behind every tick without bound; set max_window > interval, or 0 for no cap",
					col.name, col.maxWindow, col.interval)
			}
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
	if c.Cardinality.LabelValueSampleCap < 0 {
		return fmt.Errorf("cardinality.label_value_sample_cap %d invalid: must be >= 0 (0 disables label-value capture)", c.Cardinality.LabelValueSampleCap)
	}
	if w, cr := c.Cardinality.WarningThreshold, c.Cardinality.CriticalThreshold; w < 0 || cr < 0 {
		return fmt.Errorf("cardinality warning_threshold/critical_threshold must be >= 0 (got %d/%d)", w, cr)
	} else if w > 0 && cr > 0 && cr < w {
		return fmt.Errorf("cardinality.critical_threshold %d invalid: must be >= warning_threshold %d", cr, w)
	}
	// The threshold-vs-metric_limit relationship is advisory (a threshold above
	// the limit can never fire, since a metric's series pin at the limit) — see
	// Warnings(). It is NOT a hard error, so lowering metric_limit never breaks an
	// existing config that kept the default thresholds.

	if c.Collectors.Devices.PostureLogMode != "" &&
		!oneOf(c.Collectors.Devices.PostureLogMode, "changes", "always", "off") {
		return fmt.Errorf("collectors.devices.posture_log_mode %q invalid: must be one of changes, always, off", c.Collectors.Devices.PostureLogMode)
	}

	if !oneOf(c.Streaming.Decompress, "auto", "gzip", "zstd", "none") {
		return fmt.Errorf("streaming.decompress %q invalid: must be one of auto, gzip, zstd, none", c.Streaming.Decompress)
	}

	// Receiver paths are registered verbatim with http.ServeMux.HandleFunc, which
	// panics at receiver startup on a malformed pattern. An empty path is fine (the
	// receiver substitutes its default), but a configured path must be a rooted
	// absolute path ("/...") — a value like "tailscale/webhook" is parsed by the mux
	// as a host-scoped pattern and silently never matches. Validate it up front.
	if err := validateReceiverPath("streaming.path", c.Streaming.Path); err != nil {
		return err
	}
	if err := validateReceiverPath("webhook.path", c.Webhook.Path); err != nil {
		return err
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
		seenTargetID := make(map[string]int, len(nm.Targets))
		for i, t := range nm.Targets {
			if t.URL == "" {
				return fmt.Errorf("collectors.node_metrics.targets[%d].url is required", i)
			}
			// Reject two static targets that resolve to the same EFFECTIVE identity
			// (normalized URL + node-identity label). Such a pair scrapes one endpoint
			// twice under one identity and, for counters, corrupts each other's delta
			// baseline (the two source series would share a baseline key). Targets that
			// differ only by URL, or only by instance, are fine — e.g. one verify-on and
			// one skip-verify HTTPS scrape of the same URL, labeled distinctly.
			id := nodeMetricsTargetIdentity(t)
			if j, dup := seenTargetID[id]; dup {
				return fmt.Errorf("collectors.node_metrics.targets[%d] duplicates targets[%d]: both resolve to "+
					"the same target identity (url %q, instance %q) — remove one or give them distinct instances",
					i, j, t.URL, effectiveNodeMetricsInstance(t))
			}
			seenTargetID[id] = i
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

// nodeMetricsTargetIdentity is the effective identity of a node-metrics scrape
// target: its normalized URL plus its effective node-identity label. It MUST stay in
// lockstep with the runtime identity in internal/collector/nodemetrics
// (targetIdentity/effectiveInstance/normalizeTargetURL) so a config that passes
// validation and the set the collector actually dedups/keys baselines by agree.
func nodeMetricsTargetIdentity(t NodeMetricsTarget) string {
	return normalizeNodeMetricsURL(t.URL) + "\x00" + effectiveNodeMetricsInstance(t)
}

// effectiveNodeMetricsInstance mirrors the collector's node-identity resolution: the
// explicit Instance when set, else the host:port parsed from the URL (falling back to
// the raw URL when it cannot be parsed).
func effectiveNodeMetricsInstance(t NodeMetricsTarget) string {
	if t.Instance != "" {
		return t.Instance
	}
	u, err := url.Parse(t.URL)
	if err != nil || u.Host == "" {
		return t.URL
	}
	return u.Host
}

// normalizeNodeMetricsURL canonicalizes a target URL for identity comparison,
// lowercasing the (case-insensitive) scheme and host while leaving the path/query
// byte-exact. A URL that fails to parse falls back to its raw string. It mirrors the
// collector's normalizeTargetURL.
func normalizeNodeMetricsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String()
}
