package app

import (
	"context"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

type organizationTailnetLister interface {
	OrganizationTailnets(context.Context, string) ([]tsapi.OrganizationTailnet, error)
}

// loadOrganizationRoster inventories the configured organization without
// manufacturing per-tailnet credentials. The returned IDs are retained by the
// app as its discovered set; only explicitly credentialed tailnets become
// collection runtimes.
func loadOrganizationRoster(ctx context.Context, client organizationTailnetLister, organization string, emitter telemetry.Emitter) ([]tsapi.OrganizationTailnet, error) {
	tailnets, err := client.OrganizationTailnets(ctx, organization)
	if err != nil {
		return nil, err
	}
	emitter.Gauge(appcatalog.DocOrganizationTailnetsCount.Name,
		appcatalog.DocOrganizationTailnetsCount.Unit,
		appcatalog.DocOrganizationTailnetsCount.Description,
		float64(len(tailnets)), nil)
	return tailnets, nil
}
