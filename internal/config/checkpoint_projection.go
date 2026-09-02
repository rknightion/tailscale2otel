package config

import (
	"fmt"
	"strings"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/objectstore"
)

// validateKubernetesCheckpointProjection rejects a configuration whose bounded
// object-store seen rows cannot fit the current Kubernetes checkpoint shard.
// The guard deliberately covers only checkpoint.store=kubernetes: file and
// memory stores do not have the ConfigMap data ceiling.
func (c *Config) validateKubernetesCheckpointProjection() error {
	if c.Checkpoint.Store != "kubernetes" {
		return nil
	}
	feeds := c.checkpointProjectionFeeds()
	if len(feeds) == 0 {
		return nil
	}
	projection, err := objectstore.ProjectCheckpointSize(
		feeds,
		collector.ShardKey,
		collector.KubernetesCheckpointDataLimit,
	)
	if err != nil {
		return fmt.Errorf("checkpoint.store=kubernetes: project object-store checkpoint size: %w", err)
	}
	for shard, details := range projection.Shards {
		if !details.OverLimit {
			continue
		}
		paths := strings.Join(details.MaxSeenKeysPaths, ", ")
		if paths == "" {
			paths = "the enabled object-store max_seen_keys settings"
		}
		return fmt.Errorf(
			"checkpoint.store=kubernetes projected checkpoint shard %q is %d bytes > %d-byte ConfigMap data limit: lower one or more of %s",
			shard,
			details.Bytes,
			projection.Limit,
			paths,
		)
	}
	return nil
}

// checkpointProjectionFeeds resolves the same object-store destinations that
// app registers and supplies their operator-facing max_seen_keys paths for a
// useful startup error. It intentionally projects only the bounded seen rows;
// cursor, scan and gap state have no configuration-only cardinality bound and
// remain protected by the store's visible no-truncation oversize error.
func (c *Config) checkpointProjectionFeeds() []objectstore.CheckpointProjectionFeed {
	var feeds []objectstore.CheckpointProjectionFeed
	add := func(tailnet, path, signal string, dest ObjectStoreConfig) {
		feeds = append(feeds, objectstore.CheckpointProjectionFeed{
			Tailnet:         tailnet,
			Provider:        "s3",
			Signal:          signal,
			Endpoint:        dest.Endpoint,
			Bucket:          dest.Bucket,
			Prefix:          dest.Prefix,
			MaxSeenKeys:     dest.MaxSeenKeys,
			MaxSeenKeysPath: path,
		})
	}

	if len(c.Tailnets) == 0 {
		tailnet := c.Tailscale.Tailnet
		if c.Collectors.Flowlogs.Enabled && objectStoreSource(c.Collectors.Flowlogs.Source) {
			if dest, ok := c.FlowObjectStore(tailnet); ok {
				add(tailnet, "collectors.flowlogs.objectstore.max_seen_keys", objectStoreFlowSpec.signal, dest)
			}
		}
		if c.Collectors.Auditlogs.Enabled && objectStoreSource(c.Collectors.Auditlogs.Source) {
			if dest, ok := c.AuditObjectStore(tailnet); ok {
				add(tailnet, "collectors.auditlogs.objectstore.max_seen_keys", objectStoreAuditSpec.signal, dest)
			}
		}
		if c.Collectors.K8sAudit.Enabled {
			if dest, ok := c.K8sAuditObjectStore(tailnet); ok {
				add(tailnet, "collectors.k8s_audit.objectstore.max_seen_keys", objectStoreK8sAuditSpec.signal, dest)
			}
		}
		return feeds
	}

	for i, tailnet := range c.Tailnets {
		if c.Collectors.Flowlogs.Enabled && objectStoreSource(c.Collectors.Flowlogs.Source) {
			if dest, ok := c.FlowObjectStore(tailnet.Name); ok {
				add(tailnet.Name, fmt.Sprintf("tailnets[%d].objectstore.flow.max_seen_keys", i), objectStoreFlowSpec.signal, dest)
			}
		}
		if c.Collectors.Auditlogs.Enabled && objectStoreSource(c.Collectors.Auditlogs.Source) {
			if dest, ok := c.AuditObjectStore(tailnet.Name); ok {
				add(tailnet.Name, fmt.Sprintf("tailnets[%d].objectstore.audit.max_seen_keys", i), objectStoreAuditSpec.signal, dest)
			}
		}
		if c.Collectors.K8sAudit.Enabled {
			if dest, ok := c.K8sAuditObjectStore(tailnet.Name); ok {
				add(tailnet.Name, fmt.Sprintf("tailnets[%d].objectstore.k8s_audit.max_seen_keys", i), objectStoreK8sAuditSpec.signal, dest)
			}
		}
	}
	return feeds
}
