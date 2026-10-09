package app

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

const (
	ingressWALSourceStream  = "stream"
	ingressWALSignalHEC     = "hec"
	ingressWALSourceWebhook = "webhook"
	ingressWALSignalWebhook = "webhook"
	ingressWALInitialRetry  = 100 * time.Millisecond
	ingressWALMaximumRetry  = 5 * time.Second
	ingressWALWindowEntries = 64
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
	tailnet  string
	source   string
	signal   string
	prepare  func(context.Context, []byte, time.Time) (telemetry.IngressWork, error)
	delivery *telemetry.Provider
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

	replayMu   sync.Mutex
	progressMu sync.Mutex
	progress   map[ingresswal.Generation]*ingressWALProgress
	order      uint64
	terminal   bool

	wait      func(context.Context, <-chan struct{}, time.Duration) ingressWALWaitResult
	closeOnce sync.Once
	closeErr  error
}

type ingressWALProgress struct {
	entry    ingresswal.PreparedEntry
	work     *telemetry.IngressWork
	receipt  *telemetry.IngressReceipt
	delivery *telemetry.Provider
	order    uint64
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
		wal:      wal,
		routes:   make(map[ingressWALRouteKey]ingressWALRoute, len(configured)),
		wake:     make(chan struct{}, ingressWALWakeCapacity),
		state:    ingressWALStateDisabled,
		progress: make(map[ingresswal.Generation]*ingressWALProgress),
		wait:     waitIngressWAL,
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
	if route.tailnet == "" || route.prepare == nil || route.delivery == nil {
		return false
	}
	switch {
	case route.source == ingressWALSourceStream && route.signal == ingressWALSignalHEC:
		return true
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
		// A WAL that has failed permanently — corrupt, incompatible, unowned, or
		// carrying an unknown persisted route — can never be replayed, so an
		// entry appended to it would be acknowledged to the sender and then
		// never applied. Fail closed here rather than relying on the underlying
		// store to reject the write: it may well accept it. The startup drain's
		// receiver budget depends on this (see App.startIngressWAL) and so does
		// the live worker, which can reach the same state long after the
		// listeners have bound.
		if c.Health().State == ingressWALStateFailed || c.routes[key].delivery.IngressFailure() != nil {
			c.setState(ingressWALStateFailed)
			return errIngressWALAppend
		}
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
		// Re-checked after the write, because replay can mark the WAL permanently
		// failed while this append is in flight. The entry is on disk by now and
		// will never replay, so refuse the REQUEST rather than acknowledge a
		// payload that cannot be applied: the sender retries, and one orphan
		// entry is no worse than the rest of an unreplayable WAL. Done here
		// instead of holding a lock across Append, which would serialize every
		// receiver behind one fsync to close a window this already makes
		// unobservable to the sender.
		if c.Health().State == ingressWALStateFailed || c.routes[key].delivery.IngressFailure() != nil {
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
	if c.state != ingressWALStateStopped && (c.state != ingressWALStateFailed || state == ingressWALStateStopped) {
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
	if c.Health().State == ingressWALStateFailed {
		// Failed admission/application stays failed, but later ACKs of already
		// registered, unrelated receipts can still retire their generations.
		c.replayMu.Lock()
		err := c.commitEligible(ctx)
		c.replayMu.Unlock()
		if err != nil {
			// Bounded class only: raw storage errors can carry WAL entry names,
			// which embed the opaque envelope ID.
			err, _ = boundedIngressWALReplayError(err)
		}
		return errors.Join(errIngressWALApply, err)
	}
	c.setState(ingressWALStateReplaying)
	for {
		err := c.replay(ctx)
		if err == nil {
			c.setState(ingressWALStateReady)
			return nil
		}
		if !errors.Is(err, errIngressWALFlush) {
			return err
		}
		if c.wait(ctx, c.wake, ingressWALInitialRetry) == ingressWALWaitCanceled {
			return canceledWaitError(ctx)
		}
	}
}

// canceledWaitError never reports success for a wait that ended by
// cancellation, even when an injected wait reports it without ctx ending.
func canceledWaitError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
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

	var err error
	if !c.terminal {
		err = c.fillWindow(ctx)
		if err == nil {
			err = c.applyWindow(ctx)
		}
	}
	// Poisoned work fails closed, but already eligible unrelated generations
	// may still finish their durable cleanup. No callback is retried here.
	err = errors.Join(err, c.commitEligible(ctx))
	if err == nil {
		if c.terminal {
			// Terminal drain only finishes receipts already applied before the
			// lifecycle seal; unapplied disk backlog waits for the next process.
			if !c.liveReceipts() {
				return nil
			}
		} else if c.wal.Health().PendingEntries == 0 {
			return nil
		}
		if !c.terminal {
			// Nothing failed: applied work is waiting for its normal scheduled
			// collection or delivery. That is healthy, so it must not read as
			// retrying (readiness treats retrying as a failure) unless one of
			// the providers that work depends on reports real delivery trouble.
			if c.deliveryRetrying() {
				c.setState(ingressWALStateRetrying)
			} else {
				c.setState(ingressWALStateReady)
			}
		}
		return errIngressWALFlush
	}
	bounded, failed := boundedIngressWALReplayError(err)
	if failed {
		c.setState(ingressWALStateFailed)
	} else if !c.terminal {
		// A terminal drain stays visibly draining while it retries receipts.
		c.setState(ingressWALStateRetrying)
	}
	return bounded
}

// deliveryRetrying reports whether any provider owning a live receipt in the
// window currently has failing delivery.
func (c *ingressWALCoordinator) deliveryRetrying() bool {
	seen := make(map[*telemetry.Provider]bool)
	for _, progress := range c.orderedWindow() {
		if progress.receipt == nil || progress.delivery == nil || seen[progress.delivery] {
			continue
		}
		seen[progress.delivery] = true
		if progress.delivery.IngressDeliveryRetrying() {
			return true
		}
	}
	return false
}

// liveReceipts reports applied, non-poisoned receipts still awaiting delivery
// or generation-checked retirement.
func (c *ingressWALCoordinator) liveReceipts() bool {
	for _, progress := range c.orderedWindow() {
		if progress.receipt != nil && progress.receipt.Phase() != telemetry.Poisoned {
			return true
		}
	}
	return false
}

func (c *ingressWALCoordinator) observeCommit(g ingresswal.Generation) {
	c.progressMu.Lock()
	progress := c.progress[g]
	c.progressMu.Unlock()
	if progress != nil && progress.receipt != nil && progress.receipt.Eligible() {
		_ = c.releaseProgress(g, progress)
	}
}

func (c *ingressWALCoordinator) releaseProgress(g ingresswal.Generation, progress *ingressWALProgress) error {
	if err := c.wal.ReleasePrepared(g); err != nil {
		return err
	}
	if err := progress.delivery.ReleaseIngress(progress.receipt); err != nil {
		return errIngressWALApply
	}
	c.progressMu.Lock()
	delete(c.progress, g)
	c.progressMu.Unlock()
	return nil
}

func (c *ingressWALCoordinator) fillWindow(ctx context.Context) error {
	health := c.wal.Health()
	c.progressMu.Lock()
	held := make([]ingresswal.Generation, 0, len(c.progress))
	var used int64
	for g, progress := range c.progress {
		held = append(held, g)
		if progress.entry.EncodedBytes > health.MaxBytes-used {
			c.progressMu.Unlock()
			return errIngressWALReplay
		}
		used += progress.entry.EncodedBytes
	}
	remaining := min(ingressWALWindowEntries, health.MaxEntries) - len(held)
	c.progressMu.Unlock()
	if remaining <= 0 || used >= health.MaxBytes {
		return nil
	}
	entries, err := c.wal.PrepareWindow(ctx, ingresswal.WindowLimits{MaxEntries: remaining, MaxBytes: health.MaxBytes - used}, held, c.observeCommit)
	if err != nil {
		return err
	}
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	for _, entry := range entries {
		if _, exists := c.progress[entry.Generation]; exists {
			continue
		}
		c.order++
		c.progress[entry.Generation] = &ingressWALProgress{entry: entry, order: c.order}
	}
	return nil
}

func (c *ingressWALCoordinator) orderedWindow() []*ingressWALProgress {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	result := make([]*ingressWALProgress, 0, len(c.progress))
	for _, progress := range c.progress {
		result = append(result, progress)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].order < result[j].order })
	return result
}

// One bounded application worker skips providers with pre-effect backpressure.
// All complete original programs in a provider window share one first-cover
// group. Delivery happens independently; no ForceFlush or per-body Collect.
func (c *ingressWALCoordinator) applyWindow(ctx context.Context) error {
	blocked := make(map[*telemetry.Provider]bool)
	providers := make(map[*telemetry.Provider]bool)
	defer func() {
		for provider := range providers {
			provider.SealIngress()
		}
	}()
	for _, progress := range c.orderedWindow() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress.receipt != nil {
			if progress.receipt.Phase() == telemetry.Poisoned {
				return errIngressWALApply
			}
			continue
		}
		envelope := progress.entry.Envelope
		route, ok := c.routes[ingressWALRouteKey{tailnet: envelope.Tailnet, source: envelope.Source, signal: envelope.Signal}]
		if !ok || !validIngressWALRoute(route) {
			return errIngressWALRoute
		}
		if blocked[route.delivery] {
			continue
		}
		if progress.work == nil {
			work, err := route.prepare(ctx, envelope.Body, envelope.Accepted)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errIngressWALApply
			}
			progress.work = &work
			progress.delivery = route.delivery
		}
		receipt, err := route.delivery.ReserveIngress(progress.work.Bounds)
		if errors.Is(err, telemetry.ErrIngressBackpressure) {
			blocked[route.delivery] = true
			continue
		}
		if err != nil {
			return errIngressWALApply
		}
		// Register BEFORE entry. Errors/panics after Building keep this receipt;
		// a canceled waiter never discards it or invites reapplication.
		progress.receipt = receipt
		providers[route.delivery] = true
		err = route.delivery.ApplyIngress(ctx, receipt, *progress.work)
		if err != nil {
			if receipt.Phase() == telemetry.Reserved {
				if releaseErr := route.delivery.ReleaseUnstarted(receipt); releaseErr != nil {
					return errIngressWALApply
				}
				progress.receipt = nil
				if errors.Is(err, telemetry.ErrIngressBackpressure) {
					blocked[route.delivery] = true
					continue
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return errIngressWALApply
		}
		progress.work = nil
	}
	return nil
}

func (c *ingressWALCoordinator) commitEligible(ctx context.Context) error {
	var combined error
	for _, progress := range c.orderedWindow() {
		if progress.receipt == nil || !progress.receipt.Eligible() {
			continue
		}
		outcome, err := c.wal.CommitPrepared(ctx, progress.entry.Generation)
		if err != nil {
			combined = errors.Join(combined, err)
			continue
		}
		if outcome == ingresswal.PreparedRetired {
			combined = errors.Join(combined, c.releaseProgress(progress.entry.Generation, progress))
		}
	}
	return combined
}

func boundedIngressWALReplayError(err error) (error, bool) {
	// A poisoned entered callback remains a permanent application failure even
	// if cancellation or a cleanup error is joined to the same outcome.
	if errors.Is(err, errIngressWALApply) {
		return errIngressWALApply, true
	}
	if errors.Is(err, errIngressWALRoute) {
		return errIngressWALRoute, true
	}
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
			return bounded, errors.Is(err, errIngressWALRoute) || errors.Is(err, errIngressWALApply)
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
	c.replayMu.Lock()
	c.terminal = true // Terminal drain never prepares or applies disk backlog.
	c.replayMu.Unlock()
	c.setState(ingressWALStateDraining)
	for {
		err := c.replay(ctx)
		if err == nil {
			c.setState(ingressWALStateDraining)
			return nil
		}
		if !errors.Is(err, errIngressWALFlush) {
			return err
		}
		if c.wait(ctx, c.wake, ingressWALInitialRetry) == ingressWALWaitCanceled {
			return canceledWaitError(ctx)
		}
	}
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
