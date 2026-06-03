package config

import (
	"fmt"
	"slices"
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

	// Every node-metrics target must have a URL when the scraper is enabled.
	if c.Collectors.NodeMetrics.Enabled {
		for i, t := range c.Collectors.NodeMetrics.Targets {
			if t.URL == "" {
				return fmt.Errorf("collectors.node_metrics.targets[%d].url is required", i)
			}
		}
	}

	return nil
}
