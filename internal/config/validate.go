package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
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
			name string
			cfg  CollectorConfig
		}{
			{"flowlogs", c.Collectors.Flowlogs},
			{"auditlogs", c.Collectors.Auditlogs},
		}
		for _, col := range dualLogCollectors {
			if col.cfg.Enabled && pollsSource(col.cfg.Source) {
				src := col.cfg.Source
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
	return w
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
	if !oneOf(c.OTLP.Protocol, "grpc", "http", "stdout") {
		return fmt.Errorf("otlp.protocol %q invalid: must be one of grpc, http, stdout", c.OTLP.Protocol)
	}
	if !oneOf(c.Tailscale.Auth.Method, "oauth", "apikey") {
		return fmt.Errorf("tailscale.auth.method %q invalid: must be one of oauth, apikey", c.Tailscale.Auth.Method)
	}
	if !oneOf(c.Checkpoint.Store, "memory", "file") {
		return fmt.Errorf("checkpoint.store %q invalid: must be one of memory, file", c.Checkpoint.Store)
	}

	// Per-collector source validation. Empty source is allowed for collectors
	// that don't use one (users, keys, settings, acl, dns).
	collectors := []struct {
		name string
		cfg  CollectorConfig
	}{
		{"devices", c.Collectors.Devices},
		{"flowlogs", c.Collectors.Flowlogs},
		{"auditlogs", c.Collectors.Auditlogs},
		{"users", c.Collectors.Users},
		{"keys", c.Collectors.Keys},
		{"settings", c.Collectors.Settings},
		{"acl", c.Collectors.Acl},
		{"dns", c.Collectors.Dns},
	}
	for _, col := range collectors {
		if col.cfg.Source != "" && !oneOf(col.cfg.Source, "poll", "stream", "both") {
			return fmt.Errorf("collectors.%s.source %q invalid: must be one of poll, stream, both", col.name, col.cfg.Source)
		}
	}

	if !oneOf(c.Collectors.Flowlogs.LogMode, "per_connection", "per_record", "off") {
		return fmt.Errorf("collectors.flowlogs.log_mode %q invalid: must be one of per_connection, per_record, off", c.Collectors.Flowlogs.LogMode)
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

	// Profiling is opt-in. The pprof handlers are mounted on the admin server, so
	// they need it enabled; the Pyroscope push agent needs a server to push to.
	if c.Profiling.Pprof.Enabled && !c.Admin.Enabled {
		return fmt.Errorf("profiling.pprof.enabled requires admin.enabled: true")
	}
	if c.Profiling.Pyroscope.Enabled && c.Profiling.Pyroscope.ServerAddress == "" {
		return fmt.Errorf("profiling.pyroscope.enabled requires profiling.pyroscope.server_address")
	}

	return nil
}
