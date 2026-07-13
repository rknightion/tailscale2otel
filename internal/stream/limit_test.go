package stream_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/stream"
)

const reasonTooLarge = "too_large"

// TestMaxBodyBytes_UncompressedTooLargeRejected confirms a body over the
// configured cap is rejected with 413 and a visible too_large rejection counter,
// and is never parsed/processed.
func TestMaxBodyBytes_UncompressedTooLargeRejected(t *testing.T) {
	s, rec := newServer(t, stream.Options{MaxBodyBytes: 16})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
		strings.NewReader(strings.Repeat("x", 100)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooLarge}); p.Value != 1 {
		t.Fatalf("rejected{reason=too_large} = %v, want 1", p.Value)
	}
	if pts := rec.MetricPoints(metricRecords); len(pts) != 0 {
		t.Fatalf("records emitted for an oversized body: %+v", pts)
	}
}

// TestMaxBodyBytes_GzipBombRejected confirms the cap bounds the DECOMPRESSED
// size: a tiny gzip that inflates past the cap is rejected, so a zip bomb cannot
// OOM the receiver.
func TestMaxBodyBytes_GzipBombRejected(t *testing.T) {
	const cap = 64 * 1024
	s, rec := newServer(t, stream.Options{MaxBodyBytes: cap})

	gz := gzipBytes(t, bytes.Repeat([]byte("a"), 1<<20)) // 1 MiB -> ~1 KiB gzip
	if len(gz) >= cap {
		t.Fatalf("precondition: compressed size %d should be well under the %d cap", len(gz), cap)
	}
	h := http.Header{}
	h.Set("Content-Encoding", "gzip")

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", h, bytes.NewReader(gz))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (gzip bomb)", w.Code)
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooLarge}); p.Value != 1 {
		t.Fatalf("rejected{reason=too_large} = %v, want 1", p.Value)
	}
}

// TestMaxBodyBytes_UnderLimitNotTooLarge confirms a body under the cap is read
// normally; an unparsable one fails as "unparsable", not "too_large".
func TestMaxBodyBytes_UnderLimitNotTooLarge(t *testing.T) {
	s, rec := newServer(t, stream.Options{MaxBodyBytes: 100})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
		strings.NewReader("not json"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unparsable, not too_large)", w.Code)
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonUnparsable}); p.Value != 1 {
		t.Fatalf("rejected{reason=unparsable} = %v, want 1", p.Value)
	}
}
