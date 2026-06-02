package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

const testSecret = "tskey-webhook-test-secret"

// signBody returns the value for the Tailscale-Webhook-Signature header for the
// given body and timestamp, using the verified Tailscale scheme:
// signed string = <t.Unix()> + "." + body; signature = hex(HMAC-SHA256(secret, signedString)).
func signBody(secret string, ts time.Time, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(fmt.Append(nil, ts.Unix()))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), sig)
}

// newTestServer builds a Server wired to a fresh Recorder, with tolerance
// disabled (0) so signing timestamps never get rejected as stale.
func newTestServer(t *testing.T) (*Server, *telemetrytest.Recorder) {
	t.Helper()
	rec := telemetrytest.New()
	s := New(Options{
		Listen:    "127.0.0.1:0",
		Path:      "/webhook",
		Secret:    testSecret,
		Tolerance: 0,
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, rec
}

// doPost sends a POST to path with the given body and optional signature header.
func doPost(t *testing.T, h http.Handler, path, body, sig string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if sig != "" {
		req.Header.Set("Tailscale-Webhook-Signature", sig)
	}
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw.Result()
}

// twoEventBody is a JSON array of two events: a benign nodeCreated and a
// nodeKeyExpiringInOneDay (which must map to WARN severity).
const twoEventBody = `[` +
	`{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"Node foo created","data":{"nodeID":"n1"}},` +
	`{"timestamp":"2026-06-02T10:05:00Z","version":1,"type":"nodeKeyExpiringInOneDay","tailnet":"example.com","message":"Key for bar expiring","data":{"nodeID":"n2"}}` +
	`]`

func TestHandler_ValidSignatureEmitsEventsAndCounter(t *testing.T) {
	s, rec := newTestServer(t)

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	logs := rec.LogRecords()
	if len(logs) != 2 {
		t.Fatalf("LogRecords len = %d, want 2", len(logs))
	}

	byName := map[string]telemetrytest.LogRecord{}
	for _, lr := range logs {
		byName[lr.EventName] = lr
	}

	created, ok := byName["tailscale.webhook.nodeCreated"]
	if !ok {
		t.Fatalf("missing log record with event.name=tailscale.webhook.nodeCreated; got names %v", logNames(logs))
	}
	if created.Body != "Node foo created" {
		t.Errorf("nodeCreated body = %q, want %q", created.Body, "Node foo created")
	}
	if created.SeverityText != "INFO" {
		t.Errorf("nodeCreated severity = %q, want INFO", created.SeverityText)
	}
	if got := created.Attrs["tailscale.webhook.type"]; got != "nodeCreated" {
		t.Errorf("nodeCreated attr tailscale.webhook.type = %q, want nodeCreated", got)
	}
	if got := created.Attrs["tailscale.tailnet"]; got != "example.com" {
		t.Errorf("nodeCreated attr tailscale.tailnet = %q, want example.com", got)
	}

	expiring, ok := byName["tailscale.webhook.nodeKeyExpiringInOneDay"]
	if !ok {
		t.Fatalf("missing log record with event.name=tailscale.webhook.nodeKeyExpiringInOneDay; got names %v", logNames(logs))
	}
	if expiring.SeverityText != "WARN" {
		t.Errorf("nodeKeyExpiringInOneDay severity = %q, want WARN", expiring.SeverityText)
	}

	pts := rec.MetricPoints("tailscale.webhook.events")
	if len(pts) == 0 {
		t.Fatalf("no metric points for tailscale.webhook.events")
	}
	var total float64
	for _, p := range pts {
		total += p.Value
		if _, ok := p.Attrs["tailscale.webhook.type"]; !ok {
			t.Errorf("metric point missing tailscale.webhook.type attr: %+v", p)
		}
	}
	if total != 2 {
		t.Errorf("tailscale.webhook.events total = %v, want 2", total)
	}

	// No rejection should have been recorded.
	if rej := rec.MetricPoints("tailscale.webhook.rejected"); len(rej) != 0 {
		t.Errorf("unexpected rejected metric points: %+v", rej)
	}
}

func TestHandler_TamperedSignatureRejected(t *testing.T) {
	s, rec := newTestServer(t)

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	// Tamper: flip the last hex character of the v1 signature.
	tampered := flipLast(sig)

	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, tampered)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	if len(rec.LogRecords()) != 0 {
		t.Errorf("expected no log records on tampered signature, got %d", len(rec.LogRecords()))
	}
	if pts := rec.MetricPoints("tailscale.webhook.events"); len(pts) != 0 {
		t.Errorf("expected no events counter on tampered signature, got %+v", pts)
	}

	rej := rec.MetricPoints("tailscale.webhook.rejected")
	if len(rej) == 0 {
		t.Fatalf("expected tailscale.webhook.rejected counter, got none")
	}
	var total float64
	for _, p := range rej {
		total += p.Value
		if _, ok := p.Attrs["reason"]; !ok {
			t.Errorf("rejected metric point missing reason attr: %+v", p)
		}
	}
	if total != 1 {
		t.Errorf("rejected total = %v, want 1", total)
	}
}

func TestHandler_MissingSignatureRejected(t *testing.T) {
	s, rec := newTestServer(t)

	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if len(rec.LogRecords()) != 0 {
		t.Errorf("expected no log records on missing signature, got %d", len(rec.LogRecords()))
	}
	rej := rec.MetricPoints("tailscale.webhook.rejected")
	if len(rej) == 0 {
		t.Fatalf("expected tailscale.webhook.rejected counter, got none")
	}
}

func TestHandler_RejectsNonPOST(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rw := httptest.NewRecorder()
	s.Handler().ServeHTTP(rw, req)
	if rw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", rw.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_NoSecretSkipsVerification(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{Path: "/webhook"}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// No signature header at all, but Secret == "" so verification is skipped.
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if len(rec.LogRecords()) != 2 {
		t.Errorf("LogRecords len = %d, want 2", len(rec.LogRecords()))
	}
}

func logNames(logs []telemetrytest.LogRecord) []string {
	out := make([]string, 0, len(logs))
	for _, lr := range logs {
		out = append(out, lr.EventName)
	}
	return out
}

func flipLast(s string) string {
	if s == "" {
		return s
	}
	last := s[len(s)-1]
	var repl byte = '0'
	if last == '0' {
		repl = '1'
	}
	return s[:len(s)-1] + string(repl)
}

// Run is exercised lightly to ensure it binds, serves, and shuts down on ctx
// cancellation without leaking. Full handler behaviour is covered above.
func TestRun_GracefulShutdown(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook"}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Give the listener a moment to bind, then cancel.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}
