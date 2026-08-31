package app

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

type fakeOrganizationTailnetLister struct {
	organization string
	tailnets     []tsapi.OrganizationTailnet
}

func (f *fakeOrganizationTailnetLister) OrganizationTailnets(_ context.Context, organization string) ([]tsapi.OrganizationTailnet, error) {
	f.organization = organization
	return f.tailnets, nil
}

func TestLoadOrganizationRosterEmitsInventoryCount(t *testing.T) {
	rec := telemetrytest.New()
	lister := &fakeOrganizationTailnetLister{tailnets: []tsapi.OrganizationTailnet{{ID: "one"}, {ID: "two"}}}

	got, err := loadOrganizationRoster(context.Background(), lister, "example-org", rec.Emitter())
	if err != nil {
		t.Fatalf("loadOrganizationRoster: %v", err)
	}
	if lister.organization != "example-org" {
		t.Fatalf("organization = %q, want example-org", lister.organization)
	}
	if len(got) != 2 {
		t.Fatalf("roster length = %d, want 2", len(got))
	}
	pts := rec.MetricPoints("tailscale.organization.tailnets.count")
	if len(pts) != 1 || pts[0].Value != 2 {
		t.Fatalf("inventory points = %#v, want one point with value 2", pts)
	}
}
