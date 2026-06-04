package stream_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
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
	metricRecords      = "tailscale.stream.records"
	metricRejected     = "tailscale.stream.rejected"
	metricDecodeErrors = "tailscale.stream.decode_errors"

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

// captureFlowRecord mirrors the exact shape of a network-flow record from a
// real capture (.capture/logging_network.json): a NUMERIC "proto" (here 6 =
// TCP), srcNode/dstNodes node descriptors, and both virtualTraffic and
// physicalTraffic arrays. The receiver must ignore the descriptive node fields
// and route this to the flow processor on the strength of nodeId + traffic.
const captureFlowRecord = `{"logged":"2026-06-02T19:00:01.346001489Z","nodeId":"nFlowSrc1CNTRL","start":"2026-06-02T18:59:54.278418352Z","end":"2026-06-02T18:59:59.279306235Z","srcNode":{"nodeId":"nFlowSrc1CNTRL","name":"node-a.example.ts.net","addresses":["100.64.0.11"],"tags":["tag:servers"]},"dstNodes":[{"nodeId":"nFlowDst1CNTRL","name":"node-b.example.ts.net","addresses":["100.64.0.12"],"os":"macOS","user":"alice@example.com"}],"virtualTraffic":[{"proto":6,"src":"100.64.0.11:22","dst":"100.64.0.12:58544","txPkts":51,"txBytes":6420}],"physicalTraffic":[{"src":"100.64.0.12:0","dst":"10.0.0.183:57532","txPkts":53,"txBytes":8708,"rxPkts":53,"rxBytes":7004}]}`

// captureAuditRecord mirrors the exact shape of a configuration-audit record
// from a real capture (.capture/logging_config.json): an UPDATE whose "new" is
// a JSON ARRAY ("new":["tag:grafana-pdc"]) and whose "old" is an empty STRING.
// These polymorphic old/new values (string|object|array|null) must not derail
// classification or decoding.
const captureAuditRecord = `{"eventTime":"2026-06-02T19:00:05.558444283Z","type":"CONFIG","eventGroupID":"egExample0000000000000000000000000001","origin":"NODE","actor":{"id":"uExample1CNTRL","type":"USER","loginName":"alice@example.com","displayName":"Rob Knight"},"target":{"id":"nAuditTgt1CNTRL","name":"service-node.example.ts.net","type":"NODE","isEphemeral":true,"property":"ACL_TAGS"},"action":"UPDATE","old":"","new":["tag:grafana-pdc"]}`

// assertFlowAndAuditOnce asserts that exactly one flow and one audit record
// were processed and that no records were rejected.
func assertFlowAndAuditOnce(t *testing.T, rec *telemetrytest.Recorder) {
	t.Helper()
	// captureFlowRecord carries both virtualTraffic and physicalTraffic, so the
	// flow processor emits four MetricIO points (two traffic classes, each with
	// a transmit and a receive direction).
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 4 {
		t.Fatalf("MetricIO points = %d, want 4 (one capture flow processed) (%+v)", len(io), io)
	}
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
	if rej := rec.MetricPoints(metricRejected); len(rej) != 0 {
		t.Fatalf("rejected points = %d, want 0 (%+v)", len(rej), rej)
	}
}

// realHECStreamBody is the ACTUAL Tailscale log-stream wire format, pinned by a
// live capture (S4-10; sanitized: tailnet/node-ids/names/addresses anonymized).
// Each POST body is one-or-more concatenated Splunk-HEC objects with NO
// separators, shaped {"time":<float>,"event":{<record>},"fields":{"recorded":...}}.
// The flow event carries srcNode/dstNodes and a NUMERIC proto; the audit event
// uses "actionDetails" and has NO inner eventTime (its timestamp is the HEC
// "time"/"fields.recorded", which the receiver threads onto the audit record's
// EventTime — see TestStream_AuditEventTimeFromHECEnvelope). This body holds one
// flow object followed by one audit object.
const realHECStreamBody = `{"time":1780500776.773,"event":{"nodeId":"n0001CNTRL","start":"2026-06-03T15:32:54.272130712Z","end":"2026-06-03T15:32:59.27411903Z","srcNode":{"nodeId":"n0001CNTRL","name":"gateway.example.ts.net","addresses":["100.64.0.1","fd7a:115c:a1e0::1"],"tags":["tag:networking"]},"dstNodes":[{"nodeId":"n0002CNTRL","name":"peer-a.example.ts.net","addresses":["100.64.0.2","fd7a:115c:a1e0::2"],"os":"linux","tags":["tag:server"]}],"subnetTraffic":[{"proto":99,"src":"10.0.0.254:0","dst":"100.64.0.3:0","txPkts":8,"txBytes":216}],"physicalTraffic":[{"src":"100.64.0.4:0","dst":"192.0.2.40:8","txPkts":1,"txBytes":32}]},"fields":{"recorded":"2026-06-03T15:33:01.552946176Z"}}` +
	`{"time":1780500887.356,"event":{"eventGroupID":"abc123def456","origin":"CONFIG_API","actor":{"id":"u0001CNTRL","type":"OAUTH_CLIENT","loginName":"","displayName":"OAuth client"},"target":{"id":"k0001CNTRL","name":"API access token","type":"OAUTH_ACCESS_TOKEN"},"action":"CREATE","actionDetails":"scopes - all:read"},"fields":{"recorded":"2026-06-03T15:34:47.809040387Z"}}`

// TestEnvelope_RealTailscaleHEC locks in the live-captured Tailscale envelope:
// concatenated {"time","event","fields"} HEC objects, the "event" carrying a flow
// or audit record, classified and routed. (Characterization: the parser already
// unwraps "event" and reads successive JSON values, which the capture confirms.)
func TestEnvelope_RealTailscaleHEC(t *testing.T) {
	s, rec := newServer(t, stream.Options{}) // no token here; auth is covered separately
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(realHECStreamBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	recs := rec.MetricPoints(metricRecords)
	if fp := findPoint(t, recs, map[string]string{attrType: typeFlow}); fp.Value != 1 {
		t.Errorf("%s{type=flow} = %v, want 1", metricRecords, fp.Value)
	}
	if ap := findPoint(t, recs, map[string]string{attrType: typeAudit}); ap.Value != 1 {
		t.Errorf("%s{type=audit} = %v, want 1", metricRecords, ap.Value)
	}
	if rej := rec.MetricPoints(metricRejected); len(rej) != 0 {
		t.Errorf("rejected points = %d, want 0 (%+v)", len(rej), rej)
	}
}

// TestHandler_TailscaleBasicAuth pins the real Tailscale auth scheme (live
// capture, S4-10): HTTP Basic auth, base64(user:<token>) — NOT
// "Authorization: Splunk <token>". A token-protected receiver MUST accept it,
// otherwise every real Tailscale delivery is rejected as unauthorized.
func TestHandler_TailscaleBasicAuth(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:"+testToken)))
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", h, strings.NewReader(captureFlowRecord))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Tailscale Basic auth must be accepted)", resp.Code)
	}
	if rej := rec.MetricPoints(metricRejected); len(rej) != 0 {
		t.Errorf("unexpected rejection of valid Basic auth: %+v", rej)
	}
}

// TestHandler_WrongBasicTokenRejected confirms a Basic header whose password is
// not the configured token is still rejected.
func TestHandler_WrongBasicTokenRejected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:wrong-token")))
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", h, strings.NewReader(captureFlowRecord))
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (wrong Basic token)", resp.Code)
	}
	if rej := rec.MetricPoints(metricRejected); len(rej) == 0 {
		t.Errorf("expected a rejection counter for wrong Basic token")
	}
}

// TestEnvelope_BareRecord pins the "bare single record" envelope using the
// real capture flow shape (numeric proto): a lone JSON object routes to the
// flow processor.
func TestEnvelope_BareRecord(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(captureFlowRecord))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	// captureFlowRecord has virtualTraffic + physicalTraffic: four MetricIO
	// points (two traffic classes x transmit/receive).
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 4 {
		t.Fatalf("MetricIO points = %d, want 4 (capture flow processed) (%+v)", len(io), io)
	}
	recs := rec.MetricPoints(metricRecords)
	if fp := findPoint(t, recs, map[string]string{attrType: typeFlow}); fp.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, fp.Value)
	}
	if rej := rec.MetricPoints(metricRejected); len(rej) != 0 {
		t.Fatalf("rejected points = %d, want 0 (%+v)", len(rej), rej)
	}
}

// TestEnvelope_NDJSONBatch pins the NDJSON-batch envelope using real capture
// shapes: one flow record then one audit record, newline-delimited.
func TestEnvelope_NDJSONBatch(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	body := captureFlowRecord + "\n" + captureAuditRecord + "\n"
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	assertFlowAndAuditOnce(t, rec)
}

// TestEnvelope_SplunkEventWrapper pins the Splunk-HEC {"event":<record>}
// envelope using the real capture audit shape (array-valued "new").
func TestEnvelope_SplunkEventWrapper(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	body := `{"event":` + captureAuditRecord + `,"sourcetype":"tailscale","time":1717354805}`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if ev := rec.MetricPoints(audit.MetricAuditEvents); len(ev) != 1 {
		t.Fatalf("%s points = %d, want 1 (%+v)", audit.MetricAuditEvents, len(ev), ev)
	}
	recs := rec.MetricPoints(metricRecords)
	if ap := findPoint(t, recs, map[string]string{attrType: typeAudit}); ap.Value != 1 {
		t.Fatalf("%s{type=audit} = %v, want 1", metricRecords, ap.Value)
	}
	// The array-valued "new" must have been rendered onto the audit log record.
	var sawNew bool
	for _, lr := range rec.LogRecords() {
		if strings.Contains(lr.Attrs["tailscale.audit.new"], "tag:grafana-pdc") {
			sawNew = true
		}
	}
	if !sawNew {
		t.Fatalf("audit log record with new containing tag:grafana-pdc not found in %+v", rec.LogRecords())
	}
	if rej := rec.MetricPoints(metricRejected); len(rej) != 0 {
		t.Fatalf("rejected points = %d, want 0 (%+v)", len(rej), rej)
	}
}

// TestEnvelope_NDJSONSalvagesMalformedLine pins the line-by-line salvage path:
// when one NDJSON line is malformed, the surrounding valid lines must still be
// parsed and routed. This exercises the split-by-newline fallback (the loop
// converted to strings.SplitSeq in P2-1).
func TestEnvelope_NDJSONSalvagesMalformedLine(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	// A valid flow, a torn/garbage line, then a valid audit. The decoder cannot
	// cleanly stream all three, forcing the newline-split salvage path.
	body := captureFlowRecord + "\n" + `{"oops": broken json` + "\n" + captureAuditRecord + "\n"
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	// Both valid records survived the malformed middle line.
	recs := rec.MetricPoints(metricRecords)
	if fp := findPoint(t, recs, map[string]string{attrType: typeFlow}); fp.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, fp.Value)
	}
	if ap := findPoint(t, recs, map[string]string{attrType: typeAudit}); ap.Value != 1 {
		t.Fatalf("%s{type=audit} = %v, want 1", metricRecords, ap.Value)
	}
}

// TestEnvelope_TailscaleLogsWrapper pins the Tailscale {"logs":[...]} batch
// envelope (the shape the .capture files themselves use at top level) carrying
// one flow + one audit record from the real capture shapes.
func TestEnvelope_TailscaleLogsWrapper(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	body := `{"logs":[` + captureFlowRecord + `,` + captureAuditRecord + `]}`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	assertFlowAndAuditOnce(t, rec)
}

// auditLogRecord returns the single emitted audit log record (EventName
// "tailscale.config.audit"), or fails.
func auditLogRecord(t *testing.T, rec *telemetrytest.Recorder) telemetrytest.LogRecord {
	t.Helper()
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.config.audit" {
			return lr
		}
	}
	t.Fatalf("no audit log record found in %+v", rec.LogRecords())
	return telemetrytest.LogRecord{}
}

// assertTimeNear fails unless got is within tol of want (and not zero).
func assertTimeNear(t *testing.T, got, want time.Time, tol time.Duration) {
	t.Helper()
	if got.IsZero() {
		t.Fatalf("timestamp is zero; want ~%s", want.Format(time.RFC3339Nano))
	}
	d := got.Sub(want)
	if d < 0 {
		d = -d
	}
	if d > tol {
		t.Fatalf("timestamp = %s, want ~%s (diff %s > tol %s)",
			got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano), d, tol)
	}
}

// TestStream_AuditEventTimeFromHECEnvelope is the S4-10 fidelity fix: a streamed
// configuration-audit record carries NO inner "eventTime"; its timestamp lives in
// the HEC envelope "time" (unix seconds), a sibling of "event". The receiver must
// thread that envelope time onto the audit record so the emitted OTEL log record
// bears the event's real occurrence time instead of falling back to the ingest
// (observed) time. realHECStreamBody's audit object has "time":1780500887.356.
func TestStream_AuditEventTimeFromHECEnvelope(t *testing.T) {
	s, rec := newServer(t, stream.Options{})
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(realHECStreamBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	lr := auditLogRecord(t, rec)
	want := time.Unix(1780500887, 356_000_000).UTC()
	assertTimeNear(t, lr.Timestamp, want, time.Millisecond)
}

// TestStream_AuditInnerEventTimeWinsOverEnvelope guards the "only when zero"
// rule: when a streamed audit record DOES carry its own eventTime, the envelope
// time must NOT override it. captureAuditRecord has eventTime 2026-06-02T19:00:05Z;
// the surrounding HEC envelope advertises a different time (1780500887.356).
func TestStream_AuditInnerEventTimeWinsOverEnvelope(t *testing.T) {
	s, rec := newServer(t, stream.Options{})
	body := `{"time":1780500887.356,"event":` + captureAuditRecord + `,"fields":{"recorded":"2026-06-03T15:34:47.809040387Z"}}`
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	lr := auditLogRecord(t, rec)
	want, err := time.Parse(time.RFC3339Nano, "2026-06-02T19:00:05.558444283Z")
	if err != nil {
		t.Fatalf("parse want: %v", err)
	}
	assertTimeNear(t, lr.Timestamp, want, time.Microsecond)
}

// TestStream_AuditEventTimeFallsBackToFieldsRecorded checks the fallback: when the
// HEC envelope has NO "time" but does carry "fields.recorded" (RFC3339, the
// publisher's record time), an inner-eventTime-less audit record uses that.
func TestStream_AuditEventTimeFallsBackToFieldsRecorded(t *testing.T) {
	s, rec := newServer(t, stream.Options{})
	body := `{"event":{"eventGroupID":"g9","origin":"CONFIG_API","actor":{"id":"u1","loginName":"a@example.com"},"target":{"id":"k1","type":"OAUTH_ACCESS_TOKEN"},"action":"CREATE"},"fields":{"recorded":"2026-06-03T15:34:47.809040387Z"}}`
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	lr := auditLogRecord(t, rec)
	want, err := time.Parse(time.RFC3339Nano, "2026-06-03T15:34:47.809040387Z")
	if err != nil {
		t.Fatalf("parse want: %v", err)
	}
	assertTimeNear(t, lr.Timestamp, want, time.Microsecond)
}

// TestStream_AuditEventTimeFromLogsWrapperEnvelope guards that the HEC envelope
// time is propagated through the {"time":..,"logs":[..]} batch wrapper too, not
// only the {"event":..} wrapper. The parser is documented as defensive across
// both shapes, so an inner-eventTime-less audit record inside a timestamped logs
// batch must still receive the envelope time (not fall back to ingest time).
func TestStream_AuditEventTimeFromLogsWrapperEnvelope(t *testing.T) {
	s, rec := newServer(t, stream.Options{})
	inner := `{"eventGroupID":"g7","origin":"CONFIG_API","actor":{"id":"u1","loginName":"a@example.com"},"target":{"id":"k1","type":"OAUTH_ACCESS_TOKEN"},"action":"CREATE"}`
	body := `{"time":1780500887.356,"logs":[` + inner + `]}`
	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	lr := auditLogRecord(t, rec)
	want := time.Unix(1780500887, 356_000_000).UTC()
	assertTimeNear(t, lr.Timestamp, want, time.Millisecond)
}

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

func TestHandler_MalformedFlowRecordCountsDecodeError(t *testing.T) {
	s, rec := newServer(t, stream.Options{Token: testToken})

	// Classifiable as a flow (has nodeId + traffic) but the typed FlowLog decode
	// fails (start is a number, not an RFC3339 string). Batched with a valid flow
	// so we can assert the good one still flows while the bad one is counted, not
	// silently swallowed.
	malformed := `{"nodeId":"x","virtualTraffic":[{"proto":6}],"start":123}`
	body := captureFlowRecord + "\n" + malformed + "\n"
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}

	// The valid flow was still processed.
	p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow})
	if p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}

	// The malformed flow was counted as a decode error.
	dp := findPoint(t, rec.MetricPoints(metricDecodeErrors), map[string]string{attrType: typeFlow})
	if dp.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricDecodeErrors, dp.Value)
	}
	if dp.Kind != "sum" || !dp.Monotonic {
		t.Fatalf("decode_errors = %+v, want a monotonic sum (counter)", dp)
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
