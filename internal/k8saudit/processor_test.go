package k8saudit

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func execObject() Object {
	return Object{
		StableID: "nRECORDER01CNTRL",
		Event: Event{
			Type:      EventTypeKubernetesAPIRequest,
			Timestamp: 1785325858,
			UserAgent: "kubectl/v1.36.3 (darwin/arm64) kubernetes/0f29094",
			Request: RequestInfo{
				Method: "GET",
				// Carries the command in its query string; must never be emitted.
				// Uses the real recon command seen in testdata/corpus.ndjson (also
				// classify_test.go's "recon" case) so ClassifyCommand genuinely
				// resolves to "recon" here, per classify.go's frozen patterns — a
				// bare "id" alone falls through to the interactive_shell shell-binary
				// fallback instead.
				Path: "/api/v1/namespaces/otel-demo/pods/p1/exec?command=sh&command=-c&command=echo+RECORDING_TEST_OK%3B+id%3B+uname+-a&container=kafka",
				QueryParameters: map[string][]string{
					"command":   {"sh", "-c", "echo RECORDING_TEST_OK; id; uname -a"},
					"container": {"kafka"},
				},
			},
			Kubernetes: KubernetesInfo{
				IsResourceRequest: true,
				Path:              "/api/v1/namespaces/otel-demo/pods/p1/exec",
				Verb:              "get",
				APIGroup:          "",
				Namespace:         "otel-demo",
				Resource:          "pods",
				Subresource:       "exec",
				Name:              "p1",
			},
			Source:      Source{Node: "laptop.example.ts.net", NodeUser: "user@example.com"},
			Destination: Destination{Node: "recorder.example.ts.net"},
		},
	}
}

func TestProcess_EmitsRequestCounterAndLog(t *testing.T) {
	rec := telemetrytest.New()
	NewProcessor().Process(execObject(), rec.Emitter())

	pts := rec.MetricPoints("tailscale.k8s.api.requests")
	if len(pts) != 1 {
		t.Fatalf("requests points = %d, want 1", len(pts))
	}
	if got := pts[0].Attrs[attrSubresource]; got != "exec" {
		t.Fatalf("subresource = %q", got)
	}
	if got := pts[0].Attrs[attrUserAgent]; got != "kubectl" {
		t.Fatalf("user_agent = %q, want normalized 'kubectl'", got)
	}
	if len(rec.LogRecords()) != 1 {
		t.Fatalf("log records = %d, want 1", len(rec.LogRecords()))
	}
}

// The single most important guard in this package.
func TestProcess_NeverEmitsRawRequestPath(t *testing.T) {
	rec := telemetrytest.New()
	NewProcessor().Process(execObject(), rec.Emitter())
	for _, r := range rec.LogRecords() {
		for k, v := range r.Attrs {
			if strings.Contains(v, "?command=") {
				t.Fatalf("attribute %q leaked the raw query string: %q", k, v)
			}
		}
	}
	for _, m := range rec.MetricNames() {
		for _, p := range rec.MetricPoints(m) {
			for k, v := range p.Attrs {
				if strings.Contains(v, "?") {
					t.Fatalf("metric %s attribute %q leaked a query string: %q", m, k, v)
				}
			}
		}
	}
}

func TestProcess_ExecEmitsBoundedCommandClass(t *testing.T) {
	rec := telemetrytest.New()
	NewProcessor().Process(execObject(), rec.Emitter())
	pts := rec.MetricPoints("tailscale.k8s.api.exec_sessions")
	if len(pts) != 1 {
		t.Fatalf("exec_sessions points = %d, want 1", len(pts))
	}
	if got := pts[0].Attrs[attrCommandClass]; got != "recon" {
		t.Fatalf("command_class = %q, want recon", got)
	}
}

func TestProcess_CommandTextIsOnByDefaultAndSuppressible(t *testing.T) {
	rec := telemetrytest.New()
	NewProcessor().Process(execObject(), rec.Emitter())
	if _, ok := rec.LogRecords()[0].Attrs[attrCommand]; !ok {
		t.Fatal("command text absent; it is on by default")
	}

	rec2 := telemetrytest.New()
	NewProcessor(WithEmitCommandText(false)).Process(execObject(), rec2.Emitter())
	if _, ok := rec2.LogRecords()[0].Attrs[attrCommand]; ok {
		t.Fatal("command text present despite WithEmitCommandText(false)")
	}
	// The class survives redaction — it is not PII.
	if len(rec2.MetricPoints("tailscale.k8s.api.exec_sessions")) != 1 {
		t.Fatal("command_class metric must survive command-text redaction")
	}
}

func TestProcess_SensitiveReadCounted(t *testing.T) {
	obj := execObject()
	obj.Event.Kubernetes = KubernetesInfo{
		IsResourceRequest: true, Verb: "get", Resource: "secrets",
		Namespace: "tailscale", Name: "operator-oauth", Path: "/api/v1/namespaces/tailscale/secrets/operator-oauth",
	}
	obj.Event.Request.QueryParameters = nil
	rec := telemetrytest.New()
	NewProcessor().Process(obj, rec.Emitter())
	if len(rec.MetricPoints("tailscale.k8s.api.sensitive_reads")) != 1 {
		t.Fatal("secrets read not counted as sensitive")
	}
}

func TestProcess_UnknownTypeCountsSchemaDrift(t *testing.T) {
	obj := execObject()
	obj.Event.Type = "some-future-event"
	rec := telemetrytest.New()
	NewProcessor().Process(obj, rec.Emitter())
	if len(rec.MetricPoints("tailscale.k8s.schema_drift")) == 0 {
		t.Fatal("unknown event type must record schema drift")
	}
}

func TestProcess_HostileValuesStayBounded(t *testing.T) {
	rec := telemetrytest.New()
	p := NewProcessor()
	for i := range 2000 {
		obj := execObject()
		obj.Event.UserAgent = fmt.Sprintf("evil-%d/1.0", i)
		obj.Event.Kubernetes.Resource = fmt.Sprintf("crd-%d", i)
		obj.Event.Kubernetes.Verb = fmt.Sprintf("verb-%d", i)
		p.Process(obj, rec.Emitter())
	}
	seen := map[string]struct{}{}
	for _, pt := range rec.MetricPoints("tailscale.k8s.api.requests") {
		seen[pt.Attrs[attrUserAgent]+"|"+pt.Attrs[attrResource]+"|"+pt.Attrs[attrVerb]] = struct{}{}
	}
	if len(seen) != 1 {
		t.Fatalf("2000 hostile inputs produced %d series, want 1 (all 'other')", len(seen))
	}
}

func TestProcessSession_EmitsSessionSignals(t *testing.T) {
	h, err := DecodeCastHeader([]byte(realCastHeader))
	if err != nil {
		t.Fatalf("DecodeCastHeader: %v", err)
	}
	rec := telemetrytest.New()
	NewProcessor().ProcessSession(h, rec.Emitter())
	pts := rec.MetricPoints("tailscale.k8s.session.started")
	if len(pts) != 1 {
		t.Fatalf("session.started points = %d, want 1", len(pts))
	}
	if pts[0].Attrs[attrSessionType] != "exec" {
		t.Fatalf("session_type = %q", pts[0].Attrs[attrSessionType])
	}
}

// TestProcess_RealCorpusStaysBoundedAndLeakFree drives every one of the 57
// sanitized real .event objects in testdata/corpus.ndjson through
// DecodeObject + Process, proving on real data (not just the hand-built
// execObject fixture) that: every line decodes, no emitted metric attribute
// value leaks a query string or the raw exec command, and the request
// counter's series count stays small despite the corpus's real variety of
// user agents/verbs/resources/namespaces.
func TestProcess_RealCorpusStaysBoundedAndLeakFree(t *testing.T) {
	f, err := os.Open("testdata/corpus.ndjson")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	rec := telemetrytest.New()
	p := NewProcessor()

	lines := 0
	scanner := bufio.NewScanner(f)
	// Real .event objects can carry a full command line in their queryParameters
	// echo, which is long enough to need a larger-than-default scan buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lines++
		obj, err := DecodeObject(line)
		if err != nil {
			t.Fatalf("line %d: DecodeObject: %v", lines, err)
		}
		p.Process(obj, rec.Emitter())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan corpus: %v", err)
	}
	if lines != 57 {
		t.Fatalf("corpus lines = %d, want 57 (has the fixture changed?)", lines)
	}

	seenSeries := map[string]struct{}{}
	for _, m := range rec.MetricNames() {
		for _, pt := range rec.MetricPoints(m) {
			for k, v := range pt.Attrs {
				if strings.Contains(v, "?") {
					t.Fatalf("metric %s attribute %q leaked a query string: %q", m, k, v)
				}
				if strings.Contains(v, "command=") {
					t.Fatalf("metric %s attribute %q leaked raw command text: %q", m, k, v)
				}
			}
		}
	}
	for _, pt := range rec.MetricPoints("tailscale.k8s.api.requests") {
		key := pt.Attrs[attrVerb] + "|" + pt.Attrs[attrResource] + "|" + pt.Attrs[attrSubresource] + "|" +
			pt.Attrs[attrAPIGroup] + "|" + pt.Attrs[attrNamespace] + "|" + pt.Attrs[attrUserAgent] + "|" +
			pt.Attrs[attrUser] + "|" + pt.Attrs[attrRecorder]
		seenSeries[key] = struct{}{}
	}
	if len(seenSeries) == 0 {
		t.Fatal("no tailscale.k8s.api.requests series recorded from a 57-line real corpus")
	}
	if len(seenSeries) >= 200 {
		t.Fatalf("tailscale.k8s.api.requests produced %d distinct series from 57 real events, want < 200", len(seenSeries))
	}
}
