package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// This file holds the anti-regression test for #524: a registered collector that
// records no api-availability state is DARK, and nothing used to notice.
//
// The gap it closes was not hypothetical. Before #524 only 5 collector packages
// called apistate.Observe, so 12 of the 17 registered collectors could never
// report anything but `unknown` — including `devices`, which on the reference
// deployment ran every 60s at 100% success and still showed `unknown` on all
// three of its capability-matrix rows. Worse than the cosmetics: the two shipped
// alert rules (ts2o-api-credential-rejected, ts2o-api-scope-denied) read that
// same metric, so a revoked credential or a scope regression affecting any of
// those 12 could fire NEITHER alert.
//
// The per-package tests each prove their own collector observes. Only this test
// proves the SET is complete, which is the property that actually decays: the
// failure mode is a collector added later that nobody remembers to wire.

// apiStateExempt lists registered collectors that legitimately record no
// availability state, with the reason. Adding a name here must be a deliberate
// act with a defensible reason — the default is that every collector reports.
//
// It is deliberately empty. It exists so that a future exemption has to be
// argued for in code review rather than achieved by quietly not wiring
// something, which is exactly how the original 12 went dark.
var apiStateExempt = map[string]string{}

// collectAll drives every registered collector on the runtime once against rec
// and returns the collector names that were driven. Errors are expected and
// ignored: the point is that the collector OBSERVED its outcome, not that the
// outcome was good.
func collectAll(t *testing.T, a *App, rec *telemetrytest.Recorder) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var driven []string
	now := time.Now()
	for _, e := range a.runtimes[0].registry.Entries() {
		name := e.Collector.Name()
		driven = append(driven, name)
		switch c := e.Collector.(type) {
		case collector.WindowCollector:
			_, _ = c.CollectWindow(ctx, now.Add(-time.Hour), now, rec.Emitter())
		case collector.SnapshotCollector:
			_ = c.Collect(ctx, rec.Emitter())
		default:
			t.Fatalf("collector %q is neither a Snapshot nor a Window collector", name)
		}
	}
	return driven
}

// observedCollectors returns the set of collector names that emitted at least
// one tailscale2otel.api.availability point. This is the ALERTING half: the two
// shipped rules read this metric.
func observedCollectors(rec *telemetrytest.Recorder) map[string]bool {
	out := map[string]bool{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		if c := p.Attrs[semconv.AttrCollector]; c != "" {
			out[c] = true
		}
	}
	return out
}

// trackedCollectors returns the set of collector names present in the runtime's
// shared availability tracker. This is the STATUS-PAGE half, and it is a
// genuinely separate assertion from observedCollectors: apistate.Observe emits
// the metric whether or not a tracker was passed (a nil *Tracker is a no-op by
// design), so a collector that calls Observe correctly but is never given
// WithAPIState in registerCollectors alerts fine and still shows `unknown` on
// the capability matrix. Checking only the metric passes that bug — verified by
// deleting the contacts wiring, which the metric-only check did not catch.
func trackedCollectors(a *App) map[string]bool {
	out := map[string]bool{}
	for _, e := range a.runtimes[0].apiState.Snapshot() {
		out[e.Collector] = true
	}
	return out
}

// apiStateFixtureApp builds an App whose control-plane client and object store
// both point at srv, with every collector enabled.
//
// The fixture server answers 500 to everything ON PURPOSE. This test does not
// need happy-path response bodies for seventeen different endpoints — it needs
// each collector to make its call and record the outcome, and a uniform failure
// reaches that in one line while staying immune to upstream schema drift. A
// collector that only observed on success would still fail here, because
// apistate.Observe is required at the call site, before the error branch.
func apiStateFixtureApp(t *testing.T, srv *httptest.Server, tune func(*config.Config)) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"

	// Static node-metrics target: with an empty target set nodemetrics returns
	// early without probing anything, which is correct behavior but would make
	// this fixture silently skip it.
	cfg.Collectors.NodeMetrics.Enabled = true
	cfg.Collectors.NodeMetrics.Targets = []config.NodeMetricsTarget{
		{URL: "http://127.0.0.1:1/metrics", Instance: "fixture"},
	}

	// Distinct prefixes: config.Validate rejects two feeds naming the same
	// destination, because both would ingest every object in it.
	for prefix, os := range map[string]*config.ObjectStoreConfig{
		"flow/":  &cfg.Collectors.Flowlogs.ObjectStore,
		"audit/": &cfg.Collectors.Auditlogs.ObjectStore,
	} {
		os.Endpoint = srv.URL
		os.Region = "eu-west-2"
		os.Bucket = "fixture"
		os.Prefix = prefix
		os.PathStyle = true
		os.AllowInsecureHTTP = true
	}

	if tune != nil {
		tune(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return baseTestApp(t, cfg, srv.URL, telemetrytest.New())
}

// TestEveryRegisteredCollectorRecordsAPIState is the #524 anti-regression test:
// collector #18 cannot ship dark.
//
// Two scenarios are needed because the ingestion sources are mutually exclusive
// per signal — a runtime that polls flow logs does not also register the object
// store reader or the feature probe — so no single configuration registers every
// collector at once.
func TestEveryRegisteredCollectorRecordsAPIState(t *testing.T) {
	scenarios := []struct {
		name string
		tune func(*config.Config)
	}{
		{"poll sources", nil},
		{
			// Registers the three collectors the polling configuration cannot:
			// objectstore, objectstore-audit and the flowlogs-feature probe that
			// stands in for the poller when ingestion is not polled.
			"object-store sources",
			func(c *config.Config) {
				c.Collectors.Flowlogs.Source = "objectstore"
				c.Collectors.Auditlogs.Source = "objectstore"
			},
		},
	}

	covered := map[string]bool{}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "fixture", http.StatusInternalServerError)
			}))
			defer srv.Close()

			rec := telemetrytest.New()
			a := apiStateFixtureApp(t, srv, sc.tune)
			driven := collectAll(t, a, rec)
			if len(driven) == 0 {
				t.Fatal("no collectors registered; the fixture config is wrong, not the code")
			}
			observed := observedCollectors(rec)
			tracked := trackedCollectors(a)

			for _, name := range driven {
				covered[name] = true
				if reason, ok := apiStateExempt[name]; ok {
					if observed[name] || tracked[name] {
						t.Errorf("collector %q is listed in apiStateExempt (%q) but DOES record "+
							"availability; delete the exemption", name, reason)
					}
					continue
				}
				if !observed[name] {
					t.Errorf("collector %q emitted no %s point.\n"+
						"It is registered and running, so neither ts2o-api-credential-rejected nor "+
						"ts2o-api-scope-denied can fire for it (#524).\n"+
						"Fix: call apistate.Observe at its API call site, before the error branch. "+
						"Do NOT add it to apiStateExempt without a reason that survives review.",
						name, apistate.MetricAvailability)
				}
				if !tracked[name] {
					t.Errorf("collector %q recorded nothing on the shared availability tracker.\n"+
						"Its capability-matrix row can therefore only ever read `unknown`, which is "+
						"the #524 symptom, even if its metric is fine.\n"+
						"Fix: pass the runtime tracker to it in registerCollectors — WithAPIState("+
						"rt.apiState) for an options-slice collector, Options.APIState for "+
						"nodemetrics/objectstore.", name)
				}
			}
		})
	}

	// The scenarios above must between them register every collector this app can
	// run. Without this, a new collector reachable only under some third source
	// combination would be neither driven nor noticed, and the test would pass by
	// simply never looking at it.
	for name := range CollectorCapability {
		if !covered[name] {
			t.Errorf("collector %q is in CollectorCapability but no scenario in this test registers "+
				"it, so nothing proves it records availability. Add a scenario that does.", name)
		}
	}
}

// TestAPIStateOperationsAreNamedConstants guards the operation label's
// cardinality contract. tailscale.api.operation is registered as a bounded,
// non-identifying attribute (internal/telemetry/pii/registry.go), which holds
// only while every value is a compile-time constant. An operation name built
// from per-entity data (a device ID, a service name) would be an unbounded
// label on a metric that is also zero-seeded across seven states.
func TestAPIStateOperationsAreNamedConstants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fixture", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rec := telemetrytest.New()
	a := apiStateFixtureApp(t, srv, nil)
	collectAll(t, a, rec)

	seen := map[string]bool{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		seen[p.Attrs[semconv.AttrAPIOperation]] = true
	}
	if len(seen) == 0 {
		t.Fatal("no operations observed; the fixture is broken")
	}
	var names []string
	for op := range seen {
		names = append(names, op)
		if op == "" {
			t.Error("an availability point carries an empty operation label")
		}
		// Every legitimate value is an upstream operationId (lowerCamelCase), a
		// bounded snake_case subrequest/local-probe name, or an operationId with a
		// bounded dotted suffix (logstream keys its probe per log type, exactly two
		// values). Anything carrying a separator we never use is per-entity data
		// that escaped: an ID, a URL, a device name.
		if strings.ContainsAny(op, " /:@") {
			t.Errorf("operation %q looks like per-entity data, not a constant", op)
		}
	}
	sort.Strings(names)
	t.Logf("observed %d operations: %v", len(names), names)
}
