package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
)

const (
	flowReplayCapacityPath = "collectors.flowlogs.replay_seen_capacity"
	maxFlowReplayCapacity  = 1048576 // existing validation limit
	flowReplayHexBytes     = 2 * sha256.Size
	flowReplayKeyBytes     = len(collector.FlowReplayCheckpointPrefix) + flowReplayHexBytes
	replayExpiryJSONBytes  = len("2006-01-02T15:04:05.999999999Z")
	cursorTimeJSONBytes    = len("2006-01-02T15:04:05.999999999+07:00")

	// Leave room for other checkpoint rows. Kubernetes also needs room for
	// gzip framing/expansion: spend raw JSON bytes, without assuming ANY
	// savings from compression (of digests, expiries or repeated key prefixes).
	fileReplayJSONBudget = int(safefile.MaxCheckpointBytes) - (256 << 10)
	kubeReplayJSONBudget = collector.KubernetesCheckpointDataLimit - (64 << 10)

	// A fixed aggregate budget per pass, not per shard: charge each runtime
	// 64 units plus its raw name bytes before allocating any shard metadata.
	// Projection never generates rows or runs a compressor. Names are only
	// walked a constant number of times, regardless of requested capacity.
	flowReplayMetadataBudget = 2 * int(safefile.MaxConfigBytes)
)

func (c *Config) usesDurableFlowReplay() bool {
	f := c.Collectors.Flowlogs
	return c.resolvedProvider() == "tailscale" && f.Enabled && pollsSource(f.Source) && f.ReplayOverlap.D() > 0 &&
		(c.Checkpoint.Store == "file" || c.Checkpoint.Store == "kubernetes")
}

func (c *Config) applyFlowReplayCheckpointDefault() {
	if c.usesDurableFlowReplay() {
		limit, _ := c.flowReplayCheckpointCapacity()
		// Keep validation's existing positive range even if the estimate cannot
		// fit one row. Warnings explains that case without advising zero.
		f := &c.Collectors.Flowlogs
		f.ReplaySeenCapacity = min(f.ReplaySeenCapacity, max(limit, 1))
	}
}

func (c *Config) flowReplayCheckpointWarning() string {
	if !c.usesDurableFlowReplay() {
		return ""
	}
	requested := c.Collectors.Flowlogs.ReplaySeenCapacity
	if requested < 1 || requested > maxFlowReplayCapacity {
		return "" // the existing validation error covers this
	}
	limit, assessed := c.flowReplayCheckpointCapacity()
	if assessed && requested <= limit {
		return ""
	}
	prefix := fmt.Sprintf("%s=%d", flowReplayCapacityPath, requested)
	storeLimit := c.Checkpoint.Store + " checkpoint budget"
	if c.Checkpoint.Store == "kubernetes" {
		// Each ConfigMap shard holds at most 1 MiB of data; say so in numbers.
		storeLimit = fmt.Sprintf("kubernetes checkpoint budget (the ConfigMap shard limit is 1 MiB (%d bytes))", collector.KubernetesCheckpointDataLimit)
	}
	if !assessed {
		return prefix + ": flow replay checkpoint sizing exceeded its startup work budget; no positive recommendation is available. Reduce the number or length of polled tailnet names, use memory checkpoints, or disable replay_overlap."
	}
	if limit < 1 {
		return fmt.Sprintf("%s: no positive capacity fits the conservative %s. Reduce the number or length of polled tailnet names, use memory checkpoints, or disable replay_overlap.", prefix, storeLimit)
	}
	return fmt.Sprintf("%s exceeds the conservative %s; set %s: %d or lower to reduce replay state.", prefix, storeLimit, flowReplayCapacityPath, limit)
}

// flowReplayCheckpointCapacity returns the common per-runtime capacity that
// fits the most constrained checkpoint. An unassessed result is not advice.
func (c *Config) flowReplayCheckpointCapacity() (capacity int, assessed bool) {
	// App uses bare keys with zero or one configured tailnet entry, and the
	// configured name as the namespace only when there is real fan-out.
	tailnets := c.Tailnets
	if len(tailnets) <= 1 {
		tailnets = []TailnetConfig{{}}
	}
	work := flowReplayMetadataBudget
	for i := range tailnets {
		nameBytes := len(tailnets[i].Name)
		if nameBytes > work-64 {
			return 0, false
		}
		work -= 64 + nameBytes
	}
	if c.Checkpoint.Store == "file" {
		return fileFlowReplayCapacity(tailnets), true
	}
	return kubernetesFlowReplayCapacity(tailnets), true
}

func fileFlowReplayCapacity(tailnets []TailnetConfig) int {
	fixed, perCapacity := len("{\n\n}"), 0
	const rowSyntax = len("  \"\": \"\",\n") // allow a comma even on the last row
	for i := range tailnets {
		prefixBytes := jsonNamespaceBytes(tailnets[i].Name)
		fixed += prefixBytes + len(collector.FlowCheckpointName) + cursorTimeJSONBytes + rowSyntax
		perCapacity += prefixBytes + flowReplayKeyBytes + replayExpiryJSONBytes + rowSyntax
	}
	return replayBudgetCapacity(fileReplayJSONBudget, fixed, perCapacity)
}

type kubeReplayShardCost struct {
	fixed, perCapacity int
}

func kubernetesFlowReplayCapacity(tailnets []TailnetConfig) int {
	shards := make(map[string]kubeReplayShardCost)
	// Some names containing slashes route each digest to its own shard. Do
	// not group those by a fake digest or materialize any such shard. Store
	// the family prefix to account for overlaps with fixed shard names below.
	splitRows := make(map[string]int)
	digest := strings.Repeat("0", flowReplayHexBytes)
	const rowSyntax = len(`"":"",`) // allow a comma even on the last row
	for i := range tailnets {
		namespace := tailnets[i].Name
		prefixBytes := jsonNamespaceBytes(namespace)
		replayCost := prefixBytes + flowReplayKeyBytes + replayExpiryJSONBytes + rowSyntax
		if replayCost > kubeReplayJSONBudget {
			return 0
		}
		if namespace != "" {
			namespace += "/"
		}
		cursor := namespace + collector.FlowCheckpointName
		cursorShard := collector.ShardKey(cursor)
		cost := shards[cursorShard]
		cost.fixed += prefixBytes + len(collector.FlowCheckpointName) + cursorTimeJSONBytes + rowSyntax
		shards[cursorShard] = cost

		replayPrefix := namespace + collector.FlowReplayCheckpointPrefix
		replayKey := replayPrefix + digest
		replayShard := collector.ShardKey(replayKey)
		// ShardKey returns a prefix, and cannot branch on the value of a
		// 64-character hex digest. If the whole key is returned, each digest
		// has its own shard. Unique runtime names imply disjoint families.
		if replayShard == replayKey {
			if replayCost+kubeReplayEnvelopeBytes(prefixBytes+flowReplayKeyBytes+2) > kubeReplayJSONBudget {
				return 0
			}
			splitRows[replayPrefix] = replayCost
			continue
		}
		cost = shards[replayShard]
		cost.perCapacity += replayCost
		shards[replayShard] = cost
	}

	capacity := maxFlowReplayCapacity
	for shard, cost := range shards {
		cost.fixed += kubeReplayEnvelopeBytes(jsonQuotedBytes(shard))
		if n := len(shard) - flowReplayHexBytes; n > 0 {
			if single, ok := splitRows[shard[:n]]; ok {
				var decoded [sha256.Size]byte
				if _, err := hex.Decode(decoded[:], []byte(shard[n:])); err == nil {
					cost.fixed += single // worst case: this digest is retained
				}
			}
		}
		capacity = min(capacity, replayBudgetCapacity(kubeReplayJSONBudget, cost.fixed, cost.perCapacity))
	}
	return capacity
}

func replayBudgetCapacity(budget, fixed, perCapacity int) int {
	if fixed > budget {
		return 0
	}
	if perCapacity == 0 {
		return maxFlowReplayCapacity
	}
	return (budget - fixed) / perCapacity
}

func kubeReplayEnvelopeBytes(quotedShardBytes int) int {
	return quotedShardBytes + 64 // bound the version and JSON envelope punctuation
}

func jsonNamespaceBytes(name string) int {
	if name == "" {
		return 0
	}
	return jsonQuotedBytes(name) - 2 + 1 // strip JSON quotes and add the namespace slash
}

func jsonQuotedBytes(key string) int {
	data, _ := json.Marshal(key) // encoding a string cannot fail
	return len(data)
}
