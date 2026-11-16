package postureintegrations

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

type fakeAPI struct {
	ints []tsapi.PostureIntegration
	err  error
}

func (f *fakeAPI) PostureIntegrations(context.Context) ([]tsapi.PostureIntegration, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ints, nil
}

var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "posture_integrations" {
		t.Fatalf("Name() = %q, want posture_integrations", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
}

func TestCollectEmitsErrorGauge(t *testing.T) {
	sync := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	api := &fakeAPI{ints: []tsapi.PostureIntegration{
		{ID: "ok", Provider: "intune", Status: tsapi.PostureIntegrationStatus{LastSync: sync}},
		{ID: "bad", Provider: "jamf", Status: tsapi.PostureIntegrationStatus{LastSync: sync, Error: "auth token expired"}},
	}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	byInt := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.posture_integration.error") {
		byInt[p.Attrs[attrIntegration]] = p.Value
	}
	if byInt["ok"] != 0 {
		t.Errorf("error gauge for ok integration = %v, want 0", byInt["ok"])
	}
	if byInt["bad"] != 1 {
		t.Errorf("error gauge for failing integration = %v, want 1", byInt["bad"])
	}
	// The raw error text must never appear on any attribute value.
	for _, p := range rec.MetricPoints("tailscale.posture_integration.error") {
		for k, v := range p.Attrs {
			if v == "auth token expired" {
				t.Errorf("raw error text leaked into attribute %q", k)
			}
		}
	}
}

func TestCollectEmitsPerIntegration(t *testing.T) {
	sync := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	api := &fakeAPI{ints: []tsapi.PostureIntegration{{
		ID:       "p1",
		Provider: "intune",
		Status: tsapi.PostureIntegrationStatus{
			LastSync: sync, MatchedCount: 4, PossibleMatchedCount: 5, ProviderHostCount: 10,
		},
	}}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if cnt := rec.MetricPoints("tailscale.posture_integrations.count"); len(cnt) != 1 || cnt[0].Value != 1 {
		t.Fatalf("count = %+v, want one point value 1", cnt)
	}

	single := func(name string) telemetrytest.MetricPoint {
		t.Helper()
		pts := rec.MetricPoints(name)
		if len(pts) != 1 {
			t.Fatalf("%s points = %d, want 1", name, len(pts))
		}
		if pts[0].Attrs["tailscale.posture.provider"] != "intune" {
			t.Errorf("%s provider attr = %q, want intune", name, pts[0].Attrs["tailscale.posture.provider"])
		}
		if pts[0].Attrs["tailscale.posture.integration"] != "p1" {
			t.Errorf("%s integration attr = %q, want p1", name, pts[0].Attrs["tailscale.posture.integration"])
		}
		return pts[0]
	}

	if p := single("tailscale.posture_integration.matched"); p.Value != 4 {
		t.Errorf("matched = %v, want 4", p.Value)
	}
	if p := single("tailscale.posture_integration.possible_matched"); p.Value != 5 {
		t.Errorf("possible_matched = %v, want 5", p.Value)
	}
	if p := single("tailscale.posture_integration.provider_hosts"); p.Value != 10 {
		t.Errorf("provider_hosts = %v, want 10", p.Value)
	}
	ls := single("tailscale.posture_integration.last_sync")
	if ls.Unit != "s" {
		t.Errorf("last_sync unit = %q, want s", ls.Unit)
	}
	if ls.Value != float64(sync.Unix()) {
		t.Errorf("last_sync = %v, want %v", ls.Value, float64(sync.Unix()))
	}
}

func TestCollectEmptyIntegrations(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{ints: nil}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.posture_integrations.count"); len(cnt) != 1 || cnt[0].Value != 0 {
		t.Fatalf("count = %+v, want one point value 0", cnt)
	}
	if m := rec.MetricPoints("tailscale.posture_integration.matched"); len(m) != 0 {
		t.Fatalf("matched points = %d, want 0", len(m))
	}
}

func TestLastSyncSkippedWhenZero(t *testing.T) {
	api := &fakeAPI{ints: []tsapi.PostureIntegration{{
		ID: "p1", Provider: "intune",
		Status: tsapi.PostureIntegrationStatus{MatchedCount: 1}, // zero LastSync
	}}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if ls := rec.MetricPoints("tailscale.posture_integration.last_sync"); len(ls) != 0 {
		t.Fatalf("last_sync points = %d, want 0 when LastSync is zero", len(ls))
	}
	if m := rec.MetricPoints("tailscale.posture_integration.matched"); len(m) != 1 {
		t.Fatalf("matched points = %d, want 1 (other metrics still emitted)", len(m))
	}
}

func TestCollectPropagatesError(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{err: context.DeadlineExceeded}, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("expected error, got nil")
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

// TestClassifiesInsteadOfBlanketPropagating is the #420 regression. This
// collector used to `return err` for everything, so a missing devices:read
// scope, an expired credential and a flaky 503 were one indistinguishable
// scrape failure.
func TestClassifiesInsteadOfBlanketPropagating(t *testing.T) {
	tests := []struct {
		name          string
		code          int
		wantState     apistate.State
		wantErr       bool
		wantActonable bool
		wantCount     bool // a count=0 gauge is emitted
	}{
		{"401 revoked credential", 401, apistate.StateCredentialRejected, true, true, false},
		{"403 missing scope", 403, apistate.StateScopeDenied, true, true, false},
		{"404 feature absent", 404, apistate.StateDisabled, false, false, true},
		{"503 transient", 503, apistate.StateTransientFailure, true, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			api := &fakeAPI{err: &tsapi.StatusError{Code: tc.code}}
			err := New(api, 0).Collect(context.Background(), rec.Emitter())
			if tc.wantErr != (err != nil) {
				t.Fatalf("Collect() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got := availabilityStates(t, rec)["getPostureIntegrations"]; got != string(tc.wantState) {
				t.Errorf("availability = %q, want %q", got, tc.wantState)
			}
			if got := tc.wantState.Actionable(); got != tc.wantActonable {
				t.Errorf("%s.Actionable() = %v, want %v", tc.wantState, got, tc.wantActonable)
			}
			cnt := rec.MetricPoints("tailscale.posture_integrations.count")
			if tc.wantCount {
				if len(cnt) != 1 || cnt[0].Value != 0 {
					t.Errorf("count = %+v, want a single 0 (the endpoint is absent, so there are none)", cnt)
				}
			} else if len(cnt) != 0 {
				t.Errorf("count emitted %d points after a %d; we never learned how many integrations exist", len(cnt), tc.code)
			}
		})
	}
}

// Test401And403AreObservablyDifferent states the invariant directly: the two
// must never collapse to one outcome anywhere in this collector.
func Test401And403AreObservablyDifferent(t *testing.T) {
	state := func(code int) string {
		rec := telemetrytest.New()
		_ = New(&fakeAPI{err: &tsapi.StatusError{Code: code}}, 0).Collect(context.Background(), rec.Emitter())
		return availabilityStates(t, rec)["getPostureIntegrations"]
	}
	if a, b := state(401), state(403); a == b {
		t.Fatalf("401 and 403 both produced state %q; they must be distinguishable", a)
	}
}

func TestAvailabilitySupportedAndTracker(t *testing.T) {
	probe := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	c := New(&fakeAPI{ints: []tsapi.PostureIntegration{{ID: "p1", Provider: "intune"}}}, 0, WithAPIState(tr))
	c.now = func() time.Time { return probe }

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := availabilityStates(t, rec)["getPostureIntegrations"]; got != string(apistate.StateSupported) {
		t.Errorf("availability = %q, want supported", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Collector != "posture_integrations" {
		t.Fatalf("tracker snapshot = %+v, want one posture_integrations entry", snap)
	}
	lp := rec.MetricPoints(apistate.MetricLastProbe)
	if len(lp) != 1 || lp[0].Value != float64(probe.Unix()) {
		t.Fatalf("last_probe = %+v, want one point at %v", lp, probe.Unix())
	}
}

// TestNoAgeHistogram documents a deliberate #426 omission, so nobody "fixes" it
// by inventing a timestamp. The upstream PostureIntegration schema exposes only
// `configUpdated` (last config edit) and `status.lastSync` — there is NO
// creation timestamp, so no age distribution is emitted for this entity family.
func TestNoAgeHistogram(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{ints: []tsapi.PostureIntegration{{ID: "p1", Provider: "intune"}}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range rec.MetricNames() {
		if strings.Contains(name, ".age") {
			t.Errorf("emitted age metric %q, but the posture schema has no creation timestamp", name)
		}
	}
}

func TestIntegrationGaugesDropWhenIntegrationRemoved(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(disabled), func(t *testing.T) {
			api := &fakeAPI{ints: []tsapi.PostureIntegration{{ID: "example-integration", Provider: "intune", Status: tsapi.PostureIntegrationStatus{LastSync: time.Unix(1700000000, 0), MatchedCount: 2, PossibleMatchedCount: 3, ProviderHostCount: 4, Error: "synthetic sync error"}}}}
			c := New(api, 0)
			rec := telemetrytest.New()
			collect := func() {
				t.Helper()
				if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
					t.Fatal(err)
				}
			}
			collect()
			names := []string{metricMatched, metricPossible, metricProviderHosts, metricLastSync, metricError}
			before := map[string][]telemetrytest.MetricPoint{}
			for _, name := range names {
				before[name] = rec.MetricPoints(name)
				if len(before[name]) != 1 {
					t.Fatalf("missing initial %s", name)
				}
			}
			api.err = errors.New("synthetic list failure")
			if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
				t.Fatal("expected error")
			}
			for _, name := range names {
				if !sameMetricPoints(rec.MetricPoints(name), before[name]) {
					t.Errorf("%s changed after failure", name)
				}
			}
			api.err = nil
			api.ints = nil
			if disabled {
				api.err = &tsapi.StatusError{Code: 404}
			}
			collect()
			for _, name := range names {
				if got := rec.MetricPoints(name); len(got) != 0 {
					t.Errorf("%s retains removed integration: %v", name, got)
				}
			}
		})
	}
}

// sameMetricPoints compares snapshots without depending on SDK iteration order.
func sameMetricPoints(a, b []telemetrytest.MetricPoint) bool {
	if len(a) != len(b) {
		return false
	}
	for _, point := range a {
		found := false
		for _, other := range b {
			if reflect.DeepEqual(point, other) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
