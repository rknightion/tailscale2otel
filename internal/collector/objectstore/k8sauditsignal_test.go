package objectstore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/k8saudit"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// realEventLine is one sanitized object straight off a live recorder bucket.
const realEventLine = `{"stableID":"nREC00000001CNTRL","event":{"type":"kubernetes-api-request","id":"nREC00000001CNTRL/events/2026-07-29T11:50:58.722743575Z.event","timestamp":1785325858,"userAgent":"kubectl/v1.36.3 (darwin/arm64) kubernetes/0f29094","request":{"method":"GET","path":"/api/v1/namespaces/otel-demo/pods/p1/exec?command=sh&command=-c&command=id&container=kafka","body":"","queryParameters":{"command":["sh","-c","id"],"container":["kafka"]}},"kubernetes":{"IsResourceRequest":true,"Path":"/api/v1/namespaces/otel-demo/pods/p1/exec","Verb":"get","APIPrefix":"api","APIGroup":"","APIVersion":"v1","Namespace":"otel-demo","Resource":"pods","Subresource":"exec","Name":"p1","Parts":["pods","p1","exec"],"FieldSelector":"","LabelSelector":""},"source":{"node":"node0.example-tailnet.ts.net","nodeID":"nSRC00000001CNTRL","nodeUserID":1000000000000001,"nodeUser":"user0@example.com"},"destination":{"node":"node1.example-tailnet.ts.net","nodeID":"nDST00000001CNTRL"}},"timestamp":"2026-07-29 11:50:58 +0000 UTC"}`

const realCastHeaderLine = `{"command":"sh -c clear; (bash || ash || sh)","connectionID":"","dstNode":"node1.example-tailnet.ts.net","dstNodeID":"nDST00000001CNTRL","env":null,"height":16,"kubernetes":{"Container":"kafka","Namespace":"otel-demo","PodName":"kafka-1","SessionType":"exec"},"localUser":"","srcNode":"node0.example-tailnet.ts.net","srcNodeID":"nSRC00000001CNTRL","srcNodeUser":"user0@example.com","srcNodeUserID":1000000000000001,"sshUser":"","timestamp":1785325858,"version":2,"width":155}`

func k8sSignal() SignalProcessor { return NewK8sAuditSignal(k8saudit.NewProcessor()) }

func TestK8sAuditSignal_Name(t *testing.T) {
	if got := k8sSignal().Signal(); got != semconv.IngestSignalK8sAudit {
		t.Fatalf("Signal() = %q, want %q", got, semconv.IngestSignalK8sAudit)
	}
}

func TestK8sAuditSignal_AcceptsEventObject(t *testing.T) {
	rec, err := k8sSignal().PrepareRecord(context.Background(), []byte(realEventLine), time.Now())
	if err != nil {
		t.Fatalf("PrepareRecord: %v", err)
	}
	if rec == nil {
		t.Fatal("nil prepared record")
	}
	tr := telemetrytest.New()
	ts := rec.Commit(tr.Emitter())
	if ts.EventTime.IsZero() {
		t.Error("EventTime zero: the freshness observers would see no progress")
	}
	if len(tr.MetricPoints("tailscale.k8s.api.requests")) != 1 {
		t.Fatalf("Commit emitted no request counter; metrics=%v", tr.MetricNames())
	}
}

func TestK8sAuditSignal_AcceptsCastHeader(t *testing.T) {
	rec, err := k8sSignal().PrepareRecord(context.Background(), []byte(realCastHeaderLine), time.Now())
	if err != nil {
		t.Fatalf("PrepareRecord: %v", err)
	}
	tr := telemetrytest.New()
	if ts := rec.Commit(tr.Emitter()); ts.EventTime.IsZero() {
		t.Error("EventTime zero for a cast header")
	}
	if len(tr.MetricPoints("tailscale.k8s.session.started")) != 1 {
		t.Fatalf("cast header emitted no session counter; metrics=%v", tr.MetricNames())
	}
}

// Frames share an object with the header. They must be silently ignored rather
// than counted as corruption, or one long session mints thousands of bogus
// data-quality observations and can fail an otherwise healthy object.
func TestK8sAuditSignal_FramesAreNoOpNotErrors(t *testing.T) {
	for _, frame := range []string{
		`[0.124251882,"o","RECORDING_TEST_OK\n"]`,
		`[1.5,"o","uid=1654(app) gid=1654(app)\n"]`,
	} {
		rec, err := k8sSignal().PrepareRecord(context.Background(), []byte(frame), time.Now())
		if err != nil {
			t.Fatalf("frame %q must not be an error, got %v", frame, err)
		}
		tr := telemetrytest.New()
		ts := rec.Commit(tr.Emitter())
		if names := tr.MetricNames(); len(names) != 0 {
			t.Errorf("frame emitted metrics %v; it must emit nothing", names)
		}
		if n := len(tr.LogRecords()); n != 0 {
			t.Errorf("frame emitted %d log records; it must emit nothing", n)
		}
		if !ts.EventTime.IsZero() || !ts.CaptureTime.IsZero() {
			t.Error("frame contributed a timestamp; it must contribute none")
		}
	}
}

func TestK8sAuditSignal_RejectsGarbage(t *testing.T) {
	for _, in := range []string{`{"nope":1}`, `null`, `{}`, `not json at all`} {
		if _, err := k8sSignal().PrepareRecord(context.Background(), []byte(in), time.Now()); !errors.Is(err, ErrRecordDecode) {
			t.Errorf("PrepareRecord(%s) err = %v, want ErrRecordDecode", in, err)
		}
	}
}

// The end-to-end guard: drive the whole sanitized corpus through the adapter as
// the engine would, and assert nothing leaks a query string into telemetry.
func TestK8sAuditSignal_RealCorpusIsLeakFree(t *testing.T) {
	raw, err := os.ReadFile("../../k8saudit/testdata/corpus.ndjson")
	if err != nil {
		t.Skipf("corpus fixture unavailable: %v", err)
	}
	s := k8sSignal()
	tr := telemetrytest.New()
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		lines++
		rec, err := s.PrepareRecord(context.Background(), []byte(line), time.Now())
		if err != nil {
			t.Fatalf("line %d failed to decode: %v", lines, err)
		}
		rec.Commit(tr.Emitter())
	}
	if lines == 0 {
		t.Fatal("corpus fixture is empty; this test would prove nothing")
	}
	for _, name := range tr.MetricNames() {
		for _, p := range tr.MetricPoints(name) {
			for k, v := range p.Attrs {
				if strings.Contains(v, "?") || strings.Contains(v, "command=") {
					t.Errorf("metric %s attribute %q leaked a query string: %q", name, k, v)
				}
			}
		}
	}
	t.Logf("drove %d real records through the adapter", lines)
}
