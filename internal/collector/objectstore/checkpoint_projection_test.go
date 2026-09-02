package objectstore

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

func TestShardKeyInitiallyUsesOneShard(t *testing.T) {
	if got := collector.ShardKey("one"); got != collector.ShardKey("two") {
		t.Fatalf("ShardKey identities differ: %q and %q", got, collector.ShardKey("two"))
	}
}

func TestProjectCheckpointSizeUsesProductionKeyConstruction(t *testing.T) {
	feed := CheckpointProjectionFeed{
		Tailnet:         "example.com",
		Provider:        "s3",
		Signal:          "k8s_audit",
		Endpoint:        "https://s3.example",
		Bucket:          "recordings",
		Prefix:          "",
		MaxSeenKeys:     2,
		MaxSeenKeysPath: "collectors.k8s_audit.objectstore.max_seen_keys",
	}
	got, err := ProjectCheckpointSize([]CheckpointProjectionFeed{feed}, collector.ShardKey, collector.KubernetesCheckpointDataLimit)
	if err != nil {
		t.Fatalf("ProjectCheckpointSize: %v", err)
	}
	shard, ok := got.Shards[collector.ShardKey("anything")]
	if !ok {
		t.Fatalf("projection shards = %+v, want the default shard", got.Shards)
	}

	scope := CheckpointScope{
		Tailnet:  feed.Tailnet,
		Provider: feed.Provider,
		Signal:   feed.Signal,
		Feed:     FeedID(feed.Endpoint, feed.Bucket, feed.Prefix),
	}
	namespace, err := scope.Namespace()
	if err != nil {
		t.Fatalf("Namespace: %v", err)
	}
	identity := projectionRecorderIdentity(0)
	key := namespace + "/" + seenRow(identity)
	if !strings.Contains(key, "/seen/"+base64.RawURLEncoding.EncodeToString([]byte(identity))) {
		t.Fatalf("projected key = %q, want namespaced seen row", key)
	}
	wantData, err := json.Marshal(map[string]time.Time{
		key: projectionTime,
		namespace + "/" + seenRow(projectionRecorderIdentity(1)): projectionTime,
	})
	if err != nil {
		t.Fatalf("json.Marshal expected row: %v", err)
	}
	if shard.Bytes != len(wantData) || shard.Entries != 2 {
		t.Fatalf("shard = %+v, want bytes=%d entries=2", shard, len(wantData))
	}
	if len(shard.MaxSeenKeysPaths) != 1 || shard.MaxSeenKeysPaths[0] != feed.MaxSeenKeysPath {
		t.Fatalf("paths = %v, want [%s]", shard.MaxSeenKeysPaths, feed.MaxSeenKeysPath)
	}
}

func TestProjectCheckpointSizeSplitsBySuppliedShardFunction(t *testing.T) {
	feeds := []CheckpointProjectionFeed{
		{Tailnet: "one", Provider: "s3", Signal: "flow", Endpoint: "https://s3.example", Bucket: "one", MaxSeenKeys: 2, MaxSeenKeysPath: "one.max_seen_keys"},
	}
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

func TestProjectCheckpointSizeMeasuredConfigurations(t *testing.T) {
	feed := func(tailnet, signal, bucket string, maxSeenKeys int) CheckpointProjectionFeed {
		return CheckpointProjectionFeed{
			Tailnet:         tailnet,
			Provider:        "s3",
			Signal:          signal,
			Endpoint:        "https://s3.example",
			Bucket:          bucket,
			MaxSeenKeys:     maxSeenKeys,
			MaxSeenKeysPath: tailnet + "." + signal + ".max_seen_keys",
		}
	}
	threeSignals := func(tailnet, suffix string) []CheckpointProjectionFeed {
		return []CheckpointProjectionFeed{
			feed(tailnet, "flow", suffix+"-flow", 5000),
			feed(tailnet, "audit", suffix+"-audit", 5000),
			feed(tailnet, "k8s_audit", suffix+"-k8s", 5000),
		}
	}
	cases := []struct {
		name        string
		feeds       []CheckpointProjectionFeed
		wantEntries int
		wantBytes   int
		wantOver    bool
	}{
		{
			name:        "one feed one tailnet",
			feeds:       []CheckpointProjectionFeed{feed("example.com", "flow", "flows", 5000)},
			wantEntries: 5000,
			wantBytes:   935001,
			wantOver:    false,
		},
		{
			name:        "three feeds one tailnet",
			feeds:       threeSignals("example.com", "one"),
			wantEntries: 15000,
			wantBytes:   2835001,
			wantOver:    true,
		},
		{
			name: "three feeds three tailnets",
			feeds: append(append(threeSignals("alpha.example.com", "alpha"),
				threeSignals("beta.example.com", "beta")...),
				threeSignals("gamma.example.com", "gamma")...),
			wantEntries: 45000,
			wantBytes:   8850001,
			wantOver:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectCheckpointSize(tc.feeds, collector.ShardKey, collector.KubernetesCheckpointDataLimit)
			if err != nil {
				t.Fatalf("ProjectCheckpointSize: %v", err)
			}
			shard := got.Shards[collector.ShardKey("any")]
			t.Logf("projected %d entries as %d bytes (limit %d)", shard.Entries, shard.Bytes, got.Limit)
			if shard.Entries != tc.wantEntries {
				t.Fatalf("entries = %d, want %d", shard.Entries, tc.wantEntries)
			}
			if shard.Bytes != tc.wantBytes {
				t.Errorf("bytes = %d, want %d", shard.Bytes, tc.wantBytes)
			}
			if shard.OverLimit != tc.wantOver {
				t.Errorf("over limit = %v, want %v", shard.OverLimit, tc.wantOver)
			}
		})
	}
}
