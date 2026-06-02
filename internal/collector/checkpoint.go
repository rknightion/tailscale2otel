package collector

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// CheckpointStore persists the high-water mark per window collector so polling
// resumes without gaps or overlaps across restarts.
type CheckpointStore interface {
	Get(name string) (time.Time, bool)
	Set(name string, t time.Time) error
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
			return nil, err
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
	data, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	// Write atomically: temp file then rename, so a crash mid-write can't
	// corrupt the checkpoint file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
