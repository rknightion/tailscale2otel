package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
)

// supportBundleLogRing stores complete JSON log records in a fixed-size ring.
// The JSON handler that writes it resolves slog.LogValuer values first, so it
// retains the same config.Secret redaction used by the live logger.
type supportBundleLogRing struct {
	mu            sync.Mutex
	entries       []string
	next          int
	size          int
	pending       []byte
	maxEntryBytes int
}

func newSupportBundleLogRing(capacity, maxEntryBytes int) *supportBundleLogRing {
	return &supportBundleLogRing{entries: make([]string, capacity), maxEntryBytes: maxEntryBytes}
}

func (r *supportBundleLogRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, p...)
	for {
		i := bytes.IndexByte(r.pending, '\n')
		if i < 0 {
			break
		}
		line := string(r.pending[:i])
		r.pending = r.pending[i+1:]
		if line == "" {
			continue
		}
		if r.maxEntryBytes > 0 && len(line) > r.maxEntryBytes {
			line = boundedSupportBundleLogLine(len(line), r.maxEntryBytes)
		}
		r.entries[r.next] = line
		r.next = (r.next + 1) % len(r.entries)
		if r.size < len(r.entries) {
			r.size++
		}
	}
	return len(p), nil
}

func boundedSupportBundleLogLine(originalBytes, limit int) string {
	line, _ := json.Marshal(struct {
		Truncated     bool `json:"truncated"`
		OriginalBytes int  `json:"original_bytes"`
	}{Truncated: true, OriginalBytes: originalBytes})
	if len(line) <= limit {
		return string(line)
	}
	return `{"truncated":true}`
}

func (r *supportBundleLogRing) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil
	}
	out := make([]string, r.size)
	start := (r.next - r.size + len(r.entries)) % len(r.entries)
	for i := range out {
		out[i] = r.entries[(start+i)%len(r.entries)]
	}
	return out
}

type supportBundleLogHandler struct {
	base    slog.Handler
	capture slog.Handler
	ring    *supportBundleLogRing
}

func (h *supportBundleLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *supportBundleLogHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.base.Handle(ctx, record)
	// Capture errors never break the application's live log path. The in-memory
	// writer currently cannot fail, but keeping this boundary explicit prevents
	// a future support feature from changing logging behavior.
	_ = h.capture.Handle(ctx, record)
	return err
}

func (h *supportBundleLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &supportBundleLogHandler{
		base: h.base.WithAttrs(attrs), capture: h.capture.WithAttrs(attrs), ring: h.ring,
	}
}

func (h *supportBundleLogHandler) WithGroup(name string) slog.Handler {
	return &supportBundleLogHandler{
		base: h.base.WithGroup(name), capture: h.capture.WithGroup(name), ring: h.ring,
	}
}

func withSupportBundleLogTail(logger *slog.Logger, capacity, maxEntryBytes int) *slog.Logger {
	if capacity <= 0 {
		return logger
	}
	ring := newSupportBundleLogRing(capacity, maxEntryBytes)
	return slog.New(&supportBundleLogHandler{
		base: logger.Handler(), capture: slog.NewJSONHandler(ring, nil), ring: ring,
	})
}

func supportBundleLogTail(logger *slog.Logger) []string {
	if logger == nil {
		return nil
	}
	h, ok := logger.Handler().(*supportBundleLogHandler)
	if !ok {
		return nil
	}
	return h.ring.snapshot()
}
