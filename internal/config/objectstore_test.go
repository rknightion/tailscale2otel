package config

import (
	"strings"
	"testing"
	"time"
)

// objectStoreConfig returns a config with flow logs coming from the export
// bucket, ready for a test to break one field at a time.
func objectStoreConfig(tune func(*Config)) *Config {
	c := Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "k"
	c.Collectors.Flowlogs.Source = "objectstore"
	c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
	c.Collectors.Flowlogs.ObjectStore.Region = "eu-west-2"
	c.Collectors.Flowlogs.ObjectStore.Bucket = "flows"
	if tune != nil {
		tune(c)
	}
	return c
}

func TestValidate_ObjectStoreSourceIsAccepted(t *testing.T) {
	if err := objectStoreConfig(nil).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// The object-store destination is process-global today. Starting two runtimes
// against it would read every object twice and attribute one copy to each
// tailnet, so validation must hold the safety boundary until #284 supplies
// explicit per-tailnet destinations.
func TestValidate_ObjectStoreRejectsMultiTailnetAttribution(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Tailscale.Tailnet = ""
		c.Tailnets = []TailnetConfig{
			{Name: "alpha.example.com", Auth: TailscaleAuth{Method: "apikey", APIKey: "alpha-key"}},
			{Name: "beta.example.com", Auth: TailscaleAuth{Method: "apikey", APIKey: "beta-key"}},
		}
	})

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted one global object-store destination for two tailnets")
	}
	for _, want := range []string{"objectstore", "multi-tailnet", "#284"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to explain the %q safety boundary", err, want)
		}
	}
}

// Each of the three required fields fails a request in a different, confusing
// way if left empty, so each is rejected by name at startup instead.
func TestValidate_ObjectStoreRequiresItsTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*Config)
		want   string
	}{
		{"no bucket", func(c *Config) { c.Collectors.Flowlogs.ObjectStore.Bucket = "" }, "bucket"},
		{"no endpoint", func(c *Config) { c.Collectors.Flowlogs.ObjectStore.Endpoint = "" }, "endpoint"},
		{"no region", func(c *Config) { c.Collectors.Flowlogs.ObjectStore.Region = "" }, "region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := objectStoreConfig(tc.break_).Validate()
			if err == nil {
				t.Fatal("Validate accepted an objectstore source with nothing to read")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// Audit logs are not exported to object storage. Accepting the value there would
// configure a path that silently collects nothing at all.
func TestValidate_ObjectStoreIsFlowLogsOnly(t *testing.T) {
	c := objectStoreConfig(func(c *Config) { c.Collectors.Auditlogs.Source = "objectstore" })
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted collectors.auditlogs.source=objectstore")
	}
	if !strings.Contains(err.Error(), "auditlogs.source") {
		t.Errorf("error = %v, want it to name the auditlogs source", err)
	}
}

// The bucket holds the SAME records the receiver does, so running both
// double-counts every connection.
func TestWarnings_ObjectStoreAlongsideStreaming(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Streaming.Enabled = true
		c.Streaming.Token = Secret("t")
	})
	if !hasWarning(c.Warnings(), "double-counted") {
		t.Errorf("warnings = %v, want one about double counting", c.Warnings())
	}
}

// A lookback shorter than the interval leaves a gap: the overlap that catches a
// late object is smaller than the time between listings, so an object landing in
// between is never seen.
func TestWarnings_LookbackShorterThanInterval(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Interval = dur(minutesDur(10))
		c.Collectors.Flowlogs.ObjectStore.Lookback = dur(minutesDur(1))
	})
	if !hasWarning(c.Warnings(), "shorter than its") {
		t.Errorf("warnings = %v, want one about the overlap gap", c.Warnings())
	}

	// The sane arrangement warns about nothing.
	ok := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Interval = dur(minutesDur(1))
		c.Collectors.Flowlogs.ObjectStore.Lookback = dur(minutesDur(60))
	})
	if hasWarning(ok.Warnings(), "shorter than its") {
		t.Errorf("warnings = %v, want none with a lookback wider than the interval", ok.Warnings())
	}
}

// The poll-only window fields do not apply, and enforcing them would reject a
// perfectly good objectstore-only deployment.
func TestValidate_ObjectStoreIgnoresPollWindowFields(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.InitialLookback = dur(0)
		c.Collectors.Flowlogs.Lag = dur(0)
	})
	if err := c.Validate(); err != nil {
		t.Errorf("Validate rejected an objectstore source over poll-only fields: %v", err)
	}
}

// minutesDur is a terser time.Duration for the table above.
func minutesDur(n int) time.Duration { return time.Duration(n) * time.Minute }

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
