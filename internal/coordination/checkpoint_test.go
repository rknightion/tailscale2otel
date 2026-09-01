package coordination

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

func TestKubernetesCheckpointClientUsesDedicatedConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default"},
		Data:       map[string]string{"config.yaml": "coordination:\n  mode: kubernetes\n"},
	})
	adapter, err := NewKubernetesCheckpointClientForClient(client, "default", "tailscale2otel")
	if err != nil {
		t.Fatalf("NewKubernetesCheckpointClientForClient: %v", err)
	}
	if _, err := adapter.CreateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{Data: "{}"}); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	config, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "tailscale2otel", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get application config ConfigMap: %v", err)
	}
	if got := config.Data["config.yaml"]; got == "" {
		t.Fatal("application config ConfigMap was overwritten by checkpoint creation")
	}
	checkpoint, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "tailscale2otel-checkpoints", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get dedicated checkpoint ConfigMap: %v", err)
	}
	if got := checkpoint.Data[collector.KubernetesCheckpointDataKey]; got != "{}" {
		t.Fatalf("checkpoint data = %q, want {}", got)
	}
}

func TestKubernetesCheckpointClientMapsErrorsAndPreservesResourceVersion(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter, err := NewKubernetesCheckpointClientForClient(client, "default", "tailscale2otel")
	if err != nil {
		t.Fatalf("NewKubernetesCheckpointClientForClient: %v", err)
	}
	if _, err := adapter.GetCheckpoint(context.Background()); !errors.Is(err, collector.ErrKubernetesCheckpointNotFound) {
		t.Fatalf("Get missing error = %v, want ErrKubernetesCheckpointNotFound", err)
	}
	created, err := adapter.CreateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{Data: "{}"})
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
	if created.Data != "{}" {
		t.Fatalf("created data = %q, want {}", created.Data)
	}

	client.PrependReactor("update", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		update := action.(clienttesting.UpdateAction).GetObject().(*corev1.ConfigMap)
		if update.ResourceVersion != "rv-before" {
			t.Fatalf("Update resourceVersion = %q, want preserved rv-before", update.ResourceVersion)
		}
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, update.Name, errors.New("stale"))
	})
	_, err = adapter.UpdateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{ResourceVersion: "rv-before", Data: `{"cursor":"value"}`})
	if !errors.Is(err, collector.ErrKubernetesCheckpointConflict) {
		t.Fatalf("Update conflict = %v, want ErrKubernetesCheckpointConflict", err)
	}

	client.PrependReactor("create", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, "tailscale2otel")
	})
	_, err = adapter.CreateCheckpoint(context.Background(), collector.KubernetesCheckpointObject{Data: "{}"})
	if !errors.Is(err, collector.ErrKubernetesCheckpointAlreadyExists) {
		t.Fatalf("Create conflict = %v, want ErrKubernetesCheckpointAlreadyExists", err)
	}
}
