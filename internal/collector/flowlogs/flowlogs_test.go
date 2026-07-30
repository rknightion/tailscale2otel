package flowlogs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
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
	resp      flowlog.NetworkResponse
	responses []flowlog.NetworkResponse
	err       error
	calls     int
	start     time.Time
	end       time.Time
}

func (f *fakeAPI) NetworkFlowLogs(_ context.Context, start, end time.Time) (flowlog.NetworkResponse, error) {
	f.calls++
	f.start, f.end = start, end
	if f.calls <= len(f.responses) {
		return f.responses[f.calls-1], f.err
	}
	return f.resp, f.err
}

type captureStore struct {
	observations []flowstore.Observation
}

func (s *captureStore) Record(o flowstore.Observation) {
	s.observations = append(s.observations, o)
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

func TestCollectWindow_BoundaryDedupKeepsSameTupleInDifferentTrafficClasses(t *testing.T) {
	// Poll-boundary de-duplication uses the same identity as cross-source
	// de-duplication, including the bounded traffic class.
	resp := oneTCPResponse()
	resp.Logs[0].SubnetTraffic = append([]flowlog.ConnectionCounts(nil), resp.Logs[0].VirtualTraffic...)
	c := New(&fakeAPI{resp: resp}, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	if _, err := c.CollectWindow(context.Background(), from, from.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}

	if got, want := sumIO(rec), float64(3600); got != want {
		t.Fatalf("io total = %v, want %v (virtual and subnet both emit)", got, want)
	}
}

func TestCollectWindow_BoundaryDedupReportsConflictingCounters(t *testing.T) {
	// An overlapping poll response can repeat a connection with revised counts.
	// It must retain the first exported counters and make the discrepancy visible
	// without adding the raw identity or counter values as metric labels.
	first := oneTCPResponse()
	changed := oneTCPResponse()
	changed.Logs[0].VirtualTraffic[0].RxBytes = 9000
	c := New(&fakeAPI{responses: []flowlog.NetworkResponse{first, changed}}, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	if _, err := c.CollectWindow(context.Background(), from, from.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() first window: %v", err)
	}
	if _, err := c.CollectWindow(context.Background(), from.Add(time.Minute), from.Add(2*time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() second window: %v", err)
	}

	if got, want := sumIO(rec), float64(1800); got != want {
		t.Fatalf("io total = %v, want %v (first poll values win)", got, want)
	}
	pts := rec.MetricPoints(flowlog.MetricDedupConflicts)
	if len(pts) != 1 {
		t.Fatalf("dedup conflict points = %d, want 1", len(pts))
	}
	if got, want := pts[0].Attrs["scope"], flowlog.DedupScopePollBoundary; got != want {
		t.Errorf("conflict scope = %q, want %q", got, want)
	}
	if got, want := pts[0].Attrs[semconv.AttrTrafficType], semconv.TrafficVirtual; got != want {
		t.Errorf("conflict traffic type = %q, want %q", got, want)
	}
	if len(pts[0].Attrs) != 2 {
		t.Errorf("conflict attrs = %#v, want exactly scope and traffic type", pts[0].Attrs)
	}
}

func TestCollectWindow_DropsOnlySemanticallyInvalidRecords(t *testing.T) {
	resp := oneTCPResponse()
	invalid := resp.Logs[0]
	invalid.NodeID = "invalid-node"
	invalid.VirtualTraffic = append([]flowlog.ConnectionCounts(nil), invalid.VirtualTraffic...)
	invalid.VirtualTraffic[0].TxBytes = -1
	resp.Logs = append([]flowlog.FlowLog{invalid}, resp.Logs...)
	c := New(&fakeAPI{resp: resp}, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()
	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)

	if _, err := c.CollectWindow(context.Background(), from, from.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}

	if got, want := sumIO(rec), float64(1800); got != want {
		t.Fatalf("io total = %v, want %v from the valid record only", got, want)
	}
	points := rec.MetricPoints(flowlog.MetricDataQuality)
	if len(points) != 1 {
		t.Fatalf("data-quality points = %d, want 1", len(points))
	}
	if got := points[0].Attrs["source"]; got != "poll" {
		t.Errorf("source = %q, want poll", got)
	}
	if got := points[0].Attrs["reason"]; got != string(flowlog.ViolationNegativeCounters) {
		t.Errorf("reason = %q, want %q", got, flowlog.ViolationNegativeCounters)
	}
}

func TestCollectWindow_BoundaryDedupPreservesEmbeddedIdentity(t *testing.T) {
	first := oneTCPResponse()
	firstLog := &first.Logs[0]
	firstLog.SrcNode = &flowlog.NodeRef{
		NodeID:    "n-laptop",
		Name:      "laptop.example.ts.net",
		Addresses: []string{"100.64.0.1"},
		Tags:      []string{"tag:workstations"},
	}
	firstLog.DstNodes = []flowlog.NodeRef{{
		NodeID:    "n-server",
		Name:      "server.example.ts.net",
		Addresses: []string{"100.64.0.2"},
		User:      "operator@example.com",
		OS:        "linux",
	}}

	second := first
	second.Logs = append([]flowlog.FlowLog(nil), first.Logs...)
	second.Logs[0].VirtualTraffic = append(
		append([]flowlog.ConnectionCounts(nil), firstLog.VirtualTraffic...),
		flowlog.ConnectionCounts{
			Proto:   6,
			Src:     "100.64.0.1:23456",
			Dst:     "100.64.0.2:443",
			TxPkts:  5,
			TxBytes: 500,
			RxPkts:  4,
			RxBytes: 400,
		},
	)

	store := &captureStore{}
	proc := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
		NodeDims: true,
		Store:    store,
	})
	c := New(&fakeAPI{responses: []flowlog.NetworkResponse{first, second}}, proc, 0, 0, nil, nil)
	rec := telemetrytest.New()

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	if _, err := c.CollectWindow(context.Background(), from, from.Add(time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() first window: %v", err)
	}
	if _, err := c.CollectWindow(context.Background(), from.Add(time.Minute), from.Add(2*time.Minute), rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() second window: %v", err)
	}

	logs := rec.LogRecords()
	if len(logs) != 2 {
		t.Fatalf("log records = %d, want 2 (boundary duplicate suppressed and new connection emitted)", len(logs))
	}
	if got := logs[1].Attrs[semconv.AttrSrcTags]; got != "tag:workstations" {
		t.Errorf("new connection %s = %q, want tag:workstations", semconv.AttrSrcTags, got)
	}
	if got := logs[1].Attrs[semconv.AttrDstUser]; got != "operator@example.com" {
		t.Errorf("new connection %s = %q, want operator@example.com", semconv.AttrDstUser, got)
	}

	directStore := &captureStore{}
	direct := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
		NodeDims: true,
		Store:    directStore,
	})
	directFlow := second.Logs[0]
	directFlow.VirtualTraffic = directFlow.VirtualTraffic[1:]
	direct.Process(directFlow, telemetrytest.New().Emitter())

	if len(store.observations) != 2 {
		t.Fatalf("store observations = %d, want 2", len(store.observations))
	}
	if len(directStore.observations) != 1 {
		t.Fatalf("direct store observations = %d, want 1", len(directStore.observations))
	}
	if got, want := store.observations[1], directStore.observations[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("deduped store observation differs from direct processing:\n got: %+v\nwant: %+v", got, want)
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

// TestCollectWindow_Forbidden403DisablesFeature verifies that a genuine HTTP
// 403 response from the flow-log fetch — surfaced as a *tsapi.StatusError with
// Code 403, optionally wrapped — is treated as the feature being disabled:
// feature.enabled=0 is emitted and the window end is returned with no error, so
// the scheduler advances rather than retrying.
func TestCollectWindow_Forbidden403DisablesFeature(t *testing.T) {
	statusErr := &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/api/v2/tailnet/-/network-logging/configuration", Code: 403, Body: "feature disabled"}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"bare StatusError", statusErr},
		{"wrapped StatusError", fmt.Errorf("flow logs: %w", statusErr)},
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

// TestCollectWindow_AmbiguousErrorTextNotMisclassified verifies the fix for
// issue #95: a fetch error whose *text* happens to contain "403" or
// "forbidden" — but which is NOT a *tsapi.StatusError with Code 403 (e.g. a
// proxy-port number embedded in the error, or a 5xx error whose HTML body
// mentions "Forbidden") — must NOT be classified as the feature being
// disabled. It must propagate as a real error with a zero high-water mark, so
// the scheduler retries the window instead of silently advancing past it.
func TestCollectWindow_AmbiguousErrorTextNotMisclassified(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"403 substring from a non-tsapi error", errors.New("dial tcp 10.0.0.1:8403: connect: connection refused")},
		{"forbidden substring from a non-403 StatusError", &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/api/v2/tailnet/-/network-logging/configuration", Code: 502, Body: "<html><body>403 Forbidden by upstream proxy</body></html>"}},
		{"wrapped forbidden substring, not a StatusError", fmt.Errorf("flow logs: %w", errors.New("request Forbidden: internal proxy error"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAPI{err: tc.err}
			c := New(a, newProcessor(), 0, 0, nil, nil)
			rec := telemetrytest.New()

			from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
			to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)

			hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
			if !errors.Is(err, tc.err) {
				t.Fatalf("CollectWindow() error = %v, want %v propagated (not misclassified as 403)", err, tc.err)
			}
			if !hwm.IsZero() {
				t.Fatalf("CollectWindow() high-water mark = %v, want zero time (checkpoint must not advance)", hwm)
			}
			if pts := rec.MetricPoints(metricFeatureEnabled); len(pts) != 0 {
				t.Fatalf("MetricPoints(%q) = %d, want 0 (must not emit feature=0 on an ambiguous error)", metricFeatureEnabled, len(pts))
			}
			if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
				t.Fatalf("MetricPoints(%q) = %d, want 0 (nothing processed)", flowlog.MetricIO, len(pts))
			}
		})
	}
}

// availabilityStates returns, per operation, the single state whose gauge is 1.
func availabilityStates(t *testing.T, rec *telemetrytest.Recorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		op := p.Attrs["tailscale.api.operation"]
		st := p.Attrs["tailscale.api.state"]
		switch p.Value {
		case 1:
			if prev, dup := out[op]; dup {
				t.Fatalf("operation %q has two states at 1: %q and %q", op, prev, st)
			}
			out[op] = st
		case 0:
		default:
			t.Fatalf("availability gauge for %q/%q = %v, want 0 or 1", op, st, p.Value)
		}
	}
	return out
}

// TestAvailability403IsDisabledNot401 is the #420 regression for the one
// collector that legitimately reads 403 as a feature gate. Flow logging is a
// documented Premium/Enterprise feature, so listNetworkFlowLogs declares
// Disposition{DisabledOn: []int{403}} — and BECAUSE it declares it explicitly,
// no other collector inherits that reading. A 401 on the same call must stay a
// credential rejection.
func TestAvailability403IsDisabledNot401(t *testing.T) {
	tests := []struct {
		name          string
		code          int
		wantState     apistate.State
		wantAdvance   bool
		wantErr       bool
		wantActonable bool
	}{
		{"403 premium gate", 403, apistate.StateDisabled, true, false, false},
		{"401 revoked credential", 401, apistate.StateCredentialRejected, false, true, true},
		{"429 rate limited", 429, apistate.StateTransientFailure, false, true, true},
	}
	from := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{err: &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/x", Code: tc.code, Body: "body"}}
			rec := telemetrytest.New()
			c := New(api, newProcessor(), 0, 0, nil, nil)

			hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
			if tc.wantErr != (err != nil) {
				t.Fatalf("CollectWindow() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantAdvance != hwm.Equal(to) {
				t.Fatalf("high-water mark = %v (advance=%v), want advance=%v", hwm, hwm.Equal(to), tc.wantAdvance)
			}

			states := availabilityStates(t, rec)
			if got := states["listNetworkFlowLogs"]; got != string(tc.wantState) {
				t.Errorf("availability = %q, want %q", got, tc.wantState)
			}
			if got := tc.wantState.Actionable(); got != tc.wantActonable {
				t.Errorf("%s.Actionable() = %v, want %v", tc.wantState, got, tc.wantActonable)
			}
		})
	}
}

// TestAvailabilitySupportedOnSuccess pins the happy path and the injected clock
// feeding the last-probe gauge.
func TestAvailabilitySupportedOnSuccess(t *testing.T) {
	from := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	probe := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)

	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	c := New(&fakeAPI{resp: oneTCPResponse()}, newProcessor(), 0, 0, nil, nil, WithAPIState(tr))
	c.now = func() time.Time { return probe }

	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow: %v", err)
	}
	if got := availabilityStates(t, rec)["listNetworkFlowLogs"]; got != string(apistate.StateSupported) {
		t.Errorf("availability = %q, want supported", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Collector != "flowlogs" || snap[0].Operation != "listNetworkFlowLogs" {
		t.Fatalf("tracker snapshot = %+v, want one flowlogs/listNetworkFlowLogs entry", snap)
	}
	lp := rec.MetricPoints(apistate.MetricLastProbe)
	if len(lp) != 1 || lp[0].Value != float64(probe.Unix()) {
		t.Fatalf("last_probe = %+v, want one point at %v", lp, probe.Unix())
	}
}

// TestAvailabilityUnknownOnCancellation: shutdown must not flap the collector
// into a failure state, and unknown is not Actionable.
func TestAvailabilityUnknownOnCancellation(t *testing.T) {
	from := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := New(&fakeAPI{err: context.Canceled}, newProcessor(), 0, 0, nil, nil)

	if _, err := c.CollectWindow(context.Background(), from, from.Add(time.Minute), rec.Emitter()); err == nil {
		t.Fatal("expected the cancellation error to propagate")
	}
	if got := availabilityStates(t, rec)["listNetworkFlowLogs"]; got != string(apistate.StateUnknown) {
		t.Errorf("availability = %q, want unknown", got)
	}
	if apistate.StateUnknown.Actionable() {
		t.Fatal("StateUnknown must not be Actionable")
	}
}

// The boundary matrix (#433) proves the DECODER accepts a null or empty response
// body. This proves what happens next: the collector emits nothing at all, rather
// than a phantom zero-valued point, and still advances the high-water mark so the
// window is not re-fetched forever.
//
// A zero-valued point here would be worse than no point. Flow-log metrics are
// per-pair counters, so a phantom point would mint a series for a src/dst pair
// that never sent a byte, and top-N rollup decisions are made on exactly those
// series.
func TestCollectWindow_EmptyAndNullResponseEmitNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp flowlog.NetworkResponse
	}{
		{"nil logs (what the real API returns for a quiet window)", flowlog.NetworkResponse{}},
		{"empty logs slice", flowlog.NetworkResponse{Logs: []flowlog.FlowLog{}}},
		{"a log with no traffic at all", flowlog.NetworkResponse{Logs: []flowlog.FlowLog{{
			NodeID: "n-laptop",
			Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAPI{resp: tc.resp}
			c := New(a, newProcessor(), 0, 0, nil, nil)
			rec := telemetrytest.New()

			from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
			to := from.Add(time.Minute)

			hwm, err := c.CollectWindow(context.Background(), from, to, rec.Emitter())
			if err != nil {
				t.Fatalf("CollectWindow: %v", err)
			}
			if !hwm.Equal(to) {
				t.Fatalf("high-water mark = %v, want %v — an empty window must still advance, or "+
					"it is re-fetched forever", hwm, to)
			}
			for _, metric := range []string{flowlog.MetricIO, flowlog.MetricPackets, flowlog.MetricFlows} {
				if pts := rec.MetricPoints(metric); len(pts) != 0 {
					t.Errorf("%s emitted %d point(s), want 0: %+v", metric, len(pts), pts)
				}
			}
			if logs := rec.LogRecords(); len(logs) != 0 {
				t.Errorf("emitted %d log record(s), want 0: %+v", len(logs), logs)
			}
		})
	}
}

// TestCollectWindow_ThreadsTraceContextOntoFlowLog is the #367 acceptance test
// for the poll path: CollectWindow's ctx (the scheduler's poll-tick context)
// must reach the processor so a sampled span produces a native TraceID/SpanID
// on the emitted flow log — the ctx must not be silently discarded in favor of
// context.Background().
func TestCollectWindow_ThreadsTraceContextOntoFlowLog(t *testing.T) {
	a := &fakeAPI{resp: oneTCPResponse()}
	c := New(a, newProcessor(), 0, 0, nil, nil)
	rec := telemetrytest.New()

	wantTraceID := trace.TraceID{0x0c, 0x0d}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     trace.SpanID{0x0e},
		TraceFlags: trace.FlagsSampled,
	}))

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)
	if _, err := c.CollectWindow(ctx, from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}

	recs := rec.LogRecords()
	if len(recs) == 0 {
		t.Fatalf("no log records emitted")
	}
	for i, r := range recs {
		if r.TraceID != wantTraceID.String() {
			t.Errorf("record %d TraceID = %q, want %q", i, r.TraceID, wantTraceID.String())
		}
	}
}
