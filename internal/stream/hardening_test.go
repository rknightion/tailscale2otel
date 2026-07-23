package stream_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/stream"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// Rejection reasons added by the hardening work (#209/#228/#229 + the
// fail-closed token gate). Declared here rather than in stream_test.go so the
// hardening surface is legible in one place.
const (
	reasonAuthRequired   = "auth_required"
	reasonTooManyRecords = "too_many_records"
	reasonOverloaded     = "overloaded"
)

// maxRecordsPerRequest mirrors the unexported constant of the same name in the
// package under test. These tests live outside the package (they drive the real
// HTTP handler), so the value is restated here; constants_test.go inside the
// package asserts the two agree, which is what keeps this honest.
const maxRecordsPerRequest = 500_000

// -----------------------------------------------------------------------------
// Fail-closed streaming token
// -----------------------------------------------------------------------------

// TestInsecureOpen_NetworkReachableWithoutTokenIsRefused pins the fail-closed
// gate: a receiver with NO token bound to an address other hosts can reach must
// refuse every POST with 403 rather than ingest unauthenticated data. The body
// has to name both remedies so an operator can act on it without reading source.
func TestInsecureOpen_NetworkReachableWithoutTokenIsRefused(t *testing.T) {
	for _, listen := range []string{":9099", "0.0.0.0:9099", "[::]:9099", "100.64.0.1:9099", "", "garbage"} {
		t.Run(listen, func(t *testing.T) {
			s, rec := newServer(t, stream.Options{Listen: listen})

			w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
				strings.NewReader(hecFlowBody))

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 for an untokened non-loopback bind; body=%q", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, "streaming.token") || !strings.Contains(body, "streaming.listen") {
				t.Fatalf("body = %q, want it to name both streaming.token and streaming.listen", body)
			}
			if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonAuthRequired}); p.Value != 1 {
				t.Fatalf("%s{reason=auth_required} = %v, want 1", metricRejected, p.Value)
			}
			if pts := rec.MetricPoints(metricRecords); len(pts) != 0 {
				t.Fatalf("records emitted despite the auth_required refusal: %+v", pts)
			}
		})
	}
}

// TestInsecureOpen_LoopbackWithoutTokenStillServes keeps the deliberate carve-out
// honest: a loopback bind is unreachable from another host, so an untokened
// receiver there is still allowed to ingest.
func TestInsecureOpen_LoopbackWithoutTokenStillServes(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:0", "[::1]:9099", "localhost:9099"} {
		t.Run(listen, func(t *testing.T) {
			s, rec := newServer(t, stream.Options{Listen: listen})

			w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
				strings.NewReader(hecFlowBody))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 on a loopback bind; body=%q", w.Code, w.Body.String())
			}
			if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
				t.Fatalf("MetricIO points = %d, want 2 (flow processed)", len(io))
			}
		})
	}
}

// TestInsecureOpen_TokenSetOnPublicBindServes confirms the gate keys on the
// MISSING token, not on the bind: a tokened receiver on a public address serves
// normally.
func TestInsecureOpen_TokenSetOnPublicBindServes(t *testing.T) {
	s, _ := newServer(t, stream.Options{Listen: "0.0.0.0:9099", Token: testToken})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(),
		strings.NewReader(hecFlowBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
}

// TestInsecureOpen_LogsErrorAtStartup asserts Run announces the misconfiguration
// loudly once at startup, so the refusal is not a silent mystery in production.
func TestInsecureOpen_LogsErrorAtStartup(t *testing.T) {
	var buf bytes.Buffer
	rec := telemetrytest.New()
	cache := enrich.NewDeviceCache()
	flowProc := flowlog.NewProcessor(cache, flowlog.Options{})
	auditProc := audit.NewProcessor()
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	// Run is canceled immediately; the ERROR must be logged before the bind
	// attempt resolves either way.
	s := stream.New(stream.Options{Listen: "0.0.0.0:0"}, flowProc, auditProc, rec.Emitter(), logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = s.Run(ctx)

	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Fatalf("log output = %q, want an ERROR for the untokened public bind", out)
	}
	if !strings.Contains(out, "streaming.token") {
		t.Fatalf("log output = %q, want it to name streaming.token", out)
	}
}

// -----------------------------------------------------------------------------
// #229 per-request record cap
// -----------------------------------------------------------------------------

// TestRecordCap_ConcatenatedObjectsRejected is the #229 control: a body of tiny
// concatenated objects amplifies bytes into record objects, so the byte cap alone
// does not bound memory. Beyond the record cap the whole request is refused with
// 413 and rejected{reason=too_many_records}, and nothing is emitted.
func TestRecordCap_ConcatenatedObjectsRejected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	body := strings.Repeat("{}", maxRecordsPerRequest+1)
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyRecords}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_records} = %v, want 1", metricRejected, p.Value)
	}
	if pts := rec.MetricPoints(metricRecords); len(pts) != 0 {
		t.Fatalf("records emitted for an over-cap body: %+v", pts)
	}
}

// TestRecordCap_NDJSONLinesRejected covers the second extraction path: the
// line-scan fallback used when the body does not start with valid JSON must be
// bounded too, otherwise the cap is trivially bypassed by prefixing garbage.
func TestRecordCap_NDJSONLinesRejected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	// A leading non-JSON line forces the stream decoder to yield nothing, so
	// extraction falls through to the line scan.
	var b strings.Builder
	b.WriteString("not json\n")
	for i := 0; i <= maxRecordsPerRequest; i++ {
		b.WriteString("{}\n")
	}
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(b.String()))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyRecords}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_records} = %v, want 1", metricRejected, p.Value)
	}
}

// TestRecordCap_LogsWrapperRejected covers the unwrap path: one top-level
// {"logs":[...]} wrapper can carry more records than the top-level value count,
// so the cap has to bound what unwrapping produces, not just what the decoder
// yields.
func TestRecordCap_LogsWrapperRejected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	var b strings.Builder
	b.WriteString(`{"logs":[`)
	for i := 0; i <= maxRecordsPerRequest; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("{}")
	}
	b.WriteString("]}")
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(b.String()))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyRecords}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_records} = %v, want 1", metricRejected, p.Value)
	}
}

// TestRecordCap_NormalBatchUnaffected guards against the cap regressing ordinary
// ingestion: a realistic multi-record batch is far under the cap and must still
// be accepted whole.
func TestRecordCap_NormalBatchUnaffected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
		strings.NewReader(realHECStreamBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	recs := rec.MetricPoints(metricRecords)
	if fp := findPoint(t, recs, map[string]string{attrType: typeFlow}); fp.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, fp.Value)
	}
	if ap := findPoint(t, recs, map[string]string{attrType: typeAudit}); ap.Value != 1 {
		t.Fatalf("%s{type=audit} = %v, want 1", metricRecords, ap.Value)
	}
}

// -----------------------------------------------------------------------------
// #228 bounded unwrap depth
// -----------------------------------------------------------------------------

// deepEnvelope wraps leaf in depth levels of {"event": ...}.
func deepEnvelope(depth int, leaf string) string {
	var b strings.Builder
	b.Grow(depth*9 + len(leaf) + depth)
	for range depth {
		b.WriteString(`{"event":`)
	}
	b.WriteString(leaf)
	for range depth {
		b.WriteString(`}`)
	}
	return b.String()
}

// TestUnwrapDepth_DeeplyNestedEnvelopeIsBoundedNotQuadratic is the #228 control.
// The leaf is ~1 MiB and the nesting ~4000 levels deep: under the old unwrapper
// (which re-scanned the full remaining bytes at EVERY level) this is ~8 GiB of
// JSON scanning for one request and the test would time out. With the depth cap
// only a handful of levels are ever decoded, so it completes immediately. There
// is deliberately NO wall-clock assertion here — the test either finishes or the
// package times out, which is the honest signal.
func TestUnwrapDepth_DeeplyNestedEnvelopeIsBoundedNotQuadratic(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	leaf := `{"pad":"` + strings.Repeat("x", 1<<20) + `"}`
	body := deepEnvelope(4000, leaf)

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	// Nothing survives the walk, so the request carries no records at all: the
	// receiver reports it unparsable rather than corrupt. What matters is that it
	// is bounded and that the batch was NOT flagged structurally corrupt.
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", w.Code, w.Body.String())
	}
	if pts := rec.MetricPoints(metricRejected); len(pts) == 0 {
		t.Fatalf("no rejection recorded for an over-deep envelope")
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonUnparsable}); p.Value != 1 {
		t.Fatalf("%s{reason=unparsable} = %v, want 1", metricRejected, p.Value)
	}
	for _, p := range rec.MetricPoints(metricRejected) {
		if p.Attrs[attrReason] == reasonMalformed {
			t.Fatalf("over-deep envelope flagged the batch corrupt; it must be a bounded DROP, not a corruption verdict")
		}
	}
}

// TestUnwrapDepth_OverDeepRecordIsDroppedNotCorrupt pins the drop semantics: an
// over-deep envelope alongside a valid record is a forward-compatible per-record
// SKIP (#67 unwrap_drop) — the batch still succeeds and the valid record is
// emitted.
func TestUnwrapDepth_OverDeepRecordIsDroppedNotCorrupt(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	body := deepEnvelope(50, `{"actor":{"id":"u1"},"action":"CREATE"}`) + "\n" + captureFlowRecord + "\n"
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (over-deep record is a skip, not a rejection); body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}); p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}
	if p := findPoint(t, rec.MetricPoints(metricSkipped), map[string]string{attrReason: reasonUnwrapDrop}); p.Value != 1 {
		t.Fatalf("%s{reason=unwrap_drop} = %v, want 1", metricSkipped, p.Value)
	}
}

// TestUnwrapDepth_DocumentedShapesStillDecode guards the cap is not so tight it
// breaks the envelope shapes the receiver documents: a HEC {"event":...} wrapper
// inside a {"logs":[...]} batch is two levels and must still decode.
func TestUnwrapDepth_DocumentedShapesStillDecode(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0"})

	body := `{"time":1780500887.356,"logs":[{"event":` + captureFlowRecord + `}]}`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}); p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}
}

// -----------------------------------------------------------------------------
// #209 aggregate admission control
// -----------------------------------------------------------------------------

// blockingBody is a request body whose first Read blocks until release is
// closed, pinning a handler inside readBody so a second request meets a full
// admission semaphore.
type blockingBody struct {
	reading chan struct{} // closed on the first Read
	release chan struct{} // closed by the test to let the body complete
	once    bool
	rest    io.Reader
}

func newBlockingBody(payload string) *blockingBody {
	return &blockingBody{
		reading: make(chan struct{}),
		release: make(chan struct{}),
		rest:    strings.NewReader(payload),
	}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.reading)
		<-b.release
	}
	return b.rest.Read(p)
}

// TestAdmissionControl_BeyondLimitRejectedWith503 is the #209 control: with the
// budget full, a further request is refused with 503 + Retry-After instead of
// buffering another body, so aggregate memory stays bounded no matter how many
// senders arrive at once.
func TestAdmissionControl_BeyondLimitRejectedWith503(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "127.0.0.1:0", MaxConcurrentRequests: 1})
	h := s.Handler()

	body := newBlockingBody(hecFlowBody)
	done := make(chan int, 1)
	go func() {
		w := post(t, h, http.MethodPost, "/services/collector/event", nil, body)
		done <- w.Code
	}()
	<-body.reading // the first handler now holds the only admission slot

	w := post(t, h, http.MethodPost, "/services/collector/event", nil, strings.NewReader(hecFlowBody))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request status = %d, want 503; body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonOverloaded}); p.Value != 1 {
		t.Fatalf("%s{reason=overloaded} = %v, want 1", metricRejected, p.Value)
	}

	close(body.release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
}

// TestAdmissionControl_SlotReleasedAfterRequest confirms the semaphore is
// released on the way out, so a burst does not permanently wedge the receiver.
func TestAdmissionControl_SlotReleasedAfterRequest(t *testing.T) {
	s, _ := newServer(t, stream.Options{Listen: "127.0.0.1:0", MaxConcurrentRequests: 1})
	h := s.Handler()

	for i := range 3 {
		w := post(t, h, http.MethodPost, "/services/collector/event", nil, strings.NewReader(hecFlowBody))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (slot not released?); body=%q", i, w.Code, w.Body.String())
		}
	}
}

// TestAdmissionControl_NegativeDisablesLimit pins the documented escape hatch: a
// negative MaxConcurrentRequests turns the budget off entirely.
func TestAdmissionControl_NegativeDisablesLimit(t *testing.T) {
	s, _ := newServer(t, stream.Options{Listen: "127.0.0.1:0", MaxConcurrentRequests: -1})
	h := s.Handler()

	body := newBlockingBody(hecFlowBody)
	done := make(chan int, 1)
	go func() {
		w := post(t, h, http.MethodPost, "/services/collector/event", nil, body)
		done <- w.Code
	}()
	<-body.reading

	w := post(t, h, http.MethodPost, "/services/collector/event", nil, strings.NewReader(hecFlowBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the limit disabled; body=%q", w.Code, w.Body.String())
	}

	close(body.release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
}
