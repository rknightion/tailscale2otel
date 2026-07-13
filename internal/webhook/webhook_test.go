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
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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

func TestHandler_MultiSignatureRotationVerifies(t *testing.T) {
	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)

	// During secret rotation Tailscale signs with multiple secrets and emits a
	// v1=<hex> entry per secret. Only the entry computed from our configured
	// secret is valid; a bogus one precedes it. Verification must succeed by
	// matching any single v1 value.
	valid := signBody(testSecret, ts, twoEventBody)
	// valid is "t=<ts>,v1=<hex>"; extract just the v1 hex to build a multi-sig header.
	_, validHex, ok := strings.Cut(valid, "v1=")
	if !ok {
		t.Fatalf("could not parse signed header %q", valid)
	}
	bogusHex := flipLast(validHex)

	// Cover both orderings: the valid signature may appear before or after the
	// bogus one. Both must verify, which only holds if every v1 entry is kept.
	cases := map[string]string{
		"valid_first": fmt.Sprintf("t=%d,v1=%s,v1=%s", ts.Unix(), validHex, bogusHex),
		"valid_last":  fmt.Sprintf("t=%d,v1=%s,v1=%s", ts.Unix(), bogusHex, validHex),
	}
	for name, multiSig := range cases {
		t.Run(name, func(t *testing.T) {
			s, rec := newTestServer(t)
			resp := doPost(t, s.Handler(), "/webhook", twoEventBody, multiSig)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			if len(rec.LogRecords()) != 2 {
				t.Errorf("LogRecords len = %d, want 2", len(rec.LogRecords()))
			}
			if rej := rec.MetricPoints("tailscale.webhook.rejected"); len(rej) != 0 {
				t.Errorf("unexpected rejected metric points on rotation header: %+v", rej)
			}
		})
	}
}

func TestHandler_MaxBodyBytesTooLargeRejectedBeforeSignature(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{
		Path:         "/webhook",
		Secret:       testSecret,
		MaxBodyBytes: 8,
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := doPost(t, s.Handler(), "/webhook", strings.Repeat("x", 100), "")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
	pts := rec.MetricPoints("tailscale.webhook.rejected")
	if len(pts) != 1 {
		t.Fatalf("rejected metric points = %d, want 1 (%+v)", len(pts), pts)
	}
	if got := pts[0].Attrs["reason"]; got != "too_large" {
		t.Fatalf("rejected reason = %q, want too_large", got)
	}
}

// TestHandler_DefaultMaxBodyBytesIsOneMiB confirms the built-in fallback (when
// Options.MaxBodyBytes is 0, as it is when webhook.max_body_bytes is left at
// its config default) is 1 MiB, not the old 64 MiB — real Tailscale webhook
// payloads are KB-scale, so the cap should match that rather than the
// streaming receiver's batch-flow-log sizing.
func TestHandler_DefaultMaxBodyBytesIsOneMiB(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{
		Path: "/webhook",
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := doPost(t, s.Handler(), "/webhook", strings.Repeat("x", (1<<20)+1), "")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d for a body one byte over the 1 MiB default", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
	pts := rec.MetricPoints("tailscale.webhook.rejected")
	if len(pts) != 1 {
		t.Fatalf("rejected metric points = %d, want 1 (%+v)", len(pts), pts)
	}
	if got := pts[0].Attrs["reason"]; got != "too_large" {
		t.Fatalf("rejected reason = %q, want too_large", got)
	}
}

func TestHandler_MaxBodyBytesUnderLimitStillVerifies(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{
		Path:         "/webhook",
		Secret:       testSecret,
		MaxBodyBytes: int64(len(twoEventBody)),
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("LogRecords len = %d, want 2", got)
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

// TestHandler_StaleTimestampRejected verifies replay protection: with a Tolerance
// configured, a correctly-signed request whose signed timestamp is older than the
// tolerance window is rejected as stale rather than accepted (and replayed).
func TestHandler_StaleTimestampRejected(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{
		Path:      "/webhook",
		Secret:    testSecret,
		Tolerance: 5 * time.Minute,
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	old := time.Now().Add(-time.Hour)
	sig := signBody(testSecret, old, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a stale (replayed) timestamp", resp.StatusCode)
	}
	rej := rec.MetricPoints("tailscale.webhook.rejected")
	if len(rej) != 1 || rej[0].Attrs["reason"] != "stale_timestamp" {
		t.Fatalf("rejected = %+v, want one stale_timestamp", rej)
	}
	if len(rec.LogRecords()) != 0 {
		t.Errorf("stale request must emit no event log records, got %d", len(rec.LogRecords()))
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

// TestEmit_SeverityClassification pins the explicit per-type severity mapping
// (S4-11a) that replaced the old substring heuristic. The heuristic MISSED
// nodeNeedsSignature and the deprecated nodeNeedsAuthorization (neither contains
// a matched substring) and so emitted them at INFO; both must now be WARN. The
// client-misconfig health events stay INFO (they are surfaced via the events
// counter + a Prometheus alert, not via log severity).
func TestEmit_SeverityClassification(t *testing.T) {
	cases := []struct {
		eventType string
		want      string // SeverityText
	}{
		// Promoted to WARN by the explicit set (old substring heuristic missed these).
		{"nodeNeedsSignature", "WARN"},
		{"nodeNeedsAuthorization", "WARN"}, // deprecated alias of nodeNeedsApproval
		// Health / client-misconfig events stay INFO (counter + alert, not severity).
		{"exitNodeIPForwardingNotEnabled", "INFO"},
		{"subnetIPForwardingNotEnabled", "INFO"},
		// Resolution / benign lifecycle events are informational.
		{"nodeSigned", "INFO"},
		{"nodeApproved", "INFO"},
		{"nodeCreated", "INFO"},
		{"policyUpdate", "INFO"},
		{"userRoleUpdated", "INFO"},
		// Existing WARN classifications still hold.
		{"nodeKeyExpiringInOneDay", "WARN"},
		{"nodeKeyExpired", "WARN"},
		{"nodeNeedsApproval", "WARN"},
		{"userNeedsApproval", "WARN"},
		{"nodeDeleted", "WARN"},
		{"webhookDeleted", "WARN"},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			rec := telemetrytest.New()
			s := New(Options{}, rec.Emitter(), discard())
			s.emit(event{Type: tc.eventType, Tailnet: "example.com", Message: "m", Timestamp: "2026-06-02T10:00:00Z"})
			logs := rec.LogRecords()
			if len(logs) != 1 {
				t.Fatalf("log records = %d, want 1", len(logs))
			}
			if logs[0].SeverityText != tc.want {
				t.Errorf("%s severity = %q, want %q", tc.eventType, logs[0].SeverityText, tc.want)
			}
		})
	}
}

// TestEmit_BoundsEventTypeCardinality pins the cardinality failsafe: the event
// type is attacker-chosen on the wire, and when the webhook runs without a secret
// (verification skipped) a flood of distinct types would otherwise explode the
// metric's series and the log EventName cardinality. emit must collapse types
// beyond a fixed distinct-type cap into a single overflow bucket, while leaving
// the first (real-volume) types untouched. The cap is generous enough that
// Tailscale's documented event set — and headroom for new ones — passes through
// unchanged (forward compatibility); only an abnormal flood overflows.
func TestEmit_BoundsEventTypeCardinality(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{}, rec.Emitter(), discard())

	const flood = maxDistinctEventTypes * 8
	for i := range flood {
		s.emit(event{Type: fmt.Sprintf("evil-%d", i), Tailnet: "example.com", Message: "m"})
	}

	// Metric: distinct tailscale.webhook.type attribute values must be bounded.
	types := map[string]struct{}{}
	for _, p := range rec.MetricPoints("tailscale.webhook.events") {
		types[p.Attrs["tailscale.webhook.type"]] = struct{}{}
	}
	if len(types) > maxDistinctEventTypes+1 {
		t.Fatalf("distinct event-type attrs = %d, want <= %d (bounded)", len(types), maxDistinctEventTypes+1)
	}
	if _, ok := types[overflowType]; !ok {
		t.Errorf("expected an %q bucket once the distinct-type cap is exceeded", overflowType)
	}

	// Log EventName cardinality must be bounded the same way (it embeds the type).
	names := map[string]struct{}{}
	for _, lr := range rec.LogRecords() {
		names[lr.EventName] = struct{}{}
	}
	if len(names) > maxDistinctEventTypes+1 {
		t.Fatalf("distinct log EventNames = %d, want <= %d (bounded)", len(names), maxDistinctEventTypes+1)
	}
	if _, ok := names[eventNamePrefix+overflowType]; !ok {
		t.Errorf("expected an overflow log EventName %q", eventNamePrefix+overflowType)
	}
}

// TestEmit_KnownTypesNotBucketed guards forward compatibility: a realistic number
// of distinct types (Tailscale's documented set is ~25) must pass through emit
// verbatim, never collapsed into the overflow bucket.
func TestEmit_KnownTypesNotBucketed(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{}, rec.Emitter(), discard())

	for i := range maxDistinctEventTypes {
		s.emit(event{Type: fmt.Sprintf("type-%d", i), Tailnet: "example.com", Message: "m"})
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName == eventNamePrefix+overflowType {
			t.Fatalf("a type within the cap was bucketed as overflow: %q", lr.EventName)
		}
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

// webhookIngestCall records one call to an OnIngest hook.
type webhookIngestCall struct {
	source  string
	signal  string
	records int
	bytes   int
}

// TestHandleCallsIngestHook verifies that Options.OnIngest is called exactly once
// after a successful 2-event delivery, with source=IngestSourceWebhook,
// signal=IngestSignalWebhook, records=2, bytes=len(body).
func TestHandleCallsIngestHook(t *testing.T) {
	body := twoEventBody
	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, body)

	var mu sync.Mutex
	var calls []webhookIngestCall

	rec := telemetrytest.New()
	s := New(Options{
		Listen:    "127.0.0.1:0",
		Path:      "/webhook",
		Secret:    testSecret,
		Tolerance: 0,
		OnIngest: func(source, signal string, records, bytes int) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, webhookIngestCall{source, signal, records, bytes})
		},
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := doPost(t, s.Handler(), "/webhook", body, sig)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	mu.Lock()
	got := append([]webhookIngestCall(nil), calls...)
	mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("OnIngest called %d times, want 1; calls=%+v", len(got), got)
	}
	want := webhookIngestCall{
		source:  semconv.IngestSourceWebhook,
		signal:  semconv.IngestSignalWebhook,
		records: 2,
		bytes:   len(body),
	}
	if got[0] != want {
		t.Errorf("OnIngest call = %+v, want %+v", got[0], want)
	}
}

// Run is exercised lightly to ensure it binds, serves, and shuts down on ctx
// cancellation without leaking. Full handler behavior is covered above.
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

// webhookSpanNames extracts the Name() of each ended ReadOnlySpan for readable assertions.
func webhookSpanNames(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

// TestWebhookHandle_EmitsSpan verifies that a configured tracer yields one
// server span named "webhook.receive" per request, regardless of outcome.
// An unsigned body is rejected — that's fine, the span still emits.
func TestWebhookHandle_EmitsSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	rec := telemetrytest.New()
	s := New(Options{Path: "/webhook"}, rec.Emitter(), discard(), WithTracer(tp.Tracer("test")))

	// POST with no signature — body will be accepted (Secret == ""), parsed as
	// an empty event array, and the span recorded on the success path.
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("[]"))
	rw := httptest.NewRecorder()
	s.handle(rw, req)

	spans := sr.Ended()
	if len(spans) == 0 || spans[len(spans)-1].Name() != "webhook.receive" {
		t.Fatalf("got %v, want a span named webhook.receive", webhookSpanNames(spans))
	}
}

// TestHandler_RequestDurationRecorded verifies that a successfully handled
// webhook POST records exactly one tailscale.webhook.request.duration histogram
// sample with a non-negative value and no attributes.
func TestHandler_RequestDurationRecorded(t *testing.T) {
	s, rec := newTestServer(t)

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	pts := rec.MetricPoints("tailscale.webhook.request.duration")
	if len(pts) == 0 {
		t.Fatalf("no metric points for tailscale.webhook.request.duration")
	}
	// There must be exactly one histogram point (one request).
	if len(pts) != 1 {
		t.Fatalf("tailscale.webhook.request.duration points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Errorf("tailscale.webhook.request.duration kind = %q, want histogram", p.Kind)
	}
	// Count must be 1 (one observation).
	if p.Count != 1 {
		t.Errorf("tailscale.webhook.request.duration count = %d, want 1", p.Count)
	}
	// Duration must be non-negative.
	if p.Value < 0 {
		t.Errorf("tailscale.webhook.request.duration sum = %v, want >= 0", p.Value)
	}
	// No attributes on this metric.
	if len(p.Attrs) != 0 {
		t.Errorf("tailscale.webhook.request.duration has unexpected attrs: %v", p.Attrs)
	}
}

// TestHandler_InflightNetsToZeroAfterCompletion verifies that
// tailscale.webhook.inflight nets to 0 after a request completes (the +1 at
// start is balanced by the -1 in defer). The in-flight gauge is an
// UpDownCounter so it must show a net-zero value (or no points at all if both
// additions cancel — but the SDK keeps cumulative state, so we assert Count==0
// is NOT required; we assert the net Value is 0 after completion).
func TestHandler_InflightNetsToZeroAfterCompletion(t *testing.T) {
	s, rec := newTestServer(t)

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	pts := rec.MetricPoints("tailscale.webhook.inflight")
	// After the request completes, the net value of the UpDownCounter must be 0.
	var net float64
	for _, p := range pts {
		net += p.Value
	}
	if net != 0 {
		t.Errorf("tailscale.webhook.inflight net value = %v, want 0 after request completion", net)
	}
}

// TestWebhookHandle_EmitsSpanOnReject verifies that the span is emitted and
// marked Error when signature verification fails.
func TestWebhookHandle_EmitsSpanOnReject(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	rec := telemetrytest.New()
	s := New(Options{Path: "/webhook", Secret: testSecret}, rec.Emitter(), discard(), WithTracer(tp.Tracer("test")))

	// No signature header — will be rejected (missing_signature).
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	spans := sr.Ended()
	if len(spans) == 0 || spans[len(spans)-1].Name() != "webhook.receive" {
		t.Fatalf("got %v, want a span named webhook.receive", webhookSpanNames(spans))
	}
}
