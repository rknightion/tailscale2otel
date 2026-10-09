package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// diagSentinel stands in for sensitive text a backend might put in a non-2xx
// response body (an echoed credential, a signed URL). It is synthetic.
const diagSentinel = "SENTINEL-not-a-real-secret-9f3c"

// diagBackend is a local fake OTLP/HTTP backend. Every export gets the
// configured status and a body that carries the sentinel; status can be flipped
// to 200 to model recovery.
type diagBackend struct {
	mu     sync.Mutex
	status int
}

func (b *diagBackend) set(status int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = status
}

func (b *diagBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = r.Body.Read(make([]byte, 1))
	b.mu.Lock()
	status := b.status
	b.mu.Unlock()
	if status/100 == 2 {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "upstream rejected request, echoing Authorization: Bearer %s", diagSentinel)
}

type diagRig struct {
	tracker *deliveryTracker
	logs    *bytes.Buffer
	backend *diagBackend
	metrics func(context.Context) error
	logsExp func(context.Context) error
}

// newDiagRig wires the REAL OTLP/HTTP metric and log exporters (through the
// counting wrappers that feed the delivery tracker) at a local fake backend,
// with the tracker's diagnostics bound to a buffer-backed slog logger.
func newDiagRig(t *testing.T) *diagRig {
	t.Helper()
	backend := &diagBackend{status: http.StatusOK}
	return newDiagRigWith(t, backend, backend)
}

// newDiagRigWith is newDiagRig against an arbitrary handler (for backends that
// need to misbehave at the wire level).
func newDiagRigWith(t *testing.T, handler http.Handler, backend *diagBackend) *diagRig {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	var buf bytes.Buffer
	tracker := newDeliveryTracker()
	tracker.setDiagnostics(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	opts := Options{
		Protocol: "http", Endpoint: server.URL, Insecure: true,
		Transport: TransportOptions{Timeout: 2 * time.Second, Compression: "none", Retry: &RetryPolicy{Enabled: false}},
	}
	me, err := newMetricExporter(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	le, err := newLogExporter(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = me.Shutdown(c)
		_ = le.Shutdown(c)
	})
	cm := newCountingMetricExporter(me, false, tracker)
	cl := newCountingLogExporter(le, false, tracker)

	at := time.Date(2000, 1, 1, 0, 1, 0, 0, time.UTC)
	data := metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(attribute.String("service.name", "synthetic-diag")),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{Name: "synthetic-diag"},
			Metrics: []metricdata.Metrics{{
				Name: "synthetic.diag.count", Unit: "1",
				Data: metricdata.Sum[int64]{
					Temporality: metricdata.CumulativeTemporality, IsMonotonic: true,
					DataPoints: []metricdata.DataPoint[int64]{{StartTime: at.Add(-time.Minute), Time: at, Value: 1}},
				},
			}},
		}},
	}
	var rec sdklog.Record
	rec.SetTimestamp(at)
	rec.SetBody(attribute.StringValue("synthetic body"))

	return &diagRig{
		tracker: tracker, logs: &buf, backend: backend,
		metrics: func(ctx context.Context) error { return cm.Export(ctx, &data) },
		logsExp: func(ctx context.Context) error { return cl.Export(ctx, []sdklog.Record{rec}) },
	}
}

func (r *diagRig) state(signal string) DeliveryState {
	for _, s := range r.tracker.states() {
		if s.Signal == signal {
			return s
		}
	}
	return DeliveryState{}
}

// The first-failure line (and every later one) must carry only a bounded class,
// never the backend's response body, for each way a backend can say no.
func TestDeliveryDiagnosticsExcludeBackendResponseBody(t *testing.T) {
	cases := []struct {
		status int
		class  string
	}{
		{http.StatusUnauthorized, errClassUnauthenticated},
		{http.StatusBadRequest, errClassInvalid},
		{http.StatusInternalServerError, errClassOther},
		// The SDK reports 429/502/503/504 as "retry-able request failure" with
		// the status code dropped, so the only class reachable from the error
		// text is "other"; the point here is that the body still stays out.
		{http.StatusServiceUnavailable, errClassOther},
		{http.StatusTooManyRequests, errClassOther},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			r := newDiagRig(t)
			r.backend.set(tc.status)
			ctx := context.Background()
			if err := r.metrics(ctx); err == nil {
				t.Fatal("metrics export unexpectedly succeeded against a failing backend")
			}
			if err := r.logsExp(ctx); err == nil {
				t.Fatal("logs export unexpectedly succeeded against a failing backend")
			}

			out := r.logs.String()
			if strings.Contains(out, diagSentinel) || strings.Contains(strings.ToLower(out), "bearer") {
				t.Fatalf("diagnostic log leaked backend response text:\n%s", out)
			}
			for _, signal := range []string{SignalMetrics, SignalLogs} {
				if want := "error_class=" + tc.class; !strings.Contains(out, "signal="+signal) || !strings.Contains(out, want) {
					t.Errorf("log lacks signal=%s / %s:\n%s", signal, want, out)
				}
				st := r.state(signal)
				if st.Failures != 1 || st.ConsecutiveFailures != 1 || st.LastErrorClass != tc.class {
					t.Errorf("%s health = failures %d consecutive %d class %q, want 1/1/%q",
						signal, st.Failures, st.ConsecutiveFailures, st.LastErrorClass, tc.class)
				}
			}
		})
	}
}

// Only the SDK-controlled part of an error may choose the class: a status code
// inside a port, or a keyword inside the backend's body, must not.
func TestClassifyExportErrorIgnoresBackendBodyAndPorts(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"port digits", errors.New("failed to send metrics to http://127.0.0.1:54012/v1/metrics: 500 Internal Server Error"), errClassOther},
		{"body keywords", errors.New("failed to send logs to http://h/v1/logs: 500 Internal Server Error (body: 401 timeout unavailable)"), errClassOther},
		{"retryable body keywords", errors.New("retry-able request failure: body: 403 deadline exceeded"), errClassOther},
		{"real status still wins", errors.New("failed to send logs to http://127.0.0.1:50401/v1/logs: 401 Unauthorized (body: ok)"), errClassUnauthenticated},
	}
	for _, tc := range tests {
		if got := classifyExportError(tc.err); got != tc.want {
			t.Errorf("%s: class = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// rawStatusBackend answers every request with the literal status line it is
// given, so the reason phrase is whatever the server chose.
func rawStatusBackend(statusLine string) http.Handler {
	return rawResponseLineBackend("HTTP/1.1 " + statusLine)
}

// rawResponseLineBackend also permits malformed protocol-version tokens.
func rawResponseLineBackend(responseLine string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		body := "echoing Authorization: Bearer " + diagSentinel
		_, _ = fmt.Fprintf(buf, "%s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			responseLine, len(body), body)
		_ = buf.Flush()
	})
}

// The HTTP class follows the numeric status code the SDK prints, never the
// reason phrase: a server chooses that wording, and a phrase such as
// "401 Unauthorized" or "Timeout" on a 500 must not steer the class or reason.
func TestDeliveryDiagnosticsHTTPClassFollowsNumericStatusNotReasonPhrase(t *testing.T) {
	cases := []struct {
		statusLine string
		class      string
	}{
		{"500 401 Unauthorized", errClassOther},
		{"500 Timeout", errClassOther},
		{"500 Service Unavailable", errClassOther},
		{"500 certificate error", errClassOther},
		{"500 429 Too Many Requests", errClassOther},
		{"500 OTLP partial success: x", errClassOther},
		{"401 Timeout", errClassUnauthenticated},
		{"400 Service Unavailable", errClassInvalid},
		{"403 deadline exceeded", errClassUnauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.statusLine, func(t *testing.T) {
			r := newDiagRigWith(t, rawStatusBackend(tc.statusLine), nil)
			if err := r.metrics(context.Background()); err == nil {
				t.Fatal("metrics export unexpectedly succeeded")
			}
			if err := r.logsExp(context.Background()); err == nil {
				t.Fatal("logs export unexpectedly succeeded")
			}
			out := r.logs.String()
			if strings.Contains(out, diagSentinel) {
				t.Fatalf("log leaked backend text:\n%s", out)
			}
			if !strings.Contains(out, "error_class="+tc.class) {
				t.Errorf("log lacks error_class=%s:\n%s", tc.class, out)
			}
			if strings.Contains(out, "reason=") {
				t.Errorf("an HTTP status error must not carry a reason (the phrase is server-chosen):\n%s", out)
			}
			for _, signal := range []string{SignalMetrics, SignalLogs} {
				st := r.state(signal)
				if st.LastErrorClass != tc.class || st.Failures != 1 || st.PartialSuccesses != 0 {
					t.Errorf("%s health = %+v, want class %q, 1 failure, no partial success", signal, st, tc.class)
				}
			}
		})
	}
}

// net/http's malformed-status errors quote server-controlled text, not a
// transport failure. None of that text may select a class or reason.
func TestClassifyExportErrorMalformedHTTP(t *testing.T) {
	for _, shape := range []string{"malformed HTTP status code", "malformed HTTP response", "malformed HTTP version"} {
		for _, phrase := range []string{
			"timeout", "i/o timeout", "deadline exceeded", "context canceled",
			"unauthenticated", "permission denied", "unavailable",
			"connection refused", "connection reset", "no such host", "certificate",
			"OTLP partial success: x", "rpc error: code = Unauthenticated",
		} {
			t.Run(shape+"/"+phrase, func(t *testing.T) {
				err := fmt.Errorf("failed to upload metrics: Post %q: net/http: HTTP/1.x transport connection broken: %s %q",
					"http://example.invalid/v1/metrics", shape, "HTTP/1.1 "+phrase)
				if got := classifyExportError(err); got != errClassOther {
					t.Errorf("class = %q, want %q", got, errClassOther)
				}
				if got := exportErrorReason(err); got != "" {
					t.Errorf("reason = %q, want empty", got)
				}
			})
		}
	}
	for _, err := range []error{
		errors.New(`Post "http://example.invalid": dial tcp: i/o timeout`),
		errors.New(`Post "http://example.invalid": read tcp: i/o timeout`),
		fmt.Errorf("export: %w", context.DeadlineExceeded),
	} {
		if got := classifyExportError(err); got != errClassTimeout {
			t.Errorf("classifyExportError(%v) = %q, want timeout", err, got)
		}
	}
}

func TestClassifyExportErrorMalformedHTTPWire(t *testing.T) {
	type wireCase struct {
		responseLine string
		wantError    string
	}
	var cases []wireCase
	for _, statusLine := range []string{"timeout", "i/o timeout", "unavailable", "certificate", "OTLP partial success: x"} {
		cases = append(cases, wireCase{"HTTP/1.1 " + statusLine, "malformed HTTP status code"})
	}
	for _, version := range []string{"timeout", "unavailable", "certificate"} {
		cases = append(cases, wireCase{version + " 500 Internal Server Error", "malformed HTTP version"})
	}
	for _, tc := range cases {
		t.Run(tc.responseLine, func(t *testing.T) {
			r := newDiagRigWith(t, rawResponseLineBackend(tc.responseLine), nil)
			for _, export := range []func(context.Context) error{r.metrics, r.logsExp} {
				err := export(context.Background())
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want net/http %s error, got %v", tc.wantError, err)
				}
				if got := classifyExportError(err); got != errClassOther {
					t.Errorf("class = %q, want other", got)
				}
				if got := exportErrorReason(err); got != "" {
					t.Errorf("reason = %q, want empty", got)
				}
			}
			for _, signal := range []string{SignalMetrics, SignalLogs} {
				if st := r.state(signal); st.LastErrorClass != errClassOther || st.Failures != 1 || st.PartialSuccesses != 0 {
					t.Errorf("%s health = %+v, want other and one failure", signal, st)
				}
			}
			if out := r.logs.String(); strings.Contains(out, "reason=") || strings.Contains(out, diagSentinel) {
				t.Errorf("diagnostic contains backend reason or body: %s", out)
			}
		})
	}
}

// gRPC errors classify on the "code = X" token only; the description is
// server-chosen free text. A transport reason is kept only for Unavailable,
// where gRPC reports local dial and connection failures.
func TestClassifyExportErrorGRPCUsesCodeTokenNotDescription(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		class      string
		wantReason string
	}{
		{"desc mentions timeout", errors.New("rpc error: code = Unauthenticated desc = invalid token, request timeout"), errClassUnauthenticated, ""},
		{"wrapped prefix", errors.New("failed to upload metrics: rpc error: code = ResourceExhausted desc = deadline exceeded"), errClassRateLimited, ""},
		{"internal with transport words", errors.New("rpc error: code = Internal desc = connection refused 401"), errClassOther, ""},
		{"deadline code", errors.New("rpc error: code = DeadlineExceeded desc = whatever"), errClassTimeout, ""},
		{"unavailable keeps reason", errors.New("rpc error: code = Unavailable desc = connection error: connection refused"), errClassUnavailable, "connection refused"},
		{"unavailable without allowlisted reason", errors.New("rpc error: code = Unavailable desc = " + diagSentinel), errClassUnavailable, ""},
	}
	for _, tc := range tests {
		if got := classifyExportError(tc.err); got != tc.class {
			t.Errorf("%s: class = %q, want %q", tc.name, got, tc.class)
		}
		if got := exportErrorReason(tc.err); got != tc.wantReason {
			t.Errorf("%s: reason = %q, want %q", tc.name, got, tc.wantReason)
		}
	}
}

// An endpoint port that happens to equal a status code must not pick the class.
func TestClassifyExportErrorIgnoresStatusLookalikePorts(t *testing.T) {
	tests := []struct {
		err   error
		class string
	}{
		{errors.New(`Post "http://127.0.0.1:401/v1/metrics": novel transport failure`), errClassOther},
		{errors.New(`failed to upload metrics: failed to send metrics to http://127.0.0.1:503/v1/metrics: 500 Internal Server Error (body: x)`), errClassOther},
		{errors.New(`failed to send logs to http://127.0.0.1:429/v1/logs: 400 Bad Request (body: x)`), errClassInvalid},
		{errors.New(`Post "http://127.0.0.1:403/v1/logs": dial tcp 127.0.0.1:403: connect: connection refused`), errClassUnavailable},
	}
	for _, tc := range tests {
		if got := classifyExportError(tc.err); got != tc.class {
			t.Errorf("classifyExportError(%v) = %q, want %q", tc.err, got, tc.class)
		}
	}
}

// An error nobody has seen before, joined from several transport errors, must
// land in the "other" class with no raw text in the log, while the failure
// still counts toward health.
func TestDeliveryDiagnosticsUnknownJoinedErrorIsClassOnly(t *testing.T) {
	d := newDeliveryTracker()
	var buf bytes.Buffer
	d.setDiagnostics(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), nil, "")

	joined := errors.Join(
		errors.New("novel transport failure carrying "+diagSentinel),
		fmt.Errorf("wrapped: %w", errors.New("second leaf "+diagSentinel)),
	)
	for range 3 {
		d.observe(SignalLogs, joined, 0.1)
	}

	out := buf.String()
	if strings.Contains(out, diagSentinel) {
		t.Fatalf("joined unknown error leaked into the log:\n%s", out)
	}
	if !strings.Contains(out, "error_class="+errClassOther) {
		t.Errorf("log lacks error_class=%s:\n%s", errClassOther, out)
	}
	var st DeliveryState
	for _, s := range d.states() {
		if s.Signal == SignalLogs {
			st = s
		}
	}
	if st.Failures != 3 || st.ConsecutiveFailures != 3 || !st.Failing() || st.LastErrorClass != errClassOther {
		t.Errorf("health = %+v, want 3 failures, failing, class %q", st, errClassOther)
	}
}

// A sustained outage followed by recovery: summary and recovery lines carry
// counts, never backend text, and the health accounting stays exact.
func TestDeliveryDiagnosticsRecoverySummaryKeepsCountsWithoutRawText(t *testing.T) {
	r := newDiagRig(t)
	now := time.Unix(1_000, 0)
	r.tracker.now = func() time.Time { return now }
	ctx := context.Background()

	r.backend.set(http.StatusServiceUnavailable)
	const failed = 4
	for range failed {
		if err := r.metrics(ctx); err == nil {
			t.Fatal("metrics export unexpectedly succeeded")
		}
		now = now.Add(r.tracker.summaryInterval) // every later failure crosses the summary boundary
	}
	r.backend.set(http.StatusOK)
	if err := r.metrics(ctx); err != nil {
		t.Fatalf("recovery export failed: %v", err)
	}

	out := r.logs.String()
	if strings.Contains(out, diagSentinel) {
		t.Fatalf("log leaked backend response text:\n%s", out)
	}
	for _, msg := range []string{"OTLP export failing", "OTLP export still failing", "OTLP export recovered"} {
		if !strings.Contains(out, msg) {
			t.Errorf("log lacks %q:\n%s", msg, out)
		}
	}
	// The recovery line itself must carry the counts: the last "still failing"
	// line also prints failed_exports=4, so a whole-log match proves nothing.
	var recovery string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "OTLP export recovered") {
			recovery = line
		}
	}
	if want := fmt.Sprintf("failed_exports=%d", failed); !strings.Contains(recovery, want) {
		t.Errorf("recovery line lacks %s: %q", want, recovery)
	}
	if want := "failing_since=" + time.Unix(1_000, 0).Format(time.RFC3339); !strings.Contains(recovery, want) {
		t.Errorf("recovery line lacks %s: %q", want, recovery)
	}

	st := r.state(SignalMetrics)
	if st.Exports != failed+1 || st.Failures != failed || st.ConsecutiveFailures != 0 ||
		st.LastErrorClass != errClassOther || st.LastSuccessAt.IsZero() {
		t.Errorf("health after recovery = %+v, want %d exports, %d failures, streak reset, class retained",
			st, failed+1, failed)
	}
}
