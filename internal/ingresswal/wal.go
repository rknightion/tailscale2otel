package ingresswal

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrFull identifies a byte or entry capacity refusal.
	ErrFull = errors.New("ingress WAL is full")
	// ErrCorrupt identifies malformed, truncated, or checksum-invalid state.
	ErrCorrupt = errors.New("ingress WAL state is corrupt")
	// ErrIncompatible identifies a record written by an unsupported format.
	ErrIncompatible = errors.New("ingress WAL state is incompatible")
	// ErrOwnership identifies a second writer or an unsafe filesystem object.
	ErrOwnership = errors.New("ingress WAL ownership refused")
	// ErrClosed identifies use after Close.
	ErrClosed = errors.New("ingress WAL is closed")
	// ErrUnsupported identifies a platform where the durable file primitives
	// are unavailable. Stateless operation remains buildable on that platform.
	ErrUnsupported = errors.New("ingress WAL is unsupported on this platform")
)

// CapacityLimit identifies which explicit capacity bound rejected an append.
type CapacityLimit string

const (
	LimitBytes   CapacityLimit = "bytes"
	LimitEntries CapacityLimit = "entries"
)

// FullError reports the capacity bound that refused a record.
type FullError struct {
	Limit     CapacityLimit
	Maximum   int64
	Current   int64
	Requested int64
}

func (e *FullError) Error() string {
	return fmt.Sprintf("ingress WAL %s capacity exhausted: current=%d requested=%d maximum=%d",
		e.Limit, e.Current, e.Requested, e.Maximum)
}

func (e *FullError) Unwrap() error { return ErrFull }

// CorruptError reports corrupt persisted state without including payload data.
type CorruptError struct {
	Reason string
}

func (e *CorruptError) Error() string { return "ingress WAL corrupt state: " + e.Reason }
func (e *CorruptError) Unwrap() error { return ErrCorrupt }

func newCorruptError(reason string) error { return &CorruptError{Reason: reason} }

// IncompatibleError reports an unsupported on-disk record version.
type IncompatibleError struct {
	Version byte
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("ingress WAL record version %d is unsupported", e.Version)
}

func (e *IncompatibleError) Unwrap() error { return ErrIncompatible }

// OwnershipError reports an object the WAL cannot safely own or a live writer.
type OwnershipError struct {
	Reason string
}

func (e *OwnershipError) Error() string { return "ingress WAL ownership refused: " + e.Reason }
func (e *OwnershipError) Unwrap() error { return ErrOwnership }

// Options has no public defaults: callers must choose both capacity bounds.
type Options struct {
	Directory  string
	MaxBytes   int64
	MaxEntries int
}

// Health is a payload-free snapshot of local WAL capacity and lifecycle state.
type Health struct {
	PendingBytes      int64
	PendingEntries    int
	CompletionMarkers int
	OrphanStages      int
	OrphanBytes       int64
	MaxBytes          int64
	MaxEntries        int
	Closed            bool
}

// Handler applies one exact persisted envelope during oldest-first replay.
type Handler func(context.Context, Envelope) error

// CommitObserver acknowledges one opaque ID after replay has durably committed
// it. It receives no envelope or transport data. Replay invokes it without the
// Store state mutex, but while replay serialization is held; an observer must
// not re-enter Replay or Close.
type CommitObserver func(string)

// WAL is the receiver-facing durability seam.
type WAL interface {
	Append(context.Context, Envelope) error
	Commit(context.Context, string) error
	Replay(context.Context, Handler, CommitObserver) error
	Health() Health
	Close() error
}

type pendingEntry struct {
	name     string
	size     int64
	sequence uint64
	durable  bool
}

type completionMarker struct {
	name          string
	sequence      uint64
	entrySequence uint64
}

type completedSnapshot struct {
	id     string
	marker completionMarker
}

// Store is an owner-locked filesystem WAL.
type Store struct {
	mu           sync.Mutex
	replayMu     sync.Mutex
	opts         Options
	ops          fileOps
	dirFile      *os.File
	lockFile     *os.File
	pending      map[string]pendingEntry
	completed    map[string]completionMarker
	orphanStages map[string]int64
	bytes        int64
	orphanBytes  int64
	nextEntry    uint64
	nextMarker   uint64
	closed       bool
}

type fileOps struct {
	platformSupported func() error
	openDirectory     func(string) (*os.File, error)
	openDirectoryAt   func(*os.File, string) (*os.File, error)
	mkdirAt           func(*os.File, string, os.FileMode) error
	openAt            func(*os.File, string, int, os.FileMode) (*os.File, error)
	createExclusiveAt func(*os.File, string, os.FileMode) (*os.File, error)
	publishNoReplace  func(*os.File, string, string) error
	removeAt          func(*os.File, string) error
	modeAt            func(*os.File, string) (os.FileMode, error)
	readDir           func(*os.File) ([]os.DirEntry, error)
	lstat             func(string) (os.FileInfo, error)
	write             func(*os.File, []byte) (int, error)
	syncFile          func(*os.File) error
	closeFile         func(*os.File) error
	syncDir           func(*os.File) error
	lockExclusive     func(*os.File) error
	unlock            func(*os.File) error
	randomRead        func([]byte) (int, error)
}

var realFileOps = fileOps{
	platformSupported: platformSupported,
	openDirectory:     platformOpenDirectory,
	openDirectoryAt:   platformOpenDirectoryAt,
	mkdirAt:           platformMkdirAt,
	openAt:            platformOpenAt,
	createExclusiveAt: platformCreateExclusiveAt,
	publishNoReplace:  platformPublishNoReplace,
	removeAt:          platformRemoveAt,
	modeAt:            platformModeAt,
	readDir: func(directory *os.File) ([]os.DirEntry, error) {
		if _, err := directory.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return directory.ReadDir(-1)
	},
	lstat:         os.Lstat,
	write:         func(file *os.File, data []byte) (int, error) { return file.Write(data) },
	syncFile:      func(file *os.File) error { return file.Sync() },
	closeFile:     func(file *os.File) error { return file.Close() },
	syncDir:       func(directory *os.File) error { return directory.Sync() },
	lockExclusive: platformLockExclusive,
	unlock:        platformUnlock,
	randomRead:    rand.Read,
}

const (
	entrySuffix       = ".wal"
	lockName          = ".owner.lock"
	completionPrefix  = ".completed-"
	completionSuffix  = ".mark"
	appendStagePrefix = ".ingresswal-append-"
	doneStagePrefix   = ".ingresswal-complete-"
	stageSuffix       = ".tmp"
	sequenceDigits    = 20
	stageRandomBytes  = 16
)

// New opens an explicitly bounded filesystem WAL and takes exclusive writer
// ownership until Close.
func New(opts Options) (*Store, error) {
	return newStore(opts, realFileOps)
}

func newStore(opts Options, ops fileOps) (*Store, error) {
	if opts.Directory == "" {
		return nil, fmt.Errorf("ingress WAL directory is required")
	}
	if opts.MaxBytes <= 0 || opts.MaxEntries <= 0 {
		return nil, fmt.Errorf("ingress WAL MaxBytes and MaxEntries must both be positive")
	}
	if err := ops.platformSupported(); err != nil {
		return nil, err
	}
	store := &Store{
		opts:         opts,
		ops:          ops,
		pending:      make(map[string]pendingEntry),
		completed:    make(map[string]completionMarker),
		orphanStages: make(map[string]int64),
	}
	if err := store.open(); err != nil {
		_ = store.releaseResources()
		return nil, err
	}
	return store, nil
}

func (s *Store) open() error {
	clean := filepath.Clean(s.opts.Directory)
	parentPath, base := filepath.Dir(clean), filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) {
		return &OwnershipError{Reason: "directory must name one final child"}
	}
	parent, err := s.ops.openDirectory(parentPath)
	if err != nil {
		return fmt.Errorf("ingress WAL open existing parent directory: %w", err)
	}
	defer func() { _ = s.ops.closeFile(parent) }()

	directory, err := s.ops.openDirectoryAt(parent, base)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.ops.mkdirAt(parent, base, 0o700); err != nil {
			return fmt.Errorf("ingress WAL create final directory: %w", err)
		}
		directory = nil
		err = nil
	}
	if err != nil {
		return &OwnershipError{Reason: "WAL path is not a no-follow directory"}
	}
	// Sync the parent even when the child is already visible. A prior startup
	// may have created it and then received a parent-fsync error; retry must not
	// treat namespace visibility as proof that creation is durable.
	if err := s.ops.syncDir(parent); err != nil {
		return fmt.Errorf("ingress WAL sync parent directory: %w", err)
	}
	if directory == nil {
		directory, err = s.ops.openDirectoryAt(parent, base)
	}
	if err != nil {
		return &OwnershipError{Reason: "WAL path is not a no-follow directory"}
	}
	s.dirFile = directory
	if err := s.dirFile.Chmod(0o700); err != nil {
		return fmt.Errorf("ingress WAL secure directory: %w", err)
	}
	if err := s.revalidateDirectory(); err != nil {
		return err
	}
	if err := s.acquireLock(); err != nil {
		return err
	}
	if err := s.sweepStages(); err != nil {
		return err
	}
	if err := s.load(); err != nil {
		return err
	}
	// Crash recovery cannot treat a visible hard link as proof that its
	// publication reached stable storage. Establish one directory barrier and
	// revalidate the configured path before making loaded state replayable.
	if len(s.pending) > 0 || len(s.completed) > 0 {
		if err := s.syncAndRevalidate(); err != nil {
			return fmt.Errorf("ingress WAL establish recovery durability barrier: %w", err)
		}
		for id, entry := range s.pending {
			entry.durable = true
			s.pending[id] = entry
		}
	}
	return nil
}

func (s *Store) acquireLock() error {
	file, err := s.ops.openAt(s.dirFile, lockName, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return &OwnershipError{Reason: "owner lock cannot be opened safely"}
	}
	s.lockFile = file
	info, err := file.Stat()
	if err != nil {
		return &OwnershipError{Reason: "owner lock cannot be inspected safely"}
	}
	if !info.Mode().IsRegular() {
		return &OwnershipError{Reason: "owner lock is not a regular file"}
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("ingress WAL secure owner lock: %w", err)
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return &OwnershipError{Reason: "owner lock is not an owner-only regular file"}
	}
	if err := s.ops.lockExclusive(file); err != nil {
		return &OwnershipError{Reason: "another writer owns the directory"}
	}
	return nil
}

func (s *Store) sweepStages() error {
	entries, err := s.ops.readDir(s.dirFile)
	if err != nil {
		return fmt.Errorf("ingress WAL list staging files: %w", err)
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if !inStageNamespace(name) {
			continue
		}
		if !validStageName(name) {
			return newCorruptError("malformed staging filename")
		}
		mode, err := s.ops.modeAt(s.dirFile, name)
		if err != nil {
			return fmt.Errorf("ingress WAL inspect staging file: %w", err)
		}
		if !mode.IsRegular() {
			return &OwnershipError{Reason: "staging entry is not a regular file"}
		}
		if err := s.ops.removeAt(s.dirFile, name); err != nil {
			return fmt.Errorf("ingress WAL remove staging file: %w", err)
		}
		removed = true
	}
	if removed {
		if err := s.ops.syncDir(s.dirFile); err != nil {
			return fmt.Errorf("ingress WAL sync staging cleanup: %w", err)
		}
	}
	return nil
}

func (s *Store) load() error {
	entries, err := s.ops.readDir(s.dirFile)
	if err != nil {
		return fmt.Errorf("ingress WAL list directory: %w", err)
	}
	entryNames := make(map[string]string)
	entrySequences := make(map[uint64]bool)
	markerSequences := make(map[uint64]bool)

	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, entrySuffix):
			sequence, id, ok := parseEntryName(name)
			if !ok {
				return newCorruptError("malformed entry filename")
			}
			if entrySequences[sequence] {
				return newCorruptError("duplicate entry sequence")
			}
			entrySequences[sequence] = true
			if _, duplicate := entryNames[id]; duplicate {
				return newCorruptError("duplicate entry identity")
			}
			entryNames[id] = name
			s.nextEntry = max(s.nextEntry, sequence)

		case strings.HasPrefix(name, completionPrefix):
			sequence, id, ok := parseMarkerName(name)
			if !ok {
				return newCorruptError("malformed completion filename")
			}
			if markerSequences[sequence] {
				return newCorruptError("duplicate completion sequence")
			}
			markerSequences[sequence] = true
			if _, duplicate := s.completed[id]; duplicate {
				return newCorruptError("duplicate completion identity")
			}
			if err := s.requireOwnedRegularAt(name); err != nil {
				return err
			}
			markerID, markerSequence, entrySequence, readErr := s.readMarker(name)
			if readErr != nil {
				return readErr
			}
			if markerID != id {
				return newCorruptError("completion marker identity mismatch")
			}
			if markerSequence != sequence {
				return newCorruptError("completion marker sequence mismatch")
			}
			s.completed[id] = completionMarker{
				name: name, sequence: sequence, entrySequence: entrySequence,
			}
			s.nextMarker = max(s.nextMarker, sequence)

		case inStageNamespace(name):
			return newCorruptError("staging entry remained after startup sweep")
		}
	}

	for id, name := range entryNames {
		sequence, filenameID, _ := parseEntryName(name)
		if err := s.requireOwnedRegularAt(name); err != nil {
			return err
		}
		data, readErr := s.readBounded(name)
		if readErr != nil {
			return readErr
		}
		record, decodeErr := decodeRecord(data)
		if decodeErr != nil {
			return decodeErr
		}
		if record.Envelope.ID != filenameID || record.Sequence != sequence {
			return newCorruptError("entry filename does not match record")
		}
		size := int64(len(data))
		if err := s.reserve(size); err != nil {
			return err
		}
		s.pending[id] = pendingEntry{
			name: name, size: size, sequence: sequence, durable: false,
		}
	}
	return nil
}

// Append durably creates one immutable entry before returning success.
func (s *Store) Append(ctx context.Context, envelope Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateEnvelope(envelope); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.cleanupOrphansLocked(); err != nil {
		return err
	}
	if _, completed := s.completed[envelope.ID]; completed {
		if _, _, err := s.cleanupCompletionLocked(envelope.ID, nil); err != nil {
			return err
		}
	}
	if existing, ok := s.pending[envelope.ID]; ok {
		onDisk, readErr := s.readBounded(existing.name)
		if readErr != nil {
			return readErr
		}
		record, decodeErr := decodeRecord(onDisk)
		if decodeErr != nil {
			return decodeErr
		}
		if envelopesEquivalent(record.Envelope, envelope) {
			if !existing.durable {
				if err := s.syncAndRevalidate(); err != nil {
					return err
				}
				existing.durable = true
				s.pending[envelope.ID] = existing
			}
			return nil
		}
		return &OwnershipError{Reason: "opaque ID already belongs to a different entry"}
	}
	if s.nextEntry == math.MaxUint64 {
		return newCorruptError("entry sequence exhausted")
	}
	sequence := s.nextEntry + 1
	data, err := encodeRecord(envelope, sequence)
	if err != nil {
		return err
	}
	if err := s.reserve(int64(len(data))); err != nil {
		return err
	}
	reserved := true
	defer func() {
		if reserved {
			s.release(int64(len(data)))
		}
	}()

	name := entryName(sequence, envelope.ID)
	result, writeErr := s.atomicCreate(ctx, name, appendStagePrefix, data)
	if result.orphan != "" {
		orphanSize := int64(0)
		if !result.landed {
			orphanSize = int64(len(data))
			s.release(orphanSize)
			s.orphanBytes += orphanSize
			reserved = false
		}
		s.orphanStages[result.orphan] = orphanSize
	}
	if !result.landed {
		return writeErr
	}
	s.pending[envelope.ID] = pendingEntry{
		name: name, size: int64(len(data)), sequence: sequence, durable: writeErr == nil,
	}
	s.nextEntry = sequence
	reserved = false
	return writeErr
}

// Commit durably marks handler success, then removes both the pending entry and
// the transient marker. A retained marker exists only to finish ambiguous
// cleanup without invoking the handler again.
func (s *Store) Commit(ctx context.Context, id string) error {
	_, err := s.commitGeneration(ctx, id, 0)
	return err
}

func (s *Store) commitGeneration(
	ctx context.Context,
	id string,
	expectedSequence uint64,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !validID(id) {
		return false, fmt.Errorf("ingress WAL commit: invalid opaque ID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrClosed
	}
	if err := s.cleanupOrphansLocked(); err != nil {
		return false, err
	}
	if marker, completed := s.completed[id]; completed {
		if expectedSequence != 0 && marker.entrySequence != expectedSequence {
			return false, nil
		}
		notify, _, err := s.cleanupCompletionLocked(id, nil)
		return notify, err
	}
	entry, ok := s.pending[id]
	if !ok {
		return false, nil
	}
	if expectedSequence != 0 && entry.sequence != expectedSequence {
		return false, nil
	}
	if !entry.durable {
		if err := s.syncAndRevalidate(); err != nil {
			return false, fmt.Errorf("ingress WAL sync pending entry before completion: %w", err)
		}
		entry.durable = true
		s.pending[id] = entry
	}
	if s.nextMarker == math.MaxUint64 {
		return false, newCorruptError("completion sequence exhausted")
	}
	sequence := s.nextMarker + 1
	marker, err := encodeMarker(id, sequence, entry.sequence)
	if err != nil {
		return false, err
	}
	name := markerName(sequence, id)
	result, writeErr := s.atomicCreate(context.Background(), name, doneStagePrefix, marker)
	if result.orphan != "" {
		s.orphanStages[result.orphan] = 0
	}
	if writeErr != nil {
		if result.landed {
			removeErr := s.ops.removeAt(s.dirFile, name)
			syncErr := s.ops.syncDir(s.dirFile)
			if (removeErr != nil && !errors.Is(removeErr, os.ErrNotExist)) || syncErr != nil {
				s.completed[id] = completionMarker{
					name: name, sequence: sequence, entrySequence: entry.sequence,
				}
				s.nextMarker = sequence
				return false, errors.Join(writeErr, removeErr, syncErr)
			}
		}
		return false, writeErr
	}
	s.completed[id] = completionMarker{
		name: name, sequence: sequence, entrySequence: entry.sequence,
	}
	s.nextMarker = sequence
	notify, _, err := s.cleanupCompletionLocked(id, nil)
	return notify, err
}

func (s *Store) cleanupCompletionLocked(
	id string,
	expected *completionMarker,
) (notify, cleaned bool, err error) {
	marker, completed := s.completed[id]
	if !completed {
		return false, false, nil
	}
	if expected != nil && marker != *expected {
		return false, false, nil
	}
	if entry, pending := s.pending[id]; pending {
		if entry.sequence == marker.entrySequence {
			if err := s.ops.removeAt(s.dirFile, entry.name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, false, fmt.Errorf("ingress WAL remove completed entry: %w", err)
			}
			if err := s.ops.syncDir(s.dirFile); err != nil {
				return false, false, fmt.Errorf("ingress WAL sync completed entry cleanup: %w", err)
			}
			delete(s.pending, id)
			s.release(entry.size)
			notify = true
		}
	} else {
		notify = true
	}
	if err := s.ops.removeAt(s.dirFile, marker.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, false, fmt.Errorf("ingress WAL remove completion marker: %w", err)
	}
	if err := s.ops.syncDir(s.dirFile); err != nil {
		return false, false, fmt.Errorf("ingress WAL sync completion marker cleanup: %w", err)
	}
	delete(s.completed, id)
	if err := s.revalidateDirectory(); err != nil {
		return false, false, err
	}
	return notify, true, nil
}

// Replay serializes snapshots so one pending ID cannot reach two handlers.
func (s *Store) Replay(ctx context.Context, handler Handler, observer CommitObserver) error {
	if handler == nil {
		return fmt.Errorf("ingress WAL replay handler is required")
	}
	s.replayMu.Lock()
	defer s.replayMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if err := s.cleanupOrphansLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	completed := make([]completedSnapshot, 0, len(s.completed))
	for id, marker := range s.completed {
		completed = append(completed, completedSnapshot{id: id, marker: marker})
	}
	sort.Slice(completed, func(i, j int) bool {
		if completed[i].marker.sequence == completed[j].marker.sequence {
			return completed[i].id < completed[j].id
		}
		return completed[i].marker.sequence < completed[j].marker.sequence
	})
	s.mu.Unlock()

	for _, snapshot := range completed {
		notify, err := s.cleanupCompletedSnapshot(ctx, snapshot)
		if err != nil {
			return err
		}
		if notify && observer != nil {
			observer(snapshot.id)
		}
	}

	s.mu.Lock()
	entries := make([]pendingEntry, 0, len(s.pending))
	ids := make(map[string]string, len(s.pending))
	for id, entry := range s.pending {
		if _, completed := s.completed[id]; entry.durable && !completed {
			entries = append(entries, entry)
			ids[entry.name] = id
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].sequence < entries[j].sequence })
	s.mu.Unlock()

	for _, snapshot := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := ids[snapshot.name]
		s.mu.Lock()
		entry, ok := s.pending[id]
		if !ok || !entry.durable || entry.sequence != snapshot.sequence {
			s.mu.Unlock()
			continue
		}
		data, err := s.readBounded(entry.name)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		record, err := decodeRecord(data)
		if err != nil {
			return err
		}
		if record.Envelope.ID != id || record.Sequence != entry.sequence {
			return newCorruptError("entry filename does not match record")
		}
		if err := handler(ctx, record.Envelope); err != nil {
			return err
		}
		committed, err := s.commitGeneration(ctx, id, snapshot.sequence)
		if err != nil {
			return err
		}
		if committed && observer != nil {
			observer(id)
		}
	}
	return nil
}

func (s *Store) cleanupCompletedSnapshot(
	ctx context.Context,
	snapshot completedSnapshot,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrClosed
	}
	if err := s.cleanupOrphansLocked(); err != nil {
		return false, err
	}
	notify, _, err := s.cleanupCompletionLocked(snapshot.id, &snapshot.marker)
	return notify, err
}

// Health returns capacity counters only; it never exposes payload or route data.
func (s *Store) Health() Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Health{
		PendingBytes:      s.bytes,
		PendingEntries:    len(s.pending),
		CompletionMarkers: len(s.completed),
		OrphanStages:      len(s.orphanStages),
		OrphanBytes:       s.orphanBytes,
		MaxBytes:          s.opts.MaxBytes,
		MaxEntries:        s.opts.MaxEntries,
		Closed:            s.closed,
	}
}

// Close releases exclusive writer ownership. It is idempotent.
func (s *Store) Close() error {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.releaseResources()
}

func (s *Store) reserve(size int64) error {
	if len(s.pending) >= s.opts.MaxEntries {
		return &FullError{
			Limit: LimitEntries, Maximum: int64(s.opts.MaxEntries),
			Current: int64(len(s.pending)), Requested: 1,
		}
	}
	if size > s.opts.MaxBytes-s.bytes-s.orphanBytes {
		return &FullError{
			Limit: LimitBytes, Maximum: s.opts.MaxBytes,
			Current: s.bytes + s.orphanBytes, Requested: size,
		}
	}
	s.bytes += size
	return nil
}

func (s *Store) release(size int64) {
	s.bytes -= size
	if s.bytes < 0 {
		s.bytes = 0
	}
}

type atomicCreateResult struct {
	landed bool
	orphan string
}

func (s *Store) atomicCreate(
	ctx context.Context,
	destination, stagePrefix string,
	data []byte,
) (atomicCreateResult, error) {
	if err := s.revalidateDirectory(); err != nil {
		return atomicCreateResult{}, err
	}
	stage, file, err := s.createStage(stagePrefix)
	if err != nil {
		return atomicCreateResult{}, err
	}
	closed := false
	closeFile := func() error {
		if !closed {
			closeErr := s.ops.closeFile(file)
			closed = true
			return closeErr
		}
		return nil
	}
	cleanupFailure := func(cause error) (atomicCreateResult, error) {
		closeErr := closeFile()
		removeErr := s.ops.removeAt(s.dirFile, stage)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return atomicCreateResult{orphan: stage}, errors.Join(cause, closeErr,
				fmt.Errorf("ingress WAL remove failed staging file: %w", removeErr))
		}
		return atomicCreateResult{}, errors.Join(cause, closeErr)
	}
	written, err := s.ops.write(file, data)
	if err != nil {
		return cleanupFailure(fmt.Errorf("ingress WAL write staging file: %w", err))
	}
	if written != len(data) {
		return cleanupFailure(fmt.Errorf("ingress WAL write staging file: %w", io.ErrShortWrite))
	}
	if err := s.ops.syncFile(file); err != nil {
		return cleanupFailure(fmt.Errorf("ingress WAL sync staging file: %w", err))
	}
	if err := closeFile(); err != nil {
		return cleanupFailure(fmt.Errorf("ingress WAL close staging file: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return cleanupFailure(err)
	}
	if err := s.revalidateDirectory(); err != nil {
		return cleanupFailure(err)
	}
	if err := s.ops.publishNoReplace(s.dirFile, stage, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return cleanupFailure(&OwnershipError{Reason: "destination already exists outside the active index"})
		}
		return cleanupFailure(fmt.Errorf("ingress WAL publish staging file without replacement: %w", err))
	}
	if err := s.ops.removeAt(s.dirFile, stage); err != nil {
		return atomicCreateResult{landed: true, orphan: stage},
			fmt.Errorf("ingress WAL remove published staging name: %w", err)
	}
	if err := s.ops.syncDir(s.dirFile); err != nil {
		return atomicCreateResult{landed: true}, fmt.Errorf("ingress WAL sync directory: %w", err)
	}
	if err := s.revalidateDirectory(); err != nil {
		return atomicCreateResult{landed: true}, err
	}
	return atomicCreateResult{landed: true}, nil
}

func (s *Store) cleanupOrphansLocked() error {
	if len(s.orphanStages) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.orphanStages))
	for name := range s.orphanStages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := s.ops.removeAt(s.dirFile, name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("ingress WAL retry staging cleanup: %w", err)
		}
	}
	if err := s.ops.syncDir(s.dirFile); err != nil {
		return fmt.Errorf("ingress WAL sync staging cleanup retry: %w", err)
	}
	for _, name := range names {
		s.orphanBytes -= s.orphanStages[name]
		delete(s.orphanStages, name)
	}
	if s.orphanBytes < 0 {
		s.orphanBytes = 0
	}
	return s.revalidateDirectory()
}

func (s *Store) createStage(prefix string) (string, *os.File, error) {
	for range 32 {
		random := make([]byte, stageRandomBytes)
		n, err := s.ops.randomRead(random)
		if err != nil {
			return "", nil, fmt.Errorf("ingress WAL staging random source: %w", err)
		}
		if n != len(random) {
			return "", nil, fmt.Errorf("ingress WAL staging random source: %w", io.ErrUnexpectedEOF)
		}
		name := prefix + hex.EncodeToString(random) + stageSuffix
		file, err := s.ops.createExclusiveAt(s.dirFile, name, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("ingress WAL create exclusive staging file: %w", err)
		}
		if err := file.Chmod(0o600); err != nil {
			_ = s.ops.closeFile(file)
			_ = s.ops.removeAt(s.dirFile, name)
			return "", nil, fmt.Errorf("ingress WAL secure staging file: %w", err)
		}
		return name, file, nil
	}
	return "", nil, &OwnershipError{Reason: "could not allocate a unique staging name"}
}

func (s *Store) readBounded(name string) ([]byte, error) {
	file, err := s.ops.openAt(s.dirFile, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, &OwnershipError{Reason: "state file cannot be opened without following links"}
	}
	defer func() { _ = s.ops.closeFile(file) }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("ingress WAL inspect state file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, &OwnershipError{Reason: "state file is not an owner-only regular file"}
	}
	reader := io.LimitReader(file, s.opts.MaxBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("ingress WAL read state file: %w", err)
	}
	if int64(len(data)) > s.opts.MaxBytes {
		return nil, newCorruptError("state file exceeds configured byte capacity")
	}
	return data, nil
}

func (s *Store) syncAndRevalidate() error {
	if err := s.ops.syncDir(s.dirFile); err != nil {
		return fmt.Errorf("ingress WAL sync directory: %w", err)
	}
	return s.revalidateDirectory()
}

func (s *Store) revalidateDirectory() error {
	held, err := s.dirFile.Stat()
	if err != nil {
		return &OwnershipError{Reason: "held directory cannot be inspected"}
	}
	current, err := s.ops.lstat(s.opts.Directory)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() ||
		!os.SameFile(held, current) {
		return &OwnershipError{Reason: "configured directory was replaced"}
	}
	return nil
}

func (s *Store) releaseResources() error {
	var errs []error
	if s.lockFile != nil {
		errs = append(errs, s.ops.unlock(s.lockFile), s.ops.closeFile(s.lockFile))
		s.lockFile = nil
	}
	if s.dirFile != nil {
		errs = append(errs, s.ops.closeFile(s.dirFile))
		s.dirFile = nil
	}
	return errors.Join(errs...)
}

func (s *Store) requireOwnedRegularAt(name string) error {
	mode, err := s.ops.modeAt(s.dirFile, name)
	if err != nil {
		return fmt.Errorf("ingress WAL inspect state file: %w", err)
	}
	if !mode.IsRegular() || mode.Perm()&0o077 != 0 {
		return &OwnershipError{Reason: "state file is not an owner-only regular file"}
	}
	return nil
}

func entryName(sequence uint64, id string) string {
	return fmt.Sprintf("%020d-%s%s", sequence, id, entrySuffix)
}

func parseEntryName(name string) (uint64, string, bool) {
	return parseSequencedName(name, "", entrySuffix)
}

func markerName(sequence uint64, id string) string {
	return fmt.Sprintf("%s%020d-%s%s", completionPrefix, sequence, id, completionSuffix)
}

func parseMarkerName(name string) (uint64, string, bool) {
	return parseSequencedName(name, completionPrefix, completionSuffix)
}

func parseSequencedName(name, prefix, suffix string) (uint64, string, bool) {
	value, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return 0, "", false
	}
	value, ok = strings.CutSuffix(value, suffix)
	if !ok || len(value) != sequenceDigits+1+idLength || value[sequenceDigits] != '-' {
		return 0, "", false
	}
	sequence, err := strconv.ParseUint(value[:sequenceDigits], 10, 64)
	id := value[sequenceDigits+1:]
	return sequence, id, err == nil && sequence > 0 && validID(id)
}

func inStageNamespace(name string) bool {
	return strings.HasPrefix(name, appendStagePrefix) || strings.HasPrefix(name, doneStagePrefix)
}

func validStageName(name string) bool {
	for _, prefix := range []string{appendStagePrefix, doneStagePrefix} {
		random, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		random, ok = strings.CutSuffix(random, stageSuffix)
		if !ok || len(random) != stageRandomBytes*2 || random != strings.ToLower(random) {
			return false
		}
		decoded, err := hex.DecodeString(random)
		return err == nil && len(decoded) == stageRandomBytes
	}
	return false
}

func envelopesEquivalent(left, right Envelope) bool {
	return left.ID == right.ID &&
		left.Tailnet == right.Tailnet &&
		left.Source == right.Source &&
		left.Signal == right.Signal &&
		bytes.Equal(left.Body, right.Body)
}

const (
	markerMagic       = "TS2DONE1"
	markerHeaderBytes = len(markerMagic) + 1 + 8 + 8
)

func encodeMarker(id string, sequence, entrySequence uint64) ([]byte, error) {
	if !validID(id) {
		return nil, fmt.Errorf("ingress WAL marker: invalid opaque ID")
	}
	if sequence == 0 {
		return nil, fmt.Errorf("ingress WAL marker: invalid sequence")
	}
	if entrySequence == 0 {
		return nil, fmt.Errorf("ingress WAL marker: invalid entry sequence")
	}
	data := make([]byte, markerHeaderBytes+len(id)+checksumBytes)
	copy(data, markerMagic)
	data[len(markerMagic)] = recordVersion
	sequenceAt := len(markerMagic) + 1
	binary.BigEndian.PutUint64(data[sequenceAt:sequenceAt+8], sequence)
	binary.BigEndian.PutUint64(data[sequenceAt+8:markerHeaderBytes], entrySequence)
	copy(data[markerHeaderBytes:], id)
	sum := sha256Bytes(data[:markerHeaderBytes+len(id)])
	copy(data[markerHeaderBytes+len(id):], sum)
	return data, nil
}

func (s *Store) readMarker(name string) (string, uint64, uint64, error) {
	data, err := s.readBounded(name)
	if err != nil {
		return "", 0, 0, err
	}
	if len(data) != markerHeaderBytes+idLength+checksumBytes ||
		string(data[:len(markerMagic)]) != markerMagic {
		return "", 0, 0, newCorruptError("completion marker is malformed")
	}
	if version := data[len(markerMagic)]; version != recordVersion {
		return "", 0, 0, &IncompatibleError{Version: version}
	}
	checksumAt := markerHeaderBytes + idLength
	want := sha256Bytes(data[:checksumAt])
	if !bytes.Equal(data[checksumAt:], want) {
		return "", 0, 0, newCorruptError("completion marker checksum mismatch")
	}
	sequenceAt := len(markerMagic) + 1
	sequence := binary.BigEndian.Uint64(data[sequenceAt : sequenceAt+8])
	if sequence == 0 {
		return "", 0, 0, newCorruptError("completion marker sequence is invalid")
	}
	entrySequence := binary.BigEndian.Uint64(data[sequenceAt+8 : markerHeaderBytes])
	if entrySequence == 0 {
		return "", 0, 0, newCorruptError("completion marker entry sequence is invalid")
	}
	id := string(data[markerHeaderBytes:checksumAt])
	if !validID(id) {
		return "", 0, 0, newCorruptError("completion marker identity is invalid")
	}
	return id, sequence, entrySequence, nil
}

func sha256Bytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
