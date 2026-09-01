package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/coordination"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// runCoordinated keeps the admin listener live while it campaigns. Every
// collector, receiver, replay worker and heartbeat remains inside runActive,
// so only the Lease holder can start them.
func (a *App) runCoordinated(ctx context.Context) error {
	a.coordinationMu.Lock()
	a.coordinationParent = ctx
	a.coordinationMu.Unlock()
	if a.adminSrv != nil {
		go a.runAdmin(ctx) //nolint:gosec // G118 false positive: runAdmin's only context.Background is the bounded graceful-shutdown context
	}
	c, err := coordination.New(coordination.Options{
		LeaseName:     a.cfg.Coordination.LeaseName,
		Namespace:     a.cfg.Coordination.Namespace,
		LeaseDuration: a.cfg.Coordination.LeaseDuration.D(),
		RenewDeadline: a.cfg.Coordination.RenewDeadline.D(),
		RetryPeriod:   a.cfg.Coordination.RetryPeriod.D(),
		Logger:        a.logger,
		Observe:       a.observeCoordination,
	})
	if err != nil {
		return err
	}
	err = c.Run(ctx, func(activeCtx context.Context) error {
		return a.runActive(activeCtx, false)
	})
	if errors.Is(err, coordination.ErrLeadershipLost) {
		// Demotion is an expected, successful process exit. The active callback
		// has already stopped and flushed before client-go invokes this path.
		return nil
	}
	if err != nil {
		return fmt.Errorf("run lease coordination: %w", err)
	}
	return nil
}

func (a *App) observeCoordination(s coordination.Status) {
	a.coordinationMu.Lock()
	a.coordinationStatus = s
	a.coordinationMu.Unlock()
	value := 0.0
	if s.State == coordination.StateLeader {
		value = 1
	}
	a.procEmitter.Gauge(appcatalog.DocCoordinationLeader.Name, appcatalog.DocCoordinationLeader.Unit,
		appcatalog.DocCoordinationLeader.Description, value, telemetry.Attrs{
			"coordination.mode":       "kubernetes",
			"coordination.lease_name": s.LeaseName,
			"coordination.namespace":  s.Namespace,
			"coordination.identity":   s.Identity,
			"coordination.state":      string(s.State),
		})
}

func (a *App) currentCoordination() coordination.Status {
	a.coordinationMu.RLock()
	defer a.coordinationMu.RUnlock()
	return a.coordinationStatus
}

// markCoordinationSteppedDown records an API-server renewal demotion before
// runActive tears down telemetry. A root-context cancellation is an ordinary
// process shutdown and intentionally remains stopped rather than stepped down.
func (a *App) markCoordinationSteppedDown() {
	a.coordinationMu.RLock()
	root := a.coordinationParent
	status := a.coordinationStatus
	a.coordinationMu.RUnlock()
	if root == nil || root.Err() != nil || status.State != coordination.StateLeader {
		return
	}
	status.State = coordination.StateSteppedDown
	a.observeCoordination(status)
}
