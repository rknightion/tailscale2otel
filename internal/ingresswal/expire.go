package ingresswal

import (
	"context"
	"sort"
	"time"
)

// ExpiredEntry is the payload-free route of one entry retired by ExpireBefore,
// for loss accounting. It carries no body.
type ExpiredEntry struct {
	Tailnet      string
	Source       string
	Signal       string
	Accepted     time.Time
	EncodedBytes int64
}

// ExpireBefore retires, oldest first, the pending entries accepted before
// cutoff, through the same durable completion path as a delivered entry, so
// they never replay again. It stops at the first entry accepted at or after
// cutoff: entries are appended in acceptance order, so everything after it is
// newer. Entries already registered in this process's prepared window, and
// entries already completed, are never touched; nor is an entry for which
// eligible (when non-nil) reports false, so a route the caller cannot account
// for keeps its existing fail-closed replay behavior. A zero cutoff retires
// nothing.
//
// This is deliberate data loss for work too old to be worth replaying (the
// WAL is a short-horizon buffer ahead of delivery, not a historical store);
// callers must account for every returned entry.
func (s *Store) ExpireBefore(
	ctx context.Context,
	cutoff time.Time,
	eligible func(tailnet, source, signal string) bool,
) ([]ExpiredEntry, error) {
	if cutoff.IsZero() {
		return nil, nil
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
	held := make(map[string]bool, len(s.prepared))
	for g := range s.prepared {
		held[g.id] = true
	}
	type candidate struct {
		id    string
		entry pendingEntry
	}
	candidates := make([]candidate, 0, len(s.pending))
	for id, entry := range s.pending {
		if _, done := s.completed[id]; done || held[id] {
			continue
		}
		candidates = append(candidates, candidate{id: id, entry: entry})
	}
	s.mu.Unlock()
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].entry.sequence < candidates[j].entry.sequence })

	var expired []ExpiredEntry
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return expired, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return expired, ErrClosed
		}
		data, err := s.readBounded(c.entry.name)
		s.mu.Unlock()
		if err != nil {
			return expired, err
		}
		record, err := decodeRecord(data)
		if err != nil {
			return expired, err
		}
		if record.Envelope.ID != c.id || record.Sequence != c.entry.sequence {
			return expired, newCorruptError("expiry entry identity mismatch")
		}
		if !record.Envelope.Accepted.Before(cutoff) {
			break
		}
		if eligible != nil && !eligible(record.Envelope.Tailnet, record.Envelope.Source, record.Envelope.Signal) {
			continue
		}
		if _, err := s.commitGeneration(ctx, c.id, c.entry.sequence); err != nil {
			return expired, err
		}
		expired = append(expired, ExpiredEntry{
			Tailnet:      record.Envelope.Tailnet,
			Source:       record.Envelope.Source,
			Signal:       record.Envelope.Signal,
			Accepted:     record.Envelope.Accepted,
			EncodedBytes: int64(len(data)),
		})
	}
	return expired, nil
}
