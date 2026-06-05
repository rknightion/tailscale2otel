package contacts

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
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
