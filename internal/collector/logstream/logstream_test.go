package logstream

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

type fakeAPI struct {
	fn func(logType string) (*tsapi.LogStreamStatus, error)
}

func (f *fakeAPI) LogStreamStatus(_ context.Context, logType string) (*tsapi.LogStreamStatus, error) {
	return f.fn(logType)
}

var _ collector.SnapshotCollector = (*Collector)(nil)

// sampleStatus is a configured-stream status; extra mutates it for a test.
func sampleStatus(extra func(*tsapi.LogStreamStatus)) *tsapi.LogStreamStatus {
	st := &tsapi.LogStreamStatus{
		LastActivity:       time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC),
		MaxBodySize:        1000,
		MaxNumEntries:      100,
		NumBytesSent:       1000,
		NumEntriesSent:     50,
		NumTotalRequests:   10,
		NumFailedRequests:  1,
		NumSpoofedEntries:  0,
		NumMaxBodyRequests: 2,
	}
	if extra != nil {
		extra(st)
	}
	return st
}

func byType(pts []telemetrytest.MetricPoint) map[string]float64 {
	out := map[string]float64{}
	for _, p := range pts {
		out[p.Attrs["tailscale.logstream.type"]] = p.Value
	}
	return out
}

// networkOnly returns a configured status for "network" and a 404 for the other
// log type (so tests can focus on one configured stream).
func networkOnly(extra func(*tsapi.LogStreamStatus)) *fakeAPI {
	return &fakeAPI{fn: func(lt string) (*tsapi.LogStreamStatus, error) {
		if lt == "network" {
			return sampleStatus(extra), nil
		}
		return nil, &tsapi.StatusError{Code: 404}
	}}
}

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "logstream" {
		t.Fatalf("Name() = %q, want logstream", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
}

func TestIndependentProbeIntervals(t *testing.T) {
	var at = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	calls := map[string]int{}
	api := &fakeAPI{fn: func(logType string) (*tsapi.LogStreamStatus, error) {
		calls[logType]++
		return sampleStatus(nil), nil
	}}
	c := New(api, 10*time.Second,
		WithProbeIntervals(30*time.Second, 10*time.Second))
	c.now = func() time.Time { return at }
	if got := c.PollInterval(); got != 10*time.Second {
		t.Fatalf("PollInterval() = %v, want 10s", got)
	}

	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("initial Collect: %v", err)
	}
	if calls["configuration"] != 1 || calls["network"] != 1 {
		t.Fatalf("initial calls = %v, want one probe of each type", calls)
	}

	at = at.Add(10 * time.Second)
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("10s Collect: %v", err)
	}
	if calls["configuration"] != 1 || calls["network"] != 2 {
		t.Fatalf("10s calls = %v, want configuration unchanged and network +1", calls)
	}

	at = at.Add(20 * time.Second)
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("30s Collect: %v", err)
	}
	if calls["configuration"] != 2 || calls["network"] != 3 {
		t.Fatalf("30s calls = %v, want both probes due", calls)
	}
}

func TestProbeIntervalsZeroInheritSharedInterval(t *testing.T) {
	c := New(&fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
		return &tsapi.LogStreamStatus{}, nil
	}}, 15*time.Second, WithProbeIntervals(0, 0))
	if got := c.PollInterval(); got != 15*time.Second {
		t.Fatalf("PollInterval() = %v, want shared 15s", got)
	}
}

func TestGatingOn404(t *testing.T) {
	api := &fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
		return nil, &tsapi.StatusError{Code: 404}
	}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect returned error for 404 (should be idle): %v", err)
	}
	cfg := byType(rec.MetricPoints("tailscale.logstream.configured"))
	if cfg["network"] != 0 || cfg["configuration"] != 0 {
		t.Fatalf("configured = %v, want both 0", cfg)
	}
	if got := rec.MetricPoints("tailscale.logstream.bytes_sent"); len(got) != 0 {
		t.Fatalf("health emitted when unconfigured: %d points", len(got))
	}
}

func TestScrapeErrorOn5xx(t *testing.T) {
	api := &fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
		return nil, &tsapi.StatusError{Code: 503}
	}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect should return an error on 5xx")
	}
}

func TestEmpty200IsNotConfigured(t *testing.T) {
	api := &fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
		return &tsapi.LogStreamStatus{}, nil // 200 but all-zero
	}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	cfg := byType(rec.MetricPoints("tailscale.logstream.configured"))
	if cfg["network"] != 0 || cfg["configuration"] != 0 {
		t.Fatalf("empty-200 configured = %v, want both 0", cfg)
	}
	if got := rec.MetricPoints("tailscale.logstream.bytes_sent"); len(got) != 0 {
		t.Fatalf("health emitted for empty 200: %d points", len(got))
	}
}

func TestConfiguredGaugesAndCounterSeed(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(networkOnly(nil), 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	cfg := byType(rec.MetricPoints("tailscale.logstream.configured"))
	if cfg["network"] != 1 || cfg["configuration"] != 0 {
		t.Fatalf("configured = %v, want network 1 / configuration 0", cfg)
	}
	la := rec.MetricPoints("tailscale.logstream.last_activity")
	if len(la) != 1 || la[0].Unit != "s" {
		t.Fatalf("last_activity = %+v, want one point unit s", la)
	}
	if want := float64(time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC).Unix()); la[0].Value != want {
		t.Errorf("last_activity = %v, want %v", la[0].Value, want)
	}
	if er := byType(rec.MetricPoints("tailscale.logstream.error")); er["network"] != 0 {
		t.Errorf("error gauge = %v, want 0 (no lastError)", er["network"])
	}
	// Counters seed on the first scrape — no cumulative emitted yet.
	if got := rec.MetricPoints("tailscale.logstream.bytes_sent"); len(got) != 0 {
		t.Fatalf("counters should seed (no emit) on first scrape, got %d points", len(got))
	}
}

func TestCounterDeltaAcrossScrapes(t *testing.T) {
	bytes := int64(1000)
	api := networkOnly(func(st *tsapi.LogStreamStatus) { st.NumBytesSent = bytes })
	c := New(api, 0)
	rec := telemetrytest.New()

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil { // seed at 1000
		t.Fatalf("seed Collect: %v", err)
	}
	bytes = 1500
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil { // delta 500
		t.Fatalf("delta Collect: %v", err)
	}

	bs := rec.MetricPoints("tailscale.logstream.bytes_sent")
	if len(bs) != 1 {
		t.Fatalf("bytes_sent points = %d, want 1", len(bs))
	}
	if bs[0].Kind != "sum" || !bs[0].Monotonic {
		t.Errorf("bytes_sent kind=%q monotonic=%v, want sum/true", bs[0].Kind, bs[0].Monotonic)
	}
	if bs[0].Unit != "By" {
		t.Errorf("bytes_sent unit = %q, want By", bs[0].Unit)
	}
	if bs[0].Value != 500 {
		t.Errorf("bytes_sent delta = %v, want 500", bs[0].Value)
	}
}

func TestCounterReset(t *testing.T) {
	bytes := int64(1000)
	api := networkOnly(func(st *tsapi.LogStreamStatus) { st.NumBytesSent = bytes })
	c := New(api, 0)
	rec := telemetrytest.New()

	_ = c.Collect(context.Background(), rec.Emitter()) // seed at 1000
	bytes = 300                                        // cumulative dropped → stream recreated
	_ = c.Collect(context.Background(), rec.Emitter())

	bs := rec.MetricPoints("tailscale.logstream.bytes_sent")
	if len(bs) != 1 || bs[0].Value != 300 {
		t.Fatalf("bytes_sent after reset = %+v, want one point value 300 (current)", bs)
	}
}

// availabilityStates returns, per operation, the single state whose gauge is 1.
// The metric is zero-seeded across the whole state set, so exactly one point per
// operation must be 1 and every other point 0.
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

// Test401And403AreDistinctAndNotUnconfigured is the core #420 regression. The
// collector used to fold ANY 4xx into "no stream configured", so a revoked
// credential (401) and a missing scope (403) both read as a healthy, deliberate
// zero — exactly the failure mode the issue exists to kill.
func Test401And403AreDistinctAndNotUnconfigured(t *testing.T) {
	tests := []struct {
		code      int
		wantState string
	}{
		{401, "credential_rejected"},
		{403, "scope_denied"},
	}
	seen := map[string]bool{}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			api := &fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
				return nil, &tsapi.StatusError{Code: tc.code}
			}}
			rec := telemetrytest.New()
			err := New(api, 0).Collect(context.Background(), rec.Emitter())
			if err == nil {
				t.Fatalf("Collect returned nil for a %d; a rejected credential must not read as success", tc.code)
			}
			// It must NOT claim the stream is unconfigured: we were denied, we
			// did not learn that no sink exists.
			if cfg := rec.MetricPoints("tailscale.logstream.configured"); len(cfg) != 0 {
				t.Errorf("configured emitted %d points on a %d; we never learned whether a stream exists", len(cfg), tc.code)
			}
			states := availabilityStates(t, rec)
			for _, lt := range logTypes {
				op := "getLogStreamingStatus." + lt
				if states[op] != tc.wantState {
					t.Errorf("availability[%s] = %q, want %q", op, states[op], tc.wantState)
				}
			}
			if !apistate.State(tc.wantState).Actionable() {
				t.Errorf("state %q must be Actionable — it is a real regression", tc.wantState)
			}
			seen[tc.wantState] = true
		})
	}
	if len(seen) != 2 {
		t.Fatalf("401 and 403 produced %d distinct states, want 2", len(seen))
	}
}

// Test404IsDisabledAndNotActionable pins the other half: an absent endpoint is
// benign and must never page anyone.
func Test404IsDisabledAndNotActionable(t *testing.T) {
	api := &fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
		return nil, &tsapi.StatusError{Code: 404}
	}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect returned an error for a 404 (feature not configured): %v", err)
	}
	states := availabilityStates(t, rec)
	for _, lt := range logTypes {
		op := "getLogStreamingStatus." + lt
		if states[op] != string(apistate.StateDisabled) {
			t.Errorf("availability[%s] = %q, want disabled", op, states[op])
		}
	}
	if apistate.StateDisabled.Actionable() {
		t.Fatal("StateDisabled must not be Actionable — a feature being off is not a fault")
	}
	if cfg := byType(rec.MetricPoints("tailscale.logstream.configured")); cfg["network"] != 0 {
		t.Errorf("configured = %v, want 0 for a 404", cfg)
	}
}

// TestTransientFailureOn5xx keeps 5xx classified as retryable, distinct from
// both the credential states and disabled.
func TestTransientFailureOn5xx(t *testing.T) {
	api := &fakeAPI{fn: func(string) (*tsapi.LogStreamStatus, error) {
		return nil, &tsapi.StatusError{Code: 503}
	}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect should return an error on 5xx")
	}
	states := availabilityStates(t, rec)
	if got := states["getLogStreamingStatus.network"]; got != string(apistate.StateTransientFailure) {
		t.Errorf("availability = %q, want transient_failure", got)
	}
}

// TestSupportedStateAndTracker checks the happy path records supported, and that
// an injected tracker sees both operations (the admin status page reads it).
func TestSupportedStateAndTracker(t *testing.T) {
	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	c := New(networkOnly(nil), 0, WithAPIState(tr))
	c.now = func() time.Time { return time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC) }
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	states := availabilityStates(t, rec)
	if states["getLogStreamingStatus.network"] != string(apistate.StateSupported) {
		t.Errorf("network availability = %q, want supported", states["getLogStreamingStatus.network"])
	}
	// The other log type 404s in networkOnly.
	if states["getLogStreamingStatus.configuration"] != string(apistate.StateDisabled) {
		t.Errorf("configuration availability = %q, want disabled", states["getLogStreamingStatus.configuration"])
	}

	snap := tr.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("tracker snapshot = %d entries, want 2", len(snap))
	}
	for _, e := range snap {
		if e.Collector != "logstream" {
			t.Errorf("tracker entry collector = %q, want logstream", e.Collector)
		}
		if e.LastProbe.IsZero() {
			t.Errorf("tracker entry %q has a zero LastProbe", e.Operation)
		}
	}
	lp := rec.MetricPoints(apistate.MetricLastProbe)
	if len(lp) != 2 {
		t.Fatalf("last_probe points = %d, want 2", len(lp))
	}
	want := float64(time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC).Unix())
	for _, p := range lp {
		if p.Value != want {
			t.Errorf("last_probe = %v, want %v (injected clock)", p.Value, want)
		}
	}
}

func TestErrorGaugeAndLog(t *testing.T) {
	api := networkOnly(func(st *tsapi.LogStreamStatus) { st.LastError = "splunk: connection refused" })
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if er := byType(rec.MetricPoints("tailscale.logstream.error")); er["network"] != 1 {
		t.Errorf("error gauge = %v, want 1", er["network"])
	}

	var found bool
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "tailscale.logstream.error" {
			continue
		}
		found = true
		if lr.Attrs["tailscale.logstream.type"] != "network" {
			t.Errorf("error log type attr = %q, want network", lr.Attrs["tailscale.logstream.type"])
		}
		if lr.Body == "" {
			t.Errorf("error log body is empty, want the error text")
		}
	}
	if !found {
		t.Fatal("no tailscale.logstream.error log event emitted")
	}
}
