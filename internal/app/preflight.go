package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// PrepareConfig returns a COPY of cfg with every inbound listener and durable
// side effect that -preflight/-once (issue #311) must never trigger forced
// off, regardless of what the operator's config says: the admin server, the
// Prometheus pull endpoint, the streaming receiver, the webhook receiver, and
// the ingress WAL (which opens/creates files during New() itself, independent
// of whether any receiver is enabled — see buildIngressWAL). RunOnce drives
// exactly one collection cycle directly; it never calls Run(), which is the
// only place those listeners actually start — but forcing the config too
// means the guarantee does not depend on remembering that, and it lets a test
// assert the built *App carries no listener/WAL at all even when the input
// cfg has them all enabled.
//
// preflight selects the three further suppressions that separate -preflight's
// "prove it works, change nothing" contract from -once's "run one real cycle":
//
//   - checkpoint.store=memory, so no window collector's replay-seen state and
//     no scheduler checkpoint can touch disk. Applied to -preflight always,
//     independent of -preflight-export.
//   - profiling.pyroscope.enabled and grafana_annotations off. Both start
//     during New(), before Run(): Pyroscope can push profiles and the annotation
//     writer synchronously POSTs a lifecycle marker to prove its token.
//   - collectors.acl.validate off. This is the ONE non-GET request the
//     exporter makes: POST /tailnet/{t}/acl/validate (internal/tsapi). It
//     mutates nothing — it asks the control plane to check the policy — but
//     -preflight is sold as something an operator can point at production
//     knowing it only reads, and "only reads, except for one POST you have to
//     know about" is not that guarantee. The report says so rather than
//     hiding it, so nobody reads a green preflight as proof the ACL policy
//     validates. -once, which promises a real cycle, leaves it alone.
//
// Reverse-DNS enrichment (enrichment.reverse_dns) is deliberately NOT forced
// off here: it is a bounded, cached, read-only PTR lookup rather than a
// control-plane call, and suppressing it would change what a collection cycle
// actually does. If an operator wants a fully network-silent -preflight they
// must also disable it in config.
func PrepareConfig(cfg *config.Config, preflight bool) *config.Config {
	out := *cfg
	out.Admin.Enabled = false
	out.Prometheus.Enabled = false
	out.Delivery.Mode = "otlp"
	out.Streaming.Enabled = false
	out.Webhook.Enabled = false
	out.IngressWAL.Enabled = false
	if preflight {
		out.Checkpoint.Store = "memory"
		out.Checkpoint.EvidenceStore = "memory"
		out.Profiling.Pyroscope.Enabled = false
		out.GrafanaAnnotations = config.GrafanaAnnotationsConfig{}
		out.Collectors.Acl.Validate = false
	}
	return &out
}

// ACLValidateSuppressed reports whether preparing cfg for a preflight run
// actually turned an enabled ACL validation off, so the report can disclose
// that the run did not exercise it. Silence would let a green preflight read
// as "the ACL policy checks out", which it did not test.
func ACLValidateSuppressed(cfg *config.Config) bool {
	return cfg.Collectors.Acl.Enabled && cfg.Collectors.Acl.Validate
}

// CollectorRunResult is the outcome of running exactly one (tailnet,
// collector) cycle once, via RunOnce.
type CollectorRunResult struct {
	Tailnet   string
	Collector string
	OK        bool
	Duration  time.Duration
	Err       error
	// AuthFailure is true when Err represents an unambiguous credential/auth
	// rejection (an HTTP 401 from the control plane, or an OAuth token
	// request rejected with 401) rather than a generic collection failure.
	// Deliberately NOT set for 403: this repo's collectors already use a 403
	// to mean "this feature is off/plan-gated" in several places (see
	// tsapi.StatusCode's doc comment), which is a collector-specific outcome,
	// not a "your credentials are wrong" outcome — conflating the two would
	// misdirect an operator to rotate a working credential.
	AuthFailure bool
}

// RunOnce drives exactly one collection cycle of every collector registered
// on every tailnet runtime, bypassing internal/collector.Scheduler's
// continuous ticker entirely. It is the bounded, single-shot execution mode
// behind -preflight and -once (issue #311).
//
// It never reads or writes any CheckpointStore itself: a window collector's
// [from, to) is computed fresh from Entry.InitialLookback/MaxWindow/Lag on
// every call (see onceWindow), independent of any persisted cursor — the
// checkpoint behavior operators care about (persisted vs. in-memory) is
// entirely a property of what CheckpointStore New() wired into the
// collectors' own replay-seen state (see PrepareConfig), not of RunOnce.
//
// ctx's deadline (the caller wraps this with context.WithTimeout, honoring
// -preflight-timeout) bounds the whole run. A collector whose Collect/
// CollectWindow call has not returned when ctx is done is abandoned — its
// goroutine may still be running when RunOnce returns — rather than hung on,
// and is reported as a failed result carrying ctx.Err(). Once ctx is done,
// any entry not yet started is recorded as failed with ctx.Err() too, without
// ever invoking its collector, so a run that hits its deadline still reports
// on every (tailnet, collector) pair rather than truncating the list.
func (a *App) RunOnce(ctx context.Context) []CollectorRunResult {
	var results []CollectorRunResult
	for _, rt := range a.runtimes {
		tailnet := a.runtimeConfiguredName(rt)
		for _, e := range rt.registry.Entries() {
			results = append(results, runOnceEntry(ctx, tailnet, e, rt.emitter))
		}
	}
	return results
}

// RunPrometheusOnce runs the bounded collection cycle and returns the same
// process-wide Gatherer served by /metrics. It never starts a listener or
// delivers OTLP itself; callers may gather the returned registry directly.
// A nil Gatherer means Prometheus pull delivery is unavailable on this App.
func (a *App) RunPrometheusOnce(ctx context.Context) ([]CollectorRunResult, prometheus.Gatherer) {
	return a.RunOnce(ctx), a.promGatherer
}

// runOnceEntry runs a single registered collector exactly once and classifies
// its outcome. See RunOnce's doc comment for the deadline-abandonment
// contract.
func runOnceEntry(ctx context.Context, tailnet string, e collector.Entry, emitter telemetry.Emitter) CollectorRunResult {
	name := e.Collector.Name()
	if ctx.Err() != nil {
		return classifyResult(tailnet, name, 0, ctx.Err())
	}

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		switch c := e.Collector.(type) {
		case collector.WindowCollector:
			from, to := onceWindow(c, e)
			_, err := c.CollectWindow(ctx, from, to, emitter)
			done <- err
		case collector.SnapshotCollector:
			done <- c.Collect(ctx, emitter)
		default:
			// Defensive-only: Registry.Register/RegisterWindow require a
			// SnapshotCollector/WindowCollector at compile time (see
			// internal/collector/collector.go), so every registered entry
			// already matches one of the two cases above.
			done <- fmt.Errorf("collector %q implements neither SnapshotCollector nor WindowCollector", name)
		}
	}()

	select {
	case err := <-done:
		return classifyResult(tailnet, name, time.Since(started), err)
	case <-ctx.Done():
		// The goroutine above may still be running; it is intentionally
		// abandoned rather than waited on, per RunOnce's doc comment.
		return classifyResult(tailnet, name, time.Since(started), ctx.Err())
	}
}

// classifyResult builds a CollectorRunResult, classifying err into the
// AuthFailure bucket where it unambiguously represents a rejected credential.
func classifyResult(tailnet, collectorName string, d time.Duration, err error) CollectorRunResult {
	return CollectorRunResult{
		Tailnet:     tailnet,
		Collector:   collectorName,
		OK:          err == nil,
		Duration:    d,
		Err:         err,
		AuthFailure: isAuthFailure(err),
	}
}

// isAuthFailure reports whether err represents an unambiguous credential
// rejection: an HTTP 401 from the Tailscale API (tsapi.StatusError, the only
// sanctioned way to read a status code off an API error — see its doc
// comment), or an OAuth token request rejected with 401 (which tsapi's own
// transport surfaces as an *oauth2.RetrieveError rather than a StatusError,
// since there is no API response to wrap — see tsapi/transport.go's
// logFinal). 403 is deliberately excluded: this repo's collectors already
// treat some 403s as "feature not enabled on this plan", a collector-level
// outcome, not a credential problem.
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	if code, ok := tsapi.StatusCode(err); ok && code == http.StatusUnauthorized {
		return true
	}
	// Headscale reaches the same verdict through its own status type. Both
	// providers must classify, or the exit code silently means something
	// different depending on which control plane an operator runs.
	if code, ok := hsapi.StatusCode(err); ok && code == http.StatusUnauthorized {
		return true
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusUnauthorized {
		return true
	}
	return false
}

// onceWindow computes a one-shot [from, to) window for a WindowCollector
// (flowlogs/auditlogs) with no checkpoint to resume from: to is now minus the
// collector's trailing safety lag (mirroring the scheduler's own
// to = now - Lag()), and from is to minus the entry's configured
// InitialLookback (its cold-start lookback), capped by MaxWindow when set so
// a large InitialLookback cannot make a single preflight/once cycle request
// an unbounded amount of history.
func onceWindow(c collector.WindowCollector, e collector.Entry) (from, to time.Time) {
	to = time.Now().Add(-c.Lag())
	lookback := e.InitialLookback
	if lookback <= 0 {
		lookback = e.Interval
	}
	if e.MaxWindow > 0 && lookback > e.MaxWindow {
		lookback = e.MaxWindow
	}
	return to.Add(-lookback), to
}
