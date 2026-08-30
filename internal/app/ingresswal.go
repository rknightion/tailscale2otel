package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/ingresswal"
)

const (
	ingressWALSourceStream  = "stream"
	ingressWALSignalHEC     = "hec"
	ingressWALSourceWebhook = "webhook"
	ingressWALSignalWebhook = "webhook"
	ingressWALInitialRetry  = 100 * time.Millisecond
	ingressWALMaximumRetry  = 5 * time.Second
	ingressWALFlushTimeout  = 10 * time.Second
	ingressWALWakeCapacity  = 1
)

var (
	errIngressWALRoute  = errors.New("ingress WAL route unavailable")
	errIngressWALAppend = errors.New("ingress WAL append unavailable")
	errIngressWALApply  = errors.New("ingress WAL apply unavailable")
	errIngressWALFlush  = errors.New("ingress WAL flush unavailable")
	errIngressWALReplay = errors.New("ingress WAL replay unavailable")
	errIngressWALClose  = errors.New("ingress WAL close unavailable")
)

type ingressWALState string

const (
	ingressWALStateDisabled  ingressWALState = "disabled"
	ingressWALStateReplaying ingressWALState = "replaying"
	ingressWALStateReady     ingressWALState = "ready"
	ingressWALStateRetrying  ingressWALState = "retrying"
	ingressWALStateFull      ingressWALState = "full"
	ingressWALStateFailed    ingressWALState = "failed"
	ingressWALStateDraining  ingressWALState = "draining"
	ingressWALStateStopped   ingressWALState = "stopped"
)

type ingressWALRoute struct {
	tailnet string
	source  string
	signal  string
	apply   func(context.Context, []byte, time.Time) (bool, error)
	drain   func()
	flush   func(context.Context) error
}

type ingressWALRouteKey struct {
	tailnet string
	source  string
	signal  string
}

type ingressWALHealth struct {
	State ingressWALState
	WAL   ingresswal.Health
}

type ingressWALCoordinator struct {
	wal    ingresswal.WAL
	routes map[ingressWALRouteKey]ingressWALRoute
	wake   chan struct{}

	mu    sync.Mutex
	state ingressWALState

	replayMu     sync.Mutex
	progressMu   sync.Mutex
	progress     map[string]ingressWALProgress
	flushTimeout time.Duration

	wait      func(context.Context, <-chan struct{}, time.Duration) ingressWALWaitResult
	closeOnce sync.Once
	closeErr  error
}

type ingressWALPhase uint8

const (
	ingressWALPhasePending ingressWALPhase = iota
	ingressWALPhaseApplied
	ingressWALPhaseFlushed
)

type ingressWALProgress struct {
	phase        ingressWALPhase
	flowsApplied bool
}

type ingressWALWaitResult uint8

const (
	ingressWALWaitCanceled ingressWALWaitResult = iota
	ingressWALWaitWake
	ingressWALWaitTimer
)

func newIngressWALCoordinator(
	wal ingresswal.WAL,
	configured []ingressWALRoute,
) (*ingressWALCoordinator, error) {
	coordinator := &ingressWALCoordinator{
		wal:          wal,
		routes:       make(map[ingressWALRouteKey]ingressWALRoute, len(configured)),
		wake:         make(chan struct{}, ingressWALWakeCapacity),
		state:        ingressWALStateDisabled,
		progress:     make(map[string]ingressWALProgress),
		flushTimeout: ingressWALFlushTimeout,
		wait:         waitIngressWAL,
	}
	if wal == nil {
		if len(configured) != 0 {
			return nil, errIngressWALRoute
		}
		return coordinator, nil
	}
	for _, route := range configured {
		if !validIngressWALRoute(route) {
			return nil, errIngressWALRoute
		}
		key := route.key()
		if _, duplicate := coordinator.routes[key]; duplicate {
			return nil, errIngressWALRoute
		}
		coordinator.routes[key] = route
	}
	if len(coordinator.routes) == 0 {
		return nil, errIngressWALRoute
	}
	coordinator.state = ingressWALStateReplaying
	return coordinator, nil
}

func validIngressWALRoute(route ingressWALRoute) bool {
	if route.tailnet == "" || route.apply == nil || route.flush == nil {
		return false
	}
	switch {
	case route.source == ingressWALSourceStream && route.signal == ingressWALSignalHEC:
		return route.drain != nil
	case route.source == ingressWALSourceWebhook && route.signal == ingressWALSignalWebhook:
		return true
	default:
		return false
	}
}

func (r ingressWALRoute) key() ingressWALRouteKey {
	return ingressWALRouteKey{tailnet: r.tailnet, source: r.source, signal: r.signal}
}

func (c *ingressWALCoordinator) appender(
	tailnet, source, signal string,
) func(context.Context, []byte, time.Time) error {
	key := ingressWALRouteKey{tailnet: tailnet, source: source, signal: signal}
	if _, ok := c.routes[key]; !ok {
		return func(context.Context, []byte, time.Time) error {
			return errIngressWALRoute
		}
	}
	return func(ctx context.Context, body []byte, accepted time.Time) error {
		storedBody := bytes.Clone(body)
		id, err := ingresswal.NewID(key.tailnet, key.source, key.signal, storedBody)
		if err != nil {
			c.setState(ingressWALStateFailed)
			return errIngressWALAppend
		}
		err = c.wal.Append(ctx, ingresswal.Envelope{
			ID:       id,
			Tailnet:  key.tailnet,
			Source:   key.source,
			Signal:   key.signal,
			Accepted: accepted,
			Body:     storedBody,
		})
		if err != nil {
			if errors.Is(err, ingresswal.ErrFull) {
				c.setState(ingressWALStateFull)
				return errors.Join(errIngressWALAppend, ingresswal.ErrFull)
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			c.setState(ingressWALStateFailed)
			return errIngressWALAppend
		}
		c.signalWake()
		return nil
	}
}

func (c *ingressWALCoordinator) signalWake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *ingressWALCoordinator) setState(state ingressWALState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != ingressWALStateStopped {
		c.state = state
	}
}

func (c *ingressWALCoordinator) Health() ingressWALHealth {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	var health ingresswal.Health
	if c.wal != nil {
		health = c.wal.Health()
	}
	return ingressWALHealth{State: state, WAL: health}
}

func (c *ingressWALCoordinator) Ready() bool {
	return c.Health().State == ingressWALStateReady
}

func (c *ingressWALCoordinator) Replay(ctx context.Context) error {
	if c.wal == nil {
		return nil
	}
	c.setState(ingressWALStateReplaying)
	err := c.replay(ctx)
	if err == nil {
		c.setState(ingressWALStateReady)
	}
	return err
}

// ReplayStartup drains the accepted backlog before any ingress listener opens.
// Retryable exporter and cleanup failures use the same bounded backoff as the
// live worker. Permanent storage or route failures stay visible through
// readiness and return without permitting receiver startup.
func (c *ingressWALCoordinator) ReplayStartup(ctx context.Context) error {
	if c.wal == nil {
		return nil
	}
	failures := 0
	for {
		before := c.wal.Health().PendingEntries
		err := c.Replay(ctx)
		after := c.wal.Health().PendingEntries
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if c.Health().State == ingressWALStateFailed {
			return err
		}
		if after < before {
			failures = 1
		} else {
			failures++
		}
		switch c.wait(ctx, c.wake, ingressWALRetryDelay(failures)) {
		case ingressWALWaitCanceled:
			return ctx.Err()
		case ingressWALWaitWake:
			failures = 0
		case ingressWALWaitTimer:
		}
	}
}

func (c *ingressWALCoordinator) replay(ctx context.Context) error {
	c.replayMu.Lock()
	defer c.replayMu.Unlock()

	err := c.wal.Replay(ctx, c.applyEnvelope, c.observeCommit)
	if err == nil {
		c.progressMu.Lock()
		clear(c.progress)
		c.progressMu.Unlock()
		return nil
	}

	bounded, failed := boundedIngressWALReplayError(err)
	if failed {
		c.setState(ingressWALStateFailed)
	} else {
		c.setState(ingressWALStateRetrying)
	}
	return bounded
}

func (c *ingressWALCoordinator) observeCommit(id string) {
	c.progressMu.Lock()
	delete(c.progress, id)
	c.progressMu.Unlock()
}

// applyEnvelope applies one durable receiver body before the WAL writes its
// completion marker. c.progress is deliberately an in-memory ledger: it
// suppresses re-application across retries handled by this coordinator, but it
// is rebuilt empty after a process restart. A persisted applied marker was
// considered and rejected because route.apply is not atomic with a marker
// write (and a crash partway through apply could still replay the whole body).
// TestIngressWALCoordinator_ReappliesAfterCrashBetweenApplyAndCommit pins this
// accepted at-least-once behavior, including duplicate emitted telemetry.
func (c *ingressWALCoordinator) applyEnvelope(
	ctx context.Context,
	envelope ingresswal.Envelope,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := ingressWALRouteKey{
		tailnet: envelope.Tailnet,
		source:  envelope.Source,
		signal:  envelope.Signal,
	}
	route, ok := c.routes[key]
	if !ok || !validIngressWALRoute(route) {
		return errIngressWALRoute
	}

	c.progressMu.Lock()
	progress := c.progress[envelope.ID]
	c.progressMu.Unlock()

	if progress.phase == ingressWALPhasePending {
		flowsApplied, err := route.apply(ctx, envelope.Body, envelope.Accepted)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return errIngressWALApply
		}
		progress = ingressWALProgress{
			phase:        ingressWALPhaseApplied,
			flowsApplied: flowsApplied,
		}
		c.progressMu.Lock()
		c.progress[envelope.ID] = progress
		c.progressMu.Unlock()
	}

	if progress.phase == ingressWALPhaseApplied {
		if progress.flowsApplied && route.source == ingressWALSourceStream {
			route.drain()
		}
		flushCtx, cancel := context.WithTimeout(ctx, c.flushTimeout)
		err := route.flush(flushCtx)
		cancel()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return errIngressWALFlush
		}
		progress.phase = ingressWALPhaseFlushed
		c.progressMu.Lock()
		c.progress[envelope.ID] = progress
		c.progressMu.Unlock()
	}

	return nil
}

func boundedIngressWALReplayError(err error) (error, bool) {
	if errors.Is(err, context.Canceled) {
		return context.Canceled, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded, false
	}
	for _, bounded := range []error{
		errIngressWALRoute,
		errIngressWALApply,
		errIngressWALFlush,
	} {
		if errors.Is(err, bounded) {
			return bounded, errors.Is(err, errIngressWALRoute)
		}
	}
	for _, permanent := range []error{
		ingresswal.ErrCorrupt,
		ingresswal.ErrIncompatible,
		ingresswal.ErrOwnership,
		ingresswal.ErrClosed,
		ingresswal.ErrUnsupported,
	} {
		if errors.Is(err, permanent) {
			return errors.Join(errIngressWALReplay, permanent), true
		}
	}
	return errIngressWALReplay, false
}

func (c *ingressWALCoordinator) Run(ctx context.Context) error {
	if c.wal == nil {
		c.wait(ctx, c.wake, 0)
		return nil
	}

	failures := 0
	for {
		before := c.wal.Health().PendingEntries
		err := c.Replay(ctx)
		after := c.wal.Health().PendingEntries
		if err == nil {
			failures = 0
			switch c.wait(ctx, c.wake, 0) {
			case ingressWALWaitWake:
				continue
			case ingressWALWaitCanceled:
				return nil
			case ingressWALWaitTimer:
				continue
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}

		if after < before {
			failures = 1
		} else {
			failures++
		}
		switch c.wait(ctx, c.wake, ingressWALRetryDelay(failures)) {
		case ingressWALWaitWake:
			failures = 0
		case ingressWALWaitCanceled:
			return nil
		case ingressWALWaitTimer:
		}
	}
}

func ingressWALRetryDelay(failures int) time.Duration {
	if failures <= 1 {
		return ingressWALInitialRetry
	}
	delay := ingressWALInitialRetry
	for range failures - 1 {
		if delay >= ingressWALMaximumRetry/2 {
			return ingressWALMaximumRetry
		}
		delay *= 2
	}
	if delay > ingressWALMaximumRetry {
		return ingressWALMaximumRetry
	}
	return delay
}

func waitIngressWAL(
	ctx context.Context,
	wake <-chan struct{},
	delay time.Duration,
) ingressWALWaitResult {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ingressWALWaitCanceled
		case <-wake:
			return ingressWALWaitWake
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ingressWALWaitCanceled
	case <-wake:
		return ingressWALWaitWake
	case <-timer.C:
		return ingressWALWaitTimer
	}
}

func (c *ingressWALCoordinator) Drain(ctx context.Context) error {
	if c.wal == nil {
		return nil
	}
	c.setState(ingressWALStateDraining)
	err := c.replay(ctx)
	if err == nil {
		c.setState(ingressWALStateDraining)
	}
	return err
}

func (c *ingressWALCoordinator) Close() error {
	c.closeOnce.Do(func() {
		if c.wal != nil {
			if err := c.wal.Close(); err != nil {
				c.closeErr = errIngressWALClose
			}
		}
		c.mu.Lock()
		c.state = ingressWALStateStopped
		c.mu.Unlock()
	})
	return c.closeErr
}
