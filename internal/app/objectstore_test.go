package app

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

func TestObjectStoreOptionsMapsEveryConfigFieldExactly(t *testing.T) {
	got := objectStoreOptions(config.ObjectStoreConfig{
		Prefix:                     "prefix-marker",
		Layout:                     config.ObjectStoreLayoutFlat,
		Interval:                   config.Duration(11 * time.Second),
		Lookback:                   config.Duration(13 * time.Minute),
		InitialLookback:            config.Duration(17 * time.Hour),
		MaxObjects:                 19,
		MaxObjectWireBytes:         21,
		MaxObjectDecompressedBytes: 23,
		MaxObjectRecords:           29,
		MaxCycleWireBytes:          30,
		MaxCycleDecompressedBytes:  31,
		MaxCycleRecords:            37,
	})
	want := objectstore.Options{
		Prefix:                     "prefix-marker",
		Layout:                     objectstore.LayoutFlat,
		Interval:                   11 * time.Second,
		Lookback:                   13 * time.Minute,
		InitialLookback:            17 * time.Hour,
		MaxObjects:                 19,
		MaxObjectWireBytes:         21,
		MaxObjectDecompressedBytes: 23,
		MaxObjectRecords:           29,
		MaxCycleWireBytes:          30,
		MaxCycleDecompressedBytes:  31,
		MaxCycleRecords:            37,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("objectStoreOptions() = %+v, want exact config-only mapping %+v", got, want)
	}
}

// multiTailnetObjectStoreCfg is a two-tailnet config where each entry owns a
// complete destination and its own credentials (the #284 contract).
func multiTailnetObjectStoreCfg(tune func(*config.Config)) *config.Config {
	c := config.Default()
	c.Tailscale.Tailnet = ""
	c.Collectors.Flowlogs.Source = "objectstore"
	c.Tailnets = []config.TailnetConfig{
		{
			Name: "alpha.example.com",
			Auth: config.TailscaleAuth{Method: "apikey", APIKey: "alpha-key"},
			ObjectStore: config.TailnetObjectStore{Flow: config.ObjectStoreConfig{
				Endpoint:        "https://s3.eu-west-2.amazonaws.com",
				Region:          "eu-west-2",
				Bucket:          "alpha-flows",
				Prefix:          "exports/",
				Interval:        config.Duration(90 * time.Second),
				AccessKeyID:     "ALPHA-ACCESS-canary",
				SecretAccessKey: "ALPHA-SECRET-canary",
				SessionToken:    "ALPHA-SESSION-canary",
			}},
		},
		{
			Name: "beta.example.com",
			Auth: config.TailscaleAuth{Method: "apikey", APIKey: "beta-key"},
			ObjectStore: config.TailnetObjectStore{Flow: config.ObjectStoreConfig{
				Endpoint:        "https://s3.us-east-1.amazonaws.com",
				Region:          "us-east-1",
				Bucket:          "beta-flows",
				Interval:        config.Duration(150 * time.Second),
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

// Legacy/single mode keeps reading the one global block.
func TestObjectStoreDestinationFor_LegacyUsesGlobalBlock(t *testing.T) {
	cfg := objectStoreTestConfig(func(c *config.Config) {
		c.Collectors.Flowlogs.ObjectStore.AccessKeyID = "GLOBAL-ACCESS-canary"
	})
	rt := &tailnetRuntime{configuredName: "example.com", name: "example.com"}

	got, err := objectStoreDestinationFor(objectStoreFlowFeed, rt, runtimeDeps{cfg: cfg})
	if err != nil {
		t.Fatalf("objectStoreDestinationFor: %v", err)
	}
	if got.Bucket != "flows" || got.AccessKeyID.Reveal() != "GLOBAL-ACCESS-canary" {
		t.Errorf("dest = %s / %q, want the global block", got.Bucket, got.AccessKeyID.Reveal())
	}
}

// Each runtime resolves its OWN entry, and only that entry's credentials are
// ever revealed while building it.
func TestObjectStoreDestinationFor_MultiTailnetIsolatesDestinationsAndCredentials(t *testing.T) {
	cfg := multiTailnetObjectStoreCfg(func(c *config.Config) {
		// A fully-populated global block must be invisible to both runtimes.
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.global.example"
		c.Collectors.Flowlogs.ObjectStore.Region = "global-region-1"
		c.Collectors.Flowlogs.ObjectStore.Bucket = "GLOBAL-BUCKET-canary"
		c.Collectors.Flowlogs.ObjectStore.AccessKeyID = "GLOBAL-ACCESS-canary"
	})
	d := runtimeDeps{cfg: cfg, multi: true}

	alpha, err := objectStoreDestinationFor(objectStoreFlowFeed, &tailnetRuntime{configuredName: "alpha.example.com"}, d)
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	beta, err := objectStoreDestinationFor(objectStoreFlowFeed, &tailnetRuntime{configuredName: "beta.example.com"}, d)
	if err != nil {
		t.Fatalf("beta: %v", err)
	}
	if alpha.Bucket != "alpha-flows" || beta.Bucket != "beta-flows" {
		t.Errorf("buckets = %q/%q, want alpha-flows/beta-flows", alpha.Bucket, beta.Bucket)
	}
	if alpha.Interval.D() != 90*time.Second || beta.Interval.D() != 150*time.Second {
		t.Errorf("intervals = %v/%v, want 90s/150s from each entry", alpha.Interval.D(), beta.Interval.D())
	}
	for name, pair := range map[string]struct {
		dest      config.ObjectStoreConfig
		forbidden []string
	}{
		"alpha": {alpha, []string{"BETA-ACCESS-canary", "GLOBAL-ACCESS-canary", "GLOBAL-BUCKET-canary"}},
		"beta":  {beta, []string{"ALPHA-ACCESS-canary", "GLOBAL-ACCESS-canary", "GLOBAL-BUCKET-canary"}},
	} {
		reachable := pair.dest.Bucket + "|" + pair.dest.Endpoint + "|" +
			pair.dest.AccessKeyID.Reveal() + "|" + pair.dest.SecretAccessKey.Reveal() + "|" +
			pair.dest.SessionToken.Reveal()
		for _, forbidden := range pair.forbidden {
			if strings.Contains(reachable, forbidden) {
				t.Errorf("%s runtime could reach %q", name, forbidden)
			}
		}
	}
}

// A one-entry tailnets: list is still multi-tailnet CONFIG even though there is
// only one runtime (d.multi is false): the destination lives under the entry.
func TestObjectStoreDestinationFor_SingleEntryListUsesTheEntry(t *testing.T) {
	cfg := multiTailnetObjectStoreCfg(func(c *config.Config) {
		c.Tailnets = c.Tailnets[:1]
		c.Collectors.Flowlogs.ObjectStore.Bucket = "GLOBAL-BUCKET-canary"
	})
	got, err := objectStoreDestinationFor(
		objectStoreFlowFeed,
		&tailnetRuntime{configuredName: "alpha.example.com"},
		runtimeDeps{cfg: cfg, multi: false})
	if err != nil {
		t.Fatalf("objectStoreDestinationFor: %v", err)
	}
	if got.Bucket != "alpha-flows" {
		t.Errorf("bucket = %q, want alpha-flows (never the global block)", got.Bucket)
	}
}

// A runtime with no destination of its own must not silently fall back.
func TestObjectStoreDestinationFor_UnconfiguredTailnetIsAnError(t *testing.T) {
	cfg := multiTailnetObjectStoreCfg(nil)
	_, err := objectStoreDestinationFor(objectStoreFlowFeed, &tailnetRuntime{configuredName: "gamma.example.com"},
		runtimeDeps{cfg: cfg, multi: true})
	if err == nil {
		t.Fatal("objectStoreDestinationFor = nil error, want a refusal for an unconfigured tailnet")
	}
	if !strings.Contains(err.Error(), "gamma.example.com") {
		t.Errorf("error = %v, want it to name the tailnet", err)
	}
}

// The checkpoint identity is the LITERAL configured tailnet name, including the
// "-" placeholder, and the #453 namespace shape is frozen.
func TestObjectStoreScopeUsesLiteralConfiguredTailnetName(t *testing.T) {
	dest := config.ObjectStoreConfig{
		Endpoint: "https://s3.eu-west-2.amazonaws.com",
		Bucket:   "flows",
		Prefix:   "exports/",
	}
	got := objectStoreScope("flow", "-", dest)
	want := objectstore.CheckpointScope{
		Tailnet:  "-",
		Provider: "s3",
		Signal:   "flow",
		Feed:     objectstore.FeedID(dest.Endpoint, dest.Bucket, dest.Prefix),
	}
	if got != want {
		t.Fatalf("objectStoreScope = %+v, want %+v", got, want)
	}
	ns, err := got.Namespace()
	if err != nil {
		t.Fatalf("Namespace: %v", err)
	}
	wantNS := "objectstore/v1/" + base64.RawURLEncoding.EncodeToString([]byte("-")) + "/s3/flow/" + want.Feed
	if ns != wantNS {
		t.Errorf("namespace = %q, want %q", ns, wantNS)
	}
}

// The scope's tailnet comes from the configured name, never from the RESOLVED
// display name: resolution is a label concern, and letting it into the
// checkpoint path would move a runtime's durable state when a lookup succeeds.
func TestObjectStoreTailnetIdentityPrefersConfiguredName(t *testing.T) {
	cfg := objectStoreTestConfig(func(c *config.Config) { c.Tailscale.Tailnet = "-" })
	rt := &tailnetRuntime{configuredName: "-", name: "resolved.example.com"}
	if got := objectStoreTailnetIdentity(rt, cfg); got != "-" {
		t.Errorf("identity = %q, want the literal configured \"-\"", got)
	}
	// The single-runtime assembly seam leaves configuredName empty.
	if got := objectStoreTailnetIdentity(&tailnetRuntime{}, cfg); got != "-" {
		t.Errorf("identity = %q, want the configured tailscale.tailnet fallback", got)
	}
}
