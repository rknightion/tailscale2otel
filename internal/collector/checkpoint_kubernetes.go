package collector

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// KubernetesCheckpointDataKey is the binaryData entry carrying one compressed checkpoint shard.
const KubernetesCheckpointDataKey = "checkpoints.json.gz"

// KubernetesCheckpointDataLimit is Kubernetes' 1 MiB ConfigMap data limit, per shard.
// The API server's ValidateConfigMap sums len(value) over BinaryData after JSON
// decoding, so base64 wire expansion is deliberately not part of this budget.
const KubernetesCheckpointDataLimit = 1 << 20

// KubernetesCheckpointDecodedLimit bounds gzip expansion and the JSON map the
// process must allocate while opening a shard. The encoder applies the same
// ceiling so the store never writes state a later process must refuse to open.
const KubernetesCheckpointDecodedLimit = 128 << 20

var (
	ErrKubernetesCheckpointNotFound        = errors.New("kubernetes checkpoint ConfigMap not found")
	ErrKubernetesCheckpointAlreadyExists   = errors.New("kubernetes checkpoint ConfigMap already exists")
	ErrKubernetesCheckpointConflict        = errors.New("kubernetes checkpoint resource version conflict")
	ErrKubernetesCheckpointTooLarge        = errors.New("kubernetes checkpoint ConfigMap data exceeds 1 MiB")
	ErrKubernetesCheckpointDecodedTooLarge = errors.New("kubernetes checkpoint decoded data exceeds 128 MiB")
	ErrKubernetesCheckpointWriteUncertain  = errors.New("kubernetes checkpoint write result is uncertain")
)

// KubernetesCheckpointObject is one checkpoint ConfigMap. Data is its binaryData payload.
type KubernetesCheckpointObject struct {
	Shard                          string
	ResourceVersion                string
	Data                           []byte
	LegacyMigrated                 bool
	LegacyMigrationResourceVersion string
}

// KubernetesCheckpointClient exposes current gzip shards and the former single JSON ConfigMap.
type KubernetesCheckpointClient interface {
	ListCheckpoints(context.Context) ([]KubernetesCheckpointObject, error)
	GetLegacyCheckpoint(context.Context) (KubernetesCheckpointObject, error)
	MarkLegacyCheckpointMigrated(context.Context, string) (string, error)
	CreateCheckpoint(context.Context, KubernetesCheckpointObject) (KubernetesCheckpointObject, error)
	UpdateCheckpoint(context.Context, KubernetesCheckpointObject) (KubernetesCheckpointObject, error)
}

type KubernetesCheckpointStoreOption func(*kubernetesCheckpointStore)

// WithKubernetesCheckpointWriteDebounce changes the interval between writes.
// A non-positive duration makes mutations synchronous for callers and tests.
func WithKubernetesCheckpointWriteDebounce(d time.Duration) KubernetesCheckpointStoreOption {
	return func(s *kubernetesCheckpointStore) { s.writeDebounce = d }
}

// WithKubernetesCheckpointFatalWrite reports a write whose server-side result
// cannot be known. A coordinated caller uses it to relinquish leadership and
// restart from the API server's authoritative resourceVersion.
func WithKubernetesCheckpointFatalWrite(report func(error)) KubernetesCheckpointStoreOption {
	return func(s *kubernetesCheckpointStore) { s.fatalWrite = report }
}

type kubernetesCheckpointShard struct {
	resourceVersion                string
	legacyMigrationResourceVersion string
	m                              map[string]time.Time
}

// kubernetesCheckpointStore persists collector namespaces independently. A conflict
// is not retried: a deposed leader must never reload then overwrite the winner.
type kubernetesCheckpointStore struct {
	mu                             sync.Mutex
	client                         KubernetesCheckpointClient
	shards                         map[string]*kubernetesCheckpointShard
	legacyMigrationResourceVersion string
	writeDebounce                  time.Duration
	timer                          *time.Timer
	dirty                          map[string]bool
	persistErr                     error
	fatalWrite                     func(error)
}

const (
	defaultKubernetesCheckpointWriteDebounce = 5 * time.Second
	kubernetesCheckpointWriteTimeout         = 30 * time.Second
)

// NewKubernetesCheckpointStore opens current gzip shards or migrates the legacy
// single-ConfigMap JSON state before returning a durable checkpoint store.
func NewKubernetesCheckpointStore(ctx context.Context, client KubernetesCheckpointClient, opts ...KubernetesCheckpointStoreOption) (CheckpointStore, error) {
	if client == nil {
		return nil, errors.New("kubernetes checkpoint client is required")
	}
	ctx, cancel := context.WithTimeout(ctx, kubernetesCheckpointWriteTimeout)
	defer cancel()
	s := &kubernetesCheckpointStore{client: client, shards: map[string]*kubernetesCheckpointShard{}, dirty: map[string]bool{}, writeDebounce: defaultKubernetesCheckpointWriteDebounce}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	objects, err := client.ListCheckpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Kubernetes checkpoint shards: %w", err)
	}
	for _, object := range objects {
		shard, rows, err := decodeKubernetesCheckpoint(object.Data)
		if err != nil {
			return nil, fmt.Errorf("decode Kubernetes checkpoint shard at resourceVersion %q: %w", object.ResourceVersion, err)
		}
		if object.Shard != "" && object.Shard != shard {
			return nil, fmt.Errorf("kubernetes checkpoint shard identity mismatch: object=%q payload=%q", object.Shard, shard)
		}
		if _, exists := s.shards[shard]; exists {
			return nil, fmt.Errorf("duplicate Kubernetes checkpoint shard %q", shard)
		}
		for key := range rows {
			// Format v1 makes shard ownership an on-disk invariant. A future
			// ownership change needs an explicit versioned migration; silently
			// re-homing a mis-owned row here would rewrite corrupt state on open.
			if want := ShardKey(key); want != shard {
				return nil, fmt.Errorf("kubernetes checkpoint shard %q contains key %q owned by shard %q", shard, key, want)
			}
		}
		s.shards[shard] = &kubernetesCheckpointShard{resourceVersion: object.ResourceVersion, legacyMigrationResourceVersion: object.LegacyMigrationResourceVersion, m: rows}
	}
	baseline, baselineOK := s.legacyMigrationBaseline()
	if baselineOK {
		s.legacyMigrationResourceVersion = baseline
	}
	// A complete, shard-owned baseline is the durable completion signal. Without
	// one, a partial migration must merge only missing legacy rows so it cannot
	// replace a newer row already written to a shard. The old JSON is retained so
	// a rollback to the single-ConfigMap release can still reopen its state.
	legacy, legacyErr := client.GetLegacyCheckpoint(ctx)
	if legacyErr != nil && !errors.Is(legacyErr, ErrKubernetesCheckpointNotFound) {
		return nil, fmt.Errorf("open legacy Kubernetes checkpoint ConfigMap: %w", legacyErr)
	}
	if legacyErr == nil && !baselineOK {
		rows := map[string]time.Time{}
		if len(legacy.Data) > 0 {
			if err := json.Unmarshal(legacy.Data, &rows); err != nil {
				return nil, fmt.Errorf("decode legacy Kubernetes checkpoint ConfigMap: %w", err)
			}
		}
		for key, value := range rows {
			shard := ShardKey(key)
			state := s.shardForLocked(shard)
			if _, exists := state.m[key]; exists {
				continue
			}
			state.m[key] = value
			s.dirty[shard] = true
		}
		if err := s.persistDirtyLocked(ctx); err != nil {
			return nil, fmt.Errorf("migrate legacy Kubernetes checkpoint ConfigMap: %w", err)
		}
		legacyResourceVersion, err := client.MarkLegacyCheckpointMigrated(ctx, legacy.ResourceVersion)
		if err != nil {
			return nil, fmt.Errorf("mark legacy Kubernetes checkpoint migration complete: %w", err)
		}
		if err := s.persistLegacyMigrationResourceVersionLocked(ctx, legacyResourceVersion); err != nil {
			return nil, fmt.Errorf("persist legacy Kubernetes checkpoint migration resourceVersion: %w", err)
		}
	} else if legacyErr == nil && baseline != legacy.ResourceVersion {
		// Once every shard agrees on a baseline, an older release can remove
		// the legacy marker while advancing its cursors. Reconcile that writer's
		// newer rows, then advance the shard-owned baseline. Do not restore the
		// marker: it is not read to establish completion and an old writer can
		// remove it again.
		rows := map[string]time.Time{}
		if len(legacy.Data) > 0 {
			if err := json.Unmarshal(legacy.Data, &rows); err != nil {
				return nil, fmt.Errorf("decode rollback-era legacy Kubernetes checkpoint ConfigMap: %w", err)
			}
		}
		for key, value := range rows {
			shard := ShardKey(key)
			state := s.shardForLocked(shard)
			if current, exists := state.m[key]; exists && !value.After(current) {
				continue
			}
			state.m[key] = value
			s.dirty[shard] = true
		}
		if err := s.persistDirtyLocked(ctx); err != nil {
			return nil, fmt.Errorf("reconcile rollback-era legacy Kubernetes checkpoint ConfigMap: %w", err)
		}
		if err := s.persistLegacyMigrationResourceVersionLocked(ctx, legacy.ResourceVersion); err != nil {
			return nil, fmt.Errorf("persist reconciled legacy Kubernetes checkpoint resourceVersion: %w", err)
		}
	}
	return s, nil
}

func (s *kubernetesCheckpointStore) shardForLocked(shard string) *kubernetesCheckpointShard {
	state := s.shards[shard]
	if state == nil {
		state = &kubernetesCheckpointShard{legacyMigrationResourceVersion: s.legacyMigrationResourceVersion, m: map[string]time.Time{}}
		s.shards[shard] = state
	}
	return state
}

// legacyMigrationBaseline returns a durable baseline only when every shard
// agrees. A missing or partial baseline is treated as an interrupted metadata
// write, so the next open re-merges rather than losing rollback-era cursor
// advances.
func (s *kubernetesCheckpointStore) legacyMigrationBaseline() (string, bool) {
	baseline := ""
	for _, state := range s.shards {
		if state.legacyMigrationResourceVersion == "" {
			return "", false
		}
		if baseline == "" {
			baseline = state.legacyMigrationResourceVersion
			continue
		}
		if baseline != state.legacyMigrationResourceVersion {
			return "", false
		}
	}
	return baseline, baseline != ""
}

func (s *kubernetesCheckpointStore) persistLegacyMigrationResourceVersionLocked(ctx context.Context, resourceVersion string) error {
	if resourceVersion == "" {
		return errors.New("legacy Kubernetes checkpoint migration returned an empty resourceVersion")
	}
	for shard, state := range s.shards {
		if state.legacyMigrationResourceVersion == resourceVersion {
			continue
		}
		state.legacyMigrationResourceVersion = resourceVersion
		s.dirty[shard] = true
	}
	if err := s.persistDirtyLocked(ctx); err != nil {
		return err
	}
	s.legacyMigrationResourceVersion = resourceVersion
	return nil
}

func (s *kubernetesCheckpointStore) Get(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.shards[ShardKey(name)]
	if state == nil {
		return time.Time{}, false
	}
	t, ok := state.m[name]
	return t, ok
}

func (s *kubernetesCheckpointStore) Set(name string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	shard := ShardKey(name)
	s.shardForLocked(shard).m[name] = t
	return s.persistAfterMutationLocked(shard)
}

func (s *kubernetesCheckpointStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, state := range s.shards {
		out = append(out, keysOf(state.m)...)
	}
	return out
}

func (s *kubernetesCheckpointStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	shard := ShardKey(name)
	state := s.shards[shard]
	if state == nil {
		return nil
	}
	if _, ok := state.m[name]; !ok {
		return nil
	}
	delete(state.m, name)
	return s.persistAfterMutationLocked(shard)
}

func (s *kubernetesCheckpointStore) setBatch(updates map[string]time.Time, deletes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := map[string]bool{}
	for _, key := range deletes {
		shard := ShardKey(key)
		if state := s.shards[shard]; state != nil {
			delete(state.m, key)
			changed[shard] = true
		}
	}
	for key, value := range updates {
		shard := ShardKey(key)
		s.shardForLocked(shard).m[key] = value
		changed[shard] = true
	}
	for shard := range changed {
		s.dirty[shard] = true
	}
	if s.writeDebounce > 0 {
		s.markDirtyLocked()
		return nil
	}
	shards := make([]string, 0, len(changed))
	for shard := range changed {
		shards = append(shards, shard)
	}
	sort.Strings(shards)
	ctx, cancel := context.WithTimeout(context.Background(), kubernetesCheckpointWriteTimeout)
	defer cancel()
	var errs []error
	for _, shard := range shards {
		if err := s.persistOneDirtyShardLocked(ctx, shard); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *kubernetesCheckpointStore) persistAfterMutationLocked(shard string) error {
	s.dirty[shard] = true
	if s.writeDebounce > 0 {
		s.markDirtyLocked()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), kubernetesCheckpointWriteTimeout)
	defer cancel()
	return s.persistOneDirtyShardLocked(ctx, shard)
}

func (s *kubernetesCheckpointStore) persistOneDirtyShardLocked(ctx context.Context, shard string) error {
	if !s.dirty[shard] {
		return nil
	}
	if err := s.persistShardLocked(ctx, shard); err != nil {
		s.persistErr = err
		return err
	}
	delete(s.dirty, shard)
	if len(s.dirty) == 0 {
		s.persistErr = nil
	}
	return nil
}

func (s *kubernetesCheckpointStore) markDirtyLocked() {
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
	ctx, cancel := context.WithTimeout(context.Background(), kubernetesCheckpointWriteTimeout)
	err := s.persistDirtyLocked(ctx)
	cancel()
	s.mu.Unlock()
	if err != nil {
		slog.Default().Warn("debounced Kubernetes checkpoint persist failed; it will retry on the next mutation or shutdown flush", "error", err)
	}
}

func (s *kubernetesCheckpointStore) persistDirtyLocked(ctx context.Context) error {
	if len(s.dirty) == 0 {
		return s.persistErr
	}
	shards := make([]string, 0, len(s.dirty))
	for shard := range s.dirty {
		shards = append(shards, shard)
	}
	sort.Strings(shards)
	var errs []error
	for _, shard := range shards {
		if err := s.persistShardLocked(ctx, shard); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(s.dirty, shard)
	}
	s.persistErr = errors.Join(errs...)
	return s.persistErr
}

func (s *kubernetesCheckpointStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), kubernetesCheckpointWriteTimeout)
	defer cancel()
	return s.persistDirtyLocked(ctx)
}

func (s *kubernetesCheckpointStore) persistShardLocked(ctx context.Context, shard string) error {
	state := s.shards[shard]
	compressed, uncompressedBytes, err := EncodeKubernetesCheckpointShard(shard, state.m)
	if err != nil {
		return err
	}
	if len(compressed) > KubernetesCheckpointDataLimit {
		return fmt.Errorf("%w: shard %q is %d bytes", ErrKubernetesCheckpointTooLarge, shard, len(compressed))
	}
	object := KubernetesCheckpointObject{Shard: shard, ResourceVersion: state.resourceVersion, Data: compressed, LegacyMigrationResourceVersion: state.legacyMigrationResourceVersion}
	if state.resourceVersion == "" {
		object, err = s.client.CreateCheckpoint(ctx, object)
	} else {
		object, err = s.client.UpdateCheckpoint(ctx, object)
	}
	if err != nil {
		persistErr := fmt.Errorf("persist Kubernetes checkpoint shard %q at resourceVersion %q: %w", shard, state.resourceVersion, err)
		// Conflict and AlreadyExists prove that this request did not win. Any
		// other Create/Update error can be an accepted write whose response was
		// lost; retrying from the old resourceVersion then wedges forever, while
		// reloading could let a deposed leader overwrite its successor. Exit the
		// active lifecycle and reopen from authoritative state instead.
		if !errors.Is(err, ErrKubernetesCheckpointConflict) && !errors.Is(err, ErrKubernetesCheckpointAlreadyExists) {
			persistErr = fmt.Errorf("%w: %w", ErrKubernetesCheckpointWriteUncertain, persistErr)
			if s.fatalWrite != nil {
				s.fatalWrite(persistErr)
			}
		}
		return persistErr
	}
	if object.ResourceVersion == "" {
		return fmt.Errorf("persist Kubernetes checkpoint shard %q returned an empty resourceVersion", shard)
	}
	state.resourceVersion = object.ResourceVersion
	ratio := float64(uncompressedBytes) / float64(len(compressed))
	shardDigest := sha256.Sum256([]byte(shard))
	slog.Default().Info("persisted Kubernetes checkpoint shard", "checkpoint.shard_count", len(s.shards), "checkpoint.shard", fmt.Sprintf("%x", shardDigest[:8]), "checkpoint.uncompressed_size", uncompressedBytes, "checkpoint.compressed_size", len(compressed), "checkpoint.compression_ratio", ratio)
	return nil
}

type kubernetesCheckpointEnvelope struct {
	Version     int                  `json:"version"`
	Shard       string               `json:"shard"`
	Checkpoints map[string]time.Time `json:"checkpoints"`
}

const kubernetesCheckpointFormatVersion = 1

// EncodeKubernetesCheckpointShard is the single production encoder shared by
// persistence and configuration projection, so the startup arithmetic cannot
// drift from the bytes Kubernetes receives.
func EncodeKubernetesCheckpointShard(shard string, rows map[string]time.Time) ([]byte, int, error) {
	return encodeKubernetesCheckpointShard(shard, rows, KubernetesCheckpointDecodedLimit)
}

func encodeKubernetesCheckpointShard(shard string, rows map[string]time.Time, decodedLimit int) ([]byte, int, error) {
	data, err := json.Marshal(kubernetesCheckpointEnvelope{
		Version:     kubernetesCheckpointFormatVersion,
		Shard:       shard,
		Checkpoints: rows,
	})
	if err != nil {
		return nil, 0, err
	}
	if len(data) > decodedLimit {
		return nil, len(data), fmt.Errorf("%w: shard %q is %d bytes (limit %d)", ErrKubernetesCheckpointDecodedTooLarge, shard, len(data), decodedLimit)
	}
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(data); err != nil {
		return nil, 0, err
	}
	if err := w.Close(); err != nil {
		return nil, 0, err
	}
	return out.Bytes(), len(data), nil
}

func decodeKubernetesCheckpoint(data []byte) (string, map[string]time.Time, error) {
	return decodeKubernetesCheckpointWithLimit(data, KubernetesCheckpointDecodedLimit)
}

func decodeKubernetesCheckpointWithLimit(data []byte, decodedLimit int64) (string, map[string]time.Time, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", nil, err
	}
	defer r.Close()
	limited := &io.LimitedReader{R: r, N: decodedLimit + 1}
	decoder := json.NewDecoder(limited)
	var envelope kubernetesCheckpointEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		if limited.N == 0 {
			return "", nil, fmt.Errorf("%w: limit %d", ErrKubernetesCheckpointDecodedTooLarge, decodedLimit)
		}
		return "", nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if limited.N == 0 {
			return "", nil, fmt.Errorf("%w: limit %d", ErrKubernetesCheckpointDecodedTooLarge, decodedLimit)
		}
		if err == nil {
			return "", nil, errors.New("trailing JSON value")
		}
		return "", nil, err
	}
	if limited.N == 0 {
		return "", nil, fmt.Errorf("%w: limit %d", ErrKubernetesCheckpointDecodedTooLarge, decodedLimit)
	}
	if envelope.Version != kubernetesCheckpointFormatVersion {
		return "", nil, fmt.Errorf("unsupported format version %d", envelope.Version)
	}
	if envelope.Shard == "" {
		return "", nil, errors.New("empty shard identity")
	}
	if envelope.Checkpoints == nil {
		envelope.Checkpoints = map[string]time.Time{}
	}
	return envelope.Shard, envelope.Checkpoints, nil
}

var _ CheckpointStore = (*kubernetesCheckpointStore)(nil)
var _ CheckpointFlusher = (*kubernetesCheckpointStore)(nil)
var _ checkpointBatchStore = (*kubernetesCheckpointStore)(nil)
