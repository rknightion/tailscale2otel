package coordination

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
)

func TestCheckpointConfigMapNameIsValidAndShardSpecific(t *testing.T) {
	legacy := CheckpointConfigMapName("tailscale2otel", "")
	firstLease := strings.Repeat("a", 63)
	secondLease := strings.Repeat("a", 62) + "b"
	first := CheckpointConfigMapName(firstLease, "objectstore/v1/dA/s3/flow/feed")
	second := CheckpointConfigMapName(firstLease, "objectstore/v1/dA/s3/audit/feed")
	otherLease := CheckpointConfigMapName(secondLease, "objectstore/v1/dA/s3/flow/feed")
	if legacy == first || first == second {
		t.Fatalf("names not shard-specific: %q %q %q", legacy, first, second)
	}
	if first == otherLease {
		t.Fatalf("two Lease names collided on shard name %q", first)
	}
	if legacy != "tailscale2otel-checkpoints" {
		t.Fatalf("legacy name = %q, want exact former name", legacy)
	}
	for _, name := range []string{legacy, first, second, otherLease} {
		if len(name) > 63 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			t.Fatalf("invalid ConfigMap name %q", name)
		}
	}
}

func TestKubernetesCheckpointClientListsOnlyItsLeaseShards(t *testing.T) {
	client := fake.NewSimpleClientset()
	first, err := NewKubernetesCheckpointClientForClient(client, "default", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewKubernetesCheckpointClientForClient(client, "default", "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, adapter := range []*KubernetesCheckpointClient{first, second} {
		if _, err := adapter.CreateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{Shard: "flowlogs", Data: []byte{1}}); err != nil {
			t.Fatal(err)
		}
	}
	for name, adapter := range map[string]*KubernetesCheckpointClient{"first": first, "second": second} {
		listed, err := adapter.ListCheckpoints(context.Background())
		if err != nil || len(listed) != 1 {
			t.Fatalf("%s ListCheckpoints = %+v, %v; want exactly its own shard", name, listed, err)
		}
	}
}

func TestKubernetesCheckpointClientMarksLegacyMigrationWithoutChangingData(t *testing.T) {
	legacy := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel-checkpoints", Namespace: "default", ResourceVersion: "7"},
		Data:       map[string]string{"checkpoints.json": `{"flowlogs":"2026-09-02T12:00:00Z"}`},
	}
	client := fake.NewSimpleClientset(legacy)
	adapter, err := NewKubernetesCheckpointClientForClient(client, "default", "tailscale2otel")
	if err != nil {
		t.Fatal(err)
	}
	before, err := adapter.GetLegacyCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	markedResourceVersion, err := adapter.MarkLegacyCheckpointMigrated(context.Background(), before.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	after, err := adapter.GetLegacyCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !after.LegacyMigrated || string(after.Data) != string(before.Data) {
		t.Fatalf("marked legacy = migrated=%v data=%q; want unchanged %q", after.LegacyMigrated, after.Data, before.Data)
	}
	if markedResourceVersion != after.ResourceVersion {
		t.Fatalf("marked resourceVersion = %q, want %q", markedResourceVersion, after.ResourceVersion)
	}
}

func TestKubernetesCheckpointClientUsesDedicatedBinaryShards(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default"}, Data: map[string]string{"config.yaml": "safe"}})
	adapter, err := NewKubernetesCheckpointClientForClient(client, "default", "tailscale2otel")
	if err != nil {
		t.Fatal(err)
	}
	object, err := adapter.CreateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{Shard: "flowlogs", Data: []byte{1, 2, 3}, LegacyMigrationResourceVersion: "legacy-7"})
	if err != nil {
		t.Fatal(err)
	}
	_ = object // real apis assign resourceVersion; client-go fake deliberately does not.
	cm, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), CheckpointConfigMapName("tailscale2otel", "flowlogs"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cm.BinaryData[collector.KubernetesCheckpointDataKey]; string(got) != string([]byte{1, 2, 3}) {
		t.Fatalf("binary data = %v", got)
	}
	if got := cm.Annotations[checkpointShardAnnotation]; got != "flowlogs" {
		t.Fatalf("shard annotation = %q, want flowlogs", got)
	}
	if got := cm.Annotations[checkpointLegacyMigrationResourceVersionAnnotation]; got != "legacy-7" {
		t.Fatalf("migration resourceVersion annotation = %q, want legacy-7", got)
	}
	listed, err := adapter.ListCheckpoints(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListCheckpoints = %+v, %v", listed, err)
	}
	if got := listed[0].LegacyMigrationResourceVersion; got != "legacy-7" {
		t.Fatalf("listed migration resourceVersion = %q, want legacy-7", got)
	}
}

func TestKubernetesCheckpointClientRejectsMisnamedShard(t *testing.T) {
	leaseName := "tailscale2otel"
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "misplaced-checkpoint",
			Namespace: "default",
			Labels: map[string]string{
				checkpointShardLabel: "true",
				checkpointOwnerLabel: checkpointOwner(leaseName),
			},
			Annotations: map[string]string{checkpointShardAnnotation: "flowlogs"},
		},
		BinaryData: map[string][]byte{collector.KubernetesCheckpointDataKey: {1, 2, 3}},
	})
	adapter, err := NewKubernetesCheckpointClientForClient(client, "default", leaseName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ListCheckpoints(context.Background()); err == nil || !strings.Contains(err.Error(), "misplaced-checkpoint") {
		t.Fatalf("ListCheckpoints error = %v, want misplaced object rejection", err)
	}
}

func TestKubernetesCheckpointClientMapsConflictAndPreservesShardResourceVersion(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter, err := NewKubernetesCheckpointClientForClient(client, "default", "tailscale2otel")
	if err != nil {
		t.Fatal(err)
	}
	client.PrependReactor("update", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		update := action.(clienttesting.UpdateAction).GetObject().(*corev1.ConfigMap)
		if update.ResourceVersion != "rv-before" {
			t.Fatalf("resourceVersion = %q", update.ResourceVersion)
		}
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, update.Name, errors.New("stale"))
	})
	_, err = adapter.UpdateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{Shard: "flowlogs", ResourceVersion: "rv-before", Data: []byte("x")})
	if !errors.Is(err, collector.ErrKubernetesCheckpointConflict) {
		t.Fatalf("Update conflict = %v", err)
	}
}
