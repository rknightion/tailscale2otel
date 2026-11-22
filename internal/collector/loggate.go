package collector

import "time"

// StateLogGate remembers the last emitted signature for each current entity.
// It is not safe for concurrent use; the owning collector serializes Collect.
type StateLogGate[K comparable] struct {
	states map[K]stateLogEntry
}

type stateLogEntry struct {
	signature string
	emitted   time.Time
}

// NewStateLogGate returns an empty in-memory snapshot-log gate.
func NewStateLogGate[K comparable]() *StateLogGate[K] {
	return &StateLogGate[K]{states: make(map[K]stateLogEntry)}
}

// Due records a first observation, changed signature, or daily reminder.
func (g *StateLogGate[K]) Due(id K, signature string, now time.Time) bool {
	state, ok := g.states[id]
	if !ok || state.signature != signature || now.Sub(state.emitted) >= 24*time.Hour {
		g.states[id] = stateLogEntry{signature: signature, emitted: now}
		return true
	}
	return false
}

// Retain forgets entities absent from a successful observation.
func (g *StateLogGate[K]) Retain(current map[K]struct{}) {
	for id := range g.states {
		if _, ok := current[id]; !ok {
			delete(g.states, id)
		}
	}
}
