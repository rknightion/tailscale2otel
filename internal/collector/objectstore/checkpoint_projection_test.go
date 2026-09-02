package objectstore

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

func TestShardKeyUsesObjectStoreCollectorNamespace(t *testing.T) {
	base := "objectstore/v1/ZXhhbXBsZS5jb20/s3/flow/feed"
	for name, row := range map[string]string{
		"cursor": base + "/" + cursorKey,
		"scan":   base + "/" + scanRowPrefix("flow/") + base64.RawURLEncoding.EncodeToString([]byte("flow/object.ndjson")),
		"seen":   base + "/" + seenRow("identity"),
		"gap":    base + "/gap/pending/a/b/1/2",
	} {
		if got := collector.ShardKey(row); got != base {
			t.Fatalf("%s shard = %q, want %q", name, got, base)
		}
	}
	if got := collector.ShardKey("flowlogs"); got != "flowlogs" {
		t.Fatalf("ordinary cursor shard = %q", got)
	}
	if got := collector.ShardKey("flowlogs/replay/seen/hash"); got != "flowlogs" {
		t.Fatalf("single-tailnet replay shard = %q, want flowlogs", got)
	}
	if got := collector.ShardKey("tailnet/flowlogs/replay/seen/hash"); got != "tailnet/flowlogs" {
		t.Fatalf("multi-tailnet replay shard = %q, want tailnet/flowlogs", got)
	}
	if got := collector.ShardKey("flowlogs/auditlogs"); got != "flowlogs/auditlogs" {
		t.Fatalf("collector-like tailnet shard = %q, want flowlogs/auditlogs", got)
	}
	if got := collector.ShardKey("tailnet/acl/revision/opaque"); got != "tailnet/acl" {
		t.Fatalf("ACL state shard = %q, want tailnet/acl", got)
	}
}

// Production Collect coverage for these rows lives with the collector
// contracts: atomicity_test.go asserts seen/gap writes after Collect, while
// layout_test.go asserts the scan row and objectstore_test.go the cursor path.
// This test owns only the storage shard mapping shared with config projection.

func TestProjectCheckpointSizeUsesProductionKeyConstruction(t *testing.T) {
	feed := CheckpointProjectionFeed{
		Tailnet:         "example.com",
		Provider:        "s3",
		Signal:          "k8s_audit",
		Endpoint:        "https://s3.example",
		Bucket:          "recordings",
		MaxSeenKeys:     2,
		MaxSeenKeysPath: "collectors.k8s_audit.objectstore.max_seen_keys",
	}
	got, err := ProjectCheckpointSize([]CheckpointProjectionFeed{feed}, collector.ShardKey, collector.KubernetesCheckpointDataLimit)
	if err != nil {
		t.Fatalf("ProjectCheckpointSize: %v", err)
	}
	scope := CheckpointScope{Tailnet: feed.Tailnet, Provider: feed.Provider, Signal: feed.Signal, Feed: FeedID(feed.Endpoint, feed.Bucket, feed.Prefix)}
	namespace, err := scope.Namespace()
	if err != nil {
		t.Fatalf("Namespace: %v", err)
	}
	shard, ok := got.Shards[namespace]
	if !ok {
		t.Fatalf("projection shards = %+v, want %q", got.Shards, namespace)
	}
	identity := projectionRecorderIdentity(0)
	key := namespace + "/" + seenRow(identity)
	if !strings.Contains(key, "/seen/"+base64.RawURLEncoding.EncodeToString([]byte(identity))) {
		t.Fatalf("projected key = %q, want namespaced seen row", key)
	}
	rows := map[string]time.Time{
		key: projectionTime,
		namespace + "/" + seenRow(projectionRecorderIdentity(1)): projectionTime,
	}
	wantData, _, err := collector.EncodeKubernetesCheckpointShard(namespace, rows)
	if err != nil {
		t.Fatalf("EncodeKubernetesCheckpointShard: %v", err)
	}
	if shard.Bytes != len(wantData) || shard.Entries != 2 {
		t.Fatalf("shard = %+v, want bytes=%d entries=2", shard, len(wantData))
	}
	if len(shard.MaxSeenKeysPaths) != 1 || shard.MaxSeenKeysPaths[0] != feed.MaxSeenKeysPath {
		t.Fatalf("paths = %v, want [%s]", shard.MaxSeenKeysPaths, feed.MaxSeenKeysPath)
	}
}

func TestProjectCheckpointSizeSplitsBySuppliedShardFunction(t *testing.T) {
	feeds := []CheckpointProjectionFeed{{Tailnet: "one", Provider: "s3", Signal: "flow", Endpoint: "https://s3.example", Bucket: "one", MaxSeenKeys: 2, MaxSeenKeysPath: "one.max_seen_keys"}}
	shardFn := func(key string) string {
		if strings.Contains(key, "/seen/"+base64.RawURLEncoding.EncodeToString([]byte(projectionRecorderIdentity(0)))) {
			return "a"
		}
		return "b"
	}
	got, err := ProjectCheckpointSize(feeds, shardFn, collector.KubernetesCheckpointDataLimit)
	if err != nil {
		t.Fatalf("ProjectCheckpointSize: %v", err)
	}
	if len(got.Shards) != 2 || got.Shards["a"].Entries != 1 || got.Shards["b"].Entries != 1 {
		t.Fatalf("shards = %+v, want one entry in each supplied shard", got.Shards)
	}
}

func TestProjectCheckpointSizeUsesRealGzipShards(t *testing.T) {
	feeds := []CheckpointProjectionFeed{
		{Tailnet: "one.example", Provider: "s3", Signal: "flow", Endpoint: "https://s3.example", Bucket: "one", MaxSeenKeys: 5000, MaxSeenKeysPath: "one.max_seen_keys"},
		{Tailnet: "one.example", Provider: "s3", Signal: "audit", Endpoint: "https://s3.example", Bucket: "two", MaxSeenKeys: 5000, MaxSeenKeysPath: "two.max_seen_keys"},
	}
	got, err := ProjectCheckpointSize(feeds, collector.ShardKey, collector.KubernetesCheckpointDataLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Shards) != 2 {
		t.Fatalf("shards = %d, want 2", len(got.Shards))
	}
	for shard, details := range got.Shards {
		if details.Entries != 5000 || details.OverLimit {
			t.Fatalf("shard %q = %+v", shard, details)
		}
	}
}

func TestProjectCheckpointSizeMeasuredConfigurationsAfterSharding(t *testing.T) {
	feed := func(tailnet, signal, bucket string) CheckpointProjectionFeed {
		return CheckpointProjectionFeed{
			Tailnet: tailnet, Provider: "s3", Signal: signal,
			Endpoint: "https://s3.example", Bucket: bucket, MaxSeenKeys: 5000,
			MaxSeenKeysPath: tailnet + "." + signal + ".max_seen_keys",
		}
	}
	threeSignals := func(tailnet, suffix string) []CheckpointProjectionFeed {
		return []CheckpointProjectionFeed{
			feed(tailnet, "flow", suffix+"-flow"),
			feed(tailnet, "audit", suffix+"-audit"),
			feed(tailnet, "k8s_audit", suffix+"-k8s"),
		}
	}
	cases := []struct {
		name       string
		feeds      []CheckpointProjectionFeed
		wantShards int
		wantTotal  int
		wantMax    int
	}{
		{name: "one feed one tailnet", feeds: []CheckpointProjectionFeed{feed("example.com", "flow", "flows")}, wantShards: 1, wantTotal: 15846, wantMax: 15846},
		{name: "three feeds one tailnet", feeds: threeSignals("example.com", "one"), wantShards: 3, wantTotal: 47381, wantMax: 15847},
		{name: "three feeds three tailnets", feeds: append(append(threeSignals("alpha.example.com", "alpha"), threeSignals("beta.example.com", "beta")...), threeSignals("gamma.example.com", "gamma")...), wantShards: 9, wantTotal: 143519, wantMax: 16302},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectCheckpointSize(tc.feeds, collector.ShardKey, collector.KubernetesCheckpointDataLimit)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Shards) != tc.wantShards {
				t.Fatalf("shards = %d, want %d", len(got.Shards), tc.wantShards)
			}
			total, max := 0, 0
			for shard, details := range got.Shards {
				if details.Entries != 5000 || details.OverLimit {
					t.Fatalf("shard %q = %+v, want 5000 entries below limit", shard, details)
				}
				total += details.Bytes
				if details.Bytes > max {
					max = details.Bytes
				}
			}
			if total != tc.wantTotal || max != tc.wantMax {
				t.Fatalf("compressed bytes = total %d max %d, want total %d max %d", total, max, tc.wantTotal, tc.wantMax)
			}
			t.Logf("%d shards: total compressed bytes=%d max shard bytes=%d", len(got.Shards), total, max)
		})
	}
}

func TestProjectCheckpointSizeRejectsInvalidLimit(t *testing.T) {
	if _, err := ProjectCheckpointSize(nil, collector.ShardKey, 0); err == nil {
		t.Fatal("accepted zero per-shard limit")
	}
}
