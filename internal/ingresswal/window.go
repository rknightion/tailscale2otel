package ingresswal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Generation identifies an original entry in one open Store. Its identity is
// deliberately process-local and cannot acknowledge a later same-ID append.
type Generation struct {
	owner    *Store
	id       string
	sequence uint64
}

func (g Generation) ID() string { return g.id }

type WindowLimits struct {
	MaxEntries int
	MaxBytes   int64
}
type PreparedEntry struct {
	Generation   Generation
	Envelope     Envelope
	EncodedBytes int64
}
type GenerationObserver func(Generation)
type PreparedOutcome uint8

const (
	PreparedPending PreparedOutcome = iota
	PreparedRetired
)

var errPreparedToken = errors.New("ingress WAL invalid prepared generation")

// PrepareWindow performs storage-only recovery and returns immutable originals.
// Neither application nor completion is inferred from successful preparation.
func (s *Store) PrepareWindow(ctx context.Context, limits WindowLimits, held []Generation, observe GenerationObserver) ([]PreparedEntry, error) {
	if limits.MaxEntries <= 0 || limits.MaxBytes <= 0 {
		return nil, fmt.Errorf("ingress WAL positive window limits required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.prepared == nil {
		s.prepared = make(map[Generation]PreparedOutcome)
	}
	excluded := make(map[Generation]bool, len(held))
	for _, g := range held {
		if g.owner != s {
			s.mu.Unlock()
			return nil, errPreparedToken
		}
		if _, ok := s.prepared[g]; !ok {
			s.mu.Unlock()
			return nil, errPreparedToken
		}
		excluded[g] = true
	}
	if err := s.cleanupOrphansLocked(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	completed := make([]completedSnapshot, 0, len(s.completed))
	for id, marker := range s.completed {
		completed = append(completed, completedSnapshot{id: id, marker: marker})
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].marker.sequence < completed[j].marker.sequence })
	s.mu.Unlock()
	for _, snapshot := range completed {
		notify, err := s.cleanupCompletedSnapshot(ctx, snapshot)
		if err != nil {
			return nil, err
		}
		if notify {
			g := Generation{owner: s, id: snapshot.id, sequence: snapshot.marker.entrySequence}
			s.mu.Lock()
			if _, ok := s.prepared[g]; ok {
				s.prepared[g] = PreparedRetired
			}
			s.mu.Unlock()
			if observe != nil {
				observe(g)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.revalidateDirectory(); err != nil {
		return nil, err
	}
	entries := make([]pendingEntry, 0, len(s.pending))
	ids := make(map[string]string, len(s.pending))
	for id, entry := range s.pending {
		g := Generation{owner: s, id: id, sequence: entry.sequence}
		if _, done := s.completed[id]; entry.durable && !done && !excluded[g] {
			entries = append(entries, entry)
			ids[entry.name] = id
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].sequence < entries[j].sequence })
	limit := min(limits.MaxEntries, 64, s.opts.MaxEntries)
	bytesLimit := min(limits.MaxBytes, s.opts.MaxBytes)
	var bytes int64
	newlyRegistered := 0
	result := make([]PreparedEntry, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(result) >= limit {
			break
		}
		g := Generation{owner: s, id: ids[entry.name], sequence: entry.sequence}
		if entry.size < 0 {
			return nil, newCorruptError("negative prepared size")
		}
		if entry.size > bytesLimit-bytes {
			// Skipped entries never consume a new-registration slot.
			continue
		}
		_, registered := s.prepared[g]
		if !registered && len(s.prepared)+newlyRegistered >= min(64, s.opts.MaxEntries) {
			break
		}
		data, err := s.readBounded(entry.name)
		if err != nil {
			return nil, err
		}
		record, err := decodeRecord(data)
		if err != nil {
			return nil, err
		}
		if record.Envelope.ID != g.id || record.Sequence != g.sequence || int64(len(data)) != entry.size {
			return nil, newCorruptError("prepared entry identity mismatch")
		}
		result = append(result, PreparedEntry{Generation: g, Envelope: record.Envelope, EncodedBytes: entry.size})
		bytes += entry.size
		if !registered {
			newlyRegistered++
		}
	}
	for _, entry := range result {
		s.prepared[entry.Generation] = PreparedPending
	}
	return result, nil
}

// CommitPrepared is the only prepared completion seam. The caller must first
// establish delivery eligibility; this method proves storage retirement only.
func (s *Store) CommitPrepared(ctx context.Context, g Generation) (PreparedOutcome, error) {
	if err := ctx.Err(); err != nil {
		return PreparedPending, err
	}
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return PreparedPending, ErrClosed
	}
	outcome, ok := s.prepared[g]
	if g.owner != s || !ok {
		s.mu.Unlock()
		return PreparedPending, errPreparedToken
	}
	if outcome == PreparedRetired {
		s.mu.Unlock()
		return PreparedRetired, nil
	}
	s.mu.Unlock()
	if _, err := s.commitGeneration(ctx, g.id, g.sequence); err != nil {
		return PreparedPending, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A previous attempt may have removed entry+marker durably then failed the
	// final path validation. Reconcile the directory, not just volatile metadata.
	if err := s.revalidateDirectory(); err != nil {
		return PreparedPending, err
	}
	names, err := s.ops.readDir(s.dirFile)
	if err != nil {
		return PreparedPending, err
	}
	for _, name := range names {
		if name.Name() == entryName(g.sequence, g.id) {
			return PreparedPending, nil
		}
		if strings.HasPrefix(name.Name(), completionPrefix) {
			_, id, valid := parseMarkerName(name.Name())
			if !valid {
				return PreparedPending, newCorruptError("malformed completion filename")
			}
			if id != g.id {
				continue
			}
			_, _, entrySequence, err := s.readMarker(name.Name())
			if err != nil {
				return PreparedPending, err
			}
			if entrySequence == g.sequence {
				return PreparedPending, nil
			}
		}
	}
	s.prepared[g] = PreparedRetired
	return PreparedRetired, nil
}

// ReleasePrepared invalidates only an already terminal registered token.
func (s *Store) ReleasePrepared(g Generation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.owner != s {
		return errPreparedToken
	}
	if outcome, ok := s.prepared[g]; !ok || outcome != PreparedRetired {
		return errPreparedToken
	}
	delete(s.prepared, g)
	return nil
}
