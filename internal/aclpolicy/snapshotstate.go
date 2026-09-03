package aclpolicy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
)

// SnapshotState is the policy body and snapshot-emitter baseline retained for
// change-only policy snapshot and diff emission.
type SnapshotState struct {
	Revision string
	Emitted  time.Time
	Body     string
}

// SnapshotStateStore retains the prior raw policy body beside the collector's
// checkpoint state. A file-backed implementation is supplied by the app
// composition root; MemorySnapshotStateStore is useful for ephemeral runs and
// collector tests.
type SnapshotStateStore interface {
	Load() (SnapshotState, error)
	Save(SnapshotState) error
}

// MemorySnapshotStateStore is an in-memory SnapshotStateStore.
type MemorySnapshotStateStore struct {
	mu    sync.Mutex
	state SnapshotState
}

// FileSnapshotStateStore persists snapshot state in an owner-only file. Its
// path is deliberately supplied by the composition root beside the configured
// evidence/checkpoint path; policy bodies must not be mixed into timestamp-only
// checkpoint rows.
type FileSnapshotStateStore struct {
	mu   sync.Mutex
	path string
}

// NewFileSnapshotStateStore returns a state store rooted at path.
func NewFileSnapshotStateStore(path string) *FileSnapshotStateStore {
	return &FileSnapshotStateStore{path: path}
}

// Load reads the retained state. A missing state file is an empty baseline.
func (s *FileSnapshotStateStore) Load() (SnapshotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := safefile.ReadRegular(s.path, safefile.MaxCheckpointBytes, safefile.NoSymlink)
	if errors.Is(err, os.ErrNotExist) {
		return SnapshotState{}, nil
	}
	if err != nil {
		return SnapshotState{}, err
	}
	var state SnapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return SnapshotState{}, err
	}
	return state, nil
}

// Save atomically replaces the retained state with owner-only permissions.
func (s *FileSnapshotStateStore) Save(state SnapshotState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".tailscale2otel-policy-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

// NewMemorySnapshotStateStore returns an empty, process-local state store.
func NewMemorySnapshotStateStore() *MemorySnapshotStateStore {
	return &MemorySnapshotStateStore{}
}

// Load returns the last state saved to the store.
func (s *MemorySnapshotStateStore) Load() (SnapshotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

// Save replaces the retained state.
func (s *MemorySnapshotStateStore) Save(state SnapshotState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	return nil
}
