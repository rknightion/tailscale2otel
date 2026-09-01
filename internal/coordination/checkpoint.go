package coordination

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

// KubernetesCheckpointClient adapts one ConfigMap to the collector's narrow
// persistence interface. Its namespace and Lease-derived name are the
// coordination seam.
type KubernetesCheckpointClient struct {
	configMaps corev1client.ConfigMapInterface
	name       string
}

// NewKubernetesCheckpointClient builds the in-cluster ConfigMap adapter used
// by the composition root when checkpoint.store=kubernetes.
func NewKubernetesCheckpointClient(namespace, name string) (*KubernetesCheckpointClient, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("build in-cluster Kubernetes configuration: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("build Kubernetes client: %w", err)
	}
	return NewKubernetesCheckpointClientForClient(client, namespace, name)
}

// NewKubernetesCheckpointClientForClient is the testable constructor for an
// already-built typed Kubernetes client.
func NewKubernetesCheckpointClientForClient(client kubernetes.Interface, namespace, name string) (*KubernetesCheckpointClient, error) {
	if client == nil || namespace == "" || name == "" {
		return nil, errors.New("kubernetes checkpoint client needs client, namespace, and name")
	}
	return &KubernetesCheckpointClient{
		configMaps: client.CoreV1().ConfigMaps(namespace),
		name:       CheckpointConfigMapName(name),
	}, nil
}

// CheckpointConfigMapName derives a checkpoint-only object from the Lease
// name. Keeping the resource kinds separate is not enough: the Helm chart can
// already own a configuration ConfigMap with the Lease's default name, and a
// checkpoint update replaces the ConfigMap data map wholesale.
func CheckpointConfigMapName(leaseName string) string {
	return leaseName + "-checkpoints"
}

func (c *KubernetesCheckpointClient) GetCheckpoint(ctx context.Context) (collector.KubernetesCheckpointObject, error) {
	cm, err := c.configMaps.Get(ctx, c.name, metav1.GetOptions{})
	if err != nil {
		return collector.KubernetesCheckpointObject{}, mapCheckpointError(err)
	}
	return checkpointObject(cm), nil
}

func (c *KubernetesCheckpointClient) CreateCheckpoint(ctx context.Context, object collector.KubernetesCheckpointObject) (collector.KubernetesCheckpointObject, error) {
	cm, err := c.configMaps.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: c.name},
		Data:       map[string]string{collector.KubernetesCheckpointDataKey: object.Data},
	}, metav1.CreateOptions{})
	if err != nil {
		return collector.KubernetesCheckpointObject{}, mapCheckpointError(err)
	}
	return checkpointObject(cm), nil
}

func (c *KubernetesCheckpointClient) UpdateCheckpoint(ctx context.Context, object collector.KubernetesCheckpointObject) (collector.KubernetesCheckpointObject, error) {
	cm, err := c.configMaps.Update(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: c.name, ResourceVersion: object.ResourceVersion},
		Data:       map[string]string{collector.KubernetesCheckpointDataKey: object.Data},
	}, metav1.UpdateOptions{})
	if err != nil {
		return collector.KubernetesCheckpointObject{}, mapCheckpointError(err)
	}
	return checkpointObject(cm), nil
}

func checkpointObject(cm *corev1.ConfigMap) collector.KubernetesCheckpointObject {
	return collector.KubernetesCheckpointObject{
		ResourceVersion: cm.ResourceVersion,
		Data:            cm.Data[collector.KubernetesCheckpointDataKey],
	}
}

func mapCheckpointError(err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%w: %w", collector.ErrKubernetesCheckpointNotFound, err)
	case apierrors.IsAlreadyExists(err):
		return fmt.Errorf("%w: %w", collector.ErrKubernetesCheckpointAlreadyExists, err)
	case apierrors.IsConflict(err):
		return fmt.Errorf("%w: %w", collector.ErrKubernetesCheckpointConflict, err)
	default:
		return err
	}
}

var _ collector.KubernetesCheckpointClient = (*KubernetesCheckpointClient)(nil)
