package webhooks

import (
	"context"
	"strings"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v3/internal/apistate"
	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

type fakeAPI struct {
	hooks []tsclient.Webhook
	err   error
}

func (f *fakeAPI) Webhooks(context.Context) ([]tsclient.Webhook, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hooks, nil
}

var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "webhooks" {
		t.Fatalf("Name() = %q, want webhooks", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
}

func sampleHooks() []tsclient.Webhook {
	secret := "tskey-webhook-SECRET"
	return []tsclient.Webhook{
		{
			EndpointID:       "wh-1",
			EndpointURL:      "https://hook.example/abc",
			ProviderType:     "slack",
			CreatorLoginName: "creator@example.com",
			Subscriptions:    []tsclient.WebhookSubscriptionType{"nodeCreated", "nodeDeleted"},
			Secret:           &secret,
		},
		{
			EndpointID:    "wh-2",
			EndpointURL:   "https://hook.example/def",
			ProviderType:  "",
			Subscriptions: []tsclient.WebhookSubscriptionType{"userApproved"},
		},
	}
}

func TestCollectEmitsCountAndSubscriptions(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: sampleHooks()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	cnt := rec.MetricPoints("tailscale.webhook_endpoints.count")
	if len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("webhook_endpoints.count = %+v, want one point value 2", cnt)
	}

	subs := rec.MetricPoints("tailscale.webhook_endpoint.subscriptions")
	byID := map[string]float64{}
	for _, p := range subs {
		// Fence: never leak URL, secret, or creator login.
		for k, v := range p.Attrs {
			switch v {
			case "https://hook.example/abc", "https://hook.example/def", "creator@example.com", "tskey-webhook-SECRET":
				t.Fatalf("sensitive value leaked into attr %q=%q", k, v)
			}
		}
		byID[p.Attrs["tailscale.webhook_endpoint.id"]] = p.Value
	}
	if len(subs) != 2 {
		t.Fatalf("subscriptions points = %d, want 2", len(subs))
	}
	if byID["wh-1"] != 2 {
		t.Errorf("wh-1 subscriptions = %v, want 2", byID["wh-1"])
	}
	if byID["wh-2"] != 1 {
		t.Errorf("wh-2 subscriptions = %v, want 1", byID["wh-2"])
	}
}

func TestCollectEmptyWebhooks(t *testing.T) {
	rec := telemetrytest.New()
	// nil slice (the API returns {"webhooks":null} when none are configured).
	if err := New(&fakeAPI{hooks: nil}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	cnt := rec.MetricPoints("tailscale.webhook_endpoints.count")
	if len(cnt) != 1 || cnt[0].Value != 0 {
		t.Fatalf("count = %+v, want one point value 0", cnt)
	}
	if subs := rec.MetricPoints("tailscale.webhook_endpoint.subscriptions"); len(subs) != 0 {
		t.Fatalf("subscriptions points = %d, want 0 when no webhooks", len(subs))
	}
}

func TestPerEntityOffDropsSubscriptions(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: sampleHooks()}, 0, WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.webhook_endpoints.count"); len(cnt) != 1 || cnt[0].Value != 2 {
		t.Fatalf("count = %+v, want value 2", cnt)
	}
	if subs := rec.MetricPoints("tailscale.webhook_endpoint.subscriptions"); len(subs) != 0 {
		t.Fatalf("subscriptions points = %d, want 0 when per_entity off", len(subs))
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

func byEvent(pts []telemetrytest.MetricPoint) map[string]float64 {
	out := map[string]float64{}
	for _, p := range pts {
		out[p.Attrs[attrEvent]] = p.Value
	}
	return out
}

// ---------------------------------------------------------------- #420 ------

// TestClassifiesInsteadOfBlanketPropagating is the #420 regression. This
// collector used to `return err` for everything — and because it goes through
// the upstream tsclient, no typed status even reached the caller.
func TestClassifiesInsteadOfBlanketPropagating(t *testing.T) {
	tests := []struct {
		name          string
		code          int
		wantState     apistate.State
		wantErr       bool
		wantActonable bool
		wantCount     bool
	}{
		{"401 revoked credential", 401, apistate.StateCredentialRejected, true, true, false},
		{"403 missing scope", 403, apistate.StateScopeDenied, true, true, false},
		{"404 feature absent", 404, apistate.StateDisabled, false, false, true},
		{"503 transient", 503, apistate.StateTransientFailure, true, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			err := New(&fakeAPI{err: &tsapi.StatusError{Code: tc.code}}, 0).Collect(context.Background(), rec.Emitter())
			if tc.wantErr != (err != nil) {
				t.Fatalf("Collect() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got := availabilityStates(t, rec)["listWebhooks"]; got != string(tc.wantState) {
				t.Errorf("availability = %q, want %q", got, tc.wantState)
			}
			if got := tc.wantState.Actionable(); got != tc.wantActonable {
				t.Errorf("%s.Actionable() = %v, want %v", tc.wantState, got, tc.wantActonable)
			}
			cnt := rec.MetricPoints("tailscale.webhook_endpoints.count")
			if tc.wantCount {
				if len(cnt) != 1 || cnt[0].Value != 0 {
					t.Errorf("count = %+v, want a single 0", cnt)
				}
			} else if len(cnt) != 0 {
				t.Errorf("count emitted %d points after a %d; we never learned how many endpoints exist", len(cnt), tc.code)
			}
		})
	}
}

// Test401And403AreObservablyDifferent states the invariant directly.
func Test401And403AreObservablyDifferent(t *testing.T) {
	state := func(code int) string {
		rec := telemetrytest.New()
		_ = New(&fakeAPI{err: &tsapi.StatusError{Code: code}}, 0).Collect(context.Background(), rec.Emitter())
		return availabilityStates(t, rec)["listWebhooks"]
	}
	if a, b := state(401), state(403); a == b {
		t.Fatalf("401 and 403 both produced state %q; they must be distinguishable", a)
	}
}

func TestAvailabilitySupportedAndTracker(t *testing.T) {
	probe := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: sampleHooks()}, 0, WithAPIState(tr))
	c.now = func() time.Time { return probe }

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := availabilityStates(t, rec)["listWebhooks"]; got != string(apistate.StateSupported) {
		t.Errorf("availability = %q, want supported", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Collector != "webhooks" || snap[0].Operation != "listWebhooks" {
		t.Fatalf("tracker snapshot = %+v, want one webhooks/listWebhooks entry", snap)
	}
}

// ---------------------------------------------------------------- #417 ------

// TestEventCoverageIsZeroSeededOverTheVocabulary: the whole documented
// subscription enum is emitted every tick, so an event category nobody
// subscribes to reads as an explicit 0 rather than a missing series.
func TestEventCoverageIsZeroSeededOverTheVocabulary(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: sampleHooks()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := byEvent(rec.MetricPoints("tailscale.webhook_endpoints.event_subscriptions"))
	if len(got) != len(EventVocabulary()) {
		t.Fatalf("event_subscriptions series = %d, want %d (the full vocabulary, zero-seeded)", len(got), len(EventVocabulary()))
	}
	for _, ev := range EventVocabulary() {
		if _, ok := got[ev]; !ok {
			t.Errorf("event %q missing from the zero-seeded coverage set", ev)
		}
	}
	// sampleHooks: wh-1 has nodeCreated+nodeDeleted, wh-2 has userApproved.
	if got["nodeCreated"] != 1 {
		t.Errorf("nodeCreated = %v, want 1 endpoint", got["nodeCreated"])
	}
	if got["userApproved"] != 1 {
		t.Errorf("userApproved = %v, want 1 endpoint", got["userApproved"])
	}
	if got["policyUpdate"] != 0 {
		t.Errorf("policyUpdate = %v, want 0 (nobody subscribes)", got["policyUpdate"])
	}
	if got["other"] != 0 {
		t.Errorf("other = %v, want 0 (no unknown values in this fixture)", got["other"])
	}
}

// TestUnknownEventFoldsToOther keeps the label bounded when upstream adds a
// nineteenth event category.
func TestUnknownEventFoldsToOther(t *testing.T) {
	hooks := []tsclient.Webhook{{
		EndpointID:    "wh-1",
		Subscriptions: []tsclient.WebhookSubscriptionType{"nodeCreated", "someFutureEvent", "anotherFutureEvent"},
	}}
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: hooks}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := byEvent(rec.MetricPoints("tailscale.webhook_endpoints.event_subscriptions"))
	if len(got) != len(EventVocabulary()) {
		t.Fatalf("series = %d, want %d — an unknown value minted a new label", len(got), len(EventVocabulary()))
	}
	// Both unknown values land on the same endpoint, so the endpoint is counted
	// once for "other" — this is endpoints-subscribed, not subscriptions.
	if got["other"] != 1 {
		t.Errorf("other = %v, want 1 endpoint", got["other"])
	}
	if _, leaked := got["someFutureEvent"]; leaked {
		t.Error("an unrecognized event value became its own label")
	}
}

// TestDesiredEventsReportsUncovered is the optional expectation set.
func TestDesiredEventsReportsUncovered(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: sampleHooks()}, 0,
		WithDesiredEvents([]string{"nodeCreated", "policyUpdate", "userSuspended"}))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := byEvent(rec.MetricPoints("tailscale.webhook_endpoints.event_desired_covered"))
	if len(got) != 3 {
		t.Fatalf("desired_covered series = %d, want 3 (one per desired event)", len(got))
	}
	if got["nodeCreated"] != 1 {
		t.Errorf("nodeCreated desired_covered = %v, want 1", got["nodeCreated"])
	}
	if got["policyUpdate"] != 0 {
		t.Errorf("policyUpdate desired_covered = %v, want 0 (uncovered)", got["policyUpdate"])
	}
	if got["userSuspended"] != 0 {
		t.Errorf("userSuspended desired_covered = %v, want 0 (uncovered)", got["userSuspended"])
	}

	// The mismatch reason is carried on a bounded log event.
	reasons := map[string]string{}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "tailscale.webhook_endpoints.event_mismatch" {
			continue
		}
		reasons[lr.Attrs[attrEvent]] = lr.Body
	}
	if len(reasons) != 2 {
		t.Fatalf("mismatch log events = %d, want 2", len(reasons))
	}
	for _, ev := range []string{"policyUpdate", "userSuspended"} {
		if !strings.Contains(reasons[ev], "uncovered") {
			t.Errorf("mismatch body for %q = %q, want it to name the uncovered reason", ev, reasons[ev])
		}
	}
}

// TestDesiredEventsUnrecognizedIsItsOwnSignal: a typo in the operator's desired
// list would otherwise fold to "other" and look permanently uncovered with no
// explanation.
func TestDesiredEventsUnrecognizedIsItsOwnSignal(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: sampleHooks()}, 0,
		WithDesiredEvents([]string{"nodeCreated", "nodeCreatd", "userSuspendd"}))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	un := rec.MetricPoints("tailscale.webhook_endpoints.desired_unrecognized")
	if len(un) != 1 || un[0].Value != 2 {
		t.Fatalf("desired_unrecognized = %+v, want a single point value 2", un)
	}
	// The unrecognized entries must not mint labels, and must not silently
	// inflate the "other" desired-coverage series either.
	got := byEvent(rec.MetricPoints("tailscale.webhook_endpoints.event_desired_covered"))
	if _, leaked := got["nodeCreatd"]; leaked {
		t.Error("an unrecognized desired event became its own label")
	}
	if len(got) != 1 {
		t.Errorf("desired_covered series = %d, want 1 (only the recognized entry)", len(got))
	}

	var found bool
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.webhook_endpoints.event_mismatch" && strings.Contains(lr.Body, "unrecognized") {
			found = true
		}
	}
	if !found {
		t.Error("no mismatch log event names the unrecognized reason")
	}
}

// TestNoDesiredEventsEmitsNoExpectationSignals: an empty set means "no
// expectation", so nothing is reported as missing.
func TestNoDesiredEventsEmitsNoExpectationSignals(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: sampleHooks()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if p := rec.MetricPoints("tailscale.webhook_endpoints.event_desired_covered"); len(p) != 0 {
		t.Errorf("desired_covered emitted %d points with no desired set", len(p))
	}
	if p := rec.MetricPoints("tailscale.webhook_endpoints.desired_unrecognized"); len(p) != 0 {
		t.Errorf("desired_unrecognized emitted %d points with no desired set", len(p))
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.webhook_endpoints.event_mismatch" {
			t.Error("mismatch log emitted with no desired set")
		}
	}
}

// TestCoverageNeverLeaksEndpointIdentity is the standing PII fence for #417.
func TestCoverageNeverLeaksEndpointIdentity(t *testing.T) {
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: sampleHooks()}, 0, WithDesiredEvents([]string{"policyUpdate"}))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	secrets := []string{"https://hook.example/abc", "https://hook.example/def", "creator@example.com", "tskey-webhook-SECRET"}
	for _, name := range rec.MetricNames() {
		for _, p := range rec.MetricPoints(name) {
			for k, v := range p.Attrs {
				for _, s := range secrets {
					if strings.Contains(v, s) {
						t.Fatalf("metric %q attr %q leaked %q", name, k, s)
					}
				}
			}
		}
	}
	for _, lr := range rec.LogRecords() {
		for _, s := range secrets {
			if strings.Contains(lr.Body, s) {
				t.Fatalf("log %q body leaked %q", lr.EventName, s)
			}
			for k, v := range lr.Attrs {
				if strings.Contains(v, s) {
					t.Fatalf("log %q attr %q leaked %q", lr.EventName, k, s)
				}
			}
		}
	}
}

// ---------------------------------------------------------------- #426 ------

func TestEndpointAgeHistogram(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	hooks := []tsclient.Webhook{
		{EndpointID: "wh-1", Created: now.Add(-48 * time.Hour)},
		{EndpointID: "wh-2", Created: now.Add(-400 * 24 * time.Hour)},
	}
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: hooks}, 0)
	c.now = func() time.Time { return now }
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.webhook_endpoint.age")
	if len(pts) != 1 {
		t.Fatalf("age points = %d, want 1 (a fleet distribution, not per endpoint)", len(pts))
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Errorf("age kind = %q, want histogram", p.Kind)
	}
	if p.Unit != "s" {
		t.Errorf("age unit = %q, want s", p.Unit)
	}
	if p.Count != 2 {
		t.Errorf("age count = %d, want 2", p.Count)
	}
	if len(p.Attrs) != 0 {
		t.Errorf("age attrs = %v, want none (fleet-level)", p.Attrs)
	}
	wantSum := (48 * time.Hour).Seconds() + (400 * 24 * time.Hour).Seconds()
	if p.Value != wantSum {
		t.Errorf("age sum = %v, want %v", p.Value, wantSum)
	}
}

// TestEndpointAgeSkipsUnknownCreated: #426 is explicit that an absent provider
// timestamp is omitted, never recorded as a zero age — a fleet of "brand new"
// endpoints is a materially wrong answer.
func TestEndpointAgeSkipsUnknownCreated(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	hooks := []tsclient.Webhook{
		{EndpointID: "wh-1", Created: now.Add(-24 * time.Hour)},
		{EndpointID: "wh-2"}, // zero Created
	}
	rec := telemetrytest.New()
	c := New(&fakeAPI{hooks: hooks}, 0)
	c.now = func() time.Time { return now }
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.webhook_endpoint.age")
	if len(pts) != 1 || pts[0].Count != 1 {
		t.Fatalf("age = %+v, want a single histogram with count 1", pts)
	}
}

// TestEndpointAgeSkippedEntirelyWhenNoneKnown avoids an empty histogram series.
func TestEndpointAgeSkippedEntirelyWhenNoneKnown(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{hooks: sampleHooks()}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.webhook_endpoint.age"); len(pts) != 0 {
		t.Fatalf("age points = %d, want 0 when no endpoint reports a Created", len(pts))
	}
}
