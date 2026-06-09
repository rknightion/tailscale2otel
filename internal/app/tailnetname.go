package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// tailnetNameResolver is the narrow slice of *tsapi.Client used to discover the
// canonical tailnet name from the audit-log response envelope.
type tailnetNameResolver interface {
	ConfigAuditLogs(ctx context.Context, start, end time.Time) (audit.ConfigurationResponse, error)
}

// resolveTailnetName best-effort resolves the canonical tailnet name (e.g.
// "m7kni.io") from the audit-log response envelope's tailnetId, used when the
// configured tailnet is the "-" placeholder. The envelope carries the name even
// for an empty log window. Returns "" on any failure (missing
// logs:configuration:read scope, API error, timeout, empty envelope) — the caller
// then keeps the literal "-". Never blocks startup beyond the internal timeout.
func resolveTailnetName(ctx context.Context, c tailnetNameResolver, now time.Time, logger *slog.Logger) string {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.ConfigAuditLogs(rctx, now.Add(-5*time.Minute), now)
	if err != nil {
		if logger != nil {
			logger.Debug("tailnet-name auto-resolution failed; using placeholder", "error", err)
		}
		return ""
	}
	return resp.TailnetName()
}

// newResolverClient builds a minimal tsapi.Client for one-shot tailnet-name
// resolution: same auth/transport as the real per-tailnet client (via
// tsapiOptionsFor) but without the self-obs request hooks, since the telemetry
// emitter does not exist yet at this point in app.New. It uses rt's API path
// verbatim (the "-" placeholder) — resolution never alters the API path.
func newResolverClient(rt config.ResolvedTailnet, logger *slog.Logger) (*tsapi.Client, error) {
	o := tsapiOptionsFor(rt)
	o.Logger = withComponent(logger, compTSAPI)
	return tsapi.NewClient(o)
}
