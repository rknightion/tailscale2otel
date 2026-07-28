package config

import (
	"strings"
	"testing"
)

// Listener addresses were only ever compared as raw strings, so validation had
// no opinion on whether an address could be bound at all and could not see two
// listeners colliding under different spellings. Both failures land in
// net.Listen instead — on a goroutine, after startup, as a log line on a
// listener that never served while the process reports itself healthy (#306).

func TestValidateRejectsUnbindableListenAddresses(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Config)
		want string // substring naming the offending field
	}{
		{"admin bare port", func(c *Config) { c.Admin.Listen = "9091" }, "admin.listen"},
		{"prometheus no port", func(c *Config) { c.Prometheus.Enabled = true; c.Prometheus.Listen = "127.0.0.1" }, "prometheus.listen"},
		{"streaming port out of range", func(c *Config) {
			c.Streaming.Enabled = true
			c.Streaming.Listen = ":65536"
			c.Streaming.Token = Secret("t")
		}, "streaming.listen"},
		{"webhook service name", func(c *Config) {
			c.Webhook.Enabled = true
			c.Webhook.Listen = "127.0.0.1:http"
			c.Webhook.Secret = Secret("s")
		}, "webhook.listen"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.set(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error: this address cannot be bound, and " +
					"net.Listen rejecting it after startup leaves an apparently healthy process")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() error = %q, want it to name %s", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsCanonicalListenerCollisions(t *testing.T) {
	tests := []struct {
		name        string
		admin, prom string
	}{
		// Same bind, different spelling: the raw-string check saw two values.
		{"wildcard alias", ":9091", "0.0.0.0:9091"},
		{"ipv6 wildcard alias", ":9091", "[::]:9091"},
		// A wildcard owns the port on every interface, so a specific address on
		// the same port cannot also bind it.
		{"wildcard over specific", ":9091", "127.0.0.1:9091"},
		{"specific under wildcard", "127.0.0.1:9091", "0.0.0.0:9091"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Admin.Listen = tc.admin
			c.Admin.Auth.Token = Secret("admin-token")
			c.Prometheus.Enabled = true
			c.Prometheus.Listen = tc.prom
			c.Prometheus.Auth.Token = Secret("prom-token")
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil for admin=%q prometheus=%q, want a collision error: "+
					"only one of them wins the net.Listen race and the other dies silently",
					tc.admin, tc.prom)
			}
			if !strings.Contains(err.Error(), "admin.listen") || !strings.Contains(err.Error(), "prometheus.listen") {
				t.Errorf("Validate() error = %q, want it to name both colliding listeners", err)
			}
		})
	}
}

// Distinct sockets must keep validating, or the check is just a blanket refusal.
func TestValidateAllowsDistinctListeners(t *testing.T) {
	tests := []struct {
		name        string
		admin, prom string
	}{
		{"different ports", "127.0.0.1:9091", "127.0.0.1:2112"},
		{"different specific hosts", "127.0.0.1:9091", "192.168.1.5:9091"},
		{"wildcard on another port", ":9091", "0.0.0.0:2112"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Admin.Listen = tc.admin
			c.Admin.Auth.Token = Secret("admin-token")
			c.Prometheus.Enabled = true
			c.Prometheus.Listen = tc.prom
			c.Prometheus.Auth.Token = Secret("prom-token")
			if err := c.Validate(); err != nil {
				t.Errorf("Validate() = %v for admin=%q prometheus=%q, want nil", err, tc.admin, tc.prom)
			}
		})
	}
}

// A disabled listener binds nothing, so neither its address nor a collision
// with it is a reason to refuse to start.
func TestValidateIgnoresDisabledListeners(t *testing.T) {
	c := Default()
	c.Admin.Listen = "127.0.0.1:9091"
	c.Admin.Auth.Token = Secret("admin-token")
	c.Prometheus.Enabled = false
	c.Prometheus.Listen = "nonsense"
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil: prometheus is disabled, so its address is never bound", err)
	}
}
