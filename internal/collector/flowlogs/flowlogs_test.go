package flowlogs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// Compile-time guarantees: *Collector is a WindowCollector and the test fake
// satisfies the (unexported) api this package depends on.
var (
	_ collector.WindowCollector = (*Collector)(nil)
	_ api                       = (*fakeAPI)(nil)
)

// fakeAPI is a canned NetworkFlowLogs source recording the window it was asked
// for, so tests can assert delegation and error propagation.
type fakeAPI struct {
	resp  flowlog.NetworkResponse
	err   error
	calls int
	start time.Time
	end   time.Time
}

func (f *fakeAPI) NetworkFlowLogs(_ context.Context, start, end time.Time) (flowlog.NetworkResponse, error) {
	f.calls++
	f.start, f.end = start, end
	return f.resp, f.err
}

// newProcessor builds a real flowlog.Processor over an empty cache with node
// dimensions enabled, matching production wiring.
func newProcessor() *flowlog.Processor {
	return flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{NodeDims: true})
}

// oneTCPResponse is a NetworkResponse with a single TCP virtual connection.
func oneTCPResponse() flowlog.NetworkResponse {
	return flowlog.NetworkResponse{
		Logs: []flowlog.FlowLog{
			{
				NodeID: "n-laptop",
				Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
				Logged: time.Date(2026, 6, 2, 12, 1, 5, 0, time.UTC),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{
						Proto:   6, // tcp (numeric IANA protocol number, per real API)
						Src:     "100.64.0.1:12345",
						Dst:     "100.64.0.2:443",
						TxPkts:  10,
						TxBytes: 1000,
						RxPkts:  8,
						RxBytes: 800,
					},
				},
			},
		},
	}
}

func TestCollectWindow_DelegatesAndAdvances(t *testing.T) {
	a := &fakeAPI{resp: oneTCPResponse()}
	c := New(a, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}
	if !hwm.Equal(to) {
		t.Fatalf("CollectWindow() high-water mark = %v, want %v (to)", hwm, to)
	}
	if a.calls != 1 {
		t.Fatalf("NetworkFlowLogs calls = %d, want 1", a.calls)
	}
	if !a.start.Equal(from) || !a.end.Equal(to) {
		t.Fatalf("NetworkFlowLogs window = [%v,%v], want [%v,%v]", a.start, a.end, from, to)
	}

	// The shared processor must have emitted the io metric for our connection
	// (tx + rx => two data points on the bytes counter).
	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatalf("MetricPoints(%q) = 0, want >0 (processor should have run)", flowlog.MetricIO)
	}
}

func TestCollectWindow_APIErrorDoesNotAdvance(t *testing.T) {
	wantErr := errors.New("boom")
	a := &fakeAPI{err: wantErr}
	c := New(a, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if !errors.Is(err, wantErr) {
		t.Fatalf("CollectWindow() error = %v, want %v", err, wantErr)
	}
	if !hwm.IsZero() {
		t.Fatalf("CollectWindow() high-water mark = %v, want zero time on error", hwm)
	}
	// Nothing should have been processed/emitted.
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
		t.Fatalf("MetricPoints(%q) = %d, want 0 on error", flowlog.MetricIO, len(pts))
	}
}

func TestNameLagAndDefaultInterval(t *testing.T) {
	// Defaults: zero interval -> 60s, zero lag -> 120s.
	def := New(&fakeAPI{}, newProcessor(), 0, 0, nil, nil)
	if def.Name() != "flowlogs" {
		t.Fatalf("Name() = %q, want flowlogs", def.Name())
	}
	if got := def.DefaultInterval(); got != 60*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 60s (zero default)", got)
	}
	if got := def.Lag(); got != 120*time.Second {
		t.Fatalf("Lag() = %v, want 120s (zero default)", got)
	}

	// Overrides are honored.
	ovr := New(&fakeAPI{}, newProcessor(), 30*time.Second, 45*time.Second, nil, nil)
	if got := ovr.DefaultInterval(); got != 30*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 30s (override)", got)
	}
	if got := ovr.Lag(); got != 45*time.Second {
		t.Fatalf("Lag() = %v, want 45s (override)", got)
	}
}

// sumIO totals every recorded value on the io bytes counter.
func sumIO(rec *telemetrytest.Recorder) float64 {
	var total float64
	for _, p := range rec.MetricPoints(flowlog.MetricIO) {
		total += p.Value
	}
	return total
}

// TestCollectWindow_BoundaryDedup verifies that a connection straddling the
// inclusive boundary of two adjacent windows is counted only once across the
// two ticks. The API window is inclusive of both ends, so a node's flow record
// can be returned in both windows; the collector's de-dup set must drop the
// repeat before the processor runs.
func TestCollectWindow_BoundaryDedup(t *testing.T) {
	resp := oneTCPResponse()
	a := &fakeAPI{resp: resp}
	c := New(a, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	w1from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	w1to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)
	// Second window shares the boundary with the first and the same canned
	// response, simulating the boundary record appearing twice.
	w2from := w1to
	w2to := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	if _, err := c.CollectWindow(context.Background(), w1from, w1to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() window 1 error = %v", err)
	}
	firstTotal := sumIO(rec)
	if firstTotal == 0 {
		t.Fatalf("io total after window 1 = 0, want >0 (first sighting must be processed)")
	}

	if _, err := c.CollectWindow(context.Background(), w2from, w2to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() window 2 error = %v", err)
	}
	secondTotal := sumIO(rec)

	// The connection was identical in both windows, so the second tick must add
	// nothing: a monotonic counter's cumulative total stays put.
	if secondTotal != firstTotal {
		t.Fatalf("io total after window 2 = %v, want %v (boundary connection counted once)", secondTotal, firstTotal)
	}
	// Both fetches still happen; only emission is suppressed.
	if a.calls != 2 {
		t.Fatalf("NetworkFlowLogs calls = %d, want 2", a.calls)
	}
}

// featurePoint returns the single tailscale.feature.enabled point, or fails.
func featurePoint(t *testing.T, rec *telemetrytest.Recorder) telemetrytest.MetricPoint {
	t.Helper()
	pts := rec.MetricPoints(metricFeatureEnabled)
	if len(pts) != 1 {
		t.Fatalf("MetricPoints(%q) = %d, want 1", metricFeatureEnabled, len(pts))
	}
	return pts[0]
}

// TestCollectWindow_FeatureCheckEnabled verifies that when featureCheck reports
// (true, nil) the collector emits feature.enabled=1 and processes the window.
func TestCollectWindow_FeatureCheckEnabled(t *testing.T) {
	a := &fakeAPI{resp: oneTCPResponse()}
	c := New(a, newProcessor(), 0, 0, func(context.Context) (bool, error) { return true, nil }, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}
	if !hwm.Equal(to) {
		t.Fatalf("CollectWindow() high-water mark = %v, want %v", hwm, to)
	}
	if a.calls != 1 {
		t.Fatalf("NetworkFlowLogs calls = %d, want 1 (enabled must fetch)", a.calls)
	}

	p := featurePoint(t, rec)
	if p.Value != 1 {
		t.Fatalf("feature.enabled = %v, want 1", p.Value)
	}
	if got := p.Attrs[semconv.AttrFeature]; got != "network_flow_logging" {
		t.Fatalf("feature attr = %q, want network_flow_logging", got)
	}
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) == 0 {
		t.Fatalf("MetricPoints(%q) = 0, want >0 (window processed)", flowlog.MetricIO)
	}
}

// TestCollectWindow_FeatureCheckDisabled verifies that when featureCheck reports
// (false, nil) the collector emits feature.enabled=0, skips the fetch, and
// returns the window end with no error (idle, not a transient failure).
func TestCollectWindow_FeatureCheckDisabled(t *testing.T) {
	a := &fakeAPI{resp: oneTCPResponse()}
	c := New(a, newProcessor(), 0, 0, func(context.Context) (bool, error) { return false, nil }, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if err != nil {
		t.Fatalf("CollectWindow() error = %v, want nil (disabled is not a failure)", err)
	}
	if !hwm.Equal(to) {
		t.Fatalf("CollectWindow() high-water mark = %v, want %v", hwm, to)
	}
	if a.calls != 0 {
		t.Fatalf("NetworkFlowLogs calls = %d, want 0 (disabled must skip fetch)", a.calls)
	}

	p := featurePoint(t, rec)
	if p.Value != 0 {
		t.Fatalf("feature.enabled = %v, want 0", p.Value)
	}
	if got := p.Attrs[semconv.AttrFeature]; got != "network_flow_logging" {
		t.Fatalf("feature attr = %q, want network_flow_logging", got)
	}
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
		t.Fatalf("MetricPoints(%q) = %d, want 0 (nothing fetched)", flowlog.MetricIO, len(pts))
	}
}

// TestCollectWindow_FeatureCheckErrorFailsOpen verifies that a featureCheck
// error does not block collection: the collector proceeds as enabled and does
// not emit the feature gauge.
func TestCollectWindow_FeatureCheckErrorFailsOpen(t *testing.T) {
	a := &fakeAPI{resp: oneTCPResponse()}
	c := New(a, newProcessor(), 0, 0, func(context.Context) (bool, error) {
		return false, errors.New("transient settings error")
	}, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
	if err != nil {
		t.Fatalf("CollectWindow() error = %v, want nil (fail-open)", err)
	}
	if !hwm.Equal(to) {
		t.Fatalf("CollectWindow() high-water mark = %v, want %v", hwm, to)
	}
	if a.calls != 1 {
		t.Fatalf("NetworkFlowLogs calls = %d, want 1 (fail-open must fetch)", a.calls)
	}
	if pts := rec.MetricPoints(metricFeatureEnabled); len(pts) != 0 {
		t.Fatalf("MetricPoints(%q) = %d, want 0 (no gauge on check error)", metricFeatureEnabled, len(pts))
	}
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) == 0 {
		t.Fatalf("MetricPoints(%q) = 0, want >0 (window processed)", flowlog.MetricIO)
	}
}

// TestCollectWindow_OnIngestHookCalled verifies that after a successful window
// the onIngest hook is called exactly once with ("poll","flow",N,0) where N is
// the post-dedup record count.
func TestCollectWindow_OnIngestHookCalled(t *testing.T) {
	// Three FlowLog entries with distinct connections so none are deduped away.
	resp := flowlog.NetworkResponse{
		Logs: []flowlog.FlowLog{
			{
				NodeID: "n-alpha",
				Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
				Logged: time.Date(2026, 6, 2, 12, 1, 5, 0, time.UTC),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: 6, Src: "100.64.0.1:10001", Dst: "100.64.0.2:443", TxPkts: 1, RxPkts: 1},
				},
			},
			{
				NodeID: "n-beta",
				Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
				Logged: time.Date(2026, 6, 2, 12, 1, 5, 0, time.UTC),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: 6, Src: "100.64.0.3:10002", Dst: "100.64.0.4:80", TxPkts: 2, RxPkts: 2},
				},
			},
			{
				NodeID: "n-gamma",
				Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
				Logged: time.Date(2026, 6, 2, 12, 1, 5, 0, time.UTC),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: 17, Src: "100.64.0.5:10003", Dst: "100.64.0.6:53", TxPkts: 3, RxPkts: 3},
				},
			},
		},
	}

	type call struct {
		source  string
		signal  string
		records int
		bytes   int
	}
	var got []call
	hook := func(source, signal string, records, bytes int) {
		got = append(got, call{source, signal, records, bytes})
	}

	a := &fakeAPI{resp: resp}
	c := New(a, newProcessor(), 0, 0, nil, hook)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("hook called %d times, want 1", len(got))
	}
	want := call{semconv.IngestSourcePoll, semconv.IngestSignalFlow, 3, 0}
	if got[0] != want {
		t.Fatalf("hook call = %+v, want %+v", got[0], want)
	}
}

// TestCollectWindow_NilOnIngestHookDoesNotPanic verifies that omitting the hook
// (nil) does not cause a nil-pointer dereference on a normal window.
func TestCollectWindow_NilOnIngestHookDoesNotPanic(t *testing.T) {
	a := &fakeAPI{resp: oneTCPResponse()}
	c := New(a, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}
}

// TestCollectWindow_Forbidden403DisablesFeature verifies that an HTTP 403 /
// forbidden error from the flow-log fetch is treated as the feature being
// disabled: feature.enabled=0 is emitted and the window end is returned with no
// error, so the scheduler advances rather than retrying.
func TestCollectWindow_Forbidden403DisablesFeature(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"403 status", errors.New("flow logs: unexpected status 403")},
		{"forbidden word", errors.New("request Forbidden: feature requires Premium")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAPI{err: tc.err}
			c := New(a, newProcessor(), 0, 0, nil, nil)
			rec := telemetrytest.New()

			from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
			to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

			hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
			if err != nil {
				t.Fatalf("CollectWindow() error = %v, want nil (403 is disabled, not transient)", err)
			}
			if !hwm.Equal(to) {
				t.Fatalf("CollectWindow() high-water mark = %v, want %v", hwm, to)
			}

			p := featurePoint(t, rec)
			if p.Value != 0 {
				t.Fatalf("feature.enabled = %v, want 0", p.Value)
			}
			if got := p.Attrs[semconv.AttrFeature]; got != "network_flow_logging" {
				t.Fatalf("feature attr = %q, want network_flow_logging", got)
			}
			if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
				t.Fatalf("MetricPoints(%q) = %d, want 0 (nothing processed)", flowlog.MetricIO, len(pts))
			}
		})
	}
}
