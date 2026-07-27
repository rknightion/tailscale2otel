package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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

// multiTailnetObjectStoreConfig returns a valid multi-tailnet config where each
// entry owns a complete, distinct object-store destination and its own
// credentials — the #284 contract's happy path.
func multiTailnetObjectStoreConfig(tune func(*Config)) *Config {
	c := Default()
	c.Tailscale.Tailnet = "" // multi mode: the list replaces the single block
	c.Collectors.Flowlogs.Source = "objectstore"
	c.Tailnets = []TailnetConfig{
		{
			Name: "alpha.example.com",
			Auth: TailscaleAuth{Method: "apikey", APIKey: "alpha-key"},
			ObjectStore: TailnetObjectStore{Flow: ObjectStoreConfig{
				Endpoint:        "https://s3.eu-west-2.amazonaws.com",
				Region:          "eu-west-2",
				Bucket:          "alpha-flows",
				Prefix:          "exports/",
				AccessKeyID:     "ALPHA-ACCESS-canary",
				SecretAccessKey: "ALPHA-SECRET-canary",
				SessionToken:    "ALPHA-SESSION-canary",
			}},
		},
		{
			Name: "beta.example.com",
			Auth: TailscaleAuth{Method: "apikey", APIKey: "beta-key"},
			ObjectStore: TailnetObjectStore{Flow: ObjectStoreConfig{
				Endpoint:        "https://s3.us-east-1.amazonaws.com",
				Region:          "us-east-1",
				Bucket:          "beta-flows",
				AccessKeyID:     "BETA-ACCESS-canary",
				SecretAccessKey: "BETA-SECRET-canary",
				SessionToken:    "BETA-SESSION-canary",
			}},
		},
	}
	if tune != nil {
		tune(c)
	}
	return c
}

func TestValidate_ObjectStoreMultiTailnetAcceptsPerTailnetDestinations(t *testing.T) {
	if err := multiTailnetObjectStoreConfig(nil).Validate(); err != nil {
		t.Fatalf("Validate rejected complete per-tailnet destinations: %v", err)
	}
}

// Legacy/single mode keeps reading the one global block, unchanged.
func TestFlowObjectStore_LegacyModeUsesGlobalBlock(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.AccessKeyID = "GLOBAL-ACCESS-canary"
	})
	got, ok := c.FlowObjectStore("example.com")
	if !ok {
		t.Fatal("FlowObjectStore(single tailnet) = !ok, want the global block")
	}
	if got.Bucket != "flows" || got.Region != "eu-west-2" {
		t.Errorf("dest = %s/%s, want flows/eu-west-2 from the global block", got.Region, got.Bucket)
	}
	if got.AccessKeyID.Reveal() != "GLOBAL-ACCESS-canary" {
		t.Errorf("dest credential = %q, want the global one", got.AccessKeyID.Reveal())
	}
}

// Each runtime resolves ONLY its own entry: no other tailnet's destination or
// credential is reachable through the resolution seam.
func TestFlowObjectStore_MultiTailnetSelectsItsOwnDestination(t *testing.T) {
	c := multiTailnetObjectStoreConfig(nil)

	alpha, ok := c.FlowObjectStore("alpha.example.com")
	if !ok {
		t.Fatal("FlowObjectStore(alpha) = !ok, want its own destination")
	}
	if alpha.Bucket != "alpha-flows" || alpha.Region != "eu-west-2" || alpha.Prefix != "exports/" {
		t.Errorf("alpha dest = %s/%s/%s, want eu-west-2/alpha-flows/exports/",
			alpha.Region, alpha.Bucket, alpha.Prefix)
	}
	beta, ok := c.FlowObjectStore("beta.example.com")
	if !ok {
		t.Fatal("FlowObjectStore(beta) = !ok, want its own destination")
	}
	if beta.Bucket != "beta-flows" || beta.Region != "us-east-1" {
		t.Errorf("beta dest = %s/%s, want us-east-1/beta-flows", beta.Region, beta.Bucket)
	}

	// Credential isolation: alpha's material must be unreachable while holding
	// beta's destination, and vice versa.
	for name, pair := range map[string]struct {
		dest      ObjectStoreConfig
		forbidden []string
	}{
		"alpha": {alpha, []string{"BETA-ACCESS-canary", "BETA-SECRET-canary", "BETA-SESSION-canary"}},
		"beta":  {beta, []string{"ALPHA-ACCESS-canary", "ALPHA-SECRET-canary", "ALPHA-SESSION-canary"}},
	} {
		revealed := pair.dest.AccessKeyID.Reveal() + "|" + pair.dest.SecretAccessKey.Reveal() + "|" +
			pair.dest.SessionToken.Reveal()
		for _, forbidden := range pair.forbidden {
			if strings.Contains(revealed, forbidden) {
				t.Errorf("%s destination exposed another tailnet's credential %q", name, forbidden)
			}
		}
	}
	if alpha.AccessKeyID.Reveal() != "ALPHA-ACCESS-canary" || beta.AccessKeyID.Reveal() != "BETA-ACCESS-canary" {
		t.Errorf("per-tailnet credentials not plumbed: alpha=%q beta=%q",
			alpha.AccessKeyID.Reveal(), beta.AccessKeyID.Reveal())
	}
	if _, ok := c.FlowObjectStore("gamma.example.com"); ok {
		t.Error("FlowObjectStore(unconfigured tailnet) = ok, want no destination")
	}
}

// The global block is never a fallback in multi-tailnet mode: two runtimes
// reading one feed is exactly the cross-attribution hazard #283 closed.
func TestFlowObjectStore_MultiTailnetNeverInheritsGlobalBlock(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.global.example"
		c.Collectors.Flowlogs.ObjectStore.Region = "global-region-1"
		c.Collectors.Flowlogs.ObjectStore.Bucket = "GLOBAL-BUCKET-canary"
		c.Collectors.Flowlogs.ObjectStore.AccessKeyID = "GLOBAL-ACCESS-canary"
		// beta declares nothing of its own.
		c.Tailnets[1].ObjectStore = TailnetObjectStore{}
	})

	beta, ok := c.FlowObjectStore("beta.example.com")
	if !ok {
		t.Fatal("FlowObjectStore(beta) = !ok, want the (empty) entry destination")
	}
	if beta.Bucket != "" || beta.Endpoint != "" || beta.Region != "" {
		t.Errorf("beta inherited the global destination: %s/%s/%s", beta.Endpoint, beta.Region, beta.Bucket)
	}
	if beta.AccessKeyID.Reveal() != "" {
		t.Errorf("beta inherited the global credential %q", beta.AccessKeyID.Reveal())
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a tailnet with no object-store destination of its own")
	}
	for _, want := range []string{"beta.example.com", "tailnets[1]", "inherit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// Destination identity and credentials are never defaulted, but the tuning
// budgets are: a list entry cannot pass through Default(), and requiring an
// operator to restate ten byte limits per tailnet would guarantee drift.
func TestFlowObjectStore_MultiTailnetBackfillsBudgetsNotDestination(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[0].ObjectStore.Flow = ObjectStoreConfig{
			Endpoint: "https://s3.eu-west-2.amazonaws.com",
			Region:   "eu-west-2",
			Bucket:   "alpha-flows",
		}
	})
	got, ok := c.FlowObjectStore("alpha.example.com")
	if !ok {
		t.Fatal("FlowObjectStore(alpha) = !ok")
	}
	def := Default().Collectors.Flowlogs.ObjectStore
	if got.Interval != def.Interval || got.Lookback != def.Lookback || got.InitialLookback != def.InitialLookback {
		t.Errorf("timings = %v/%v/%v, want the built-in defaults %v/%v/%v",
			got.Interval.D(), got.Lookback.D(), got.InitialLookback.D(),
			def.Interval.D(), def.Lookback.D(), def.InitialLookback.D())
	}
	if got.MaxObjects != def.MaxObjects ||
		got.MaxObjectWireBytes != def.MaxObjectWireBytes ||
		got.MaxObjectDecompressedBytes != def.MaxObjectDecompressedBytes ||
		got.MaxObjectRecords != def.MaxObjectRecords ||
		got.MaxCycleWireBytes != def.MaxCycleWireBytes ||
		got.MaxCycleDecompressedBytes != def.MaxCycleDecompressedBytes ||
		got.MaxCycleRecords != def.MaxCycleRecords {
		t.Errorf("budgets = %+v, want the built-in defaults", got)
	}
	if got.AccessKeyID != "" || got.SecretAccessKey != "" || got.SessionToken != "" {
		t.Error("defaulting invented credentials for a per-tailnet destination")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate rejected a destination that omits only the tuning budgets: %v", err)
	}
}

// An incomplete per-tailnet destination is rejected by name, so an MSP with many
// entries knows which one to fix.
func TestValidate_ObjectStoreMultiTailnetIncompleteDestinationNamesTailnet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*Config)
		want   string
	}{
		{"no bucket", func(c *Config) { c.Tailnets[1].ObjectStore.Flow.Bucket = "" }, "bucket"},
		{"no endpoint", func(c *Config) { c.Tailnets[1].ObjectStore.Flow.Endpoint = "" }, "endpoint"},
		{"no region", func(c *Config) { c.Tailnets[1].ObjectStore.Flow.Region = "" }, "region"},
		{"negative budget", func(c *Config) { c.Tailnets[1].ObjectStore.Flow.MaxObjectRecords = -3 }, "max_object_records"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := multiTailnetObjectStoreConfig(tc.break_).Validate()
			if err == nil {
				t.Fatal("Validate accepted an incomplete per-tailnet object-store destination")
			}
			for _, want := range []string{tc.want, "beta.example.com", "tailnets[1]"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
			for _, forbidden := range []string{
				"ALPHA-ACCESS-canary", "ALPHA-SECRET-canary", "ALPHA-SESSION-canary",
				"BETA-ACCESS-canary", "BETA-SECRET-canary", "BETA-SESSION-canary",
			} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("validation error leaked credential %q: %v", forbidden, err)
				}
			}
		})
	}
}

// Two tailnets pointing at ONE feed re-creates the cross-attribution hazard by
// hand, so the normalized (endpoint, region, bucket, prefix, path_style) tuple
// must be unique across entries.
func TestValidate_ObjectStoreMultiTailnetRejectsDuplicateFeed(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		// Same feed as alpha, written differently: trailing slash, upper-case
		// host, and an unslashed prefix.
		c.Tailnets[1].ObjectStore.Flow.Endpoint = "https://S3.EU-WEST-2.amazonaws.com/"
		c.Tailnets[1].ObjectStore.Flow.Region = "EU-WEST-2"
		c.Tailnets[1].ObjectStore.Flow.Bucket = "alpha-flows"
		c.Tailnets[1].ObjectStore.Flow.Prefix = "/exports"
	})
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted two tailnets reading the same object-store feed")
	}
	for _, want := range []string{"alpha.example.com", "beta.example.com", "tailnets[0]", "tailnets[1]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
}

// A prefix difference is a different feed, and must stay allowed: it is how one
// bucket serves several tailnets.
func TestValidate_ObjectStoreMultiTailnetAllowsSameBucketDistinctPrefix(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[1].ObjectStore.Flow = c.Tailnets[0].ObjectStore.Flow
		c.Tailnets[1].ObjectStore.Flow.Prefix = "exports/beta/"
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected one bucket with two distinct prefixes: %v", err)
	}
}

// s3.New refuses an unparseable or non-HTTP endpoint, and a client that cannot
// be built is an immutable structural fault — reject it at startup, where the
// message can name the key, rather than at collector construction.
func TestValidate_ObjectStoreRejectsStructurallyUnusableEndpoint(t *testing.T) {
	for _, endpoint := range []string{"s3.eu-west-2.amazonaws.com", "ftp://s3.example.com", "://nope", "https://"} {
		t.Run(endpoint, func(t *testing.T) {
			legacy := objectStoreConfig(func(c *Config) {
				c.Collectors.Flowlogs.ObjectStore.Endpoint = endpoint
			})
			if err := legacy.Validate(); err == nil {
				t.Errorf("Validate accepted global endpoint %q", endpoint)
			}
			multi := multiTailnetObjectStoreConfig(func(c *Config) {
				c.Tailnets[1].ObjectStore.Flow.Endpoint = endpoint
			})
			err := multi.Validate()
			if err == nil {
				t.Fatalf("Validate accepted per-tailnet endpoint %q", endpoint)
			}
			if !strings.Contains(err.Error(), "beta.example.com") {
				t.Errorf("error = %v, want it to name the offending tailnet", err)
			}
		})
	}
}

// A tailnets: list of ONE is still multi-tailnet CONFIG: the destination lives
// under the entry, not in the global block.
func TestValidate_ObjectStoreSingleEntryListStillRequiresItsOwnDestination(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.global.example"
		c.Collectors.Flowlogs.ObjectStore.Region = "global-region-1"
		c.Collectors.Flowlogs.ObjectStore.Bucket = "global-bucket"
		c.Tailnets = c.Tailnets[:1]
		c.Tailnets[0].ObjectStore = TailnetObjectStore{}
	})
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate let a one-entry tailnets list fall back to the global destination")
	}
	if !strings.Contains(err.Error(), "alpha.example.com") {
		t.Errorf("error = %v, want it to name the tailnet", err)
	}
}

// Per-entry advisories name their own key, so an MSP can tell which tailnet is
// signing over plaintext.
func TestWarnings_TailnetObjectStoreAdvisoriesNameTheEntry(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[1].ObjectStore.Flow.Endpoint = "http://storage.example.com:9000"
		c.Tailnets[1].ObjectStore.Flow.AllowInsecureHTTP = true
		c.Tailnets[1].ObjectStore.Flow.Interval = dur(minutesDur(10))
		c.Tailnets[1].ObjectStore.Flow.Lookback = dur(minutesDur(1))
	})
	warnings := c.Warnings()
	for _, want := range []string{
		"tailnets[1].objectstore.flow.allow_insecure_http",
		"tailnets[1].objectstore.flow.lookback",
	} {
		if !hasWarning(warnings, want) {
			t.Errorf("warnings = %v, want one naming %q", warnings, want)
		}
	}
}

// A global block left behind while migrating to a tailnets: list is inert, and
// silently-ignored destination config is exactly how an operator concludes the
// exporter is broken. Say so.
func TestWarnings_ObjectStoreGlobalBlockIsIgnoredInMultiTailnetMode(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.global.example"
		c.Collectors.Flowlogs.ObjectStore.Region = "global-region-1"
		c.Collectors.Flowlogs.ObjectStore.Bucket = "global-bucket"
	})
	if !hasWarning(c.Warnings(), "collectors.flowlogs.objectstore is ignored") {
		t.Errorf("warnings = %v, want one saying the global block is ignored", c.Warnings())
	}
	// Nothing to say when it was never set.
	if hasWarning(multiTailnetObjectStoreConfig(nil).Warnings(), "collectors.flowlogs.objectstore is ignored") {
		t.Error("warned about an unset global block")
	}
}

// Per-tailnet credentials are config.Secret like the global ones: a config dump
// can never leak them.
func TestTailnetObjectStoreSecretsRedactInDumps(t *testing.T) {
	c := multiTailnetObjectStoreConfig(nil)
	dump := fmt.Sprintf("%+v\n%#v", c, c)
	yamlDump, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Info("config", "tailnets", c.Tailnets)
	for _, secret := range []string{
		"ALPHA-ACCESS-canary", "ALPHA-SECRET-canary", "ALPHA-SESSION-canary",
		"BETA-ACCESS-canary", "BETA-SECRET-canary", "BETA-SESSION-canary",
	} {
		if strings.Contains(dump, secret) {
			t.Errorf("config dump leaked per-tailnet credential %q", secret)
		}
		if strings.Contains(string(yamlDump), secret) {
			t.Errorf("config YAML dump leaked per-tailnet credential %q", secret)
		}
		if strings.Contains(logs.String(), secret) {
			t.Errorf("config log leaked per-tailnet credential %q", secret)
		}
	}
	if got := c.Tailnets[0].ObjectStore.Flow.SecretAccessKey.Reveal(); got != "ALPHA-SECRET-canary" {
		t.Errorf("Reveal() = %q, want the real value preserved at the point of use", got)
	}
}

// Each of the three required fields fails a request in a different, confusing
// way if left empty, so each is rejected by name at startup instead.

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

// This test used to assert that collectors.auditlogs.source=objectstore was
// REJECTED outright, on the premise that Tailscale does not export configuration
// logs to object storage. That premise was WRONG — a live capture on 2026-07-27
// (#288) produced real configuration-log export objects — so the rejection went
// away. What survives is the real rule: the audit source is accepted, but only
// with an audit destination of its own.
//
// See internal/config/objectstore_audit_test.go for the positive cases.
func TestValidate_AuditObjectStoreNeedsItsOwnDestination(t *testing.T) {
	c := objectStoreConfig(func(c *Config) { c.Collectors.Auditlogs.Source = "objectstore" })
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted auditlogs.source=objectstore with only a FLOW destination configured")
	}
	if !strings.Contains(err.Error(), "collectors.auditlogs.objectstore") {
		t.Errorf("error = %v, want it to name the missing audit destination key", err)
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

// The layout is an explicit operator choice with exactly two values. There is
// deliberately no "auto": a flat and a partitioned bucket are distinguishable only
// by listing them, and guessing changes what the durable scan cursors mean.
func TestValidate_ObjectStoreLayoutEnum(t *testing.T) {
	for _, tc := range []struct {
		layout string
		wantOK bool
	}{
		{"", true}, // unset means partitioned
		{"partitioned", true},
		{"flat", true},
		{"auto", false},
		{"Flat", false},
		{"partitioned/flat", false},
	} {
		t.Run(tc.layout, func(t *testing.T) {
			err := objectStoreConfig(func(c *Config) {
				c.Collectors.Flowlogs.ObjectStore.Layout = tc.layout
			}).Validate()
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Validate rejected layout %q: %v", tc.layout, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate accepted layout %q", tc.layout)
			}
			for _, want := range []string{"layout", "partitioned", "flat"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// #284 gives every tailnets[] entry its own complete destination, so the layout is
// validated per entry too, and the message must name the entry's key.
func TestValidate_ObjectStoreLayoutEnumPerTailnet(t *testing.T) {
	err := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[1].ObjectStore.Flow.Layout = "auto"
	}).Validate()
	if err == nil {
		t.Fatal("Validate accepted an unsupported per-tailnet layout")
	}
	if !strings.Contains(err.Error(), "tailnets[1].objectstore.flow.layout") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

// Flat is correct for a copied export and wrong as a default, so choosing it is
// advised — never an error.
func TestWarnings_ObjectStoreFlatLayoutCosts(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Layout = "flat"
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected layout flat: %v", err)
	}
	if !hasWarning(c.Warnings(), "collectors.flowlogs.objectstore.layout") {
		t.Errorf("warnings = %v, want an advisory naming the layout key", c.Warnings())
	}
	if !hasWarning(c.Warnings(), "LIST") {
		t.Errorf("warnings = %v, want the extra LIST cost stated", c.Warnings())
	}

	partitioned := objectStoreConfig(nil)
	if hasWarning(partitioned.Warnings(), "objectstore.layout") {
		t.Errorf("warnings = %v, want none for the partitioned default", partitioned.Warnings())
	}
}

func TestWarnings_ObjectStoreFlatLayoutPerTailnet(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[1].ObjectStore.Flow.Layout = "flat"
	})
	if !hasWarning(c.Warnings(), "tailnets[1].objectstore.flow.layout") {
		t.Errorf("warnings = %v, want an advisory naming the entry's layout key", c.Warnings())
	}
}

// The zero value must resolve to partitioned at the ONE seam the app reads a
// destination through, so no caller has to re-derive it. A tailnets[] entry cannot
// pass through Default(), which is exactly where an empty value would otherwise
// reach the collector.
func TestFlowObjectStore_EmptyLayoutResolvesToPartitioned(t *testing.T) {
	if got := Default().Collectors.Flowlogs.ObjectStore.Layout; got != ObjectStoreLayoutPartitioned {
		t.Errorf("default layout = %q, want %q", got, ObjectStoreLayoutPartitioned)
	}

	single := objectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.ObjectStore.Layout = ""
	})
	dest, ok := single.FlowObjectStore("example.com")
	if !ok {
		t.Fatal("FlowObjectStore(single) = !ok")
	}
	if dest.Layout != ObjectStoreLayoutPartitioned {
		t.Errorf("single-mode layout = %q, want %q", dest.Layout, ObjectStoreLayoutPartitioned)
	}

	multi := multiTailnetObjectStoreConfig(nil) // neither entry sets a layout
	entry, ok := multi.FlowObjectStore("beta.example.com")
	if !ok {
		t.Fatal("FlowObjectStore(beta) = !ok")
	}
	if entry.Layout != ObjectStoreLayoutPartitioned {
		t.Errorf("per-tailnet layout = %q, want %q", entry.Layout, ObjectStoreLayoutPartitioned)
	}

	flat := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[0].ObjectStore.Flow.Layout = ObjectStoreLayoutFlat
	})
	alpha, ok := flat.FlowObjectStore("alpha.example.com")
	if !ok {
		t.Fatal("FlowObjectStore(alpha) = !ok")
	}
	if alpha.Layout != ObjectStoreLayoutFlat {
		t.Errorf("explicit per-tailnet layout = %q, want %q", alpha.Layout, ObjectStoreLayoutFlat)
	}
}

// The layout does not identify a FEED: two tailnets pointed at one bucket+prefix
// are the same physical objects however they are enumerated, so a differing layout
// must not let the duplicate past the #284 dedupe check.
func TestValidate_ObjectStoreLayoutIsNotPartOfTheFeedIdentity(t *testing.T) {
	c := multiTailnetObjectStoreConfig(func(c *Config) {
		c.Tailnets[1].ObjectStore.Flow.Endpoint = c.Tailnets[0].ObjectStore.Flow.Endpoint
		c.Tailnets[1].ObjectStore.Flow.Region = c.Tailnets[0].ObjectStore.Flow.Region
		c.Tailnets[1].ObjectStore.Flow.Bucket = c.Tailnets[0].ObjectStore.Flow.Bucket
		c.Tailnets[1].ObjectStore.Flow.Prefix = c.Tailnets[0].ObjectStore.Flow.Prefix
		c.Tailnets[0].ObjectStore.Flow.Layout = ObjectStoreLayoutPartitioned
		c.Tailnets[1].ObjectStore.Flow.Layout = ObjectStoreLayoutFlat
	})
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted two tailnets on one feed because their layouts differ")
	}
}
