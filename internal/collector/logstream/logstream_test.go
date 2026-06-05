package logstream

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
