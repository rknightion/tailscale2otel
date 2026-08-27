package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
)

// ErrCorruptCheckpoint reports that a checkpoint file exists but its content
// could not be decoded (bit-rot, a manual edit, or an incompatible-schema
// restore). It wraps the underlying decode error. Callers should degrade
// gracefully (start from an empty checkpoint) rather than treat it as fatal —
// the data is disposable window-cursor state, so the cost is a single cold start.
var ErrCorruptCheckpoint = errors.New("checkpoint file is corrupt or unreadable")

// ACLAuditChangeCheckpointKey stores the newest source timestamp carried by a
// classified ACL configuration-audit event. The audit processor advances it;
// the ACL collector re-emits it on every poll so the gauge survives restarts.
const ACLAuditChangeCheckpointKey = "acl/audit/last_change"

// CheckpointStore persists the high-water mark per window collector so polling
// resumes without gaps or overlaps across restarts.
type CheckpointStore interface {
	Get(name string) (time.Time, bool)
	Set(name string, t time.Time) error
	// Keys returns every stored checkpoint key (for startup migration/pruning).
	Keys() []string
	// Delete removes a stored key (used when migrating a renamed key).
	Delete(name string) error
}

// checkpointBatchStore is implemented by the built-in stores so a collector
// can persist a related set of cursor/identity changes with one file rewrite.
// It stays optional to preserve compatibility with external test stores.
type checkpointBatchStore interface {
	setBatch(updates map[string]time.Time, deletes []string) error
}

// UpdateCheckpointBatch applies deletes and updates as one persistence
// operation when store supports it. Updates win when a key appears in both
// collections. A third-party CheckpointStore falls back to the public
// Delete/Set methods in the same order.
func UpdateCheckpointBatch(store CheckpointStore, updates map[string]time.Time, deletes []string) error {
	if batch, ok := store.(checkpointBatchStore); ok {
		return batch.setBatch(updates, deletes)
	}
	for _, key := range deletes {
		if err := store.Delete(key); err != nil {
			return err
		}
	}
	for key, value := range updates {
		if err := store.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

// memoryStore keeps checkpoints in memory only (lost on restart).
type memoryStore struct {
	mu sync.Mutex
	m  map[string]time.Time
}

// NewMemoryStore returns an in-memory checkpoint store.
func NewMemoryStore() CheckpointStore {
	return &memoryStore{m: map[string]time.Time{}}
}

func (s *memoryStore) Get(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[name]
	return t, ok
}

func (s *memoryStore) Set(name string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = t
	return nil
}

func (s *memoryStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return keysOf(s.m)
}

func (s *memoryStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, name)
	return nil
}

func (s *memoryStore) setBatch(updates map[string]time.Time, deletes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range deletes {
		delete(s.m, key)
	}
	for key, value := range updates {
		s.m[key] = value
	}
	return nil
}

// fileStore persists checkpoints to a JSON file, written atomically on each Set.
type fileStore struct {
	mu   sync.Mutex
	path string
	m    map[string]time.Time
}

// NewFileStore returns a file-backed checkpoint store, loading any existing
// checkpoints from path. A missing file is not an error (starts empty).
//
// Opening the store is also where the staging-file sweep runs (#491): this is
// the only place a file-backed store comes into existence, the app builds one
// exactly once per process at startup (internal/app.checkpointStore), and it
// happens before this store has written anything, so the sweep can never race a
// save of its own. Its outcome does not gate the store — see sweepStagingFiles.
func NewFileStore(path string) (CheckpointStore, error) {
	fs := &fileStore{path: path, m: map[string]time.Time{}}
	sweepStagingFiles(path)
	data, err := safefile.ReadRegular(path, safefile.MaxCheckpointBytes, safefile.NoSymlink)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fs, nil
		}
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &fs.m); err != nil {
			// Corrupt/incompatible content: wrap so the caller can degrade to an
			// empty checkpoint instead of crash-looping startup (#69).
			return nil, fmt.Errorf("%w: %w", ErrCorruptCheckpoint, err)
		}
	}
	return fs, nil
}

// sweepStagingFiles reclaims staging files orphaned by a previous hard kill and
// logs what happened. It returns nothing on purpose: an unreadable or
// unwritable checkpoint directory must not block startup, matching how the rest
// of the checkpoint path degrades (an unwritable directory already falls back
// to in-memory checkpoints rather than failing). Worst case the leaked files
// stay leaked, which is exactly today's behavior.
func sweepStagingFiles(path string) {
	removed, err := sweepStaleStagingFiles(path)
	if err != nil {
		slog.Default().Warn("could not sweep stale checkpoint staging files; any files left by a previous hard kill will remain",
			"dir", filepath.Dir(path), "error", err)
	}
	if removed > 0 {
		slog.Default().Info("removed stale checkpoint staging files left behind by a previous hard kill",
			"dir", filepath.Dir(path), "removed", removed, "older_than", stagingFileMaxAge)
	}
}

func (s *fileStore) Get(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[name]
	return t, ok
}

func (s *fileStore) Set(name string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = t
	return s.persistLocked()
}

func (s *fileStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return keysOf(s.m)
}

func (s *fileStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; !ok {
		return nil
	}
	delete(s.m, name)
	return s.persistLocked()
}

func (s *fileStore) setBatch(updates map[string]time.Time, deletes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range deletes {
		delete(s.m, key)
	}
	for key, value := range updates {
		s.m[key] = value
	}
	return s.persistLocked()
}

// checkpointFileMode is the intended permission of the checkpoint file:
// owner-only, since it is exporter-private state.
const checkpointFileMode os.FileMode = 0o600

// persistLocked writes the current map atomically via writeFileAtomic — a
// randomized, symlink-safe temp file in the checkpoint's own directory, fsynced
// and renamed into place, with the directory fsynced afterwards — so a crash
// mid-write can't corrupt the file, the rename is durable, and a symlink parked
// at the old predictable "<path>.tmp" is never followed (#471). Callers must
// hold s.mu.
func (s *fileStore) persistLocked() error {
	data, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, checkpointFileMode)
}

// keysOf returns the keys of a checkpoint map (unordered).
func keysOf(m map[string]time.Time) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Namespaced returns a view of store whose keys are all prefixed with ns+"/".
//
// The scheduler namespaces the checkpoints it owns itself (see
// WithCheckpointNamespace); this is for a collector that keeps its OWN keys in
// the shared store — in multi-tailnet mode two tailnets would otherwise share
// one set of cursors and each skip the other's ground. An empty ns is a
// pass-through, so single-tailnet keys stay bare and continue to resolve across
// an upgrade.
func Namespaced(store CheckpointStore, ns string) CheckpointStore {
	if ns == "" {
		return store
	}
	return namespaced{store: store, prefix: ns + "/"}
}

type namespaced struct {
	store  CheckpointStore
	prefix string
}

func (n namespaced) Get(name string) (time.Time, bool)  { return n.store.Get(n.prefix + name) }
func (n namespaced) Set(name string, t time.Time) error { return n.store.Set(n.prefix+name, t) }
func (n namespaced) Delete(name string) error           { return n.store.Delete(n.prefix + name) }

func (n namespaced) setBatch(updates map[string]time.Time, deletes []string) error {
	prefixedUpdates := make(map[string]time.Time, len(updates))
	for key, value := range updates {
		prefixedUpdates[n.prefix+key] = value
	}
	prefixedDeletes := make([]string, len(deletes))
	for i, key := range deletes {
		prefixedDeletes[i] = n.prefix + key
	}
	return UpdateCheckpointBatch(n.store, prefixedUpdates, prefixedDeletes)
}

// Keys returns only this namespace's keys, with the prefix stripped, so a caller
// enumerating its own state never sees another tailnet's.
func (n namespaced) Keys() []string {
	var out []string
	for _, k := range n.store.Keys() {
		if rest, ok := strings.CutPrefix(k, n.prefix); ok {
			out = append(out, rest)
		}
	}
	return out
}
