package config

import (
	"os"
	"path/filepath"
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

func TestObjectStoreBoundsDefaults(t *testing.T) {
	os := Default().Collectors.Flowlogs.ObjectStore
	if os.MaxObjectWireBytes != 67_108_864 {
		t.Errorf("MaxObjectWireBytes = %d, want 67108864", os.MaxObjectWireBytes)
	}
	if os.MaxObjectDecompressedBytes != 33_554_432 {
		t.Errorf("MaxObjectDecompressedBytes = %d, want 33554432", os.MaxObjectDecompressedBytes)
	}
	if os.MaxObjectRecords != 100_000 {
		t.Errorf("MaxObjectRecords = %d, want 100000", os.MaxObjectRecords)
	}
	if os.MaxCycleDecompressedBytes != 268_435_456 {
		t.Errorf("MaxCycleDecompressedBytes = %d, want 268435456", os.MaxCycleDecompressedBytes)
	}
	if os.MaxCycleWireBytes != 536_870_912 {
		t.Errorf("MaxCycleWireBytes = %d, want 536870912", os.MaxCycleWireBytes)
	}
	if os.MaxCycleRecords != 500_000 {
		t.Errorf("MaxCycleRecords = %d, want 500000", os.MaxCycleRecords)
	}
}

func TestObjectStoreBoundsYAMLOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const content = `
collectors:
  flowlogs:
    source: objectstore
    objectstore:
      endpoint: https://storage.example.test
      region: test-region-1
      bucket: test-bucket
      max_object_wire_bytes: 2097152
      max_object_decompressed_bytes: 1048576
      max_object_records: 2000
      max_cycle_wire_bytes: 16777216
      max_cycle_decompressed_bytes: 4194304
      max_cycle_records: 8000
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	os := cfg.Collectors.Flowlogs.ObjectStore
	if os.MaxObjectWireBytes != 2_097_152 ||
		os.MaxObjectDecompressedBytes != 1_048_576 ||
		os.MaxObjectRecords != 2_000 ||
		os.MaxCycleWireBytes != 16_777_216 ||
		os.MaxCycleDecompressedBytes != 4_194_304 ||
		os.MaxCycleRecords != 8_000 {
		t.Errorf("object-store bounds = %d/%d/%d/%d/%d/%d, want 2097152/1048576/2000/16777216/4194304/8000",
			os.MaxObjectWireBytes, os.MaxObjectDecompressedBytes, os.MaxObjectRecords,
			os.MaxCycleWireBytes, os.MaxCycleDecompressedBytes, os.MaxCycleRecords)
	}
}

func TestObjectStoreBoundsEnvOverride(t *testing.T) {
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_WIRE_BYTES", "4194304")
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_DECOMPRESSED_BYTES", "2097152")
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_OBJECT_RECORDS", "3000")
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_WIRE_BYTES", "33554432")
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_DECOMPRESSED_BYTES", "8388608")
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__MAX_CYCLE_RECORDS", "12000")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	os := cfg.Collectors.Flowlogs.ObjectStore
	if os.MaxObjectWireBytes != 4_194_304 ||
		os.MaxObjectDecompressedBytes != 2_097_152 ||
		os.MaxObjectRecords != 3_000 ||
		os.MaxCycleWireBytes != 33_554_432 ||
		os.MaxCycleDecompressedBytes != 8_388_608 ||
		os.MaxCycleRecords != 12_000 {
		t.Errorf("object-store bounds = %d/%d/%d/%d/%d/%d, want 4194304/2097152/3000/33554432/8388608/12000",
			os.MaxObjectWireBytes, os.MaxObjectDecompressedBytes, os.MaxObjectRecords,
			os.MaxCycleWireBytes, os.MaxCycleDecompressedBytes, os.MaxCycleRecords)
	}
}

func TestObjectStoreLimitValidation(t *testing.T) {
	const (
		endpointCanary = "https://storage-validation-canary.example"
		bucketCanary   = "bucket-validation-canary"
		accessCanary   = "S3ACCESS-bounds-validation-canary"
		secretCanary   = "S3SECRET-bounds-validation-canary"
		sessionCanary  = "S3SESSION-bounds-validation-canary"
	)
	tests := []struct {
		name     string
		tune     func(*ObjectStoreConfig)
		want     string
		supplied string
		required string
	}{
		{
			name: "zero object wire bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectWireBytes = 0
			},
			want:     "max_object_wire_bytes",
			supplied: "0",
			required: "1",
		},
		{
			name: "negative object wire bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectWireBytes = -4
			},
			want:     "max_object_wire_bytes",
			supplied: "-4",
			required: "1",
		},
		{
			name: "zero cycle wire bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxCycleWireBytes = 0
			},
			want:     "max_cycle_wire_bytes",
			supplied: "0",
			required: "1",
		},
		{
			name: "negative cycle wire bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxCycleWireBytes = -6
			},
			want:     "max_cycle_wire_bytes",
			supplied: "-6",
			required: "1",
		},
		{
			name: "zero object bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectDecompressedBytes = 0
			},
			want:     "max_object_decompressed_bytes",
			supplied: "0",
			required: "1",
		},
		{
			name: "cycle wire bytes below object wire bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectWireBytes = 2048
				os.MaxCycleWireBytes = 2047
			},
			want:     "max_cycle_wire_bytes",
			supplied: "2047",
			required: "2048",
		},
		{
			name: "negative object bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectDecompressedBytes = -5
			},
			want:     "max_object_decompressed_bytes",
			supplied: "-5",
			required: "1",
		},
		{
			name: "zero object records",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectRecords = 0
			},
			want:     "max_object_records",
			supplied: "0",
			required: "1",
		},
		{
			name: "negative object records",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectRecords = -7
			},
			want:     "max_object_records",
			supplied: "-7",
			required: "1",
		},
		{
			name: "zero cycle bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxCycleDecompressedBytes = 0
			},
			want:     "max_cycle_decompressed_bytes",
			supplied: "0",
			required: "1",
		},
		{
			name: "negative cycle bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxCycleDecompressedBytes = -8
			},
			want:     "max_cycle_decompressed_bytes",
			supplied: "-8",
			required: "1",
		},
		{
			name: "zero cycle records",
			tune: func(os *ObjectStoreConfig) {
				os.MaxCycleRecords = 0
			},
			want:     "max_cycle_records",
			supplied: "0",
			required: "1",
		},
		{
			name: "negative cycle records",
			tune: func(os *ObjectStoreConfig) {
				os.MaxCycleRecords = -9
			},
			want:     "max_cycle_records",
			supplied: "-9",
			required: "1",
		},
		{
			name: "cycle bytes below object bytes",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectDecompressedBytes = 1024
				os.MaxCycleDecompressedBytes = 1023
			},
			want:     "max_cycle_decompressed_bytes",
			supplied: "1023",
			required: "1024",
		},
		{
			name: "cycle records below object records",
			tune: func(os *ObjectStoreConfig) {
				os.MaxObjectRecords = 100
				os.MaxCycleRecords = 99
			},
			want:     "max_cycle_records",
			supplied: "99",
			required: "100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := objectStoreConfig(func(c *Config) {
				os := &c.Collectors.Flowlogs.ObjectStore
				os.Endpoint = endpointCanary
				os.Bucket = bucketCanary
				os.AccessKeyID = accessCanary
				os.SecretAccessKey = secretCanary
				os.SessionToken = sessionCanary
				tc.tune(os)
			})

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate accepted invalid object-store bound %q", tc.want)
			}
			numericLimit := "(supplied " + tc.supplied + "; required >= " + tc.required + ")"
			for _, want := range []string{tc.want, numericLimit} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			for _, forbidden := range []string{
				endpointCanary,
				bucketCanary,
				accessCanary,
				secretCanary,
				sessionCanary,
			} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("validation error leaked config value %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestObjectStoreLimitValidationIgnoresDormantValues(t *testing.T) {
	tests := []struct {
		name string
		tune func(*Config)
	}{
		{
			name: "flowlogs disabled",
			tune: func(c *Config) {
				c.Collectors.Flowlogs.Enabled = false
			},
		},
		{
			name: "poll source",
			tune: func(c *Config) {
				c.Collectors.Flowlogs.Source = "poll"
			},
		},
		{
			name: "stream source",
			tune: func(c *Config) {
				c.Collectors.Flowlogs.Source = "stream"
				c.Streaming.Enabled = true
				c.Streaming.Token = "stream-token"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := objectStoreConfig(func(c *Config) {
				os := &c.Collectors.Flowlogs.ObjectStore
				os.MaxObjectWireBytes = -1
				os.MaxObjectDecompressedBytes = -1
				os.MaxObjectRecords = -2
				os.MaxCycleWireBytes = -3
				os.MaxCycleDecompressedBytes = -3
				os.MaxCycleRecords = -4
				tc.tune(c)
			})
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate rejected dormant object-store bounds: %v", err)
			}
		})
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

func TestValidate_ObjectStoreRejectsPlaintextRemoteEndpointByDefault(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "http://storage.example.com:9000"
	})
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a plaintext remote object-store endpoint")
	}
	for _, want := range []string{"allow_insecure_http", "plaintext", "credentials"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want %q", err, want)
		}
	}
}

func TestValidate_ObjectStoreAllowsPlaintextLoopbackOrExplicitOverride(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		override bool
	}{
		{"localhost", "http://localhost:9000", false},
		{"IPv4 loopback", "http://127.0.0.1:9000", false},
		{"IPv6 loopback", "http://[::1]:9000", false},
		{"explicit remote override", "http://storage.example.com:9000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := objectStoreConfig(func(c *Config) {
				c.Collectors.Flowlogs.ObjectStore.Endpoint = tc.endpoint
				c.Collectors.Flowlogs.ObjectStore.AllowInsecureHTTP = tc.override
			})
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestWarnings_ObjectStorePlaintextRemoteOverrideNamesCredentialRisk(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "http://storage.example.com:9000"
		c.Collectors.Flowlogs.ObjectStore.AllowInsecureHTTP = true
	})
	warnings := c.Warnings()
	for _, want := range []string{"plaintext", "credentials", "session tokens"} {
		if !hasWarning(warnings, want) {
			t.Errorf("warnings = %v, want %q", warnings, want)
		}
	}
}

func TestValidate_ObjectStoreErrorDoesNotLeakCredentials(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.AccessKeyID = "S3ACCESS-validation-canary"
		c.Collectors.Flowlogs.ObjectStore.SecretAccessKey = "S3SECRET-validation-canary"
		c.Collectors.Flowlogs.ObjectStore.SessionToken = "S3SESSION-validation-canary"
		c.Collectors.Flowlogs.ObjectStore.MaxObjects = -1
	})
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a negative object-store budget")
	}
	for _, secret := range []string{
		"S3ACCESS-validation-canary",
		"S3SECRET-validation-canary",
		"S3SESSION-validation-canary",
	} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("validation error leaked %q: %v", secret, err)
		}
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
