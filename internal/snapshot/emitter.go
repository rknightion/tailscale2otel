// Package snapshot emits change-driven, heartbeat-refreshed state snapshots
// with one uniform OpenTelemetry log-record shape.
package snapshot

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Kind is the bounded source family attached to every snapshot record.
type Kind string

const (
	KindPolicy              Kind = "policy"
	KindDNS                 Kind = "dns"
	KindSettings            Kind = "settings"
	KindWebhooks            Kind = "webhooks"
	KindPostureIntegrations Kind = "posture_integrations"
	KindDevice              Kind = "device"
)

// Config freezes the behavior and optional persisted baseline of an Emitter.
type Config struct {
	Emitter      telemetry.Emitter
	EventName    string
	Kind         Kind
	Heartbeat    time.Duration
	MaxBodyBytes int

	InitialRevision string
	InitialEmission time.Time
}

// State is the small baseline callers may persist beside their own checkpoint.
type State struct {
	Revision string
	Emitted  time.Time
}

// Emitter decides whether a logical snapshot should emit, chunks it, and adds
// the canonical attributes. It is safe for concurrent use.
type Emitter struct {
	mu sync.Mutex

	emitter      telemetry.Emitter
	eventName    string
	kind         Kind
	heartbeat    time.Duration
	maxBodyBytes int
	emissionSeed string
	emissionSeq  uint64
	state        State
}

// New returns a snapshot emitter seeded with an optional persisted baseline.
func New(cfg Config) (*Emitter, error) {
	if safe := SafeBodyBytes(cfg.MaxBodyBytes); safe < utf8.UTFMax {
		return nil, fmt.Errorf("snapshot max body bytes %d leaves a %d-byte chunk budget; need at least %d for lossless UTF-8", cfg.MaxBodyBytes, safe, utf8.UTFMax)
	}
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, fmt.Errorf("create snapshot emission seed: %w", err)
	}
	return &Emitter{
		emitter:      cfg.Emitter,
		eventName:    cfg.EventName,
		kind:         cfg.Kind,
		heartbeat:    cfg.Heartbeat,
		maxBodyBytes: cfg.MaxBodyBytes,
		emissionSeed: hex.EncodeToString(seed[:]),
		state: State{
			Revision: cfg.InitialRevision,
			Emitted:  cfg.InitialEmission,
		},
	}, nil
}

// SafeBodyBytes returns the largest chunk Observe will emit for a configured
// per-record body limit.
func SafeBodyBytes(limit int) int { return telemetry.SafeLogBodyBytes(limit) }

// Observe emits a change snapshot when revision differs from the baseline, or
// a heartbeat when the unchanged baseline has gone stale. An empty revision is
// replaced by the full SHA-256 content hash. It reports whether records emitted.
func (e *Emitter) Observe(at time.Time, revision, body string, attrs telemetry.Attrs) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if at.IsZero() {
		at = time.Now()
	}
	if revision == "" {
		sum := sha256.Sum256([]byte(body))
		revision = hex.EncodeToString(sum[:])
	}

	reason := ""
	if revision != e.state.Revision {
		reason = "change"
	} else if e.heartbeat > 0 && (e.state.Emitted.IsZero() || !at.Before(e.state.Emitted.Add(e.heartbeat))) {
		reason = "heartbeat"
	}
	if reason == "" {
		return false
	}

	chunks := splitUTF8(body, SafeBodyBytes(e.maxBodyBytes))
	e.emissionSeq++
	emissionID := fmt.Sprintf("%s-%d", e.emissionSeed, e.emissionSeq)
	for i, chunk := range chunks {
		recordAttrs := cloneAttrs(attrs)
		// Canonical attributes are written last so a caller cannot override the
		// query contract accidentally.
		recordAttrs["tailscale.snapshot.kind"] = string(e.kind)
		recordAttrs["tailscale.snapshot.reason"] = reason
		recordAttrs["tailscale.snapshot.revision"] = revision
		recordAttrs["tailscale.snapshot.emission_id"] = emissionID
		recordAttrs["tailscale.snapshot.bytes"] = int64(len(body))
		recordAttrs["tailscale.snapshot.seq"] = int64(i + 1)
		recordAttrs["tailscale.snapshot.total"] = int64(len(chunks))
		e.emitter.LogEvent(telemetry.Event{
			Name:      e.eventName,
			Body:      chunk,
			Severity:  telemetry.SeverityInfo,
			Timestamp: at,
			Attrs:     recordAttrs,
		})
	}
	e.state = State{Revision: revision, Emitted: at}
	return true
}

// State returns the current baseline for checkpoint-adjacent persistence.
func (e *Emitter) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func cloneAttrs(attrs telemetry.Attrs) telemetry.Attrs {
	out := make(telemetry.Attrs, len(attrs)+7)
	for key, value := range attrs {
		out[key] = value
	}
	return out
}

func splitUTF8(body string, limit int) []string {
	if limit <= 0 || len(body) <= limit {
		return []string{body}
	}
	chunks := make([]string, 0, (len(body)+limit-1)/limit)
	for start := 0; start < len(body); {
		end := start + limit
		if end >= len(body) {
			end = len(body)
		} else {
			for end > start && !utf8.RuneStart(body[end]) {
				end--
			}
		}
		if end == start {
			_, size := utf8.DecodeRuneInString(body[start:])
			end = start + size
		}
		chunks = append(chunks, body[start:end])
		start = end
	}
	return chunks
}
