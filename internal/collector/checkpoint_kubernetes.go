package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// KubernetesCheckpointDataKey is the ConfigMap data entry carrying the whole
// checkpoint map. A ConfigMap, rather than Lease annotations, is deliberately
// used because Kubernetes caps all annotations on one object at 256 KiB while
// ConfigMap data has a 1 MiB limit. The entire map is one JSON value so a
// resourceVersion-guarded update covers every cursor atomically.
const KubernetesCheckpointDataKey = "checkpoints.json"

// KubernetesCheckpointDataLimit is Kubernetes' 1 MiB ConfigMap data limit.
// The store rejects an over-limit map rather than truncating cursor or replay
// state; callers must see that durability has failed.
const KubernetesCheckpointDataLimit = 1 << 20

var (
	// ErrKubernetesCheckpointNotFound is returned by KubernetesCheckpointClient
	// when its configured ConfigMap does not exist yet.
	ErrKubernetesCheckpointNotFound = errors.New("kubernetes checkpoint ConfigMap not found")
	// ErrKubernetesCheckpointAlreadyExists is returned when another contender
	// creates the ConfigMap between this store's read and create calls.
	ErrKubernetesCheckpointAlreadyExists = errors.New("kubernetes checkpoint ConfigMap already exists")
	// ErrKubernetesCheckpointConflict reports a resourceVersion mismatch. It is
	// intentionally not retried: a deposed leader must never reload and overwrite
	// the current leader's cursor map.
	ErrKubernetesCheckpointConflict = errors.New("kubernetes checkpoint resource version conflict")
	// ErrKubernetesCheckpointTooLarge reports a map that cannot fit in a single
	// ConfigMap data payload.
	ErrKubernetesCheckpointTooLarge = errors.New("kubernetes checkpoint ConfigMap data exceeds 1 MiB")
)

// KubernetesCheckpointObject is the minimal ConfigMap shape needed by the
// checkpoint store. The coordination package adapts client-go's typed ConfigMap
// API to this type, keeping Kubernetes imports out of collector code.
type KubernetesCheckpointObject struct {
	ResourceVersion string
	Data            string
}

// KubernetesCheckpointClient is an object-bound ConfigMap client. Its adapter
// owns the ConfigMap's namespace and name, puts Data in
// KubernetesCheckpointDataKey, and maps client-go apierrors.IsNotFound,
// IsAlreadyExists, and IsConflict to the corresponding sentinels above.
//
// UpdateCheckpoint must pass ResourceVersion unchanged to the API server. A
// conflict must return an error wrapping ErrKubernetesCheckpointConflict.
type KubernetesCheckpointClient interface {
	GetCheckpoint(context.Context) (KubernetesCheckpointObject, error)
	CreateCheckpoint(context.Context, KubernetesCheckpointObject) (KubernetesCheckpointObject, error)
	UpdateCheckpoint(context.Context, KubernetesCheckpointObject) (KubernetesCheckpointObject, error)
}

// KubernetesCheckpointStoreOption configures a Kubernetes checkpoint store.
type KubernetesCheckpointStoreOption func(*kubernetesCheckpointStore)

// WithKubernetesCheckpointWriteDebounce changes the interval between ordinary
// ConfigMap writes. The default is five seconds; a non-positive duration makes
// Set and Delete synchronous for focused callers and tests.
func WithKubernetesCheckpointWriteDebounce(d time.Duration) KubernetesCheckpointStoreOption {
	return func(s *kubernetesCheckpointStore) { s.writeDebounce = d }
}

// kubernetesCheckpointStore persists the checkpoint map in one ConfigMap data
// entry. It deliberately shares fileStore's in-memory-before-persist behavior:
// a Set is cheap, Flush is a shutdown durability boundary, and an asynchronous
// failure remains available to a later Flush.
type kubernetesCheckpointStore struct {
	mu              sync.Mutex
	client          KubernetesCheckpointClient
	resourceVersion string
	m               map[string]time.Time
	writeDebounce   time.Duration
	timer           *time.Timer
	dirty           bool
	persistErr      error
}

const defaultKubernetesCheckpointWriteDebounce = 5 * time.Second

// NewKubernetesCheckpointStore opens the configured ConfigMap, creating an
// empty one when it is absent. It loads the last persisted map before returning,
// so a newly elected process starts from the previous leader's cursors.
func NewKubernetesCheckpointStore(
	ctx context.Context,
	client KubernetesCheckpointClient,
	opts ...KubernetesCheckpointStoreOption,
) (CheckpointStore, error) {
	if client == nil {
		return nil, errors.New("kubernetes checkpoint client is required")
	}
	s := &kubernetesCheckpointStore{
		client:        client,
		m:             map[string]time.Time{},
		writeDebounce: defaultKubernetesCheckpointWriteDebounce,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	object, err := client.GetCheckpoint(ctx)
	if errors.Is(err, ErrKubernetesCheckpointNotFound) {
		object, err = client.CreateCheckpoint(ctx, KubernetesCheckpointObject{Data: "{}"})
		if errors.Is(err, ErrKubernetesCheckpointAlreadyExists) {
			object, err = client.GetCheckpoint(ctx)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open Kubernetes checkpoint store: %w", err)
	}
	if object.Data != "" {
		if err := json.Unmarshal([]byte(object.Data), &s.m); err != nil {
			return nil, fmt.Errorf("decode Kubernetes checkpoint ConfigMap: %w", err)
		}
	}
	if s.m == nil {
		s.m = map[string]time.Time{}
	}
	s.resourceVersion = object.ResourceVersion
	return s, nil
}

func (s *kubernetesCheckpointStore) Get(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[name]
	return t, ok
}

func (s *kubernetesCheckpointStore) Set(name string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = t
	return s.persistAfterMutationLocked()
}

func (s *kubernetesCheckpointStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return keysOf(s.m)
}

func (s *kubernetesCheckpointStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; !ok {
		return nil
	}
	delete(s.m, name)
	return s.persistAfterMutationLocked()
}

func (s *kubernetesCheckpointStore) setBatch(updates map[string]time.Time, deletes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range deletes {
		delete(s.m, key)
	}
	for key, value := range updates {
		s.m[key] = value
	}
	return s.persistAfterMutationLocked()
}

func (s *kubernetesCheckpointStore) persistAfterMutationLocked() error {
	if s.writeDebounce > 0 {
		s.markDirtyLocked()
		return nil
	}
	s.dirty = true
	return s.persistDirtyLocked()
}

func (s *kubernetesCheckpointStore) markDirtyLocked() {
	s.dirty = true
	if s.timer == nil {
		s.timer = time.AfterFunc(s.writeDebounce, s.persistDebounced)
	}
}

func (s *kubernetesCheckpointStore) persistDebounced() {
	s.mu.Lock()
	if s.timer == nil {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	err := s.persistDirtyLocked()
	s.mu.Unlock()
	if err != nil {
		slog.Default().Warn("debounced Kubernetes checkpoint persist failed; it will retry on the next mutation or shutdown flush", "error", err)
	}
}

func (s *kubernetesCheckpointStore) persistDirtyLocked() error {
	if !s.dirty {
		return s.persistErr
	}
	s.dirty = false
	if err := s.persistLocked(); err != nil {
		s.dirty = true
		s.persistErr = err
		return err
	}
	s.persistErr = nil
	return nil
}

// Flush cancels a pending debounce timer and synchronously writes the newest
// map. A resourceVersion conflict is returned to the shutdown caller, making a
// stale leader's write visible instead of silently overwriting the new leader.
func (s *kubernetesCheckpointStore) Flush() error {
	s.mu.Lock()
	if s.timer != nil {
		timer := s.timer
		s.timer = nil
		timer.Stop()
	}
	err := s.persistDirtyLocked()
	s.mu.Unlock()
	return err
}

func (s *kubernetesCheckpointStore) persistLocked() error {
	data, err := json.Marshal(s.m)
	if err != nil {
		return err
	}
	if len(data) > KubernetesCheckpointDataLimit {
		return fmt.Errorf("%w: %d bytes", ErrKubernetesCheckpointTooLarge, len(data))
	}
	object, err := s.client.UpdateCheckpoint(context.Background(), KubernetesCheckpointObject{
		ResourceVersion: s.resourceVersion,
		Data:            string(data),
	})
	if err != nil {
		return fmt.Errorf("update Kubernetes checkpoint ConfigMap at resourceVersion %q: %w", s.resourceVersion, err)
	}
	if object.ResourceVersion == "" {
		return errors.New("update Kubernetes checkpoint ConfigMap returned an empty resourceVersion")
	}
	s.resourceVersion = object.ResourceVersion
	return nil
}

var _ CheckpointStore = (*kubernetesCheckpointStore)(nil)
var _ CheckpointFlusher = (*kubernetesCheckpointStore)(nil)
var _ checkpointBatchStore = (*kubernetesCheckpointStore)(nil)
