package flowlogs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
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
	c := New(a, newProcessor(), 0, 0)
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
	c := New(a, newProcessor(), 0, 0)
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
	def := New(&fakeAPI{}, newProcessor(), 0, 0)
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
	ovr := New(&fakeAPI{}, newProcessor(), 30*time.Second, 45*time.Second)
	if got := ovr.DefaultInterval(); got != 30*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 30s (override)", got)
	}
	if got := ovr.Lag(); got != 45*time.Second {
		t.Fatalf("Lag() = %v, want 45s (override)", got)
	}
}
