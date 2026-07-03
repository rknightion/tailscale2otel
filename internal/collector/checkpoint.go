package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrCorruptCheckpoint reports that a checkpoint file exists but its content
// could not be decoded (bit-rot, a manual edit, or an incompatible-schema
// restore). It wraps the underlying decode error. Callers should degrade
// gracefully (start from an empty checkpoint) rather than treat it as fatal —
// the data is disposable window-cursor state, so the cost is a single cold start.
var ErrCorruptCheckpoint = errors.New("checkpoint file is corrupt or unreadable")

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

// fileStore persists checkpoints to a JSON file, written atomically on each Set.
type fileStore struct {
	mu   sync.Mutex
	path string
	m    map[string]time.Time
}

// NewFileStore returns a file-backed checkpoint store, loading any existing
// checkpoints from path. A missing file is not an error (starts empty).
func NewFileStore(path string) (CheckpointStore, error) {
	fs := &fileStore{path: path, m: map[string]time.Time{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
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

// persistLocked writes the current map atomically (temp file + rename) with an
// fsync of both the temp file and its directory before/after the rename, so a
// crash mid-write can't corrupt the file and the rename is durable. Callers must
// hold s.mu.
func (s *fileStore) persistLocked() error {
	data, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// fsync the directory so the rename survives a crash/power loss.
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// keysOf returns the keys of a checkpoint map (unordered).
func keysOf(m map[string]time.Time) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
