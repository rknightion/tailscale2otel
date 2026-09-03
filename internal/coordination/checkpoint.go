package coordination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

const (
	checkpointShardLabel                               = "tailscale2otel.com/checkpoint-shard"
	checkpointOwnerLabel                               = "tailscale2otel.com/checkpoint-owner"
	checkpointShardAnnotation                          = "tailscale2otel.com/checkpoint-shard-key"
	checkpointLegacyMigrationAnnotation                = "tailscale2otel.com/checkpoint-migrated"
	checkpointLegacyMigrationResourceVersionAnnotation = "tailscale2otel.com/checkpoint-migrated-resource-version"
)

type KubernetesCheckpointClient struct {
	configMaps corev1client.ConfigMapInterface
	leaseName  string
}

// NewKubernetesCheckpointClient builds the in-cluster checkpoint ConfigMap adapter.
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

// NewKubernetesCheckpointClientForClient builds the adapter around a typed test client.
func NewKubernetesCheckpointClientForClient(client kubernetes.Interface, namespace, name string) (*KubernetesCheckpointClient, error) {
	if client == nil || namespace == "" || name == "" {
		return nil, errors.New("kubernetes checkpoint client needs client, namespace, and name")
	}
	return &KubernetesCheckpointClient{configMaps: client.CoreV1().ConfigMaps(namespace), leaseName: name}, nil
}

// CheckpointConfigMapName derives the exact legacy name for an empty shard and
// a DNS-label-safe hash name for every current shard. The hash includes both
// Lease and shard identity so two long Lease names with the same visible prefix
// cannot address the same ConfigMap.
func CheckpointConfigMapName(leaseName, shard string) string {
	if shard == "" {
		return leaseName + "-checkpoints"
	}
	base := strings.ToLower(leaseName)
	base = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, base)
	base = strings.Trim(base, "-")
	if base == "" {
		base = "checkpoint"
	}
	sum := sha256.Sum256([]byte(leaseName + "\x00" + shard))
	suffix := "-checkpoints-" + hex.EncodeToString(sum[:16])
	return trimKubernetesName(base, 63-len(suffix)) + suffix
}

func checkpointOwner(leaseName string) string {
	sum := sha256.Sum256([]byte(leaseName))
	return hex.EncodeToString(sum[:16])
}

func trimKubernetesName(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.Trim(s, "-")
}

func (c *KubernetesCheckpointClient) ListCheckpoints(ctx context.Context) ([]collector.KubernetesCheckpointObject, error) {
	selector := checkpointShardLabel + "=true," + checkpointOwnerLabel + "=" + checkpointOwner(c.leaseName)
	list, err := c.configMaps.List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, mapCheckpointError(err)
	}
	out := make([]collector.KubernetesCheckpointObject, 0, len(list.Items))
	for _, cm := range list.Items {
		shard := cm.Annotations[checkpointShardAnnotation]
		if shard == "" {
			return nil, fmt.Errorf("checkpoint ConfigMap %q has no shard annotation", cm.Name)
		}
		if want := CheckpointConfigMapName(c.leaseName, shard); cm.Name != want {
			return nil, fmt.Errorf("checkpoint ConfigMap %q has shard %q but canonical name is %q", cm.Name, shard, want)
		}
		out = append(out, checkpointObject(&cm, shard))
	}
	return out, nil
}

func (c *KubernetesCheckpointClient) GetLegacyCheckpoint(ctx context.Context) (collector.KubernetesCheckpointObject, error) {
	cm, err := c.configMaps.Get(ctx, CheckpointConfigMapName(c.leaseName, ""), metav1.GetOptions{})
	if err != nil {
		return collector.KubernetesCheckpointObject{}, mapCheckpointError(err)
	}
	return collector.KubernetesCheckpointObject{
		ResourceVersion: cm.ResourceVersion,
		Data:            []byte(cm.Data["checkpoints.json"]),
		LegacyMigrated:  cm.Annotations[checkpointLegacyMigrationAnnotation] == "gzip-shards-v1",
	}, nil
}

func (c *KubernetesCheckpointClient) MarkLegacyCheckpointMigrated(ctx context.Context, resourceVersion string) (string, error) {
	name := CheckpointConfigMapName(c.leaseName, "")
	cm, err := c.configMaps.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", mapCheckpointError(err)
	}
	if resourceVersion != "" && cm.ResourceVersion != resourceVersion {
		return "", fmt.Errorf("%w: legacy checkpoint resourceVersion changed from %q to %q", collector.ErrKubernetesCheckpointConflict, resourceVersion, cm.ResourceVersion)
	}
	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	cm.Annotations[checkpointLegacyMigrationAnnotation] = "gzip-shards-v1"
	updated, err := c.configMaps.Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return "", mapCheckpointError(err)
	}
	if updated.ResourceVersion == "" {
		return "", errors.New("marked legacy checkpoint ConfigMap returned an empty resourceVersion")
	}
	return updated.ResourceVersion, nil
}

func (c *KubernetesCheckpointClient) CreateCheckpoint(ctx context.Context, object collector.KubernetesCheckpointObject) (collector.KubernetesCheckpointObject, error) {
	cm, err := c.configMaps.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: CheckpointConfigMapName(c.leaseName, object.Shard),
			Labels: map[string]string{
				checkpointShardLabel: "true",
				checkpointOwnerLabel: checkpointOwner(c.leaseName),
			},
			Annotations: checkpointAnnotations(object),
		},
		BinaryData: map[string][]byte{collector.KubernetesCheckpointDataKey: object.Data},
	}, metav1.CreateOptions{})
	if err != nil {
		return collector.KubernetesCheckpointObject{}, mapCheckpointError(err)
	}
	return checkpointObject(cm, object.Shard), nil
}

func (c *KubernetesCheckpointClient) UpdateCheckpoint(ctx context.Context, object collector.KubernetesCheckpointObject) (collector.KubernetesCheckpointObject, error) {
	cm, err := c.configMaps.Update(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            CheckpointConfigMapName(c.leaseName, object.Shard),
			ResourceVersion: object.ResourceVersion,
			Labels: map[string]string{
				checkpointShardLabel: "true",
				checkpointOwnerLabel: checkpointOwner(c.leaseName),
			},
			Annotations: checkpointAnnotations(object),
		},
		BinaryData: map[string][]byte{collector.KubernetesCheckpointDataKey: object.Data},
	}, metav1.UpdateOptions{})
	if err != nil {
		return collector.KubernetesCheckpointObject{}, mapCheckpointError(err)
	}
	return checkpointObject(cm, object.Shard), nil
}

func checkpointObject(cm *corev1.ConfigMap, shard string) collector.KubernetesCheckpointObject {
	return collector.KubernetesCheckpointObject{Shard: shard, ResourceVersion: cm.ResourceVersion, Data: cm.BinaryData[collector.KubernetesCheckpointDataKey], LegacyMigrationResourceVersion: cm.Annotations[checkpointLegacyMigrationResourceVersionAnnotation]}
}

func checkpointAnnotations(object collector.KubernetesCheckpointObject) map[string]string {
	annotations := map[string]string{checkpointShardAnnotation: object.Shard}
	if object.LegacyMigrationResourceVersion != "" {
		annotations[checkpointLegacyMigrationResourceVersionAnnotation] = object.LegacyMigrationResourceVersion
	}
	return annotations
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
