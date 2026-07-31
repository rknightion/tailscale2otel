package contacts

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// fakeAPI implements the narrow contacts api interface for tests.
type fakeAPI struct {
	contacts *tsclient.Contacts
	err      error
}

func (f *fakeAPI) Contacts(context.Context) (*tsclient.Contacts, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.contacts, nil
}

// SnapshotCollector compile-time check.
var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "contacts" {
		t.Fatalf("Name() = %q, want contacts", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
	if got := New(&fakeAPI{}, 90*time.Second).DefaultInterval(); got != 90*time.Second {
		t.Fatalf("DefaultInterval(90s) = %v", got)
	}
}

func TestCollectEmitsNeedsVerificationPerType(t *testing.T) {
	api := &fakeAPI{contacts: &tsclient.Contacts{
		Account:  tsclient.Contact{Email: "a@b.com", NeedsVerification: false},
		Support:  tsclient.Contact{Email: "s@b.com", NeedsVerification: true},
		Security: tsclient.Contact{Email: "sec@b.com", NeedsVerification: false},
	}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.contact.needs_verification")
	byType := map[string]float64{}
	for _, p := range pts {
		if p.Kind != "gauge" {
			t.Fatalf("needs_verification kind = %q, want gauge", p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("needs_verification unit = %q, want 1", p.Unit)
		}
		// Guard against ever leaking the email address as a label.
		for k, v := range p.Attrs {
			if v == "a@b.com" || v == "s@b.com" || v == "sec@b.com" {
				t.Fatalf("contact email leaked into attr %q=%q", k, v)
			}
		}
		byType[p.Attrs["tailscale.contact.type"]] = p.Value
	}

	want := map[string]float64{"account": 0, "support": 1, "security": 0}
	if len(pts) != len(want) {
		t.Fatalf("needs_verification points = %d, want %d (%v)", len(pts), len(want), byType)
	}
	for typ, v := range want {
		got, ok := byType[typ]
		if !ok {
			t.Fatalf("missing point for contact type %q", typ)
		}
		if got != v {
			t.Fatalf("contact %q = %v, want %v", typ, got, v)
		}
	}
}

func TestCollectPropagatesError(t *testing.T) {
	api := &fakeAPI{err: context.DeadlineExceeded}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect: expected error, got nil")
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

// TestAvailabilityStates is the #524 coverage: success and every classified
// failure must each report their own distinct, correctly-mapped state, and a
// 403 must land on scope_denied — never disabled — since contacts carry no
// Disposition override.
func TestAvailabilityStates(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState apistate.State
		wantErr   bool
	}{
		{"success", nil, apistate.StateSupported, false},
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, apistate.StateCredentialRejected, true},
		{"403 scope denied", &tsapi.StatusError{Code: 403}, apistate.StateScopeDenied, true},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, apistate.StateRequestRejected, true},
		{"transient transport error", context.DeadlineExceeded, apistate.StateTransientFailure, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			api := &fakeAPI{err: tc.err}
			if tc.err == nil {
				api.contacts = &tsclient.Contacts{}
			}
			err := New(api, 0).Collect(context.Background(), rec.Emitter())
			if tc.wantErr != (err != nil) {
				t.Fatalf("Collect() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got := availabilityStates(t, rec)["getContacts"]; got != string(tc.wantState) {
				t.Errorf("availability = %q, want %q", got, tc.wantState)
			}
		})
	}
}

// TestAvailabilityTracker asserts the shared tracker is wired and the
// last-probe metric is emitted with the injected clock's timestamp.
func TestAvailabilityTracker(t *testing.T) {
	probe := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	tr := apistate.NewTracker()
	rec := telemetrytest.New()
	api := &fakeAPI{contacts: &tsclient.Contacts{}}
	c := New(api, 0, WithAPIState(tr), WithClock(func() time.Time { return probe }))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Collector != "contacts" || snap[0].Operation != "getContacts" {
		t.Fatalf("tracker snapshot = %+v, want one contacts/getContacts entry", snap)
	}
	lp := rec.MetricPoints(apistate.MetricLastProbe)
	if len(lp) != 1 || lp[0].Value != float64(probe.Unix()) {
		t.Fatalf("last_probe = %+v, want one point at %v", lp, probe.Unix())
	}
}
