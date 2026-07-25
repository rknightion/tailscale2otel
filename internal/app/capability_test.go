package app

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/apistate"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/provider"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// rowFor finds the matrix row for a (collector, subrequest) pair.
func rowFor(t *testing.T, rows []statusdata.CapabilityRow, collector, subrequest string) statusdata.CapabilityRow {
	t.Helper()
	for _, r := range rows {
		if r.Collector == collector && r.Subrequest == subrequest {
			return r
		}
	}
	t.Fatalf("no row for collector=%q subrequest=%q; rows=%+v", collector, subrequest, rows)
	return statusdata.CapabilityRow{}
}

// TestCapabilityScopesCoverEveryProviderFeature pins the join key between the
// scope map and the provider capability set: every gateable feature name must be
// modelled, or a newly added collector would silently preflight as "unknown"
// forever.
func TestCapabilityScopesCoverEveryProviderFeature(t *testing.T) {
	for _, f := range provider.AllFeatures {
		if _, ok := CapabilityScopes[f]; !ok {
			t.Errorf("provider feature %q has no entry in CapabilityScopes", f)
		}
	}
}

// TestCollectorCapabilityCoversEveryRegisteredName pins the other half of the
// join: every collector Name() that can appear in a registry must map to a
// capability, or its matrix row would carry an empty capability.
func TestCollectorCapabilityCoversEveryRegisteredName(t *testing.T) {
	names := []string{
		"devices", "users", "keys", "settings", "acl", "dns", "contacts",
		"webhooks", "posture_integrations", "logstream", "oauth_apps",
		"services", "nodemetrics", "flowlogs", "flowlogs-feature",
		"objectstore", "auditlogs",
	}
	for _, n := range names {
		capability, ok := CollectorCapability[n]
		if !ok {
			t.Errorf("collector %q has no entry in CollectorCapability", n)
			continue
		}
		if _, ok := CapabilityScopes[capability]; !ok {
			t.Errorf("collector %q maps to capability %q, which is not in CapabilityScopes", n, capability)
		}
	}
}

// TestScopePreflight is the #425 core: requested (not granted) scopes checked
// against each enabled collector's required scope, with `all` / `all:read`
// handled by tsscope.Satisfies rather than string equality.
func TestScopePreflight(t *testing.T) {
	decisions := []CollectorDecision{
		{Collector: "devices", Capability: "devices", ConfigEnabled: true, ProviderSupported: true, Registered: true,
			Subrequests: []SubrequestDecision{{Name: SubrequestDeviceInvites, Enabled: true}}},
		{Collector: "flowlogs", Capability: "flowlogs", ConfigEnabled: true, ProviderSupported: true, Registered: true},
		{Collector: "acl", Capability: "acl", ConfigEnabled: true, ProviderSupported: true, Registered: true},
		{Collector: "nodemetrics", Capability: "nodemetrics", ConfigEnabled: true, ProviderSupported: true, Registered: true},
		{Collector: "services", Capability: "services", ConfigEnabled: true, ProviderSupported: true, Registered: true},
	}

	tests := []struct {
		name   string
		scopes []string
		known  bool
		want   map[string]string // "collector[/subrequest]" -> ScopeStatus
	}{
		{
			name:   "all covers everything",
			scopes: []string{"all"},
			known:  true,
			want: map[string]string{
				"devices":                 statusdata.ScopeSatisfied,
				"devices/device_invites":  statusdata.ScopeSatisfied,
				"flowlogs":                statusdata.ScopeSatisfied,
				"acl":                     statusdata.ScopeSatisfied,
				"nodemetrics":             statusdata.ScopeNotApplicable,
				"services":                statusdata.ScopeUnknown,
			},
		},
		{
			name:   "all:read covers every read-only requirement",
			scopes: []string{"all:read"},
			known:  true,
			want: map[string]string{
				"devices":                statusdata.ScopeSatisfied,
				"devices/device_invites": statusdata.ScopeSatisfied,
				"flowlogs":               statusdata.ScopeSatisfied,
				"acl":                    statusdata.ScopeSatisfied,
			},
		},
		{
			name: "narrow but sufficient (incl. write parent implying its read child)",
			scopes: []string{
				"devices:core", // write parent implies devices:core:read
				"devices_invites:read",
				"logs:network:read",
				"policy_file:read",
			},
			known: true,
			want: map[string]string{
				"devices":                statusdata.ScopeSatisfied,
				"devices/device_invites": statusdata.ScopeSatisfied,
				"flowlogs":               statusdata.ScopeSatisfied,
				"acl":                    statusdata.ScopeSatisfied,
			},
		},
		{
			name:   "genuinely insufficient",
			scopes: []string{"devices:core:read"},
			known:  true,
			want: map[string]string{
				"devices":                statusdata.ScopeSatisfied,
				"devices/device_invites": statusdata.ScopeInsufficient,
				"flowlogs":               statusdata.ScopeInsufficient,
				"acl":                    statusdata.ScopeInsufficient,
			},
		},
		{
			name:   "non-OAuth auth: nothing is knowable, nothing is warned",
			scopes: nil,
			known:  false,
			want: map[string]string{
				"devices":                statusdata.ScopeUnknown,
				"devices/device_invites": statusdata.ScopeUnknown,
				"flowlogs":               statusdata.ScopeUnknown,
				"acl":                    statusdata.ScopeUnknown,
				"nodemetrics":            statusdata.ScopeNotApplicable,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := BuildCapabilityMatrix(CapabilityInputs{
				Decisions:   decisions,
				Scopes:      tc.scopes,
				ScopesKnown: tc.known,
			})
			for key, want := range tc.want {
				collector, sub, _ := strings.Cut(key, "/")
				got := rowFor(t, rows, collector, sub).ScopeStatus
				if got != want {
					t.Errorf("%s: ScopeStatus = %q, want %q", key, got, want)
				}
			}
		})
	}
}

// TestScopeWarningsOnlyForActionableGaps: a warning is produced only for a row
// that is actually RUNNING with an insufficient scope. Rows that are disabled,
// unsupported, unmodelled, or scope-irrelevant must stay silent, or every
// deployment would warn about collectors it deliberately turned off.
func TestScopeWarningsOnlyForActionableGaps(t *testing.T) {
	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{
			// Running, insufficient -> warn.
			{Collector: "flowlogs", Capability: "flowlogs", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			// Disabled in config -> silent.
			{Collector: "acl", Capability: "acl", ConfigEnabled: false, ProviderSupported: true},
			// Provider does not support it -> silent.
			{Collector: "dns", Capability: "dns", ConfigEnabled: true, ProviderSupported: false},
			// Unmodelled scope -> silent.
			{Collector: "services", Capability: "services", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			// No Tailscale scope needed -> silent.
			{Collector: "nodemetrics", Capability: "nodemetrics", ConfigEnabled: true, ProviderSupported: true, Registered: true},
		},
		Scopes:      []string{"devices:core:read"},
		ScopesKnown: true,
	})

	warnings := ScopeWarnings(rows)
	if len(warnings) != 1 {
		t.Fatalf("ScopeWarnings() = %v, want exactly 1 (flowlogs)", warnings)
	}
	if !strings.Contains(warnings[0], "flowlogs") || !strings.Contains(warnings[0], "logs:network:read") {
		t.Errorf("warning %q should name the collector and the missing scope", warnings[0])
	}
}

// TestBuildCapabilityMatrixReasons pins the bounded non-registration reasons.
// Registration truth comes from the caller's decision, never re-derived here.
func TestBuildCapabilityMatrixReasons(t *testing.T) {
	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{
			{Collector: "devices", Capability: "devices", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			{Collector: "acl", Capability: "acl", ConfigEnabled: false, ProviderSupported: true},
			{Collector: "dns", Capability: "dns", ConfigEnabled: true, ProviderSupported: false},
			{Collector: "flowlogs", Capability: "flowlogs", ConfigEnabled: true, ProviderSupported: true, Registered: false, Source: "stream"},
		},
		Scopes:      []string{"all:read"},
		ScopesKnown: true,
	})

	want := map[string]string{
		"devices":  "",
		"acl":      statusdata.CapabilityReasonConfigDisabled,
		"dns":      statusdata.CapabilityReasonUnsupported,
		"flowlogs": statusdata.CapabilityReasonNotRegistered,
	}
	for collector, wantReason := range want {
		r := rowFor(t, rows, collector, "")
		if r.Reason != wantReason {
			t.Errorf("%s: Reason = %q, want %q", collector, r.Reason, wantReason)
		}
		if r.Active != (wantReason == "") {
			t.Errorf("%s: Active = %v, want %v", collector, r.Active, wantReason == "")
		}
	}
	if got := rowFor(t, rows, "flowlogs", "").Source; got != "stream" {
		t.Errorf("flowlogs Source = %q, want %q (the effective ingestion source)", got, "stream")
	}
}

// TestBuildCapabilityMatrixRowsSorted keeps the machine-readable matrix stable
// for diffing and for the status page.
func TestBuildCapabilityMatrixRowsSorted(t *testing.T) {
	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{
			{Collector: "users", Capability: "users", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			{Collector: "devices", Capability: "devices", ConfigEnabled: true, ProviderSupported: true, Registered: true,
				Subrequests: []SubrequestDecision{
					{Name: SubrequestDeviceInvites, Enabled: true},
					{Name: SubrequestDevicePosture, Enabled: true},
				}},
		},
	})
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Collector + "/" + r.Subrequest
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("rows not sorted by collector/subrequest: %v", keys)
	}
}

// TestCapabilityStateAggregation: a collector with several probed operations
// reports the most operator-relevant one. Actionable states win over healthy
// ones, so a partial scope denial can never be hidden by a sibling success.
func TestCapabilityStateAggregation(t *testing.T) {
	at := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		states []apistate.State
		want   apistate.State
	}{
		{"nothing probed", nil, apistate.StateUnknown},
		{"all healthy", []apistate.State{apistate.StateSupported, apistate.StateSupported}, apistate.StateSupported},
		{"healthy beats disabled", []apistate.State{apistate.StateSupported, apistate.StateDisabled}, apistate.StateSupported},
		{"only disabled", []apistate.State{apistate.StateDisabled}, apistate.StateDisabled},
		{"scope denial beats success", []apistate.State{apistate.StateSupported, apistate.StateScopeDenied}, apistate.StateScopeDenied},
		{"credential rejection beats scope denial", []apistate.State{apistate.StateScopeDenied, apistate.StateCredentialRejected}, apistate.StateCredentialRejected},
		{"transient loses to scope denial", []apistate.State{apistate.StateTransientFailure, apistate.StateScopeDenied}, apistate.StateScopeDenied},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := apistate.NewTracker()
			for i, s := range tc.states {
				tr.Record("devices", string(rune('a'+i)), s, at)
			}
			rows := BuildCapabilityMatrix(CapabilityInputs{
				Decisions: []CollectorDecision{{Collector: "devices", Capability: "devices",
					ConfigEnabled: true, ProviderSupported: true, Registered: true}},
				Tracker: tr,
			})
			r := rowFor(t, rows, "devices", "")
			if r.State != string(tc.want) {
				t.Errorf("State = %q, want %q", r.State, tc.want)
			}
			if r.Actionable != tc.want.Actionable() {
				t.Errorf("Actionable = %v, want %v", r.Actionable, tc.want.Actionable())
			}
			if len(tc.states) > 0 {
				if r.LastProbe != at.UTC().Format(time.RFC3339) {
					t.Errorf("LastProbe = %q, want %q", r.LastProbe, at.UTC().Format(time.RFC3339))
				}
				if len(r.Operations) != len(tc.states) {
					t.Errorf("Operations = %d, want %d", len(r.Operations), len(tc.states))
				}
			}
		})
	}
}

// TestCapabilityMatrixNilTrackerIsSafe: the tracker is optional (self-obs off,
// or nothing probed yet) and must never panic.
func TestCapabilityMatrixNilTrackerIsSafe(t *testing.T) {
	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{{Collector: "devices", Capability: "devices",
			ConfigEnabled: true, ProviderSupported: true, Registered: true}},
		Tracker: nil,
	})
	r := rowFor(t, rows, "devices", "")
	if r.State != string(apistate.StateUnknown) {
		t.Errorf("State = %q, want unknown", r.State)
	}
	if r.LastProbe != "" {
		t.Errorf("LastProbe = %q, want empty", r.LastProbe)
	}
}

// TestEmitCapabilityStatusZeroSeeds mirrors apistate.EmitAvailability: the full
// state set is emitted so a recovered collector's previous state falls to 0
// instead of ghosting at 1 forever under cumulative temporality.
func TestEmitCapabilityStatusZeroSeeds(t *testing.T) {
	rec := telemetrytest.New()
	tr := apistate.NewTracker()
	tr.Record("flowlogs", "getNetworkFlowLogs", apistate.StateScopeDenied, time.Now())

	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{
			{Collector: "flowlogs", Capability: "flowlogs", ConfigEnabled: true, ProviderSupported: true, Registered: true,
				Subrequests: []SubrequestDecision{{Name: SubrequestDeviceInvites, Enabled: true}}},
		},
		Tracker: tr,
	})
	EmitCapabilityStatus(rec.Emitter(), rows)

	pts := rec.MetricPoints(MetricCapabilityStatus)
	if len(pts) != len(apistate.States()) {
		t.Fatalf("got %d points, want %d (one per state, collector-level rows only)", len(pts), len(apistate.States()))
	}
	seen := map[string]float64{}
	for _, p := range pts {
		if p.Attrs[semconv.AttrCollector] != "flowlogs" {
			t.Errorf("unexpected collector attr %q", p.Attrs[semconv.AttrCollector])
		}
		seen[p.Attrs[semconv.AttrAPIState]] = p.Value
	}
	for _, s := range apistate.States() {
		want := 0.0
		if s == apistate.StateScopeDenied {
			want = 1
		}
		if seen[string(s)] != want {
			t.Errorf("state %q = %v, want %v", s, seen[string(s)], want)
		}
	}
}

// TestEmitScopePreflight: one bounded 1/0 datapoint per modelled capability, and
// nothing at all for capabilities whose scope is unknown or not applicable — a
// 0 there would read as a real permission gap.
func TestEmitScopePreflight(t *testing.T) {
	rec := telemetrytest.New()
	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{
			{Collector: "devices", Capability: "devices", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			{Collector: "flowlogs", Capability: "flowlogs", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			{Collector: "services", Capability: "services", ConfigEnabled: true, ProviderSupported: true, Registered: true},
			{Collector: "nodemetrics", Capability: "nodemetrics", ConfigEnabled: true, ProviderSupported: true, Registered: true},
		},
		Scopes:      []string{"devices:core:read"},
		ScopesKnown: true,
	})
	EmitScopePreflight(rec.Emitter(), rows)

	got := map[string]float64{}
	for _, p := range rec.MetricPoints(MetricCapabilityScopeSatisfied) {
		got[p.Attrs[semconv.AttrCapability]] = p.Value
	}
	want := map[string]float64{"devices": 1, "flowlogs": 0}
	if len(got) != len(want) {
		t.Fatalf("scope_satisfied points = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("capability %q = %v, want %v", k, got[k], v)
		}
	}
}

// TestCapabilityCatalogMatchesEmitted is the declaration<->emission drift guard
// for the two capability metrics: unit, instrument, description and attribute
// keys must match the descriptors that document them.
func TestCapabilityCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	tr := apistate.NewTracker()
	tr.Record("devices", "listDevices", apistate.StateSupported, time.Now())
	rows := BuildCapabilityMatrix(CapabilityInputs{
		Decisions: []CollectorDecision{
			{Collector: "devices", Capability: "devices", ConfigEnabled: true, ProviderSupported: true, Registered: true},
		},
		Scopes:      []string{"all:read"},
		ScopesKnown: true,
		Tracker:     tr,
	})
	EmitCapabilityStatus(rec.Emitter(), rows)
	EmitScopePreflight(rec.Emitter(), rows)

	docs := CapabilityCatalog()
	byName := make(map[string]metricdoc.Metric, len(docs))
	for _, d := range docs {
		byName[d.Name] = d
	}
	for _, name := range []string{MetricCapabilityStatus, MetricCapabilityScopeSatisfied} {
		d, ok := byName[name]
		if !ok {
			t.Fatalf("%s has no descriptor in CapabilityCatalog()", name)
		}
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			t.Fatalf("%s was never emitted", name)
		}
		for _, p := range pts {
			if p.Unit != d.Unit {
				t.Errorf("%s unit = %q, want %q", name, p.Unit, d.Unit)
			}
			if p.Description != d.Description {
				t.Errorf("%s description drifted from its descriptor", name)
			}
			if p.Kind != "gauge" {
				t.Errorf("%s kind = %q, want gauge", name, p.Kind)
			}
		}
	}
	telemetrytest.AssertCatalogAttrs(t, rec, docs, nil)
}
