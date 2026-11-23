package collector

import "strings"

// FlowCheckpointName is the scheduler cursor key for polled flow logs, before any tailnet prefix.
const FlowCheckpointName = "flowlogs"

// FlowReplayCheckpointPrefix prefixes hex SHA-256 digests in the flow replay checkpoint.
const FlowReplayCheckpointPrefix = FlowCheckpointName + "/replay/seen/"

// ShardKey returns the collector-owned namespace for a fully constructed
// checkpoint key. Object-store state has a multi-row namespace (including a
// feed identity); ordinary scheduler cursors are already one key per collector.
// Keeping every seen, scan and gap row of one feed together preserves the
// transaction boundary their collector relies on.
func ShardKey(key string) string {
	parts := strings.Split(key, "/")
	for i := 0; i+5 < len(parts); i++ {
		if parts[i] == "objectstore" && parts[i+1] == "v1" {
			return strings.Join(parts[:i+6], "/")
		}
	}
	// Multi-tailnet state puts the tailnet before the collector namespace.
	// Check this before the single-tailnet form so a tailnet whose name happens
	// to equal a collector name remains unambiguous.
	if len(parts) >= 2 && isCheckpointCollectorNamespace(parts[1]) {
		return strings.Join(parts[:2], "/")
	}
	if len(parts) >= 1 && isCheckpointCollectorNamespace(parts[0]) {
		return parts[0]
	}
	return key
}

func isCheckpointCollectorNamespace(part string) bool {
	switch part {
	case FlowCheckpointName, "auditlogs", "acl":
		return true
	default:
		return false
	}
}
