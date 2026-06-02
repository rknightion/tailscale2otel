package config

import "fmt"

// oneOf reports whether v equals one of the allowed values.
func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
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

	return nil
}
