package config

import (
	"strings"
	"testing"
)

func kubernetesCheckpointObjectStoreConfig(t *testing.T) *Config {
	t.Helper()
	c := Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "tskey-api-test"
	c.Checkpoint.Store = "kubernetes"
	c.Coordination.Mode = "kubernetes"
	c.Collectors.Flowlogs.Source = "objectstore"
	c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
	c.Collectors.Flowlogs.ObjectStore.Region = "eu-west-2"
	c.Collectors.Flowlogs.ObjectStore.Bucket = "flows"
	return c
}

func TestValidateAllowsCompressedKubernetesCheckpointProjection(t *testing.T) {
	c := kubernetesCheckpointObjectStoreConfig(t)
	c.Collectors.Flowlogs.ObjectStore.MaxSeenKeys = 10000

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected compressed shard: %v", err)
	}
}

func TestValidateRejectsOversizedCompressedKubernetesCheckpointProjection(t *testing.T) {
	c := kubernetesCheckpointObjectStoreConfig(t)
	c.Collectors.Flowlogs.ObjectStore.MaxSeenKeys = 400000

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted an oversized compressed Kubernetes checkpoint projection")
	}
	for _, want := range []string{"projected", "1048576", "collectors.flowlogs.objectstore.max_seen_keys"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate error = %q, want it to contain %q", err, want)
		}
	}
}

func TestValidateAllowsSingleFeedDefaultKubernetesCheckpointProjection(t *testing.T) {
	c := kubernetesCheckpointObjectStoreConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected a single default-sized object-store feed: %v", err)
	}
}

func TestValidateKubernetesCheckpointProjectionSplitsEnabledFeeds(t *testing.T) {
	c := kubernetesCheckpointObjectStoreConfig(t)
	c.Collectors.Auditlogs.Source = "objectstore"
	c.Collectors.Auditlogs.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
	c.Collectors.Auditlogs.ObjectStore.Region = "eu-west-2"
	c.Collectors.Auditlogs.ObjectStore.Bucket = "audit"
	c.Collectors.K8sAudit.Enabled = true
	c.Collectors.K8sAudit.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
	c.Collectors.K8sAudit.ObjectStore.Region = "eu-west-2"
	c.Collectors.K8sAudit.ObjectStore.Bucket = "k8s-audit"

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected separate compressed feed shards: %v", err)
	}
}

func TestValidateKubernetesCheckpointProjectionSplitsMultiTailnetDestinations(t *testing.T) {
	c := Default()
	c.Tailscale.Tailnet = ""
	c.Checkpoint.Store = "kubernetes"
	c.Coordination.Mode = "kubernetes"
	c.Collectors.Flowlogs.Source = "objectstore"
	c.Tailnets = make([]TailnetConfig, 3)
	for i, name := range []string{"alpha.example.com", "beta.example.com", "gamma.example.com"} {
		c.Tailnets[i] = TailnetConfig{
			Name: name,
			Auth: TailscaleAuth{Method: "apikey", APIKey: "tskey-api-test"},
			ObjectStore: TailnetObjectStore{Flow: ObjectStoreConfig{
				Endpoint: "https://s3.eu-west-2.amazonaws.com",
				Region:   "eu-west-2",
				Bucket:   "flows-" + name[:5],
			}},
		}
	}

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected separate multi-tailnet shards: %v", err)
	}
}

func TestValidateCheckpointProjectionOnlyAppliesToKubernetesStore(t *testing.T) {
	for _, store := range []string{"file", "memory"} {
		t.Run(store, func(t *testing.T) {
			c := kubernetesCheckpointObjectStoreConfig(t)
			c.Checkpoint.Store = store
			c.Coordination.Mode = "none"
			c.Collectors.Flowlogs.ObjectStore.MaxSeenKeys = 10000
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate with checkpoint.store=%s: %v", store, err)
			}
		})
	}
}

func TestValidateCheckpointProjectionIgnoresDisabledObjectStoreDestinations(t *testing.T) {
	c := kubernetesCheckpointObjectStoreConfig(t)
	c.Collectors.Flowlogs.Enabled = false
	c.Collectors.Flowlogs.ObjectStore.MaxSeenKeys = 100000
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected an oversized but disabled object-store destination: %v", err)
	}
}
