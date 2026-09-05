package pam

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestSessionCatalogMatchesEmitted(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	s := session("catalog", start, "ssh")
	s.EndTime = &end
	s.Killed = true
	s.Events = []b0api.SessionEvent{{Type: "ssh_exec", Status: "success"}}
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, s), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}

	catalog := append(SessionCatalog(), apistate.Catalog()...)
	declared := make(map[string]metricdoc.Metric)
	for _, metric := range catalog {
		declared[metric.Name] = metric
	}
	for _, name := range rec.MetricNames() {
		doc, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is absent from SessionCatalog", name)
			continue
		}
		point := rec.MetricPoints(name)[0]
		if point.Unit != doc.Unit || point.Description != doc.Description {
			t.Errorf("%s metadata = unit %q desc %q, want unit %q desc %q", name, point.Unit, point.Description, doc.Unit, doc.Description)
		}
		wantCounter := doc.Instrument == metricdoc.Counter
		gotCounter := point.Kind == "sum" && point.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s instrument = kind %q monotonic %v, want %q", name, point.Kind, point.Monotonic, doc.Instrument)
		}
	}
	telemetrytest.AssertCatalogAttrs(t, rec, catalog, nil)
}

func TestSessionCatalogContainsEveryFamilyAndNoRecordingMetric(t *testing.T) {
	want := map[string]bool{
		metricSessions:        false,
		metricSessionDuration: false,
		metricSessionsKilled:  false,
		metricSessionsActive:  false,
		metricSessionEvents:   false,
	}
	for _, metric := range SessionCatalog() {
		if _, ok := want[metric.Name]; !ok {
			t.Errorf("unexpected session metric %q", metric.Name)
			continue
		}
		want[metric.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("session metric %q missing from catalog", name)
		}
	}
}

func TestSessionLogCatalogMatchesEmitted(t *testing.T) {
	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	s := session("session-log-catalog", start, "ssh")
	s.EndTime = &end
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, s), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, allSessionLogCategories(), enrich.DefaultAddrSet())).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	telemetrytest.AssertCatalogAttrs(t, rec, append(SessionCatalog(), apistate.Catalog()...), LogCatalog())
}
