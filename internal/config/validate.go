package config

import (
	"crypto/x509"
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/geoip"
	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v4/internal/listenaddr"
	"github.com/rknightion/tailscale2otel/v4/internal/redact"
	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
)

// Bounds on flows.retention. The floor is one bucket; the ceiling reflects that
// this is a process-memory ring, not durable storage.
const (
	minFlowsRetention              = time.Minute
	maxFlowsRetention              = 24 * time.Hour
	maxIngressWALReceiverBodyBytes = int64(64 << 20)
)

// Bounds on events.max_events (#300). The floor keeps the explorer usable at
// all; the ceiling reflects that this is a process-memory ring, not durable
// storage.
const (
	minEventsMaxEvents = 100
	// Floor for otlp.limits.*: comfortably above the truncation marker so a
	// bounded record still carries usable content.
	minOTLPLogLimitBytes = 64
	maxEventsMaxEvents   = 100000
)

// Bounds on flows.store.* (#294), the opt-in on-disk backend. Unlike
// flows.retention above this DOES size durable storage, so the ceilings are
// wider (up to a year of retention, a billion rows) — but still bounded, so a
// forgotten setting cannot grow the database without limit.
const (
	minFlowsStoreRetention     = time.Hour
	maxFlowsStoreRetention     = 365 * 24 * time.Hour
	minFlowsStoreMaxRows       = int64(10_000)
	maxFlowsStoreMaxRows       = int64(1_000_000_000)
	minFlowsStoreMaxExportRows = 100
	maxFlowsStoreMaxExportRows = 1_000_000
	minFlowsStoreQueueSize     = 64
	maxFlowsStoreQueueSize     = 1_048_576
	minFlowsStoreBatchSize     = 1
	maxFlowsStoreBatchSize     = 100_000
	minFlowsStoreFlushInterval = 100 * time.Millisecond
	maxFlowsStoreFlushInterval = 5 * time.Minute
	minFlowsStoreQueryTimeout  = time.Second
	maxFlowsStoreQueryTimeout  = 5 * time.Minute
	minFlowsStoreSweepInterval = time.Minute
	maxFlowsStoreSweepInterval = 24 * time.Hour
)

// oneOf reports whether v equals one of the allowed values.
func oneOf(v string, allowed ...string) bool {
	return slices.Contains(allowed, v)
}

// validateLogStreamingPublicURL validates the endpoint registered with the
// Tailscale log-streaming API. Tailscale permits plain HTTP only for private
// tailnet endpoints, and its current contract rejects every IPv4 literal.
func validateLogStreamingPublicURL(key, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s invalid: must be an absolute http or https URL with a host", key)
	}
	if !u.IsAbs() || u.Host == "" || u.Hostname() == "" || !validURLPort(u.Host) {
		return fmt.Errorf("%s invalid: must be an absolute http or https URL with a host", key)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%s invalid: scheme must be http or https", key)
	}
	if scheme == "http" {
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.To4() != nil {
			return fmt.Errorf("%s invalid: plain HTTP cannot use an IPv4 literal; use a private shared node hostname/FQDN or IPv6 literal", key)
		}
	}
	return nil
}

func validURLPort(host string) bool {
	if !strings.Contains(host, ":") {
		return true
	}
	if strings.HasPrefix(host, "[") {
		end := strings.LastIndex(host, "]")
		if end < 0 {
			return false
		}
		suffix := host[end+1:]
		if suffix == "" {
			return true
		}
		if !strings.HasPrefix(suffix, ":") || len(suffix) == 1 {
			return false
		}
		return validPortNumber(suffix[1:])
	}
	_, port, err := net.SplitHostPort(host)
	return err == nil && validPortNumber(port)
}

func validPortNumber(s string) bool {
	port, err := strconv.Atoi(s)
	return err == nil && port >= 1 && port <= 65535
}

func privateLogStreamingHTTPURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || strings.ToLower(u.Scheme) != "http" || u.Hostname() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip == nil || ip.To4() == nil
}

func privateLogStreamingHTTPWarning(key string) string {
	return key + " uses plain HTTP. Private log streaming requires node sharing plus policy access from " +
		"logstream@tailscale, with the appropriate device_invites and policy_file authority. " +
		"Local validation cannot verify that this endpoint is a private shared tailnet node."
}

// validateReceiverRoutes freezes the receiver-routing boundary: a routes list
// is a multi-tailnet-only replacement for legacy receiver identity. Listener,
// TLS and request budgets remain shared process settings, while each route owns
// its tailnet selector and credential.
func (c *Config) validateReceiverRoutes() error {
	multi := len(c.Tailnets) > 0
	runtimeNames := make(map[string]struct{}, len(c.Tailnets))
	for _, t := range c.Tailnets {
		runtimeNames[t.Name] = struct{}{}
	}

	if routes := c.Streaming.Routes; len(routes) > 0 {
		if !multi {
			return fmt.Errorf("streaming.routes requires configured multi-tailnet mode (tailnets: list)")
		}
		if !c.Streaming.Enabled {
			return fmt.Errorf("streaming.routes requires streaming.enabled: true")
		}
		// The default legacy path is retained to preserve single-tailnet defaults;
		// only an operator override conflicts with routes.
		if c.Streaming.Path != Default().Streaming.Path {
			return fmt.Errorf("streaming.path and streaming.routes are mutually exclusive")
		}
		if c.Streaming.Token != "" || c.Streaming.TokenFile != "" || c.Streaming.PublicURL != "" || c.Streaming.AutoConfigure {
			return fmt.Errorf("streaming.token/token_file/public_url/auto_configure and streaming.routes are mutually exclusive")
		}
		seenTailnet, seenPath := map[string]int{}, map[string]int{}
		for i, r := range routes {
			if strings.TrimSpace(r.Tailnet) == "" {
				return fmt.Errorf("streaming.routes[%d].tailnet: required", i)
			}
			if _, ok := runtimeNames[r.Tailnet]; !ok {
				return fmt.Errorf("streaming.routes[%d].tailnet %q does not match a configured runtime", i, r.Tailnet)
			}
			if j, ok := seenTailnet[r.Tailnet]; ok {
				return fmt.Errorf("streaming.routes[%d].tailnet %q duplicates routes[%d]", i, r.Tailnet, j)
			}
			seenTailnet[r.Tailnet] = i
			if err := validateReceiverPath(fmt.Sprintf("streaming.routes[%d].path", i), r.Path); err != nil || r.Path == "" {
				if err != nil {
					return err
				}
				return fmt.Errorf("streaming.routes[%d].path: required", i)
			}
			if j, ok := seenPath[r.Path]; ok {
				return fmt.Errorf("streaming.routes[%d].path %q duplicates routes[%d]", i, r.Path, j)
			}
			seenPath[r.Path] = i
			if r.Token != "" && r.TokenFile != "" {
				return fmt.Errorf("streaming.routes[%d].token and token_file are mutually exclusive", i)
			}
			if r.AutoConfigure && r.PublicURL == "" {
				return fmt.Errorf("streaming.routes[%d].auto_configure requires public_url", i)
			}
			if r.AutoConfigure {
				if err := validateLogStreamingPublicURL(fmt.Sprintf("streaming.routes[%d].public_url", i), r.PublicURL); err != nil {
					return err
				}
			}
		}
	} else if len(c.Tailnets) > 1 && c.Streaming.Enabled {
		return fmt.Errorf("streaming.enabled in multi-tailnet mode requires streaming.routes")
	}

	if routes := c.Webhook.Routes; len(routes) > 0 {
		if !multi {
			return fmt.Errorf("webhook.routes requires configured multi-tailnet mode (tailnets: list)")
		}
		if !c.Webhook.Enabled {
			return fmt.Errorf("webhook.routes requires webhook.enabled: true")
		}
		if c.Webhook.Path != Default().Webhook.Path {
			return fmt.Errorf("webhook.path and webhook.routes are mutually exclusive")
		}
		if c.Webhook.Secret != "" || c.Webhook.SecretFile != "" {
			return fmt.Errorf("webhook.secret/secret_file and webhook.routes are mutually exclusive")
		}
		seenTailnet := map[string]int{}
		hasTokenless, hasSigned := false, false
		for i, r := range routes {
			if strings.TrimSpace(r.Tailnet) == "" {
				return fmt.Errorf("webhook.routes[%d].tailnet: required", i)
			}
			if _, ok := runtimeNames[r.Tailnet]; !ok {
				return fmt.Errorf("webhook.routes[%d].tailnet %q does not match a configured runtime", i, r.Tailnet)
			}
			if j, ok := seenTailnet[r.Tailnet]; ok {
				return fmt.Errorf("webhook.routes[%d].tailnet %q duplicates routes[%d]", i, r.Tailnet, j)
			}
			seenTailnet[r.Tailnet] = i
			if r.Secret != "" && r.SecretFile != "" {
				return fmt.Errorf("webhook.routes[%d].secret and secret_file are mutually exclusive", i)
			}
			if r.Secret == "" && r.SecretFile == "" {
				hasTokenless = true
			} else {
				hasSigned = true
			}
		}
		if hasTokenless && hasSigned {
			return fmt.Errorf("webhook.routes cannot mix tokenless and signed routes on one listener")
		}
	} else if len(c.Tailnets) > 1 && c.Webhook.Enabled {
		return fmt.Errorf("webhook.enabled in multi-tailnet mode requires webhook.routes")
	}

	if err := c.GrafanaAnnotations.validate(); err != nil {
		return err
	}
	return nil
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

	// Response decode budgets vs. the container memory limit (#474). Decoding a
	// body costs several times its wire size (the decoder buffers the whole
	// top-level value, then materializes the object graph), so a budget anywhere
	// near the deployment's memory limit re-creates the OOM this control exists to
	// prevent. 64 MiB is a quarter of the 256 MiB the Helm chart ships by default.
	const budgetAdvisoryBytes = 64 << 20
	for _, b := range []struct {
		key string
		val int64
	}{
		{"tailscale.max_response_bytes", c.Tailscale.MaxResponseBytes},
		{"tailscale.max_log_response_bytes", c.Tailscale.MaxLogResponseBytes},
		{"headscale.max_response_bytes", c.Headscale.MaxResponseBytes},
	} {
		if b.val > budgetAdvisoryBytes {
			w = append(w, fmt.Sprintf("%s=%d bytes: decoding a JSON body costs several times its wire "+
				"size, so a budget this large can exceed the container memory limit (the Helm chart "+
				"defaults to 256Mi) and OOM the exporter. Raise the deployment's memory limit alongside it.",
				b.key, b.val))
		}
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

	// The same one-method-per-log-type rule applies to the export bucket, and it
	// is easier to get wrong here: the bucket holds the SAME records the API
	// returns, so a deployment that adds objectstore ingestion without turning
	// off the poller counts every connection twice.
	if c.Collectors.Flowlogs.Enabled && objectStoreSource(c.Collectors.Flowlogs.Source) && c.Streaming.Enabled {
		w = append(w, "collectors.flowlogs.source=objectstore with streaming.enabled=true: "+
			"flow logs can arrive from BOTH the export bucket and the stream receiver and be "+
			"double-counted. The bucket holds the same records the receiver does. Cross-source "+
			"de-duplication is a best-effort FAILSAFE, not a guarantee: choose ONE.")
	}
	// Per-destination advisories (overlap gap, flat layout, plaintext signing). In
	// multi-tailnet mode this is one set per tailnets[] entry, each naming its own key.
	if c.Collectors.Flowlogs.Enabled && objectStoreSource(c.Collectors.Flowlogs.Source) {
		w = append(w, c.flowObjectStoreWarnings()...)
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
	// The flow store's only consumer is the /flows page on the admin server. The
	// app declines to build the store at all when that surface is off — so this
	// is "your setting does nothing", not "you are leaking memory".
	if c.Flows.Enabled && (!c.Admin.Enabled || !c.Admin.LandingPage) {
		w = append(w, "flows.enabled=true has no effect: /flows is served by the admin "+
			"landing page, which is disabled (admin.enabled / admin.landing_page). The flow "+
			"store is not built, so no traffic history is retained. Enable the admin page to "+
			"use the flow view.")
	}

	// flows.store.directory (#294) opts into the on-disk backend. Same reasoning as
	// flows.enabled just above: the persistent store is only ever built
	// alongside the in-memory one, so a path set while /flows itself has no
	// consumer is dead configuration.
	if c.Flows.Store.Directory != "" && (!c.Flows.Enabled || !c.Admin.Enabled || !c.Admin.LandingPage) {
		w = append(w, "flows.store.directory is set but has no effect: the persistent flow store is only "+
			"built when flows.enabled=true and the admin landing page is on (admin.enabled / "+
			"admin.landing_page). No history is being written to disk.")
	}

	// Setting flows.store.directory is a genuine new data-at-rest exposure, not
	// just an operational footgun: the in-memory ring dies with the process,
	// but every row persisted to disk here — including the identities
	// (emails, node/tag names) a flow observation carries — survives a
	// restart and, unless the operator excludes the path, ends up in whatever
	// backs up that host.
	if c.Flows.Store.Directory != "" {
		w = append(w, "flows.store.directory is set: flow rows, including user identities such as "+
			"email addresses, will be written to disk at "+c.Flows.Store.Directory+" and will survive "+
			"restarts and appear in backups of that path, unlike the in-memory default. Make sure "+
			"that is intended and that the path is covered by your backup/retention policy.")
	}

	// flows.capacity_profile only tunes flowstore.Memory, the in-memory ring:
	// WithCapacityProfile is passed only when internal/app/admin_flows.go builds
	// that backend. Once flows.store.directory selects the persistent sqlite
	// backend instead, WithCapacityProfile is never called at all — the
	// sqlite store has no per-key capacity caps of its own, so the profile is
	// silently ignored, and the status page reports capacity_profile: "sqlite"
	// rather than the value the operator set. Same bug class as
	// cardinality.flow.source_port under metrics_mode: rollup — a setting that
	// looks active but is never actually applied — so flag it rather than let
	// the operator discover it by reading the source.
	if c.Flows.CapacityProfile != flowstore.ProfileDefault && c.Flows.Store.Directory != "" {
		w = append(w, fmt.Sprintf("flows.capacity_profile=%s: this only tunes the in-memory flow ring; "+
			"once flows.store.directory selects the persistent sqlite backend instead, this setting is "+
			"never read (the sqlite store has no per-key capacity caps of its own) and the status page "+
			"reports capacity_profile: \"sqlite\", not %q. Remove flows.capacity_profile, or set it back "+
			"to \"default\", to stop configuring a setting that has no effect while "+
			"flows.store.directory is set.", c.Flows.CapacityProfile, c.Flows.CapacityProfile))
	}

	// The two flow-cardinality knobs that are read by NOTHING under the wrong
	// metrics_mode (#525). Same bug class as flows.capacity_profile above, and
	// the same asymmetry decides whether it is worth a line: the operator asked
	// for MORE data and silently got none.
	//
	// (Deliberately NOT warned about: cardinality.per_entity.* and the
	// derp/subnet rollups when their owning collector is disabled. Those all
	// default to true, so an advisory would fire for everyone who merely turns a
	// collector off, about a value they never chose — and disabling a collector
	// disabling its own knobs is the requested outcome, not a lost signal.)
	//
	// Both ports are read only inside the raw per-connection family in
	// internal/flowlog/processor.go, which exists only in `all`/`both` mode. The
	// rollup family carries no L4 ports at all, by design, so under the DEFAULT
	// mode these are pure no-ops. One warning per key, not one combined message:
	// advisoryKey takes the first token, so a combined message could only ever be
	// attributed to one of the two on the status page.
	if c.Cardinality.Flow.MetricsMode == flowMetricsModeRollup {
		for _, p := range []struct {
			key string
			on  bool
		}{
			{"cardinality.flow.source_port", c.Cardinality.Flow.SourcePort},
			{"cardinality.flow.destination_port", c.Cardinality.Flow.DestinationPort},
		} {
			if !p.on {
				continue
			}
			w = append(w, fmt.Sprintf("%s=true has no effect under cardinality.flow.metrics_mode=%s "+
				"(the default): L4 ports are carried only by the raw per-connection metric family, and "+
				"the bounded rollup family deliberately has no port dimension, so no port will ever "+
				"reach a metric. Flow LOGS still carry both ports. To get ports on metrics set "+
				"cardinality.flow.metrics_mode to %q or %q — note that raises series cost sharply, "+
				"which is why the rollup family excludes them — otherwise remove %s.",
				p.key, flowMetricsModeRollup, flowMetricsModeAll, flowMetricsModeBoth, p.key))
		}
	}

	// rollup_top_n is consumed only by the rollup accumulator, which
	// internal/flowlog/processor.go builds only in rollup/both mode. Validate()
	// still range-checks it, which makes it look meaningful under `all`.
	// A value of 0 means "use the default" per the field doc, so it is not a
	// tuning and warning about it would be wrong rather than merely noisy.
	if c.Cardinality.Flow.MetricsMode == flowMetricsModeAll &&
		c.Cardinality.Flow.RollupTopN > 0 && c.Cardinality.Flow.RollupTopN != defaultFlowRollupTopN {
		w = append(w, fmt.Sprintf("cardinality.flow.rollup_top_n=%d has no effect under "+
			"cardinality.flow.metrics_mode=%s: it bounds the rollup accumulator, which is only built "+
			"in %q or %q mode, so nothing reads this value. Either switch metrics_mode to %q (bounded "+
			"cardinality) or remove cardinality.flow.rollup_top_n.",
			c.Cardinality.Flow.RollupTopN, flowMetricsModeAll,
			flowMetricsModeRollup, flowMetricsModeBoth, flowMetricsModeRollup))
	}

	// The event store's only consumer is the /events page on the admin server,
	// exactly like the flow store above (#300).
	if c.Events.Enabled && (!c.Admin.Enabled || !c.Admin.LandingPage) {
		w = append(w, "events.enabled=true has no effect: /events is served by the admin "+
			"landing page, which is disabled (admin.enabled / admin.landing_page). The event "+
			"store is not built, so no audit/webhook event history is retained. Enable the "+
			"admin page to use the event explorer.")
	}

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
	webhookCredentialMissing := c.Webhook.Secret == ""
	if len(c.Webhook.Routes) > 0 {
		webhookCredentialMissing = false
		for _, route := range c.Webhook.Routes {
			if route.Secret == "" {
				webhookCredentialMissing = true
				break
			}
		}
	}
	if c.Webhook.Enabled && webhookCredentialMissing {
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
	streamCredentialMissing := c.Streaming.Token == ""
	if len(c.Streaming.Routes) > 0 {
		streamCredentialMissing = false
		for _, route := range c.Streaming.Routes {
			if route.Token == "" {
				streamCredentialMissing = true
				break
			}
		}
	}
	if c.Streaming.Enabled && streamCredentialMissing {
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

	if c.Streaming.AutoConfigure && privateLogStreamingHTTPURL(c.Streaming.PublicURL) {
		w = append(w, privateLogStreamingHTTPWarning("streaming.public_url"))
	}
	for i, route := range c.Streaming.Routes {
		if route.AutoConfigure && privateLogStreamingHTTPURL(route.PublicURL) {
			w = append(w, privateLogStreamingHTTPWarning(fmt.Sprintf("streaming.routes[%d].public_url", i)))
		}
	}

	// streaming.public_url is handed to Tailscale as the log-streaming
	// destination, so every part of it leaves this process. Userinfo is a hard
	// error (see validateNoURLCredentials); a query string is allowed because a
	// signed-URL reverse proxy is a legitimate pattern no typed field replaces —
	// but the operator should know it is being exported (GHSA-rm3x-hhrj-94v4).
	// The value is never logged and the warning shows only the query KEY names.
	if c.Streaming.PublicURL != "" {
		if u, err := url.Parse(c.Streaming.PublicURL); err == nil && u.RawQuery != "" {
			w = append(w, "streaming.public_url carries a query string ("+redact.URL(c.Streaming.PublicURL)+
				"): this URL is registered with Tailscale as the log-streaming sink, so anything in it "+
				"leaves this process and is stored by the control plane. If the query is a reusable "+
				"credential, move the receiver's authentication to streaming.token instead.")
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

	// Geo dimensions on the RAW flow-metric families are the one genuinely
	// expensive GeoIP combination, and only when external addresses are being
	// collapsed. With collapse_external on, every external peer contributes a
	// single "external" series today; adding a country label splits it up to
	// ~250 ways, per transport/traffic_type/service already on the key.
	//
	// The ROLLUP family deliberately does NOT trigger this: it is top-N bounded
	// on (src,dst) pairs whatever dimensions the key carries, so geo there costs
	// label width, not series count. Warning about the default configuration
	// would just train operators to ignore the advisory.
	if c.Enrichment.GeoIP.Enabled && c.Cardinality.Flow.GeoDims &&
		c.Cardinality.Flow.CollapseExternal &&
		(c.Cardinality.Flow.MetricsMode == "all" || c.Cardinality.Flow.MetricsMode == "both") &&
		!c.Enrichment.GeoIP.AcknowledgeCardinality {
		w = append(w, "cardinality.flow.geo_dims=true with cardinality.flow.metrics_mode="+c.Cardinality.Flow.MetricsMode+
			" and collapse_external=true: on the RAW flow families the country label splits the single \"external\" "+
			"series into up to ~250, multiplying every transport/traffic_type/service combination. Prefer "+
			"metrics_mode: rollup (top-N bounded, so geo is nearly free there), or size cardinality.metric_limit "+
			"for the added series and set enrichment.geoip.acknowledge_cardinality=true to acknowledge. "+
			"Flow LOGS carry the geo and ASN attributes regardless of this toggle.")
	}
	// Credentials over plaintext http would be visible to anything on the path.
	// Only reachable via an explicitly overridden endpoint (a mirror or a test
	// server); MaxMind's own is https.
	if c.Enrichment.GeoIP.Enabled && c.Enrichment.GeoIP.Download.Enabled &&
		strings.HasPrefix(c.Enrichment.GeoIP.Download.Endpoint, "http://") {
		w = append(w, "enrichment.geoip.download.endpoint uses http:// — the MaxMind account ID and license key are "+
			"sent as HTTP Basic auth and would travel in the clear. Use https unless this is a trusted local mirror.")
	}

	if c.VersionChecks.Devices.Enabled && !c.Collectors.Devices.Enabled {
		w = append(w, "version_checks.devices.enabled=true but collectors.devices is disabled: per-device version-skew metrics need the devices collector and will not be emitted")
	}

	w = append(w, c.GrafanaAnnotations.warnings()...)
	// A rule whose source collector is off is silently dead: the writer starts,
	// the token works, and that category simply never produces a marker.
	if c.GrafanaAnnotations.Enabled() {
		if c.GrafanaAnnotations.Categories.ConfigChange.Enabled && !c.Collectors.Auditlogs.Enabled {
			w = append(w, "grafana_annotations.categories.config_change is enabled but collectors.auditlogs is disabled: "+
				"config-change annotations are derived from the audit log and none will ever be written")
		}
		if c.GrafanaAnnotations.Categories.Expiry.Enabled &&
			!c.Collectors.Keys.Enabled && !c.Collectors.Devices.Enabled {
			w = append(w, "grafana_annotations.categories.expiry is enabled but both collectors.keys and "+
				"collectors.devices are disabled: expiry annotations are derived from those collectors and "+
				"none will ever be written")
		}
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
// runs the poller (as opposed to relying solely on the stream receiver or the
// object store). It mirrors app.pollSource; an empty source defaults to polling.
func pollsSource(s string) bool {
	return s == "" || s == "poll" || s == "both"
}

// objectStoreSource reports whether flow logs are ingested from the export
// bucket. It mirrors app.objectStoreSource.
func objectStoreSource(s string) bool { return s == "objectstore" }

func plaintextRemoteObjectStoreEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
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
// (admin.tls / prometheus.tls / webhook.tls): cert_file and key_file must be set together
// (both-or-neither), and any set path must exist and be readable now — not
// discovered as an opaque http.Server.ListenAndServeTLS failure at startup.
// label is the config prefix (e.g. "admin") used in error messages.
func (c *Config) validateTLSFiles(label, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("%s.tls.cert_file and %s.tls.key_file must both be set or both be empty "+
			"(got cert_file=%q, key_file=%q)", label, label, certFile, keyFile)
	}
	if certFile == "" {
		return nil
	}
	for _, f := range [...]struct {
		field string
		path  string
	}{
		{"cert_file", certFile},
		{"key_file", keyFile},
	} {
		if _, err := safefile.ReadRegular(f.path, safefile.MaxPEMBytes, safefile.AllowSymlink); err != nil {
			return fmt.Errorf("%s.tls.%s %s: %w", label, f.field,
				c.pathForError(label+".tls."+f.field, f.path), err)
		}
	}
	// Readable is not the same as usable. /dev/null is readable and parses as an
	// empty PEM; a certificate paired with a different key is readable twice over.
	// Both used to reach ListenAndServeTLS, which runs on a goroutine AFTER
	// startup — so the failure was a log line on a listener that never served,
	// not a refusal to start. Load the pair the way crypto/tls will (#305).
	//
	// The error is deliberately not wrapped with %w-into-detail beyond what
	// LoadX509KeyPair says: its messages describe the FORM of the failure
	// ("failed to find any PEM data", "private key does not match public key")
	// and never echo key bytes. Config errors get logged and pasted into issues.
	if _, err := safefile.LoadX509KeyPair(certFile, keyFile, safefile.MaxPEMBytes); err != nil {
		return fmt.Errorf("%s.tls cert_file %q + key_file %q do not form a usable keypair: %w",
			label, certFile, keyFile, err)
	}
	return nil
}

// validateCAFile checks that a CA bundle is readable AND actually yields at
// least one certificate. Readability alone is not enough for the same reason it
// was not enough for keypairs (#305): x509.CertPool.AppendCertsFromPEM reports
// failure by returning false, and a pool built from an empty or non-PEM file
// silently trusts nothing — so mutual TLS would reject every scraper, or an
// outbound client would fail every handshake, with no hint why.
func (c *Config) validateCAFile(field, path string) error {
	pem, err := safefile.ReadRegular(path, safefile.MaxPEMBytes, safefile.AllowSymlink)
	if err != nil {
		return fmt.Errorf("%s %s: %w", field, c.pathForError(field, path), err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return fmt.Errorf("%s %q contains no usable PEM certificate: a CA bundle that parses to an "+
			"empty pool trusts nothing, which fails every handshake rather than erroring here", field, path)
	}
	return nil
}

// validateClientKeypair enforces the both-or-neither + actually-loadable
// contract for an OUTBOUND client certificate. Same rules as
// validateTLSFiles, but the field prefix is the caller's full config path
// because outbound blocks do not all sit under a `.tls` suffix the way the
// listener blocks do.
func (c *Config) validateClientKeypair(field, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("%s.cert_file and %s.key_file must both be set or both be empty "+
			"(got cert_file=%q, key_file=%q)", field, field, certFile, keyFile)
	}
	if certFile == "" {
		return nil
	}
	if _, err := safefile.LoadX509KeyPair(certFile, keyFile, safefile.MaxPEMBytes); err != nil {
		return fmt.Errorf("%s cert_file %q + key_file %q do not form a usable keypair: %w",
			field, certFile, keyFile, err)
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
	_, err := safefile.ReadRegular(a.WorkloadIdentity.IDTokenFile, safefile.MaxSecretBytes, safefile.AllowSymlink)
	if err != nil {
		return fmt.Errorf("%s.workload_identity.id_token_file %q: %w", label, a.WorkloadIdentity.IDTokenFile, err)
	}
	return nil
}

// Validate reports the first configuration error it finds, or nil if the
// Config is valid. It runs the exact same rule set as Diagnostics() (see
// validationChecks), stopping at the first failure — this preserves the
// historical "first error, same order" contract that config.Load and other
// existing callers depend on. Diagnostics() runs the identical rules without
// stopping, to report every independent failure in one pass (#307).
func (c *Config) Validate() error {
	_, err := runChecks(c.validationChecks(), true)
	return err
}

// resolvedProvider returns c.Provider with the "tailscale" default applied,
// matching the historical local `provider` variable Validate() used to
// compute once and read from every subsequent check.
func (c *Config) resolvedProvider() string {
	if c.Provider == "" {
		return "tailscale"
	}
	return c.Provider
}

// validationChecks builds the full ordered rule set Validate() and
// Diagnostics() both run. Each entry is a faithful copy of a rule from the
// pre-#307 Validate() body — same condition, same error text — wrapped so it
// can be run either fail-fast (Validate) or collected (Diagnostics).
//
// Loop-collapsing convention: a rule that iterates a dynamically-sized list
// (c.Tailnets, node_metrics targets/metric_allow/metric_deny) is ONE entry
// here and reports only the FIRST violation within that list, matching what
// Validate() always did for that loop. A rule that iterates a small FIXED set
// of named things (the four listeners, the two log collectors, the four TLS
// blocks) is decomposed into one entry per name, because those are
// independent problems an operator can and should see all of at once.
//
// Dependent checks: every entry here reads Config fields but never mutates
// them, so a later entry's own gating condition (e.g. "resolvedProvider() ==
// headscale", "OTLP.Protocol == grpc") is exactly as true or false whether or
// not an earlier, unrelated entry failed. That is what makes an invalid
// provider value skip every headscale.*/tailscale.* entry, and an invalid
// otlp.protocol skip both protocol-specific endpoint-shape entries, with no
// separate dependency graph required — see
// TestDiagnostics_SkipsDependentChecks.
func (c *Config) validationChecks() []configCheck {
	var checks []configCheck
	add := func(path, remediation string, fn func() error) {
		checks = append(checks, configCheck{path: path, remediation: remediation, fn: fn})
	}

	// A "*_file" secret sibling whose paired value field was ALSO set (recorded
	// by resolveSecretFiles at Load).
	add("", "Set only the value field or the _file field for this secret, not both.", func() error {
		if len(c.secretFileConflicts) > 0 {
			return fmt.Errorf("%s: set only one, not both (value XOR file)", c.secretFileConflicts[0])
		}
		return nil
	})

	// Credentials embedded in a URL's userinfo are rejected before any other
	// shape check, so no later error message can echo one back in its diagnostic
	// (GHSA-qch3-gwff-r6pf, GHSA-jp5c-3282-6882, GHSA-h5p7-qj62-m8qx,
	// GHSA-rm3x-hhrj-94v4).
	add("", "Move the credential into the field's dedicated secret setting named in the error text.", func() error {
		return c.validateNoURLCredentials()
	})

	add("provider", oneOfRemediation("provider", "tailscale", "headscale"), func() error {
		if !oneOf(c.resolvedProvider(), "tailscale", "headscale") {
			return fmt.Errorf("provider: must be \"tailscale\" or \"headscale\", got %q", c.resolvedProvider())
		}
		return nil
	})
	add("headscale.url", "Set headscale.url to your Headscale server's base URL.", func() error {
		if c.resolvedProvider() != "headscale" {
			return nil
		}
		if strings.TrimSpace(c.Headscale.URL) == "" {
			return fmt.Errorf("headscale.url: required when provider=headscale")
		}
		u, err := url.Parse(strings.TrimSpace(c.Headscale.URL))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("headscale.url must be a valid absolute HTTP(S) URL")
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("headscale.url must contain only a scheme and host; put credentials in headscale.api_key")
		}
		if u.Scheme != "https" && !httpguard.IsLoopbackHost(u.Host) {
			return fmt.Errorf("headscale.url must use HTTPS except for a loopback development endpoint")
		}
		return nil
	})
	add("headscale.api_key", "Set headscale.api_key (or TS2OTEL_HEADSCALE__API_KEY).", func() error {
		if c.resolvedProvider() != "headscale" {
			return nil
		}
		if c.Headscale.APIKey == "" {
			return fmt.Errorf("headscale.api_key: required when provider=headscale (set TS2OTEL_HEADSCALE__API_KEY)")
		}
		return nil
	})
	add("headscale.max_response_bytes", "Set headscale.max_response_bytes to a positive byte count.", func() error {
		if c.resolvedProvider() != "headscale" {
			return nil
		}
		// Response decode budget (#488). Zero/negative would mean "no body may be
		// decoded at all", which silently breaks every collector, so reject it here
		// rather than at the first poll.
		if c.Headscale.MaxResponseBytes <= 0 {
			return fmt.Errorf("headscale.max_response_bytes must be > 0 (bytes); it caps a single " +
				"Headscale API response body before decoding")
		}
		return nil
	})

	// log_level is documented (configuration.md) and framed as a validated enum,
	// so reject a value outside the set here rather than silently failing open to
	// info in cmd/tailscale2otel.parseLevel (the mismatch #106 flagged).
	add("log_format", oneOfRemediation("log_format", "text", "json"), func() error {
		if !oneOf(c.LogFormat, "text", "json") {
			return fmt.Errorf("log_format %q invalid: must be text or json", c.LogFormat)
		}
		return nil
	})
	add("log_level", oneOfRemediation("log_level", "debug", "info", "warn", "error"), func() error {
		if !oneOf(c.LogLevel, "debug", "info", "warn", "error") {
			return fmt.Errorf("log_level %q invalid: must be one of debug, info, warn, error", c.LogLevel)
		}
		return nil
	})

	add("otlp.protocol", oneOfRemediation("otlp.protocol", "grpc", "http", "stdout"), func() error {
		if !oneOf(c.OTLP.Protocol, "grpc", "http", "stdout") {
			return fmt.Errorf("otlp.protocol %q invalid: must be one of grpc, http, stdout", c.OTLP.Protocol)
		}
		return nil
	})
	// The gRPC OTLP exporter (otlp*grpc.WithEndpoint) dials a host:port address,
	// NOT a URL: a scheme or path (e.g. "https://gw.example/otlp", which is the
	// correct shape for the http protocol) makes the gRPC dialer fail to connect.
	// Catch the mismatch at load time rather than as an opaque runtime dial error.
	// (http endpoints are full URLs; stdout ignores the endpoint entirely.)
	add("otlp.endpoint", "Use a bare host:port with no scheme or path for otlp.protocol=grpc.", func() error {
		if c.OTLP.Protocol == "grpc" && c.OTLP.Endpoint != "" {
			if strings.Contains(c.OTLP.Endpoint, "://") || strings.Contains(c.OTLP.Endpoint, "/") {
				return fmt.Errorf("otlp.endpoint invalid for otlp.protocol=grpc: use a host:port " +
					"address with no scheme or path (e.g. otlp-gateway-prod-us-central-0.grafana.net:443); " +
					"a full URL is only valid for protocol=http")
			}
		}
		return nil
	})
	// The http OTLP exporter dials a full URL. A scheme-less host:port (the grpc
	// shape) parses to an empty host and silently zeroes the endpoint at runtime
	// rather than failing at load; require an http/https scheme and a host so the
	// mismatch is caught here (#52b).
	add("otlp.endpoint", "Use a full http:// or https:// URL with a host for otlp.protocol=http.", func() error {
		if c.OTLP.Protocol == "http" {
			u, err := url.Parse(c.OTLP.Endpoint)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("otlp.endpoint invalid for otlp.protocol=http: use a full URL with " +
					"an http:// or https:// scheme and a host (e.g. " +
					"https://otlp-gateway-prod-us-central-0.grafana.net/otlp); a scheme-less host:port " +
					"is only valid for protocol=grpc")
			}
		}
		return nil
	})
	add("otlp.metric_interval", "Set otlp.metric_interval to a positive duration.", func() error {
		if c.OTLP.MetricInterval.D() <= 0 {
			return fmt.Errorf("otlp.metric_interval must be > 0 (got %v); a zero or negative interval panics time.NewTicker at startup", c.OTLP.MetricInterval.D())
		}
		return nil
	})
	add("otlp.metric_export_batch_size", "Set otlp.metric_export_batch_size to a positive integer.", func() error {
		if c.OTLP.MetricExportBatchSize < 1 {
			return fmt.Errorf("otlp.metric_export_batch_size must be > 0 (got %d); unbounded cumulative metric requests can exceed backend ingest limits", c.OTLP.MetricExportBatchSize)
		}
		return nil
	})

	// Every enabled HTTP listener needs an address it can actually bind, and its
	// own. Two enabled servers on the same address race on net.Listen: one binds,
	// the other logs an ERROR and the process keeps running with that surface
	// silently dead. Check ALL four listeners pairwise, not just admin/prometheus
	// (#52f).
	//
	// Both halves compare CANONICAL addresses rather than raw strings (#306).
	// Raw strings had no opinion on whether an address was bindable at all — a
	// bare "9091" with no colon, or a port outside the 16-bit range, validated
	// fine and then failed inside net.Listen after startup — and they could not
	// see ":9091" and "0.0.0.0:9091" as the same socket.
	listenersOf := func() []struct {
		name    string
		addr    string
		enabled bool
	} {
		return []struct {
			name    string
			addr    string
			enabled bool
		}{
			{"admin.listen", c.Admin.Listen, c.Admin.Enabled},
			{"prometheus.listen", c.Prometheus.Listen, c.Prometheus.Enabled},
			{"streaming.listen", c.Streaming.Listen, c.Streaming.Enabled},
			{"webhook.listen", c.Webhook.Listen, c.Webhook.Enabled},
		}
	}
	add("", "Set the named listener to a bindable host:port address.", func() error {
		for _, l := range listenersOf() {
			// A disabled listener binds nothing, so its address is never exercised
			// and is not a reason to refuse to start.
			if !l.enabled {
				continue
			}
			if _, err := listenaddr.Canonical(l.addr); err != nil {
				return fmt.Errorf("%s: %w", l.name, err)
			}
		}
		return nil
	})
	add("", "Give each enabled HTTP listener its own address.", func() error {
		listeners := listenersOf()
		for i := range listeners {
			for j := i + 1; j < len(listeners); j++ {
				a, b := listeners[i], listeners[j]
				if a.enabled && b.enabled && listenaddr.Collides(a.addr, b.addr) {
					return fmt.Errorf("%s (%q) and %s (%q) bind the same socket: each enabled HTTP listener "+
						"needs its own address (only one wins the net.Listen race; the other dies silently). "+
						"A wildcard bind owns its port on every interface, so it collides with any address on "+
						"that port", a.name, a.addr, b.name, b.addr)
				}
			}
		}
		return nil
	})

	// flows.retention sizes an in-memory ring of one-minute buckets, so a bad
	// value is a memory fault rather than a slow query. Below a minute the ring
	// cannot hold a single bucket; beyond a day it is the wrong storage for the
	// job. Unchecked when the view is off — the store is never built.
	add("flows.retention", "Set flows.retention between 1m and 24h.", func() error {
		if !c.Flows.Enabled {
			return nil
		}
		if d := c.Flows.Retention.D(); d < minFlowsRetention || d > maxFlowsRetention {
			return fmt.Errorf("flows.retention must be between %v and %v (got %v): it sizes an "+
				"in-memory ring of one-minute buckets, not a database",
				minFlowsRetention, maxFlowsRetention, d)
		}
		return nil
	})
	add("flows.max_future_skew", "Set flows.max_future_skew between 0 and 1h.", func() error {
		if !c.Flows.Enabled {
			return nil
		}
		if d := c.Flows.MaxFutureSkew.D(); d < 0 || d > time.Hour {
			return fmt.Errorf("flows.max_future_skew must be between 0 and 1h (got %v)", d)
		}
		return nil
	})

	// flows.capacity_profile trades memory for fidelity on every per-bucket
	// dimension and the raw-connection ring (#329). It is a closed enum, not a
	// raw number: flowstore.CapsForProfile is the single source of truth for
	// which names exist and what each maps to, so a rename or a fourth profile
	// only has to change there. Unchecked when the view is off — the store is
	// never built.
	add("flows.capacity_profile", `Set flows.capacity_profile to one of "compact", "default" or "expanded".`, func() error {
		if !c.Flows.Enabled {
			return nil
		}
		if _, ok := flowstore.CapsForProfile(c.Flows.CapacityProfile); !ok {
			return fmt.Errorf(`flows.capacity_profile must be one of "compact", "default" or "expanded" (got %q): `+
				`it selects a fixed, hard-coded capacity/memory preset, not an arbitrary limit`,
				c.Flows.CapacityProfile)
		}
		return nil
	})

	// flows.store.* (#294) configures the opt-in on-disk backend
	// (internal/flowstore/sqlitestore). Every check below is a no-op both when
	// the view is off (mirroring the in-memory checks above — the store is
	// never built) AND when flows.store.directory is empty: an empty path is the
	// documented "memory only" default, so there is nothing to validate.
	flowsStoreActive := func() bool { return c.Flows.Enabled && c.Flows.Store.Directory != "" }

	// No absolute-path check here, deliberately. flows.store.directory is
	// registered in pathFields(), so a relative value is resolved against the
	// config file's own directory before this runs (#310) — exactly as
	// ingress_wal.directory and checkpoint.file_path are. Rejecting a relative
	// path would contradict that contract and make this the one filesystem
	// field in the config that behaves differently from every other.
	add("flows.store.retention",
		fmt.Sprintf("Set flows.store.retention between %v and %v.", minFlowsStoreRetention, maxFlowsStoreRetention),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if d := c.Flows.Store.Retention.D(); d < minFlowsStoreRetention || d > maxFlowsStoreRetention {
				return fmt.Errorf("flows.store.retention must be between %v and %v (got %v)",
					minFlowsStoreRetention, maxFlowsStoreRetention, d)
			}
			return nil
		})
	add("flows.store.max_rows",
		fmt.Sprintf("Set flows.store.max_rows between %d and %d.", minFlowsStoreMaxRows, maxFlowsStoreMaxRows),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if n := c.Flows.Store.MaxRows; n < minFlowsStoreMaxRows || n > maxFlowsStoreMaxRows {
				return fmt.Errorf("flows.store.max_rows must be between %d and %d (got %d)",
					minFlowsStoreMaxRows, maxFlowsStoreMaxRows, n)
			}
			return nil
		})
	add("flows.store.max_export_rows",
		fmt.Sprintf("Set flows.store.max_export_rows between %d and %d.", minFlowsStoreMaxExportRows, maxFlowsStoreMaxExportRows),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if n := c.Flows.Store.MaxExportRows; n < minFlowsStoreMaxExportRows || n > maxFlowsStoreMaxExportRows {
				return fmt.Errorf("flows.store.max_export_rows must be between %d and %d (got %d)",
					minFlowsStoreMaxExportRows, maxFlowsStoreMaxExportRows, n)
			}
			return nil
		})
	add("flows.store.queue_size",
		fmt.Sprintf("Set flows.store.queue_size between %d and %d.", minFlowsStoreQueueSize, maxFlowsStoreQueueSize),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if n := c.Flows.Store.QueueSize; n < minFlowsStoreQueueSize || n > maxFlowsStoreQueueSize {
				return fmt.Errorf("flows.store.queue_size must be between %d and %d (got %d)",
					minFlowsStoreQueueSize, maxFlowsStoreQueueSize, n)
			}
			return nil
		})
	// flows.store.batch_size is bounded on its own AND cannot exceed
	// queue_size: a transaction batch larger than the channel it drains from
	// can never fill, so it is a silent way to configure a batch that is
	// always partial.
	add("flows.store.batch_size",
		fmt.Sprintf("Set flows.store.batch_size between %d and %d, and no larger than flows.store.queue_size.",
			minFlowsStoreBatchSize, maxFlowsStoreBatchSize),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			n := c.Flows.Store.BatchSize
			if n < minFlowsStoreBatchSize || n > maxFlowsStoreBatchSize {
				return fmt.Errorf("flows.store.batch_size must be between %d and %d (got %d)",
					minFlowsStoreBatchSize, maxFlowsStoreBatchSize, n)
			}
			if n > c.Flows.Store.QueueSize {
				return fmt.Errorf("flows.store.batch_size (%d) must not exceed flows.store.queue_size (%d): "+
					"a batch larger than the queue it drains from can never fill",
					n, c.Flows.Store.QueueSize)
			}
			return nil
		})
	add("flows.store.flush_interval",
		fmt.Sprintf("Set flows.store.flush_interval between %v and %v.", minFlowsStoreFlushInterval, maxFlowsStoreFlushInterval),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if d := c.Flows.Store.FlushInterval.D(); d < minFlowsStoreFlushInterval || d > maxFlowsStoreFlushInterval {
				return fmt.Errorf("flows.store.flush_interval must be between %v and %v (got %v)",
					minFlowsStoreFlushInterval, maxFlowsStoreFlushInterval, d)
			}
			return nil
		})
	add("flows.store.query_timeout",
		fmt.Sprintf("Set flows.store.query_timeout between %v and %v.", minFlowsStoreQueryTimeout, maxFlowsStoreQueryTimeout),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if d := c.Flows.Store.QueryTimeout.D(); d < minFlowsStoreQueryTimeout || d > maxFlowsStoreQueryTimeout {
				return fmt.Errorf("flows.store.query_timeout must be between %v and %v (got %v)",
					minFlowsStoreQueryTimeout, maxFlowsStoreQueryTimeout, d)
			}
			return nil
		})
	add("flows.store.sweep_interval",
		fmt.Sprintf("Set flows.store.sweep_interval between %v and %v.", minFlowsStoreSweepInterval, maxFlowsStoreSweepInterval),
		func() error {
			if !flowsStoreActive() {
				return nil
			}
			if d := c.Flows.Store.SweepInterval.D(); d < minFlowsStoreSweepInterval || d > maxFlowsStoreSweepInterval {
				return fmt.Errorf("flows.store.sweep_interval must be between %v and %v (got %v)",
					minFlowsStoreSweepInterval, maxFlowsStoreSweepInterval, d)
			}
			return nil
		})

	// events.max_events sizes an in-memory ring of individual audit/webhook
	// events (#300), so a bad value is a memory fault, mirroring flows.retention
	// above. Unchecked when the view is off — the store is never built.
	add("events.max_events", "Set events.max_events between 100 and 100000.", func() error {
		if !c.Events.Enabled {
			return nil
		}
		if n := c.Events.MaxEvents; n < minEventsMaxEvents || n > maxEventsMaxEvents {
			return fmt.Errorf("events.max_events must be between %d and %d (got %d): it sizes an "+
				"in-memory ring of individual events, not a database",
				minEventsMaxEvents, maxEventsMaxEvents, n)
		}
		return nil
	})

	// Listener TLS blocks: both-or-neither, every configured file readable, and
	// the pair must actually LOAD. Readability alone let /dev/null and a
	// cert-with-the-wrong-key through, and both then failed inside
	// ListenAndServeTLS — on a goroutine, after startup, as a log line on a
	// listener that never served rather than a refusal to run (#305).
	add("admin.tls", "Set both admin.tls.cert_file and admin.tls.key_file to a matching, loadable keypair.", func() error {
		return c.validateTLSFiles("admin", c.Admin.TLS.CertFile, c.Admin.TLS.KeyFile)
	})
	add("prometheus.tls", "Set both prometheus.tls.cert_file and prometheus.tls.key_file to a matching, loadable keypair.", func() error {
		return c.validateTLSFiles("prometheus", c.Prometheus.TLS.CertFile, c.Prometheus.TLS.KeyFile)
	})
	// Client-cert auth on /metrics is worthless without server TLS: crypto/tls
	// only ever asks for a client certificate during a TLS handshake, so a
	// client_ca_file on a plaintext listener is silently inert — exactly the
	// half-configured-looks-configured shape #305 refused to keep tolerating.
	add("prometheus.tls.client_ca_file", "Set prometheus.tls.cert_file and key_file too: a client CA only takes effect on a TLS listener.", func() error {
		if c.Prometheus.TLS.ClientCAFile == "" {
			return nil
		}
		if c.Prometheus.TLS.CertFile == "" || c.Prometheus.TLS.KeyFile == "" {
			return fmt.Errorf("prometheus.tls.client_ca_file is set but prometheus.tls.cert_file/key_file are not: " +
				"client-certificate authentication requires the listener to serve TLS, otherwise it never runs")
		}
		return c.validateCAFile("prometheus.tls.client_ca_file", c.Prometheus.TLS.ClientCAFile)
	})
	add("prometheus.tls.client_auth", oneOfRemediation("prometheus.tls.client_auth",
		"require_and_verify", "verify_if_given", "require", "request", "none"), func() error {
		m := c.Prometheus.TLS.ClientAuth
		if m == "" {
			return nil
		}
		if !oneOf(m, "require_and_verify", "verify_if_given", "require", "request", "none") {
			return fmt.Errorf("prometheus.tls.client_auth %q invalid: must be one of "+
				"require_and_verify, verify_if_given, require, request, none", m)
		}
		// verify_if_given and require_and_verify are the only modes that check the
		// presented chain, and both need something to check it against.
		if (m == "require_and_verify" || m == "verify_if_given") && c.Prometheus.TLS.ClientCAFile == "" {
			return fmt.Errorf("prometheus.tls.client_auth %q requires prometheus.tls.client_ca_file: "+
				"there is no CA to verify the client certificate against", m)
		}
		return nil
	})
	// A limit below the truncation marker's own length would leave no room for
	// any payload, so every record would truncate to just the marker. That is a
	// silent total data loss dressed up as a working config.
	add("otlp.limits", "Set otlp.limits.log_body_bytes and log_attribute_value_bytes to at least 64.", func() error {
		for _, f := range [...]struct {
			field string
			value int
		}{
			{"log_body_bytes", c.OTLP.Limits.LogBodyBytes},
			{"log_attribute_value_bytes", c.OTLP.Limits.LogAttributeValueBytes},
		} {
			if f.value < minOTLPLogLimitBytes {
				return fmt.Errorf("otlp.limits.%s must be at least %d bytes (got %d): a smaller "+
					"bound leaves no room for content beside the truncation marker, so every "+
					"record would collapse to the marker alone. There is no unlimited setting — "+
					"set a large value if you want effectively no bound",
					f.field, minOTLPLogLimitBytes, f.value)
			}
		}
		return nil
	})
	add("prometheus.max_requests_in_flight", "Set prometheus.max_requests_in_flight to a positive count.", func() error {
		if n := c.Prometheus.MaxRequestsInFlight; n <= 0 {
			return fmt.Errorf("prometheus.max_requests_in_flight must be positive (got %d)", n)
		}
		return nil
	})
	add("prometheus.timeout", "Set prometheus.timeout to 0 (no timeout) or a positive duration.", func() error {
		if d := c.Prometheus.Timeout.D(); d < 0 {
			return fmt.Errorf("prometheus.timeout must be 0 (no timeout) or positive (got %s)", d)
		}
		return nil
	})
	// The Pyroscope upload client is OUTBOUND, so its keypair is a client
	// certificate for mTLS and is both-or-neither just like a listener's;
	// ca_file is independent (trusting a private CA needs no client cert).
	add("profiling.pyroscope.tls", "Set both profiling.pyroscope.tls.cert_file and key_file to a matching, loadable keypair.", func() error {
		return c.validateClientKeypair("profiling.pyroscope.tls",
			c.Profiling.Pyroscope.TLS.CertFile, c.Profiling.Pyroscope.TLS.KeyFile)
	})
	add("profiling.pyroscope.tls.ca_file", "Point profiling.pyroscope.tls.ca_file at a readable PEM bundle containing at least one certificate.", func() error {
		if c.Profiling.Pyroscope.TLS.CAFile == "" {
			return nil
		}
		return c.validateCAFile("profiling.pyroscope.tls.ca_file", c.Profiling.Pyroscope.TLS.CAFile)
	})
	// Streaming was the one listener with NO TLS validation at all — and the one
	// where a half-configured block is worst: stream.Run serves plain HTTP unless
	// BOTH fields are set, so a cert with a missing key silently downgraded a log
	// receiver to plaintext while looking configured for TLS (#305).
	add("streaming.tls", "Set both streaming.tls.cert_file and streaming.tls.key_file to a matching, loadable keypair.", func() error {
		return c.validateTLSFiles("streaming", c.Streaming.TLS.CertFile, c.Streaming.TLS.KeyFile)
	})
	add("webhook.tls", "Set both webhook.tls.cert_file and webhook.tls.key_file to a matching, loadable keypair.", func() error {
		return c.validateTLSFiles("webhook", c.Webhook.TLS.CertFile, c.Webhook.TLS.KeyFile)
	})

	add("tailscale.auth.method", oneOfRemediation("tailscale.auth.method", "oauth", "apikey", "workload_identity"), func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
		if !oneOf(c.Tailscale.Auth.Method, "oauth", "apikey", "workload_identity") {
			return fmt.Errorf("tailscale.auth.method %q invalid: must be one of oauth, apikey, workload_identity", c.Tailscale.Auth.Method)
		}
		return nil
	})
	add("tailscale.auth.workload_identity", "Set both tailscale.auth.workload_identity.client_id and id_token_file.", func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
		return validateWorkloadIdentity("tailscale.auth", c.Tailscale.Auth)
	})
	// Response decode budgets (#474). Zero/negative would mean "no body may be
	// decoded at all", which silently breaks every collector, so reject it here
	// rather than at the first poll.
	add("tailscale.max_response_bytes", "Set tailscale.max_response_bytes to a positive byte count.", func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
		if c.Tailscale.MaxResponseBytes <= 0 {
			return fmt.Errorf("tailscale.max_response_bytes must be > 0 (bytes); it caps a single " +
				"snapshot-endpoint response body before decoding")
		}
		return nil
	})
	add("tailscale.max_log_response_bytes", "Set tailscale.max_log_response_bytes to a positive byte count.", func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
		if c.Tailscale.MaxLogResponseBytes <= 0 {
			return fmt.Errorf("tailscale.max_log_response_bytes must be > 0 (bytes); it caps a single " +
				"flow-log/audit-log response body before decoding")
		}
		return nil
	})
	// Single tailscale: block and a tailnets: list are mutually exclusive.
	// Default() seeds tailscale.tailnet="-" (the "principal's default tailnet"
	// sentinel), so only treat an EXPLICIT non-sentinel name as a conflict.
	add("tailscale.tailnet", "Use either the single tailscale: block or the tailnets: list, not both.", func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
		if len(c.Tailnets) > 0 && c.Tailscale.Tailnet != "" && c.Tailscale.Tailnet != "-" {
			return fmt.Errorf("tailscale.tailnet=%q and tailnets: are mutually exclusive — "+
				"use the single tailscale: block OR the tailnets: list, not both", c.Tailscale.Tailnet)
		}
		return nil
	})
	// Multi-tailnet: every entry needs a name + a valid auth method (creds are
	// never inherited from the top-level block). One entry here covers the whole
	// list (loop-collapsing convention: reports the first violation only).
	add("tailnets", "Give every tailnets[] entry a unique name and a valid auth.method.", func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
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
		return nil
	})
	// Single-tailnet mode needs a tailnet name. The default is the "-" sentinel
	// (the principal's default tailnet), so an empty value can only come from an
	// explicit tailscale.tailnet: "" (or TS2OTEL_TAILSCALE__TAILNET=""). Catch it
	// here with actionable guidance rather than letting tsapi.NewClient fail at
	// startup with the opaque "Tailnet is required".
	add("tailscale.tailnet", `Set tailscale.tailnet to your tailnet's name, or "-" for the default.`, func() error {
		if c.resolvedProvider() != "tailscale" {
			return nil
		}
		if len(c.Tailnets) == 0 && strings.TrimSpace(c.Tailscale.Tailnet) == "" {
			return fmt.Errorf("tailscale.tailnet: required — set your tailnet's name " +
				"(e.g. \"example.com\") or \"-\" for the auth principal's default tailnet")
		}
		return nil
	})

	add("streaming.routes", "See the error text: routes conflicts vary by field.", func() error {
		return c.validateReceiverRoutes()
	})
	add("checkpoint.store", oneOfRemediation("checkpoint.store", "memory", "file"), func() error {
		if !oneOf(c.Checkpoint.Store, "memory", "file") {
			return fmt.Errorf("checkpoint.store %q invalid: must be one of memory, file", c.Checkpoint.Store)
		}
		return nil
	})
	add("ingress_wal", "See the error text: ingress_wal has several independent field constraints.", func() error {
		return c.validateIngressWAL()
	})

	// Source + window-timing validation. Only the two log collectors have a
	// source; an empty value defaults to poll. flowlogs and auditlogs are
	// decomposed into independent per-collector entries (not collapsed into one
	// loop) since they are two distinct, equally-likely-to-be-misconfigured
	// settings an operator should see both problems for at once.
	type logCollectorSpec struct {
		name            string
		enabled         func() bool
		source          func() string
		lag             func() time.Duration
		initialLookback func() time.Duration
		maxWindow       func() time.Duration
		interval        func() time.Duration
	}
	logCollectorSpecs := []logCollectorSpec{
		{
			name:            "flowlogs",
			enabled:         func() bool { return c.Collectors.Flowlogs.Enabled },
			source:          func() string { return c.Collectors.Flowlogs.Source },
			lag:             func() time.Duration { return c.Collectors.Flowlogs.Lag.D() },
			initialLookback: func() time.Duration { return c.Collectors.Flowlogs.InitialLookback.D() },
			maxWindow:       func() time.Duration { return c.Collectors.Flowlogs.MaxWindow.D() },
			interval:        func() time.Duration { return c.Collectors.Flowlogs.Interval.D() },
		},
		{
			name:            "auditlogs",
			enabled:         func() bool { return c.Collectors.Auditlogs.Enabled },
			source:          func() string { return c.Collectors.Auditlogs.Source },
			lag:             func() time.Duration { return c.Collectors.Auditlogs.Lag.D() },
			initialLookback: func() time.Duration { return c.Collectors.Auditlogs.InitialLookback.D() },
			maxWindow:       func() time.Duration { return c.Collectors.Auditlogs.MaxWindow.D() },
			interval:        func() time.Duration { return c.Collectors.Auditlogs.Interval.D() },
		},
	}
	// Both log types are exported to object storage: network logs and, since
	// #288, configuration logs — the latter verified against a live export on
	// 2026-07-27. The earlier "flowlogs only" restriction here rested on the
	// opposite premise and was wrong.
	validSources := []string{"poll", "stream", "both", "objectstore"}
	for _, spec := range logCollectorSpecs {
		spec := spec
		add(fmt.Sprintf("collectors.%s.source", spec.name), oneOfRemediation(fmt.Sprintf("collectors.%s.source", spec.name), validSources...), func() error {
			s := spec.source()
			if s != "" && !oneOf(s, validSources...) {
				return fmt.Errorf("collectors.%s.source %q invalid: must be one of %s",
					spec.name, s, strings.Join(validSources, ", "))
			}
			return nil
		})
		// (a) A pure-stream collector needs an ingestion path that actually exists.
		add(fmt.Sprintf("collectors.%s.source", spec.name), fmt.Sprintf("Set streaming.enabled: true (and streaming.routes in multi-tailnet mode) for collectors.%s.source=stream.", spec.name), func() error {
			if !spec.enabled() || spec.source() != "stream" {
				return nil
			}
			if len(c.Tailnets) > 1 && len(c.Streaming.Routes) == 0 {
				return fmt.Errorf("collectors.%s.source=stream in multi-tailnet mode requires streaming.routes", spec.name)
			}
			if !c.Streaming.Enabled {
				return fmt.Errorf("collectors.%s.source=stream requires streaming.enabled: true — "+
					"otherwise there is no ingestion path and %s are silently never collected", spec.name, spec.name)
			}
			return nil
		})
		// (b) The object-store path needs a bucket to read, and pointing at one
		// that does not exist would look like a tailnet with no traffic. With a
		// tailnets: list every runtime needs its OWN complete destination — see
		// validateFlowObjectStore in objectstore.go for the frozen #284 contract.
		add(fmt.Sprintf("collectors.%s", spec.name), "See the error text: the object-store destination has several independent field constraints.", func() error {
			if !spec.enabled() || !objectStoreSource(spec.source()) {
				return nil
			}
			switch spec.name {
			case "flowlogs":
				return c.validateFlowObjectStore(spec.name)
			case "auditlogs":
				return c.validateAuditObjectStore(spec.name)
			}
			return nil
		})
		// (c)/(d) Window timing applies only to the polling path.
		add(fmt.Sprintf("collectors.%s.initial_lookback", spec.name), fmt.Sprintf("Set collectors.%s.initial_lookback to a positive duration.", spec.name), func() error {
			if !spec.enabled() || !pollsSource(spec.source()) {
				return nil
			}
			if spec.initialLookback() <= 0 {
				return fmt.Errorf("collectors.%s.initial_lookback must be > 0 (got %v): a zero or "+
					"negative cold-start lookback leaves the poll window's from >= to forever, so the "+
					"collector never polls and never bootstraps its checkpoint", spec.name, spec.initialLookback())
			}
			return nil
		})
		add(fmt.Sprintf("collectors.%s.lag", spec.name), fmt.Sprintf("Set collectors.%s.lag to >= 0.", spec.name), func() error {
			if !spec.enabled() || !pollsSource(spec.source()) {
				return nil
			}
			if spec.lag() < 0 {
				return fmt.Errorf("collectors.%s.lag must be >= 0 (got %v): a negative lag pushes the "+
					"window end into the future, permanently skipping records that arrive within it",
					spec.name, spec.lag())
			}
			return nil
		})
		// A positive max_window <= interval can never catch up: catch-up advances
		// at most max_window per tick, so a backlogged poller falls further behind
		// every tick without bound. A zero/negative max_window is the intentional
		// "no cap" sentinel and is exempt.
		add(fmt.Sprintf("collectors.%s.max_window", spec.name), fmt.Sprintf("Set collectors.%s.max_window > interval, or 0 for no cap.", spec.name), func() error {
			if !spec.enabled() || !pollsSource(spec.source()) {
				return nil
			}
			if spec.maxWindow() > 0 && spec.maxWindow() <= spec.interval() {
				return fmt.Errorf("collectors.%s.max_window (%v) <= interval (%v): catch-up advances "+
					"at most max_window per tick, so with interval >= max_window the checkpoint falls "+
					"further behind every tick without bound; set max_window > interval, or 0 for no cap",
					spec.name, spec.maxWindow(), spec.interval())
			}
			return nil
		})
	}
	// The Kubernetes-audit collector is deliberately OUTSIDE the window-collector
	// loop above. That loop keys every check off spec.source(), and this collector
	// has no Source field at all: object storage is the only surface tsrecorder
	// exposes — there is no polling API, no stream and no webhook — so a source
	// toggle would offer a choice that could not be honored. It is therefore gated
	// purely on Enabled.
	add("collectors.k8s_audit", "See the error text: the object-store destination has several independent field constraints.", func() error {
		if !c.Collectors.K8sAudit.Enabled {
			return nil
		}
		return c.validateK8sAuditObjectStore()
	})
	add("collectors.flowlogs.replay_overlap", "Set collectors.flowlogs.replay_overlap between 0 and 1h.", func() error {
		if !c.Collectors.Flowlogs.Enabled || !pollsSource(c.Collectors.Flowlogs.Source) {
			return nil
		}
		overlap := c.Collectors.Flowlogs.ReplayOverlap.D()
		if overlap < 0 || overlap > time.Hour {
			return fmt.Errorf("collectors.flowlogs.replay_overlap must be between 0 and 1h (got %v)", overlap)
		}
		return nil
	})
	add("collectors.flowlogs.replay_seen_capacity", "Set collectors.flowlogs.replay_seen_capacity between 1 and 1048576.", func() error {
		if !c.Collectors.Flowlogs.Enabled || !pollsSource(c.Collectors.Flowlogs.Source) {
			return nil
		}
		overlap := c.Collectors.Flowlogs.ReplayOverlap.D()
		capacity := c.Collectors.Flowlogs.ReplaySeenCapacity
		if overlap > 0 && (capacity < 1 || capacity > 1048576) {
			return fmt.Errorf("collectors.flowlogs.replay_seen_capacity must be between 1 and 1048576 "+
				"when replay_overlap is enabled (got %d)", capacity)
		}
		return nil
	})

	// Feed collisions are checked once, across every destination this process will
	// read, so a flow/audit collision and a tailnet/tailnet collision are one rule
	// with one message rather than two that can disagree.
	add("", "See the error text: feed collisions vary by which destinations collide.", func() error {
		var inUse []objectStoreSignalSpec
		if c.Collectors.Flowlogs.Enabled && objectStoreSource(c.Collectors.Flowlogs.Source) {
			inUse = append(inUse, objectStoreFlowSpec)
		}
		if c.Collectors.Auditlogs.Enabled && objectStoreSource(c.Collectors.Auditlogs.Source) {
			inUse = append(inUse, objectStoreAuditSpec)
		}
		return c.validateObjectStoreFeeds(inUse...)
	})

	add("collectors.flowlogs.log_mode", oneOfRemediation("collectors.flowlogs.log_mode", "per_connection", "per_record", "off"), func() error {
		if !oneOf(c.Collectors.Flowlogs.LogMode, "per_connection", "per_record", "off") {
			return fmt.Errorf("collectors.flowlogs.log_mode %q invalid: must be one of per_connection, per_record, off", c.Collectors.Flowlogs.LogMode)
		}
		return nil
	})

	add("cardinality.flow.metrics_mode", oneOfRemediation("cardinality.flow.metrics_mode", "all", "rollup", "both"), func() error {
		if !oneOf(c.Cardinality.Flow.MetricsMode, "all", "rollup", "both") {
			return fmt.Errorf("cardinality.flow.metrics_mode %q invalid: must be one of all, rollup, both", c.Cardinality.Flow.MetricsMode)
		}
		return nil
	})
	add("cardinality.flow.rollup_top_n", "Set cardinality.flow.rollup_top_n to >= 0.", func() error {
		if c.Cardinality.Flow.RollupTopN < 0 {
			return fmt.Errorf("cardinality.flow.rollup_top_n %d invalid: must be >= 0 (0 selects the default)", c.Cardinality.Flow.RollupTopN)
		}
		return nil
	})
	add("cardinality.label_value_sample_cap", "Set cardinality.label_value_sample_cap to >= 0.", func() error {
		if c.Cardinality.LabelValueSampleCap < 0 {
			return fmt.Errorf("cardinality.label_value_sample_cap %d invalid: must be >= 0 (0 disables label-value capture)", c.Cardinality.LabelValueSampleCap)
		}
		return nil
	})
	add("cardinality.warning_threshold", "Set cardinality.warning_threshold and critical_threshold to >= 0.", func() error {
		if w, cr := c.Cardinality.WarningThreshold, c.Cardinality.CriticalThreshold; w < 0 || cr < 0 {
			return fmt.Errorf("cardinality warning_threshold/critical_threshold must be >= 0 (got %d/%d)", w, cr)
		}
		return nil
	})
	// The threshold-vs-metric_limit relationship is advisory (a threshold above
	// the limit can never fire, since a metric's series pin at the limit) — see
	// Warnings(). It is NOT a hard error, so lowering metric_limit never breaks an
	// existing config that kept the default thresholds. This check's own guard
	// (w>0 && cr>0) is naturally false whenever the check above already failed on
	// a negative value, so the two never both fire for the same root cause.
	add("cardinality.critical_threshold", "Set cardinality.critical_threshold >= warning_threshold.", func() error {
		w, cr := c.Cardinality.WarningThreshold, c.Cardinality.CriticalThreshold
		if w > 0 && cr > 0 && cr < w {
			return fmt.Errorf("cardinality.critical_threshold %d invalid: must be >= warning_threshold %d", cr, w)
		}
		return nil
	})

	add("collectors.devices.posture_log_mode", oneOfRemediation("collectors.devices.posture_log_mode", "changes", "always", "off"), func() error {
		if c.Collectors.Devices.PostureLogMode != "" &&
			!oneOf(c.Collectors.Devices.PostureLogMode, "changes", "always", "off") {
			return fmt.Errorf("collectors.devices.posture_log_mode %q invalid: must be one of changes, always, off", c.Collectors.Devices.PostureLogMode)
		}
		return nil
	})

	add("streaming.decompress", oneOfRemediation("streaming.decompress", "auto", "gzip", "zstd", "none"), func() error {
		if !oneOf(c.Streaming.Decompress, "auto", "gzip", "zstd", "none") {
			return fmt.Errorf("streaming.decompress %q invalid: must be one of auto, gzip, zstd, none", c.Streaming.Decompress)
		}
		return nil
	})

	// Receiver paths are registered verbatim with http.ServeMux.HandleFunc, which
	// panics at receiver startup on a malformed pattern. An empty path is fine (the
	// receiver substitutes its default), but a configured path must be a rooted
	// absolute path ("/...") — a value like "tailscale/webhook" is parsed by the mux
	// as a host-scoped pattern and silently never matches. Validate it up front.
	add("streaming.path", `Set streaming.path to a rooted absolute path (e.g. "/tailscale/webhook").`, func() error {
		return validateReceiverPath("streaming.path", c.Streaming.Path)
	})
	add("webhook.path", `Set webhook.path to a rooted absolute path (e.g. "/tailscale/webhook").`, func() error {
		return validateReceiverPath("webhook.path", c.Webhook.Path)
	})

	// Auto-configuring the log-streaming sink needs an enabled receiver and the
	// externally reachable URL to register with Tailscale.
	add("streaming.auto_configure", "Set streaming.enabled: true for streaming.auto_configure.", func() error {
		if c.Streaming.AutoConfigure && !c.Streaming.Enabled {
			return fmt.Errorf("streaming.auto_configure requires streaming.enabled: true")
		}
		return nil
	})
	add("streaming.public_url", "Set streaming.public_url for streaming.auto_configure.", func() error {
		if c.Streaming.AutoConfigure && c.Streaming.PublicURL == "" {
			return fmt.Errorf("streaming.auto_configure requires streaming.public_url to be set")
		}
		return nil
	})
	add("streaming.public_url", "Use an absolute http(s) URL with a host (see validateLogStreamingPublicURL rules).", func() error {
		if !c.Streaming.AutoConfigure || c.Streaming.PublicURL == "" {
			return nil
		}
		return validateLogStreamingPublicURL("streaming.public_url", c.Streaming.PublicURL)
	})

	// Every static node-metrics target must have a URL when the scraper is
	// enabled; when dynamic discovery is enabled its fields are validated too.
	// Either static targets OR discovery is a valid way to have something to scrape.
	add("collectors.node_metrics.targets", "Give every node_metrics target a url, and make duplicate identities distinct.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled {
			return nil
		}
		seenTargetID := make(map[string]int, len(nm.Targets))
		for i, t := range nm.Targets {
			if t.URL == "" {
				return fmt.Errorf("collectors.node_metrics.targets[%d].url is required", i)
			}
			u, err := url.Parse(t.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("collectors.node_metrics.targets[%d].url must be a valid absolute HTTP(S) URL", i)
			}
			hasCredential := t.BearerToken.Reveal() != "" || t.BearerTokenFile != "" || len(t.Headers) > 0 ||
				(t.TLS != nil && (t.TLS.CertFile != "" || t.TLS.KeyFile != ""))
			if hasCredential && u.Scheme != "https" && !httpguard.IsLoopbackHost(u.Host) {
				return fmt.Errorf("collectors.node_metrics.targets[%d].url must use HTTPS when credentials are configured, except for loopback", i)
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
					i, j, redact.URLOrigin(t.URL), effectiveNodeMetricsInstance(t))
			}
			seenTargetID[id] = i
		}
		return nil
	})
	add("collectors.node_metrics.max_response_bytes", "Set collectors.node_metrics.max_response_bytes to a positive byte count.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled {
			return nil
		}
		if nm.MaxResponseBytes <= 0 {
			return fmt.Errorf("collectors.node_metrics.max_response_bytes must be > 0")
		}
		return nil
	})
	add("collectors.node_metrics.max_samples", "Set collectors.node_metrics.max_samples to a positive integer.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled {
			return nil
		}
		if nm.MaxSamples <= 0 {
			return fmt.Errorf("collectors.node_metrics.max_samples must be > 0")
		}
		return nil
	})
	// Passthrough metric-name filters are anchored regexes; compile them up
	// front so a bad pattern is a config error rather than a silent no-op.
	add("collectors.node_metrics.metric_allow", "Fix the invalid regex in collectors.node_metrics.metric_allow.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled {
			return nil
		}
		for i, p := range nm.MetricAllow {
			if _, err := regexp.Compile(fmt.Sprintf("^(?:%s)$", p)); err != nil {
				return fmt.Errorf("collectors.node_metrics.metric_allow[%d] %q: invalid regex: %w", i, p, err)
			}
		}
		return nil
	})
	add("collectors.node_metrics.metric_deny", "Fix the invalid regex in collectors.node_metrics.metric_deny.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled {
			return nil
		}
		for i, p := range nm.MetricDeny {
			if _, err := regexp.Compile(fmt.Sprintf("^(?:%s)$", p)); err != nil {
				return fmt.Errorf("collectors.node_metrics.metric_deny[%d] %q: invalid regex: %w", i, p, err)
			}
		}
		return nil
	})
	add("collectors.node_metrics.discovery.scheme", oneOfRemediation("collectors.node_metrics.discovery.scheme", "http", "https"), func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled || !nm.Discovery.Enabled {
			return nil
		}
		if !oneOf(nm.Discovery.Scheme, "http", "https") {
			return fmt.Errorf("collectors.node_metrics.discovery.scheme %q invalid: must be one of http, https", nm.Discovery.Scheme)
		}
		return nil
	})
	add("collectors.node_metrics.discovery.port", "Set collectors.node_metrics.discovery.port between 1 and 65535.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled || !nm.Discovery.Enabled {
			return nil
		}
		if nm.Discovery.Port < 1 || nm.Discovery.Port > 65535 {
			return fmt.Errorf("collectors.node_metrics.discovery.port %d invalid: must be 1-65535", nm.Discovery.Port)
		}
		return nil
	})
	add("collectors.node_metrics.discovery.address_order", oneOfRemediation("collectors.node_metrics.discovery.address_order", "ipv4", "ipv6"), func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled || !nm.Discovery.Enabled {
			return nil
		}
		if !oneOf(nm.Discovery.AddressOrder, "ipv4", "ipv6") {
			return fmt.Errorf("collectors.node_metrics.discovery.address_order %q invalid: must be one of ipv4, ipv6", nm.Discovery.AddressOrder)
		}
		return nil
	})
	add("collectors.node_metrics.discovery.instance_source", oneOfRemediation("collectors.node_metrics.discovery.instance_source", "address", "name", "hostname"), func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled || !nm.Discovery.Enabled {
			return nil
		}
		if !oneOf(nm.Discovery.InstanceSource, "address", "name", "hostname") {
			return fmt.Errorf("collectors.node_metrics.discovery.instance_source %q invalid: must be one of address, name, hostname", nm.Discovery.InstanceSource)
		}
		return nil
	})
	add("collectors.node_metrics.discovery.interval", "Set collectors.node_metrics.discovery.interval to a positive duration.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled || !nm.Discovery.Enabled {
			return nil
		}
		if nm.Discovery.Interval.D() <= 0 {
			return fmt.Errorf("collectors.node_metrics.discovery.interval must be > 0")
		}
		return nil
	})
	add("collectors.node_metrics.discovery.max_targets", "Set collectors.node_metrics.discovery.max_targets to a positive integer.", func() error {
		nm := c.Collectors.NodeMetrics
		if !nm.Enabled || !nm.Discovery.Enabled {
			return nil
		}
		if nm.Discovery.MaxTargets <= 0 {
			return fmt.Errorf("collectors.node_metrics.discovery.max_targets must be > 0")
		}
		return nil
	})

	// Reverse-DNS enrichment: when enabled, the resolver address (if set) must be
	// an IP or IP:port, and the cache bound must be positive.
	add("enrichment.reverse_dns.server", "Set enrichment.reverse_dns.server to an IP or IP:port.", func() error {
		rd := c.Enrichment.ReverseDNS
		if !rd.Enabled || rd.Server == "" {
			return nil
		}
		host := rd.Server
		if h, _, err := net.SplitHostPort(rd.Server); err == nil {
			host = h
		}
		if net.ParseIP(host) == nil {
			return fmt.Errorf("enrichment.reverse_dns.server %q invalid: must be an IP or IP:port", rd.Server)
		}
		return nil
	})
	add("enrichment.reverse_dns.max_entries", "Set enrichment.reverse_dns.max_entries to a positive integer.", func() error {
		rd := c.Enrichment.ReverseDNS
		if !rd.Enabled {
			return nil
		}
		if rd.MaxEntries <= 0 {
			return fmt.Errorf("enrichment.reverse_dns.max_entries must be > 0 when reverse DNS is enabled")
		}
		return nil
	})
	// A negative window is not a disable switch: it would make every entry
	// unservable the instant it was written, quietly turning enrichment off
	// while the config still reads enabled. Zero is the documented disable.
	add("enrichment.reverse_dns.stale_ttl", "Set enrichment.reverse_dns.stale_ttl to >= 0 (0 disables serving stale names).", func() error {
		rd := c.Enrichment.ReverseDNS
		if !rd.Enabled {
			return nil
		}
		if rd.StaleTTL.D() < 0 {
			return fmt.Errorf("enrichment.reverse_dns.stale_ttl must be >= 0 (0 disables serving stale names)")
		}
		return nil
	})

	// GeoIP enrichment: local databases, an optional MaxMind downloader, or both.
	//
	// applyGeoIPDefaults runs FIRST and mutates the config: with the downloader
	// on and no explicit paths, it points country_database / asn_database at
	// where the downloader installs each requested edition. Without that, the
	// obvious minimal config — credentials and nothing else — would download
	// databases and never load them, the worst kind of silent no-op. It runs
	// before the checks below so "no source configured" is judged against the
	// resolved paths, not the raw ones.
	c.applyGeoIPDefaults()

	add("enrichment.geoip.country_database", "Set enrichment.geoip.country_database / asn_database, or enable enrichment.geoip.download.", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled {
			return nil
		}
		if g.CountryDatabase == "" && g.ASNDatabase == "" {
			return fmt.Errorf("enrichment.geoip.enabled is true but neither country_database nor asn_database is set " +
				"(and enrichment.geoip.download is not enabled): there is nothing to enrich from")
		}
		return nil
	})
	add("enrichment.geoip.download.account_id", "Set enrichment.geoip.download.account_id to your MaxMind account ID.", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled || !g.Download.Enabled {
			return nil
		}
		if g.Download.AccountID == "" {
			return fmt.Errorf("enrichment.geoip.download.enabled requires download.account_id (your MaxMind account ID)")
		}
		return nil
	})
	add("enrichment.geoip.download.license_key", "Set enrichment.geoip.download.license_key (or license_key_file).", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled || !g.Download.Enabled {
			return nil
		}
		if g.Download.LicenseKey == "" {
			return fmt.Errorf("enrichment.geoip.download.enabled requires download.license_key or download.license_key_file")
		}
		return nil
	})
	add("enrichment.geoip.download.editions", "List MaxMind edition IDs in enrichment.geoip.download.editions (e.g. GeoLite2-Country, GeoLite2-ASN).", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled || !g.Download.Enabled {
			return nil
		}
		if len(g.Download.Editions) == 0 {
			return fmt.Errorf("enrichment.geoip.download.enabled requires at least one entry in download.editions")
		}
		for _, ed := range g.Download.Editions {
			// The edition name is interpolated into a URL path AND a filename,
			// so it is checked here rather than sanitized later into something
			// the operator never asked for.
			if err := geoip.ValidateEdition(ed); err != nil {
				return fmt.Errorf("enrichment.geoip.download.editions: %w", err)
			}
		}
		return nil
	})
	add("enrichment.geoip.download.endpoint", "Set enrichment.geoip.download.endpoint to an absolute http(s) URL.", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled || !g.Download.Enabled || g.Download.Endpoint == "" {
			return nil
		}
		u, err := url.Parse(g.Download.Endpoint)
		if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("enrichment.geoip.download.endpoint invalid: must be an absolute http(s) URL")
		}
		return nil
	})
	// A negative reload interval is not a disable switch — 0 is the documented
	// one. Accepting a negative would make time.NewTicker panic at startup.
	add("enrichment.geoip.reload_interval", "Set enrichment.geoip.reload_interval to >= 0 (0 disables reloading).", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled {
			return nil
		}
		if g.ReloadInterval.D() < 0 {
			return fmt.Errorf("enrichment.geoip.reload_interval must be >= 0 (0 disables reloading changed database files)")
		}
		return nil
	})
	add("enrichment.geoip.download.interval", "Set enrichment.geoip.download.interval to a positive duration.", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled || !g.Download.Enabled {
			return nil
		}
		if g.Download.Interval.D() <= 0 {
			return fmt.Errorf("enrichment.geoip.download.interval must be > 0")
		}
		return nil
	})
	add("enrichment.geoip.download.timeout", "Set enrichment.geoip.download.timeout to a positive duration.", func() error {
		g := c.Enrichment.GeoIP
		if !g.Enabled || !g.Download.Enabled {
			return nil
		}
		if g.Download.Timeout.D() <= 0 {
			return fmt.Errorf("enrichment.geoip.download.timeout must be > 0")
		}
		return nil
	})
	// geo_dims with geoip off would silently emit nothing at all. Saying so is
	// much kinder than letting an operator hunt for a label that can never appear.
	add("cardinality.flow.geo_dims", "Set enrichment.geoip.enabled: true for cardinality.flow.geo_dims.", func() error {
		if c.Cardinality.Flow.GeoDims && !c.Enrichment.GeoIP.Enabled {
			return fmt.Errorf("cardinality.flow.geo_dims requires enrichment.geoip.enabled: true (nothing supplies the country labels otherwise)")
		}
		return nil
	})

	// Profiling is opt-in. The pprof handlers are mounted on the admin server, so
	// they need it enabled; the Pyroscope push agent needs a server to push to.
	add("profiling.pprof.enabled", "Set admin.enabled: true for profiling.pprof.enabled.", func() error {
		if c.Profiling.Pprof.Enabled && !c.Admin.Enabled {
			return fmt.Errorf("profiling.pprof.enabled requires admin.enabled: true")
		}
		return nil
	})
	// pprof exposes process internals (heap/goroutine dumps can contain
	// in-memory secrets), so it must not be served unauthenticated: enabling it
	// requires a shared admin.auth.token. The status page itself only warns
	// (see Warnings); pprof is the stricter surface.
	add("profiling.pprof.enabled", "Set admin.auth.token for profiling.pprof.enabled.", func() error {
		if c.Profiling.Pprof.Enabled && c.Admin.Auth.Token == "" {
			return fmt.Errorf("profiling.pprof.enabled requires admin.auth.token to be set (pprof can expose in-memory secrets via heap dumps)")
		}
		return nil
	})
	add("profiling.pyroscope.server_address", "Set profiling.pyroscope.server_address for profiling.pyroscope.enabled.", func() error {
		if c.Profiling.Pyroscope.Enabled && c.Profiling.Pyroscope.ServerAddress == "" {
			return fmt.Errorf("profiling.pyroscope.enabled requires profiling.pyroscope.server_address")
		}
		return nil
	})

	add("tracing.sampler", oneOfRemediation("tracing.sampler", "always_on", "always_off", "traceidratio", "parentbased_always_on", "parentbased_traceidratio"), func() error {
		if !oneOf(c.Tracing.Sampler, "always_on", "always_off", "traceidratio",
			"parentbased_always_on", "parentbased_traceidratio") {
			return fmt.Errorf("tracing.sampler %q invalid: must be one of always_on, always_off, traceidratio, parentbased_always_on, parentbased_traceidratio", c.Tracing.Sampler)
		}
		return nil
	})
	add("tracing.sampler_arg", "Set tracing.sampler_arg within [0,1].", func() error {
		if c.Tracing.SamplerArg < 0 || c.Tracing.SamplerArg > 1 {
			return fmt.Errorf("tracing.sampler_arg %v invalid: must be in [0,1]", c.Tracing.SamplerArg)
		}
		return nil
	})

	// Per-class sampler overrides (#372). An EMPTY sampler is the documented way
	// to inherit tracing.sampler, so it must not be run through the enum — only a
	// non-empty value is checked. The arg is checked unconditionally: a
	// nonsensical ratio left behind on a class that later switches to a ratio
	// sampler is a trap, and the range is free to enforce either way.
	for _, cl := range []struct {
		key string
		val TracingSamplerClass
	}{
		{"scrape", c.Tracing.Samplers.Scrape},
		{"receiver", c.Tracing.Samplers.Receiver},
		{"background", c.Tracing.Samplers.Background},
	} {
		samplerKey := "tracing.samplers." + cl.key + ".sampler"
		argKey := "tracing.samplers." + cl.key + ".arg"
		sampler, arg := cl.val.Sampler, cl.val.Arg
		add(samplerKey, oneOfRemediation(samplerKey, "always_on", "always_off", "traceidratio",
			"parentbased_always_on", "parentbased_traceidratio"), func() error {
			if sampler == "" {
				return nil
			}
			if !oneOf(sampler, "always_on", "always_off", "traceidratio",
				"parentbased_always_on", "parentbased_traceidratio") {
				return fmt.Errorf("%s %q invalid: must be empty (inherit tracing.sampler) or one of "+
					"always_on, always_off, traceidratio, parentbased_always_on, parentbased_traceidratio",
					samplerKey, sampler)
			}
			return nil
		})
		add(argKey, "Set "+argKey+" within [0,1].", func() error {
			if arg < 0 || arg > 1 {
				return fmt.Errorf("%s %v invalid: must be in [0,1]", argKey, arg)
			}
			return nil
		})
	}

	add("tracing.remote_parent", oneOfRemediation("tracing.remote_parent", "trust", "ignore", "link"), func() error {
		// Empty is the compatibility default (trust), so it is accepted.
		if c.Tracing.RemoteParent != "" && !oneOf(c.Tracing.RemoteParent, "trust", "ignore", "link") {
			return fmt.Errorf("tracing.remote_parent %q invalid: must be one of trust, ignore, link",
				c.Tracing.RemoteParent)
		}
		return nil
	})

	c.validateResourceEnrichment(add)
	c.validateCredentialReload(add)

	add("profiling.pyroscope.tailnet_label",
		oneOfRemediation("profiling.pyroscope.tailnet_label", "off", "hashed", "name"), func() error {
			// Empty is the compatibility default (off).
			if c.Profiling.Pyroscope.TailnetLabel != "" &&
				!oneOf(c.Profiling.Pyroscope.TailnetLabel, "off", "hashed", "name") {
				return fmt.Errorf("profiling.pyroscope.tailnet_label %q invalid: must be one of off, hashed, name",
					c.Profiling.Pyroscope.TailnetLabel)
			}
			return nil
		})

	add("profiling.pyroscope.span_profiles.enabled",
		"Enable both tracing.enabled and profiling.pyroscope.enabled, or turn span_profiles off.", func() error {
			if !c.Profiling.Pyroscope.SpanProfiles.Enabled {
				return nil
			}
			// Correlation needs a span to label and a profiler to receive the
			// labels. With either half off there is nothing to correlate and the
			// setting is silently inert — the shape #305 stopped tolerating.
			if !c.Tracing.Enabled || !c.Profiling.Pyroscope.Enabled {
				return fmt.Errorf("profiling.pyroscope.span_profiles.enabled requires both " +
					"tracing.enabled and profiling.pyroscope.enabled")
			}
			return nil
		})

	add("version_checks.cache_ttl", "Set version_checks.cache_ttl to >= 5m.", func() error {
		if (c.VersionChecks.Self.Enabled || c.VersionChecks.Devices.Enabled) && c.VersionChecks.CacheTTL.D() < 5*time.Minute {
			return fmt.Errorf("version_checks.cache_ttl must be >= 5m to avoid hammering the upstream release endpoints")
		}
		return nil
	})
	add("version_checks.timeout", "Set version_checks.timeout to a positive duration.", func() error {
		if (c.VersionChecks.Self.Enabled || c.VersionChecks.Devices.Enabled) && c.VersionChecks.Timeout.D() <= 0 {
			return fmt.Errorf("version_checks.timeout must be > 0")
		}
		return nil
	})
	add("version_checks.devices.outdated_minor_threshold", "Set version_checks.devices.outdated_minor_threshold to >= 1.", func() error {
		if c.VersionChecks.Devices.Enabled && c.VersionChecks.Devices.OutdatedMinorThreshold < 1 {
			return fmt.Errorf("version_checks.devices.outdated_minor_threshold must be >= 1")
		}
		return nil
	})

	return checks
}

func (c *Config) validateIngressWAL() error {
	if !c.IngressWAL.Enabled {
		return nil
	}

	directory := c.IngressWAL.Directory
	cleanDirectory := filepath.Clean(directory)
	if !filepath.IsAbs(directory) {
		return fmt.Errorf("ingress_wal.directory %q invalid: must be an absolute path", directory)
	}
	if cleanDirectory != directory {
		return fmt.Errorf("ingress_wal.directory %q invalid: must already be filepath-clean (use %q)",
			directory, cleanDirectory)
	}
	root := filepath.VolumeName(cleanDirectory) + string(filepath.Separator)
	if cleanDirectory == root {
		return fmt.Errorf("ingress_wal.directory %q invalid: must not be the filesystem root", directory)
	}
	if c.IngressWAL.MaxBytes <= 0 || c.IngressWAL.MaxBytes == math.MaxInt64 {
		return fmt.Errorf("ingress_wal.max_bytes must be > 0 and < %d (got %d)",
			int64(math.MaxInt64), c.IngressWAL.MaxBytes)
	}
	if c.IngressWAL.MaxEntries <= 0 {
		return fmt.Errorf("ingress_wal.max_entries must be > 0 (got %d)", c.IngressWAL.MaxEntries)
	}
	if c.IngressWAL.Corruption != "fail" {
		return fmt.Errorf("ingress_wal.corruption %q invalid: must be exactly \"fail\"",
			c.IngressWAL.Corruption)
	}

	receivers := [...]struct {
		key     string
		enabled bool
		value   int64
	}{
		{key: "streaming.max_body_bytes", enabled: c.Streaming.Enabled, value: c.Streaming.MaxBodyBytes},
		{key: "webhook.max_body_bytes", enabled: c.Webhook.Enabled, value: c.Webhook.MaxBodyBytes},
	}
	for _, receiver := range receivers {
		if receiver.enabled &&
			(receiver.value <= 0 || receiver.value > maxIngressWALReceiverBodyBytes) {
			return fmt.Errorf("%s must be > 0 and <= %d when ingress_wal.enabled=true (got %d; max %d)",
				receiver.key, maxIngressWALReceiverBodyBytes, receiver.value,
				maxIngressWALReceiverBodyBytes)
		}
	}
	return nil
}

// nodeMetricsTargetIdentity is the effective identity of a node-metrics scrape
// target: its normalized URL plus its effective node-identity label. It MUST stay in
// lockstep with the runtime identity in internal/collector/nodemetrics
// (targetIdentity/effectiveInstance/normalizeTargetURL) so a config that passes
// validation and the set the collector actually dedups/keys baselines by agree.
// validateNoURLCredentials rejects credentials embedded in a URL's userinfo
// ("https://user:password@host/...") for every setting that has a dedicated
// secret field expressing the same thing.
//
// Why a hard error rather than a warning:
//   - The credential is reusable and long-lived, and each of these values reaches
//     a lower-trust surface — the admin status page and its JSON API, or a log
//     line — where a reader who should only see "where do we push" recovers "and
//     with what".
//   - For otlp.endpoint and profiling.pyroscope.server_address the userinfo is
//     not even honored: the OTLP exporter is configured from the URL's host and
//     path, and the Pyroscope agent authenticates from basic_auth_*. Accepting
//     the value silently would keep a credential in the config that never
//     actually authenticated anything.
//   - node_metrics targets and streaming.public_url CAN work with userinfo
//     today, so this is a deliberate breaking change; the error names the typed
//     field to move the secret into, and the value is redacted in the message.
//
// The checks are unconditional (not gated on the feature being enabled) so a
// credential can never sit in the config waiting for someone to flip a flag.
// Query strings are NOT rejected — a signed query is a legitimate reverse-proxy
// pattern that no typed field replaces — they are only redacted at every
// diagnostic surface, plus flagged by Warnings for streaming.public_url.
func (c *Config) validateNoURLCredentials() error {
	checks := []struct {
		key    string
		value  string
		useFmt string // the typed field(s) to use instead
	}{
		{
			key:    "otlp.endpoint",
			value:  c.OTLP.Endpoint,
			useFmt: "put the credential in otlp.grafana_cloud.instance_id + otlp.grafana_cloud.token, or in otlp.headers (values are Secret and redact themselves); the OTLP exporter does not authenticate from URL userinfo",
		},
		{
			key:    "profiling.pyroscope.server_address",
			value:  c.Profiling.Pyroscope.ServerAddress,
			useFmt: "use profiling.pyroscope.basic_auth_user + profiling.pyroscope.basic_auth_password (or basic_auth_password_file); the Pyroscope agent authenticates from those, not from URL userinfo",
		},
		{
			key:    "streaming.public_url",
			value:  c.Streaming.PublicURL,
			useFmt: "authenticate the receiver with streaming.token instead; this URL is handed to Tailscale as the log-streaming destination, so anything embedded in it leaves this process",
		},
	}
	for i, t := range c.Collectors.NodeMetrics.Targets {
		checks = append(checks, struct {
			key    string
			value  string
			useFmt string
		}{
			key:    fmt.Sprintf("collectors.node_metrics.targets[%d].url", i),
			value:  t.URL,
			useFmt: "use the target's bearer_token / bearer_token_file, or headers (e.g. an Authorization header — header values are Secret and redact themselves)",
		})
	}
	for i, route := range c.Streaming.Routes {
		checks = append(checks, struct {
			key    string
			value  string
			useFmt string
		}{
			key:    fmt.Sprintf("streaming.routes[%d].public_url", i),
			value:  route.PublicURL,
			useFmt: "authenticate the receiver with that route's token/token_file; this URL is handed to Tailscale as the log-streaming destination",
		})
	}
	for _, ch := range checks {
		if redact.HasUserinfo(ch.value) {
			return fmt.Errorf("%s %q embeds credentials in the URL (the \"user:password@host\" form): "+
				"remove them — %s", ch.key, redact.URLOrigin(ch.value), ch.useFmt)
		}
	}
	return nil
}

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

// applyGeoIPDefaults fills in the database paths the downloader is about to
// install to, when the operator enabled the downloader and did not name paths
// explicitly.
//
// This exists because the obvious minimal configuration is `download.enabled`
// plus credentials — and without this, that configuration would faithfully
// download both databases and then load neither, because country_database and
// asn_database were still empty. A feature that quietly does nothing is worse
// than one that fails loudly, so the paths are derived rather than required.
//
// An explicitly-set path always wins, and a path is only derived for an edition
// the operator actually requested: asking for GeoLite2-Country alone must not
// invent an asn_database that will never exist.
func (c *Config) applyGeoIPDefaults() {
	g := &c.Enrichment.GeoIP
	if !g.Enabled || !g.Download.Enabled {
		return
	}
	dir := g.Download.Directory
	if dir == "" {
		dir = DefaultGeoIPDir()
		g.Download.Directory = dir
	}
	for _, ed := range g.Download.Editions {
		if geoip.ValidateEdition(ed) != nil {
			// Refused by the editions check; deriving a path from it would put
			// an attacker-influenced string into a filesystem path.
			continue
		}
		switch {
		case strings.HasSuffix(ed, "-ASN"):
			if g.ASNDatabase == "" {
				g.ASNDatabase = geoip.DatabasePath(dir, ed)
			}
		case strings.HasSuffix(ed, "-Country"), strings.HasSuffix(ed, "-City"), strings.HasSuffix(ed, "-Enterprise"):
			// City and Enterprise are supersets of Country and decode the same
			// country/continent fields, so any of them can back country_database.
			if g.CountryDatabase == "" {
				g.CountryDatabase = geoip.DatabasePath(dir, ed)
			}
		}
	}
}

// DefaultGeoIPDir is where downloaded GeoIP databases live when
// enrichment.geoip.download.directory is unset. It sits beside the checkpoint
// file in the platform's state directory: a database is regenerable state, not
// operator-edited configuration and not a cache that may vanish mid-run.
func DefaultGeoIPDir() string {
	dir := userStateDir()
	if dir == "" {
		// Same least-bad fallback as DefaultCheckpointPath: it may well be
		// writable, and if it is not, the downloader's error is reported and
		// enrichment simply stays empty.
		return filepath.Join(filepath.Dir(LegacyCheckpointPath), "geoip")
	}
	return filepath.Join(dir, stateDirName, "geoip")
}

// addCheck is the signature of validationChecks' local registrar, so the
// EPIC-04 rule groups below can be split into their own functions instead of
// growing validationChecks by another hundred lines.
type addCheck func(path, remediation string, fn func() error)

// maxResourceStringBytes bounds resource.service_namespace,
// resource.deployment_environment, and every resource.attributes key and value.
// The telemetry layer already drops an over-long attribute with a warning, but a
// single scalar in a config file is something the operator can trivially fix
// before startup, so it is refused rather than silently truncated.
const maxResourceStringBytes = 256

// maxResourceAttributes bounds how many custom Resource attributes may be
// declared. Custom attributes land in target_info rather than on every series,
// so this is not a direct cardinality cap — it keeps the Resource small enough
// to log, diff and reason about, and bounds what a config typo can inject.
const maxResourceAttributes = 32

// reservedResourceAttrKeys may never be supplied as a custom Resource
// attribute. The service.* trio is the application's own identity, which always
// wins — accepting an override here would either be silently ignored (inert
// config) or, for service.version, would put a per-build label on every metric
// series and re-break #187. tailscale.tailnet and tailscale2otel.provider are
// deliberately signal-scoped attributes rather than Resource attributes
// (roadmap item L) so they are real joinless labels on every backend; moving
// either onto the Resource would undo that.
var reservedResourceAttrKeys = []string{
	"service.name",
	"service.version",
	"service.instance.id",
	"tailscale.tailnet",
	"tailscale2otel.provider",
}

// validateResourceEnrichment registers the #380 resource: block rules.
func (c *Config) validateResourceEnrichment(add addCheck) {
	add("resource.service_namespace",
		fmt.Sprintf("Keep resource.service_namespace under %d bytes.", maxResourceStringBytes), func() error {
			if len(c.Resource.ServiceNamespace) > maxResourceStringBytes {
				return fmt.Errorf("resource.service_namespace is %d bytes: must be at most %d",
					len(c.Resource.ServiceNamespace), maxResourceStringBytes)
			}
			return nil
		})
	add("resource.deployment_environment",
		fmt.Sprintf("Keep resource.deployment_environment under %d bytes.", maxResourceStringBytes), func() error {
			if len(c.Resource.DeploymentEnvironment) > maxResourceStringBytes {
				return fmt.Errorf("resource.deployment_environment is %d bytes: must be at most %d",
					len(c.Resource.DeploymentEnvironment), maxResourceStringBytes)
			}
			return nil
		})
	add("resource.attributes",
		"Remove reserved keys, empty keys, and over-long entries from resource.attributes.", func() error {
			if len(c.Resource.Attributes) > maxResourceAttributes {
				return fmt.Errorf("resource.attributes has %d entries: must be at most %d",
					len(c.Resource.Attributes), maxResourceAttributes)
			}
			// Sorted so the reported key is deterministic across runs rather than
			// whichever one map iteration happened to reach first.
			keys := make([]string, 0, len(c.Resource.Attributes))
			for k := range c.Resource.Attributes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if strings.TrimSpace(k) == "" {
					return fmt.Errorf("resource.attributes contains an empty key")
				}
				if slices.Contains(reservedResourceAttrKeys, k) {
					return fmt.Errorf("resource.attributes key %q is reserved: %s", k, reservedResourceAttrReason(k))
				}
				if len(k) > maxResourceStringBytes {
					return fmt.Errorf("resource.attributes key %q is %d bytes: must be at most %d",
						k, len(k), maxResourceStringBytes)
				}
				if v := c.Resource.Attributes[k]; len(v) > maxResourceStringBytes {
					return fmt.Errorf("resource.attributes[%q] value is %d bytes: must be at most %d",
						k, len(v), maxResourceStringBytes)
				}
			}
			return nil
		})
}

// reservedResourceAttrReason explains why a key is refused, so the error is
// actionable rather than just a rejection.
func reservedResourceAttrReason(key string) string {
	switch key {
	case "tailscale.tailnet", "tailscale2otel.provider":
		return "it is emitted as a per-signal attribute, not a Resource attribute, so it is a real label on every backend"
	case "service.version":
		return "the metrics Resource deliberately omits it (it would become a service_version label on every series); " +
			"query the version via the tailscale2otel.build_info gauge instead"
	default:
		return "it carries the application's own identity, which always wins"
	}
}

// minCredentialReloadInterval floors the rotation poller. Below this the
// "support rotation" feature is really a stat() loop; secret projection in
// Kubernetes and Docker operates on a far longer timescale than this.
const minCredentialReloadInterval = 5 * time.Second

// validateCredentialReload registers the #362 poller rules for both the OTLP
// exporters and the Pyroscope agent.
func (c *Config) validateCredentialReload(add addCheck) {
	for _, r := range []struct {
		key string
		cfg CredentialReloadConfig
	}{
		{"otlp.credential_reload", c.OTLP.CredentialReload},
		{"profiling.pyroscope.credential_reload", c.Profiling.Pyroscope.CredentialReload},
	} {
		key, rc := r.key, r.cfg
		add(key+".interval",
			fmt.Sprintf("Set %s.interval to at least %s, or disable the poller.", key, minCredentialReloadInterval),
			func() error {
				// Disabled means "no poller", not "misconfigured": Reload() can still
				// be driven explicitly, so an unset interval there is legitimate.
				if !rc.Enabled {
					return nil
				}
				if rc.Interval.D() <= 0 {
					return fmt.Errorf("%s.enabled is true but %s.interval is not set: "+
						"an enabled poller with no interval never polls", key, key)
				}
				if rc.Interval.D() < minCredentialReloadInterval {
					return fmt.Errorf("%s.interval %s is below the %s floor",
						key, rc.Interval.D(), minCredentialReloadInterval)
				}
				return nil
			})
	}
}
