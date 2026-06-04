package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// apiObserver returns a tsapi request-observer that records one
// tailscale2otel.api.requests increment per request (keyed by endpoint and
// status code) and, when a request was retried, the retry count on
// tailscale2otel.api.retries. It is wired into tsapi only when
// self-observability is enabled. The metric descriptors live in
// internal/appcatalog (see that package for why).
func apiObserver(e telemetry.Emitter) func(endpoint string, status, attempts int) {
	return func(endpoint string, status, attempts int) {
		e.Counter(appcatalog.DocAPIRequests.Name, appcatalog.DocAPIRequests.Unit, appcatalog.DocAPIRequests.Description, 1,
			telemetry.Attrs{
				"endpoint":                  endpoint,
				"http.response.status_code": status,
			})
		if attempts > 1 {
			e.Counter(appcatalog.DocAPIRetries.Name, appcatalog.DocAPIRetries.Unit, appcatalog.DocAPIRetries.Description,
				float64(attempts-1), telemetry.Attrs{"endpoint": endpoint})
		}
	}
}

// emitComponentError records one tailscale2otel.component.errors increment for a
// failed non-collector subsystem (receivers, admin server, auto-configure),
// classified by component. Pass an appcatalog.Component* value to keep the
// attribute set closed (bounded cardinality).
func emitComponentError(e telemetry.Emitter, component string) {
	e.Counter(appcatalog.DocComponentErrors.Name, appcatalog.DocComponentErrors.Unit,
		appcatalog.DocComponentErrors.Description, 1,
		telemetry.Attrs{semconv.AttrComponent: component})
}

// componentError emits a component-error increment when self-observability is
// enabled, keeping the gate in one place for the call sites in Run/runAdmin.
func (a *App) componentError(component string) {
	if a.cfg.SelfObservability.Enabled {
		emitComponentError(a.emitter, component)
	}
}

// Admin auth-rejection reasons label MetricAdminAuthRejected. A CLOSED set keeps
// the "reason" attribute's cardinality bounded.
const (
	attrReason               = "reason"
	reasonMissingCredentials = "missing_credentials"
	reasonBadCredentials     = "bad_credentials"
)

// emitAdminAuthRejected records one tailscale2otel.admin.auth.rejected increment
// for an admin request that failed the auth gate, classified by reason. The
// descriptor lives in internal/appcatalog (see that package for why).
func emitAdminAuthRejected(e telemetry.Emitter, reason string) {
	e.Counter(appcatalog.DocAdminAuthRejected.Name, appcatalog.DocAdminAuthRejected.Unit,
		appcatalog.DocAdminAuthRejected.Description, 1,
		telemetry.Attrs{attrReason: reason})
}

// adminAuthRejected emits an admin auth-rejection increment when
// self-observability is enabled, keeping the gate in one place for the admin
// middleware call site.
func (a *App) adminAuthRejected(reason string) {
	if a.cfg.SelfObservability.Enabled {
		emitAdminAuthRejected(a.emitter, reason)
	}
}

// isCleanShutdownErr reports whether err is a normal stop signal (context
// cancellation/deadline or a closed HTTP server, including wrapped) rather than
// a real failure — so a SIGTERM does not register as a component error.
func isCleanShutdownErr(err error) bool {
	return err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, http.ErrServerClosed)
}
