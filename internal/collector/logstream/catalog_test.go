package logstream_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/logstream"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

type catalogFakeAPI struct{ bytes int64 }

func (f *catalogFakeAPI) LogStreamStatus(_ context.Context, logType string) (*tsapi.LogStreamStatus, error) {
	if logType != "network" {
		return nil, &tsapi.StatusError{Code: 404}
	}
	return &tsapi.LogStreamStatus{
		LastActivity:       time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC),
		LastError:          "boom",
		MaxBodySize:        1000,
		MaxNumEntries:      100,
		NumBytesSent:       f.bytes,
		NumEntriesSent:     f.bytes / 10,
		NumTotalRequests:   f.bytes / 100,
		NumFailedRequests:  1,
		NumSpoofedEntries:  1,
		NumMaxBodyRequests: 1,
	}, nil
}

// TestCatalogMatchesEmitted drives two scrapes (so the delta counters emit, not
// just seed) and asserts every emitted metric/log event matches the catalog.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	api := &catalogFakeAPI{bytes: 1000}
	c := logstream.New(api, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("seed Collect: %v", err)
	}
	api.bytes = 5000
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("delta Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range logstream.Catalog() {
		declared[m.Name] = m
	}
	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in logstream.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}

	logDeclared := map[string]bool{}
	for _, le := range logstream.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in logstream.LogCatalog()", lr.EventName)
		}
	}
}
