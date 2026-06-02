package stream_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/stream"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// Metric/attribute constants exercised by the receiver tests.
const (
	metricRecords  = "tailscale.stream.records"
	metricRejected = "tailscale.stream.rejected"

	attrType   = "type"
	attrReason = "reason"

	typeFlow  = "flow"
	typeAudit = "audit"

	reasonAuth       = "auth"
	reasonUnparsable = "unparsable"
)

const testToken = "s3cr3t-token"

// newServer builds a Server wired to a Recorder, returning both. The processors
// are the real shared ones (a populated cache so node resolution succeeds).
func newServer(t *testing.T, opts stream.Options) (*stream.Server, *telemetrytest.Recorder) {
	t.Helper()
	rec := telemetrytest.New()
	cache := enrich.NewDeviceCache()
	flowProc := flowlog.NewProcessor(cache, flowlog.Options{NodeDims: true})
	auditProc := audit.NewProcessor()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := stream.New(opts, flowProc, auditProc, rec.Emitter(), logger)
	return s, rec
}

// post sends a request through the Handler under test and returns the recorded
// response.
func post(t *testing.T, h http.Handler, method, path string, header http.Header, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// authHeader returns a header carrying a valid Splunk authorization token.
func authHeader() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Splunk "+testToken)
	return h
}

// findPoint returns the first metric point whose attrs match every want
// key/value, or fails.
func findPoint(t *testing.T, pts []telemetrytest.MetricPoint, want map[string]string) telemetrytest.MetricPoint {
	t.Helper()
outer:
	for _, p := range pts {
		for k, v := range want {
			if p.Attrs[k] != v {
				continue outer
			}
		}
		return p
	}
	t.Fatalf("no metric point matching %v in %+v", want, pts)
	return telemetrytest.MetricPoint{}
}

// gzipBytes gzip-compresses b.
func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// zstdBytes zstd-compresses b.
func zstdBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

// A HEC-wrapped flow record: {"event": {<flow with nodeId + virtualTraffic>}}.
const hecFlowBody = `{
  "event": {
    "logged": "2024-06-06T15:27:26Z",
    "nodeId": "nLaptop",
    "start": "2024-06-06T15:25:26Z",
    "end": "2024-06-06T15:26:26Z",
    "virtualTraffic": [
      {"proto": 6, "src": "100.64.0.1:443", "dst": "100.64.0.2:51820", "txPkts": 10, "txBytes": 1000, "rxPkts": 8, "rxBytes": 800}
    ]
  }
}`

// A bare flow record (no wrapper).
const bareFlowRecord = `{"logged":"2024-06-06T15:27:26Z","nodeId":"nLaptop","virtualTraffic":[{"proto":6,"src":"100.64.0.1:443","dst":"100.64.0.2:51820","txBytes":1000,"rxBytes":800}]}`

// A bare audit event record.
const bareAuditRecord = `{"eventTime":"2026-06-02T12:00:30Z","type":"CONFIG","eventGroupID":"g1","origin":"admin-console","actor":{"id":"u1","loginName":"alice@example.com","displayName":"Alice"},"target":{"id":"n1","name":"node.ts.net","type":"NODE"},"action":"CREATE"}`

func TestHandler_HECFlowRecord(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(hecFlowBody))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	// Splunk-HEC success ack shape.
	if got := strings.TrimSpace(w.Body.String()); !strings.Contains(got, `"text":"Success"`) || !strings.Contains(got, `"code":0`) {
		t.Fatalf("ack body = %q, want Splunk success ack", got)
	}

	// Flow was processed via the shared flow processor.
	io := rec.MetricPoints(flowlog.MetricIO)
	if len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 (flow processed) (%+v)", len(io), io)
	}

	// records{type=flow} == 1.
	recs := rec.MetricPoints(metricRecords)
	p := findPoint(t, recs, map[string]string{attrType: typeFlow})
	if p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}
	if rej := rec.MetricPoints(metricRejected); len(rej) != 0 {
		t.Fatalf("rejected points = %d, want 0 (%+v)", len(rej), rej)
	}
}

func TestHandler_NDJSONFlowAndAudit(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	body := bareFlowRecord + "\n" + bareAuditRecord + "\n"
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}

	// Flow processed.
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 (%+v)", len(io), io)
	}
	// Audit processed.
	if ev := rec.MetricPoints(audit.MetricAuditEvents); len(ev) != 1 {
		t.Fatalf("%s points = %d, want 1 (%+v)", audit.MetricAuditEvents, len(ev), ev)
	}

	recs := rec.MetricPoints(metricRecords)
	if fp := findPoint(t, recs, map[string]string{attrType: typeFlow}); fp.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, fp.Value)
	}
	if ap := findPoint(t, recs, map[string]string{attrType: typeAudit}); ap.Value != 1 {
		t.Fatalf("%s{type=audit} = %v, want 1", metricRecords, ap.Value)
	}

	// Audit log carries the action attribute (shared audit processor ran).
	var sawAction bool
	for _, lr := range rec.LogRecords() {
		if lr.Attrs["tailscale.audit.action"] == "CREATE" {
			sawAction = true
		}
	}
	if !sawAction {
		t.Fatalf("audit log record with action=CREATE not found in %+v", rec.LogRecords())
	}
}

func TestHandler_GzipBody(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	h := authHeader()
	h.Set("Content-Encoding", "gzip")
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", h, bytes.NewReader(gzipBytes(t, []byte(hecFlowBody))))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 after gzip decode (%+v)", len(io), io)
	}
	recs := rec.MetricPoints(metricRecords)
	if fp := findPoint(t, recs, map[string]string{attrType: typeFlow}); fp.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, fp.Value)
	}
}

func TestHandler_ZstdBody(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	h := authHeader()
	h.Set("Content-Encoding", "zstd")
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", h, bytes.NewReader(zstdBytes(t, []byte(hecFlowBody))))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 after zstd decode (%+v)", len(io), io)
	}
}

func TestHandler_MissingToken(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	// No Authorization header at all.
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", http.Header{}, strings.NewReader(hecFlowBody))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	rej := rec.MetricPoints(metricRejected)
	if p := findPoint(t, rej, map[string]string{attrReason: reasonAuth}); p.Value != 1 {
		t.Fatalf("%s{reason=auth} = %v, want 1", metricRejected, p.Value)
	}
	// Body must not have been processed.
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 0 {
		t.Fatalf("MetricIO points = %d, want 0 when unauthorized", len(io))
	}
}

func TestHandler_WrongToken(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	h := http.Header{}
	h.Set("Authorization", "Splunk not-the-token")
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", h, strings.NewReader(hecFlowBody))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	rej := rec.MetricPoints(metricRejected)
	if p := findPoint(t, rej, map[string]string{attrReason: reasonAuth}); p.Value != 1 {
		t.Fatalf("%s{reason=auth} = %v, want 1", metricRejected, p.Value)
	}
}

func TestHandler_GarbageBodyUnparsable(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader("this is not json at all <<<>>>"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for garbage body", w.Code)
	}
	rej := rec.MetricPoints(metricRejected)
	if p := findPoint(t, rej, map[string]string{attrReason: reasonUnparsable}); p.Value != 1 {
		t.Fatalf("%s{reason=unparsable} = %v, want 1", metricRejected, p.Value)
	}
	// Nothing should have been processed.
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 0 {
		t.Fatalf("MetricIO points = %d, want 0 for garbage body", len(io))
	}
}

func TestHandler_TailscaleLogsBatchWrapper(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	// Tailscale {"logs":[...]} batch wrapper with one flow + one audit record.
	body := `{"logs":[` + bareFlowRecord + `,` + bareAuditRecord + `]}`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))

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

func TestHandler_WrongMethodAndPath(t *testing.T) {
	s, _ := newServer(t, stream.Options{Token: testToken})

	// GET is rejected.
	w := post(t, s.Handler(), http.MethodGet, "/services/collector/event", authHeader(), nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", w.Code)
	}

	// Wrong path is rejected.
	w = post(t, s.Handler(), http.MethodPost, "/wrong/path", authHeader(), strings.NewReader(hecFlowBody))
	if w.Code != http.StatusNotFound {
		t.Fatalf("wrong-path status = %d, want 404", w.Code)
	}
}

func TestHandler_NoTokenConfiguredSkipsAuth(t *testing.T) {
	// With an empty Token, no auth header is required.
	s, rec := newServer(t, stream.Options{})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", http.Header{}, strings.NewReader(hecFlowBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when no token configured; body=%q", w.Code, w.Body.String())
	}
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 (flow processed) (%+v)", len(io), io)
	}
}

// TestRun_GracefulShutdown lightly exercises Run(): it binds an ephemeral port,
// serves one request, then cancels the context and expects a clean return.
func TestRun_GracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s, rec := newServer(t, stream.Options{Listen: addr, Path: "/services/collector/event", Token: testToken})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()

	// Wait for the listener to come up, then POST a flow record.
	client := &http.Client{Timeout: 2 * time.Second}
	var resp *http.Response
	deadline := time.Now().Add(3 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/services/collector/event", strings.NewReader(hecFlowBody))
		req.Header.Set("Authorization", "Splunk "+testToken)
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server never accepted connections: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Run status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 via Run() (%+v)", len(io), io)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want nil/ErrServerClosed/Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
