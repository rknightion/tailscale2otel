package stream_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/audit"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/stream"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// benchGzip gzip-compresses b. A copy of the gzipBytes helper in
// stream_test.go, which takes a *testing.T and so cannot be called from a
// *testing.B — inlined here rather than changing that helper's signature.
func benchGzip(b *testing.B, body []byte) []byte {
	b.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		b.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		b.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// BenchmarkStreamHandle_HECDecode drives Server.handle (via its exported
// Handler()) through httptest with the shared hecFlowBody fixture (declared in
// stream_test.go), once uncompressed and once gzip-compressed, so the
// decompression overhead on the ingest hot path is visible separately from
// decode+processing.
func BenchmarkStreamHandle_HECDecode(b *testing.B) {
	rec := telemetrytest.New()
	cache := enrich.NewDeviceCache()
	flowProc := flowlog.NewProcessor(cache, flowlog.Options{NodeDims: true})
	auditProc := audit.NewProcessor()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := stream.New(stream.Options{Token: testToken}, flowProc, auditProc, rec.Emitter(), logger)
	h := s.Handler()

	plainBody := []byte(hecFlowBody)
	gzipBody := benchGzip(b, plainBody)

	b.Run("plain", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			req := httptest.NewRequest(http.MethodPost, "/services/collector/event", bytes.NewReader(plainBody))
			req.Header.Set("Authorization", "Splunk "+testToken)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				b.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
		}
	})

	b.Run("gzip", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			req := httptest.NewRequest(http.MethodPost, "/services/collector/event", bytes.NewReader(gzipBody))
			req.Header.Set("Authorization", "Splunk "+testToken)
			req.Header.Set("Content-Encoding", "gzip")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				b.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
		}
	})
}
