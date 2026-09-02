package objectstore

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

// CheckpointProjectionFeed describes one enabled object-store destination for
// the configuration-only checkpoint-size projection. Endpoint, bucket and
// prefix are kept as separate fields because FeedID is the production feed
// identity function and must be called here rather than reimplemented by the
// config package.
type CheckpointProjectionFeed struct {
	Tailnet  string
	Provider string
	Signal   string
	Endpoint string
	Bucket   string
	Prefix   string

	MaxSeenKeys     int
	MaxSeenKeysPath string
}

// CheckpointProjection is the gzip-compressed JSON size of the projected seen rows,
// grouped by the supplied shard function. The limit is retained in the result
// so callers can report the same per-shard contract they passed in.
type CheckpointProjection struct {
	Limit  int
	Shards map[string]CheckpointProjectionShard
}

// CheckpointProjectionShard contains the serialized size and the configuration
// knobs whose rows contribute to one shard.
type CheckpointProjectionShard struct {
	Bytes            int
	Entries          int
	OverLimit        bool
	MaxSeenKeysPaths []string
}

// projectionTime is deliberately fixed so the projection is deterministic.
// Nine non-zero fractional digits exercise time.Time's maximum-width
// RFC3339Nano JSON representation, keeping the configuration-only bound safe
// for every timestamp the real checkpoint stores can write.
var projectionTime = time.Date(2026, 9, 1, 12, 0, 0, 999999999, time.UTC)

const projectionRecorderTimestamp = "2026-07-29T11:50:58.722743575Z"

// ProjectCheckpointSize serializes the bounded object-store seen rows exactly
// as the checkpoint store does, then reports each shard's byte count. The
// shard function and per-shard limit are parameters intentionally: storage and
// this projection must share the seam, while TSO-0110 will change the shard
// mapping and retain the same startup guard.
//
// Each synthetic identity follows tsrecorder's recorder layout:
// <stableID>/events/<RFC3339Nano>.event. It is only used to produce distinct,
// fixed-width worst-case identities; the durable row itself is built through
// seenRow and collector.Namespaced, the same code paths the collector writes.
func ProjectCheckpointSize(
	feeds []CheckpointProjectionFeed,
	shardKey func(string) string,
	perShardLimit int,
) (CheckpointProjection, error) {
	projection := CheckpointProjection{
		Limit:  perShardLimit,
		Shards: map[string]CheckpointProjectionShard{},
	}
	if shardKey == nil {
		return projection, fmt.Errorf("objectstore: checkpoint projection shard function is required")
	}
	if perShardLimit <= 0 {
		return projection, fmt.Errorf("objectstore: checkpoint projection per-shard limit must be positive (got %d)", perShardLimit)
	}

	base := collector.NewMemoryStore()
	type projectedNamespace struct {
		value string
		path  string
	}
	namespaces := make([]projectedNamespace, 0, len(feeds))
	seenNamespaces := make(map[string]struct{}, len(feeds))
	identityOrdinal := 0
	for i, feed := range feeds {
		if feed.MaxSeenKeys <= 0 {
			return projection, fmt.Errorf("objectstore: checkpoint projection feed %d max_seen_keys must be positive (got %d)", i, feed.MaxSeenKeys)
		}
		scope := CheckpointScope{
			Tailnet:  feed.Tailnet,
			Provider: feed.Provider,
			Signal:   feed.Signal,
			Feed:     FeedID(feed.Endpoint, feed.Bucket, feed.Prefix),
		}
		namespace, err := scope.Namespace()
		if err != nil {
			return projection, fmt.Errorf("objectstore: checkpoint projection feed %d: %w", i, err)
		}
		if _, exists := seenNamespaces[namespace]; exists {
			return projection, fmt.Errorf("objectstore: checkpoint projection feed %d duplicates namespace", i)
		}
		seenNamespaces[namespace] = struct{}{}
		path := feed.MaxSeenKeysPath
		if path == "" {
			path = "max_seen_keys"
		}
		namespaces = append(namespaces, projectedNamespace{value: namespace, path: path})

		view := collector.Namespaced(base, namespace)
		for j := 0; j < feed.MaxSeenKeys; j++ {
			identity := projectionRecorderIdentity(identityOrdinal)
			identityOrdinal++
			if err := view.Set(seenRow(identity), projectionTime); err != nil {
				return projection, fmt.Errorf("objectstore: checkpoint projection feed %d: set seen row: %w", i, err)
			}
		}
	}

	rowsByShard := make(map[string]map[string]time.Time)
	pathsByShard := make(map[string]map[string]struct{})
	for _, key := range base.Keys() {
		shard := shardKey(key)
		rows := rowsByShard[shard]
		if rows == nil {
			rows = make(map[string]time.Time)
			rowsByShard[shard] = rows
			pathsByShard[shard] = make(map[string]struct{})
		}
		value, ok := base.Get(key)
		if !ok {
			return projection, fmt.Errorf("objectstore: checkpoint projection row %q disappeared", key)
		}
		rows[key] = value
		for _, namespace := range namespaces {
			if strings.HasPrefix(key, namespace.value+"/") {
				pathsByShard[shard][namespace.path] = struct{}{}
				break
			}
		}
	}

	for shard, rows := range rowsByShard {
		paths := make([]string, 0, len(pathsByShard[shard]))
		for path := range pathsByShard[shard] {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		compressed, _, err := collector.EncodeKubernetesCheckpointShard(shard, rows)
		if err != nil {
			return projection, fmt.Errorf("objectstore: checkpoint projection shard %q: gzip: %w", shard, err)
		}
		projection.Shards[shard] = CheckpointProjectionShard{
			Bytes:            len(compressed),
			Entries:          len(rows),
			OverLimit:        len(compressed) > perShardLimit,
			MaxSeenKeysPaths: paths,
		}
	}
	return projection, nil
}

func projectionRecorderIdentity(index int) string {
	return fmt.Sprintf("nREC%08dCNTRL/events/%s.event", index, projectionRecorderTimestamp)
}
