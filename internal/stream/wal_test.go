package stream_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/stream"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// TestHandler_DurableAppendStoresExactDecompressedBatchBeforeAck catches a WAL
// path that persists compressed/request metadata, appends more than once, or
// performs accepted-delivery effects inline instead of leaving them for replay.
func TestHandler_DurableAppendStoresExactDecompressedBatchBeforeAck(t *testing.T) {
	body := []byte(captureFlowRecord + "\n" + captureAuditRecord + "\n" + `{"future":"record"}`)
	compressed := gzipBytes(t, body)
	headerSecret := "must-not-enter-wal"

	var appended [][]byte
	var acceptedAt []time.Time
	var accepted []ingest.AcceptedEvent
	var ingestCalls []ingestCall
	rec := telemetrytest.New()
	s := stream.New(stream.Options{
		Token: testToken,
		OnIngest: func(source, signal string, records, bytes int) {
			ingestCalls = append(ingestCalls, ingestCall{source, signal, records, bytes})
		},
		OnAccepted: func(event ingest.AcceptedEvent) { accepted = append(accepted, event) },
	}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{NodeDims: true}),
		audit.NewProcessor(), rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		stream.WithDurableAppend(func(_ context.Context, got []byte, at time.Time) error {
			appended = append(appended, bytes.Clone(got))
			acceptedAt = append(acceptedAt, at)
			return nil
		}))

	headers := authHeader()
	headers.Set("Content-Encoding", "gzip")
	headers.Set("X-Private-Metadata", headerSecret)
	before := time.Now()
	resp := post(t, s.Handler(), http.MethodPost,
		"/services/collector/event?request-secret=must-not-enter-wal",
		headers, bytes.NewReader(compressed))
	after := time.Now()

	if resp.Code != http.StatusOK || resp.Body.String() != `{"text":"Success","code":0}` {
		t.Fatalf("response = (%d, %q), want HEC success", resp.Code, resp.Body.String())
	}
	if len(appended) != 1 {
		t.Fatalf("append calls = %d, want 1", len(appended))
	}
	if !bytes.Equal(appended[0], body) {
		t.Fatalf("appended bytes differ from exact decompressed body")
	}
	if strings.Contains(string(appended[0]), headerSecret) ||
		strings.Contains(string(appended[0]), testToken) ||
		strings.Contains(string(appended[0]), "request-secret") {
		t.Fatalf("appended bytes contain request metadata: %q", appended[0])
	}
	if len(acceptedAt) != 1 || acceptedAt[0].Before(before) || acceptedAt[0].After(after) {
		t.Fatalf("acceptedAt = %v, want one request-time timestamp in [%s, %s]", acceptedAt, before, after)
	}
	if len(accepted) != 0 || len(ingestCalls) != 0 {
		t.Fatalf("inline callbacks ran: accepted=%+v ingest=%+v", accepted, ingestCalls)
	}
	if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 0 {
		t.Fatalf("inline flow effects = %d points, want 0", len(got))
	}
	if got := rec.MetricPoints(audit.MetricAuditEvents); len(got) != 0 {
		t.Fatalf("inline audit effects = %d points, want 0", len(got))
	}
	if got := rec.MetricPoints(metricRecords); len(got) != 0 {
		t.Fatalf("inline receiver success points = %d, want 0", len(got))
	}
	if got := rec.MetricPoints(metricSkipped); len(got) != 0 {
		t.Fatalf("inline skipped points = %d, want 0", len(got))
	}
}

// TestHandler_DurableAppendFailureIsRetryableAndHasNoAcceptedEffects catches
// an ACK or any processor/callback/success effect escaping after fsync failure.
func TestHandler_DurableAppendFailureIsRetryableAndHasNoAcceptedEffects(t *testing.T) {
	const errorCanary = "/private/wal/tailnet-secret"
	var appendCalls int
	var accepted []ingest.AcceptedEvent
	var ingestCalls []ingestCall
	var logs bytes.Buffer
	rec := telemetrytest.New()
	s := stream.New(stream.Options{
		Token: testToken,
		OnIngest: func(source, signal string, records, bytes int) {
			ingestCalls = append(ingestCalls, ingestCall{source, signal, records, bytes})
		},
		OnAccepted: func(event ingest.AcceptedEvent) { accepted = append(accepted, event) },
	}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{NodeDims: true}),
		audit.NewProcessor(), rec.Emitter(), slog.New(slog.NewTextHandler(&logs, nil)),
		stream.WithDurableAppend(func(context.Context, []byte, time.Time) error {
			appendCalls++
			return errors.New("injected fsync failure at " + errorCanary)
		}))

	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(),
		strings.NewReader(captureFlowRecord+"\n"+captureAuditRecord))

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%q", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if strings.Contains(resp.Body.String(), errorCanary) || strings.Contains(logs.String(), errorCanary) {
		t.Fatalf("append error leaked: response=%q logs=%q", resp.Body.String(), logs.String())
	}
	if appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", appendCalls)
	}
	if len(accepted) != 0 || len(ingestCalls) != 0 {
		t.Fatalf("callbacks ran after append failure: accepted=%+v ingest=%+v", accepted, ingestCalls)
	}
	if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 0 {
		t.Fatalf("flow effects = %d points, want 0", len(got))
	}
	if got := rec.MetricPoints(audit.MetricAuditEvents); len(got) != 0 {
		t.Fatalf("audit effects = %d points, want 0", len(got))
	}
	if got := rec.MetricPoints(metricRecords); len(got) != 0 {
		t.Fatalf("success points = %d, want 0", len(got))
	}
	if got := findPoint(t, rec.MetricPoints(metricRejected),
		map[string]string{attrReason: "wal_unavailable"}).Value; got != 1 {
		t.Fatalf("rejected{reason=wal_unavailable} = %v, want 1", got)
	}
}

// TestHandler_DurableAppendRunsTrustBoundaryFirst catches an appender moved
// ahead of method/routing/exposure/CSRF/auth/decompression/structural/typed or
// semantic validation.
func TestHandler_DurableAppendRunsTrustBoundaryFirst(t *testing.T) {
	tests := []struct {
		name    string
		opts    stream.Options
		method  string
		path    string
		headers http.Header
		body    string
	}{
		{
			name:   "method",
			opts:   stream.Options{Token: testToken},
			method: http.MethodGet, path: "/services/collector/event",
			headers: authHeader(), body: captureFlowRecord,
		},
		{
			name:   "exact route",
			opts:   stream.Options{Token: testToken},
			method: http.MethodPost, path: "/services/collector/event/other",
			headers: authHeader(), body: captureFlowRecord,
		},
		{
			name:   "network exposure",
			opts:   stream.Options{Listen: "0.0.0.0:9099"},
			method: http.MethodPost, path: "/services/collector/event",
			body: captureFlowRecord,
		},
		{
			name:   "csrf",
			opts:   stream.Options{Listen: loopbackListen},
			method: http.MethodPost, path: "/services/collector/event",
			headers: http.Header{"Origin": {"https://attacker.example"}},
			body:    captureFlowRecord,
		},
		{
			name:   "auth",
			opts:   stream.Options{Token: testToken},
			method: http.MethodPost, path: "/services/collector/event",
			body: captureFlowRecord,
		},
		{
			name:   "decompression",
			opts:   stream.Options{Token: testToken},
			method: http.MethodPost, path: "/services/collector/event",
			headers: func() http.Header {
				h := authHeader()
				h.Set("Content-Encoding", "gzip")
				return h
			}(),
			body: "not-gzip",
		},
		{
			name:   "structural",
			opts:   stream.Options{Token: testToken},
			method: http.MethodPost, path: "/services/collector/event",
			headers: authHeader(), body: captureFlowRecord + `{"event":`,
		},
		{
			name:   "typed",
			opts:   stream.Options{Token: testToken},
			method: http.MethodPost, path: "/services/collector/event",
			headers: authHeader(),
			body:    `{"nodeId":"n1","virtualTraffic":"not-an-array"}`,
		},
		{
			name:   "flow semantic",
			opts:   stream.Options{Token: testToken},
			method: http.MethodPost, path: "/services/collector/event",
			headers: authHeader(),
			body:    strings.Replace(captureFlowRecord, `"txBytes":6420`, `"txBytes":-1`, 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var appendCalls int
			rec := telemetrytest.New()
			s := stream.New(tt.opts,
				flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
				audit.NewProcessor(), rec.Emitter(), nil,
				stream.WithDurableAppend(func(context.Context, []byte, time.Time) error {
					appendCalls++
					return nil
				}))
			resp := post(t, s.Handler(), tt.method, tt.path, tt.headers, strings.NewReader(tt.body))
			if resp.Code >= 200 && resp.Code < 300 {
				t.Fatalf("status = %d, want refusal before append", resp.Code)
			}
			if appendCalls != 0 {
				t.Fatalf("append calls = %d, want 0", appendCalls)
			}
			if got := rec.MetricPoints(metricRecords); len(got) != 0 {
				t.Fatalf("success points = %d, want 0", len(got))
			}
		})
	}
}

// TestHandler_DurableAppendRunsConnectionBudgetFirst catches a nested-flow
// admission guard accidentally deferred until after the batch is durable.
func TestHandler_DurableAppendRunsConnectionBudgetFirst(t *testing.T) {
	const overBudgetConnections = 500_001
	body := `{"nodeId":"n1","virtualTraffic":[` +
		strings.Repeat(`{},`, overBudgetConnections-1) + `{}` + `]}`
	var appendCalls int
	s := stream.New(stream.Options{Token: testToken},
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		audit.NewProcessor(), telemetrytest.New().Emitter(), nil,
		stream.WithDurableAppend(func(context.Context, []byte, time.Time) error {
			appendCalls++
			return nil
		}))

	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event",
		authHeader(), strings.NewReader(body))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", resp.Code, resp.Body.String())
	}
	if appendCalls != 0 {
		t.Fatalf("append calls = %d, want 0", appendCalls)
	}
}

// TestHandler_DurableAppendRunsAdmissionBudgetFirst catches an appender that
// bypasses the receiver's aggregate in-flight body budget.
func TestHandler_DurableAppendRunsAdmissionBudgetFirst(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var appendCalls atomic.Int32
	s := stream.New(stream.Options{Token: testToken, MaxConcurrentRequests: 1},
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		audit.NewProcessor(), telemetrytest.New().Emitter(), nil,
		stream.WithDurableAppend(func(context.Context, []byte, time.Time) error {
			if appendCalls.Add(1) == 1 {
				close(entered)
				<-unblock
			}
			return nil
		}))

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- post(t, s.Handler(), http.MethodPost, "/services/collector/event",
			authHeader(), strings.NewReader(captureFlowRecord))
	}()
	<-entered

	second := post(t, s.Handler(), http.MethodPost, "/services/collector/event",
		authHeader(), strings.NewReader(captureFlowRecord))
	if second.Code != http.StatusServiceUnavailable {
		close(unblock)
		<-firstDone
		t.Fatalf("second status = %d, want 503; body=%q", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got != "1" {
		close(unblock)
		<-firstDone
		t.Fatalf("second Retry-After = %q, want 1", got)
	}
	if got := appendCalls.Load(); got != 1 {
		close(unblock)
		<-firstDone
		t.Fatalf("append calls while first request holds admission = %d, want 1", got)
	}

	close(unblock)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%q", first.Code, first.Body.String())
	}
}

type cancelAtEOFReader struct {
	data   []byte
	cancel context.CancelFunc
}

func (r *cancelAtEOFReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	r.cancel()
	return 0, io.EOF
}

// TestHandler_DurableAppendChecksContextImmediatelyBeforeAppend catches a body
// made durable after its request was already canceled during the validation
// phase.
func TestHandler_DurableAppendChecksContextImmediatelyBeforeAppend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var appendCalls int
	s := stream.New(stream.Options{Token: testToken},
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		audit.NewProcessor(), telemetrytest.New().Emitter(), nil,
		stream.WithDurableAppend(func(context.Context, []byte, time.Time) error {
			appendCalls++
			return nil
		}))
	req := httptest.NewRequest(http.MethodPost, "/services/collector/event",
		&cancelAtEOFReader{data: []byte(captureFlowRecord), cancel: cancel}).WithContext(ctx)
	req.Header = authHeader()
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%q", w.Code, w.Body.String())
	}
	if appendCalls != 0 {
		t.Fatalf("append calls = %d, want 0", appendCalls)
	}
}

// TestHandler_DurableAppendDoesNotRecheckContextAfterSuccess catches a sender
// retry caused only because its request context was canceled during a successful
// durable publication.
func TestHandler_DurableAppendDoesNotRecheckContextAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var appendCalls int
	s := stream.New(stream.Options{Token: testToken},
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		audit.NewProcessor(), telemetrytest.New().Emitter(), nil,
		stream.WithDurableAppend(func(context.Context, []byte, time.Time) error {
			appendCalls++
			cancel()
			return nil
		}))
	req := httptest.NewRequest(http.MethodPost, "/services/collector/event",
		strings.NewReader(captureFlowRecord)).WithContext(ctx)
	req.Header = authHeader()
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after successful append; body=%q", w.Code, w.Body.String())
	}
	if appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", appendCalls)
	}
}

// TestApplyDurable_ReconstructsAndAppliesAcceptedBatch catches replay that
// reorders signals, substitutes replay time for persisted acceptance time, or
// omits accepted receiver metrics/callbacks.
func TestApplyDurable_ReconstructsAndAppliesAcceptedBatch(t *testing.T) {
	body := []byte(captureAuditRecord + "\n" + captureFlowRecord + "\n" +
		`{"future":"record"}` + "\n" + `{"event":null}`)
	persistedAt := time.Date(2026, 7, 26, 18, 8, 54, 123, time.UTC)
	var accepted []ingest.AcceptedEvent
	var ingestCalls []ingestCall
	rec := telemetrytest.New()
	s := stream.New(stream.Options{
		OnIngest: func(source, signal string, records, bytes int) {
			ingestCalls = append(ingestCalls, ingestCall{source, signal, records, bytes})
		},
		OnAccepted: func(event ingest.AcceptedEvent) { accepted = append(accepted, event) },
	}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{NodeDims: true}),
		audit.NewProcessor(), rec.Emitter(), nil)

	result, err := s.ApplyDurable(context.Background(), body, persistedAt)
	if err != nil {
		t.Fatalf("ApplyDurable: %v", err)
	}
	if !result.FlowsApplied {
		t.Fatal("FlowsApplied = false, want true")
	}
	if len(accepted) != 2 {
		t.Fatalf("accepted events = %d, want 2: %+v", len(accepted), accepted)
	}
	if accepted[0].Signal != semconv.IngestSignalFlow || accepted[1].Signal != semconv.IngestSignalAudit {
		t.Fatalf("accepted signal order = [%q %q], want [flow audit]", accepted[0].Signal, accepted[1].Signal)
	}
	for i, event := range accepted {
		if !event.AcceptedAt.Equal(persistedAt) {
			t.Errorf("accepted event %d time = %s, want persisted %s", i, event.AcceptedAt, persistedAt)
		}
	}
	if len(ingestCalls) != 3 {
		t.Fatalf("OnIngest calls = %d, want 3: %+v", len(ingestCalls), ingestCalls)
	}
	if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 4 {
		t.Fatalf("flow IO points = %d, want 4", len(got))
	}
	if got := rec.MetricPoints(audit.MetricAuditEvents); len(got) != 1 {
		t.Fatalf("audit points = %d, want 1", len(got))
	}
	if got := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}).Value; got != 1 {
		t.Fatalf("records{flow} = %v, want 1", got)
	}
	if got := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeAudit}).Value; got != 1 {
		t.Fatalf("records{audit} = %v, want 1", got)
	}
	if got := findPoint(t, rec.MetricPoints(metricSkipped), map[string]string{attrReason: reasonUnclassified}).Value; got != 1 {
		t.Fatalf("skipped{unclassified} = %v, want 1", got)
	}
	if got := findPoint(t, rec.MetricPoints(metricSkipped), map[string]string{attrReason: reasonUnwrapDrop}).Value; got != 1 {
		t.Fatalf("skipped{unwrap_drop} = %v, want 1", got)
	}
}

// TestApplyDurable_DoesNotRevalidateRequestTimeSemantics catches replay using a
// changed semantic policy to strand an entry that was already accepted.
func TestApplyDurable_DoesNotRevalidateRequestTimeSemantics(t *testing.T) {
	body := []byte(strings.Replace(captureFlowRecord, `"txBytes":6420`, `"txBytes":-1`, 1))
	s, rec := newServer(t, stream.Options{})

	result, err := s.ApplyDurable(context.Background(), body, time.Now())
	if err != nil {
		t.Fatalf("ApplyDurable: %v", err)
	}
	if !result.FlowsApplied {
		t.Fatal("FlowsApplied = false, want true")
	}
	if got := rec.MetricPoints(metricRecords); len(got) != 1 {
		t.Fatalf("receiver success points = %d, want 1", len(got))
	}
}

// TestApplyDurable_CancellationIsAtomicAtApplicationBoundary catches replay
// cancellation after one effect returning an error with a partially applied
// batch. Cancellation before application yields zero effects; once application
// starts, the complete batch is applied exactly once in the current process.
func TestApplyDurable_CancellationIsAtomicAtApplicationBoundary(t *testing.T) {
	t.Run("canceled before application", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var accepted []ingest.AcceptedEvent
		rec := telemetrytest.New()
		s := stream.New(stream.Options{
			OnAccepted: func(event ingest.AcceptedEvent) { accepted = append(accepted, event) },
		}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
			audit.NewProcessor(), rec.Emitter(), nil)

		result, err := s.ApplyDurable(ctx, []byte(captureFlowRecord), time.Now())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ApplyDurable error = %v, want context.Canceled", err)
		}
		if result.FlowsApplied || len(accepted) != 0 {
			t.Fatalf("pre-entry effects: result=%+v accepted=%+v", result, accepted)
		}
		if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 0 {
			t.Fatalf("flow effects = %d, want 0", len(got))
		}
	})

	t.Run("canceled during application", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var accepted []ingest.AcceptedEvent
		rec := telemetrytest.New()
		s := stream.New(stream.Options{
			OnAccepted: func(event ingest.AcceptedEvent) {
				accepted = append(accepted, event)
				if len(accepted) == 1 {
					cancel()
				}
			},
		}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
			audit.NewProcessor(), rec.Emitter(), nil)
		body := []byte(captureFlowRecord + "\n" + bareFlowRecord + "\n" + captureAuditRecord)

		result, err := s.ApplyDurable(ctx, body, time.Now())
		if err != nil {
			t.Fatalf("ApplyDurable returned partial-apply error: %v", err)
		}
		if !result.FlowsApplied {
			t.Fatal("FlowsApplied = false, want true")
		}
		if len(accepted) != 3 {
			t.Fatalf("accepted events = %d, want complete batch of 3: %+v", len(accepted), accepted)
		}
		if got := findPoint(t, rec.MetricPoints(metricRecords),
			map[string]string{attrType: typeFlow}).Value; got != 2 {
			t.Fatalf("records{flow} = %v, want 2", got)
		}
		if got := findPoint(t, rec.MetricPoints(metricRecords),
			map[string]string{attrType: typeAudit}).Value; got != 1 {
			t.Fatalf("records{audit} = %v, want 1", got)
		}
	})
}

// TestApplyDurable_CorruptBodyFailsOpaque catches a stored-corrupt body being
// accepted, partially applied, or copied into logs/errors.
func TestApplyDurable_CorruptBodyFailsOpaque(t *testing.T) {
	const secret = "body-secret-must-not-be-logged"
	body := []byte(`{"nodeId":"` + secret + `","virtualTraffic":`)
	var logs bytes.Buffer
	rec := telemetrytest.New()
	s := stream.New(stream.Options{},
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		audit.NewProcessor(), rec.Emitter(), slog.New(slog.NewTextHandler(&logs, nil)))

	result, err := s.ApplyDurable(context.Background(), body, time.Now())
	if err == nil {
		t.Fatal("ApplyDurable error = nil, want stored-corrupt refusal")
	}
	if result.FlowsApplied {
		t.Fatal("FlowsApplied = true after corrupt body")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
		t.Fatalf("stored body leaked: error=%q logs=%q", err, logs.String())
	}
	if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 0 {
		t.Fatalf("flow effects = %d, want 0", len(got))
	}
	if got := rec.MetricPoints(metricRecords); len(got) != 0 {
		t.Fatalf("success effects = %d, want 0", len(got))
	}
}

// TestWithDurableAppendNilPreservesSynchronousPath catches the optional seam
// accidentally turning nil into asynchronous/durable mode.
func TestWithDurableAppendNilPreservesSynchronousPath(t *testing.T) {
	var accepted []ingest.AcceptedEvent
	var ingestCalls []ingestCall
	rec := telemetrytest.New()
	s := stream.New(stream.Options{
		Token: testToken,
		OnIngest: func(source, signal string, records, bytes int) {
			ingestCalls = append(ingestCalls, ingestCall{source, signal, records, bytes})
		},
		OnAccepted: func(event ingest.AcceptedEvent) { accepted = append(accepted, event) },
	}, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{NodeDims: true}),
		audit.NewProcessor(), rec.Emitter(), nil, stream.WithDurableAppend(nil))

	resp := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(),
		strings.NewReader(captureFlowRecord+"\n"+captureAuditRecord))

	if resp.Code != http.StatusOK || resp.Body.String() != `{"text":"Success","code":0}` {
		t.Fatalf("response = (%d, %q), want synchronous HEC success", resp.Code, resp.Body.String())
	}
	if len(accepted) != 2 || len(ingestCalls) != 3 {
		t.Fatalf("synchronous callbacks = accepted:%d ingest:%d, want 2/3", len(accepted), len(ingestCalls))
	}
	assertFlowAndAuditOnce(t, rec)
}
