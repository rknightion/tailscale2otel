// Package flowlogs implements the "flowlogs" polling collector: the POLL path
// for Tailscale network flow logs. On each tick the scheduler hands it a
// [from, to] window; the collector fetches that window via the Tailscale API and
// delegates record-to-OTEL conversion to the shared flowlog.Processor (the same
// processor used by the streaming receiver).
//
// Because the API window is inclusive of both ends, a connection straddling a
// window edge can be returned in two adjacent ticks. The collector keeps a
// bounded de-duplication set keyed by connection identity and drops repeats
// before handing the response to the processor, so a boundary connection's
// metrics are emitted exactly once. The set is bounded (FIFO eviction), so its
// memory stays small even under a long stream of unique connections.
package flowlogs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

const (
	// defaultInterval is the poll cadence used when none is configured.
	defaultInterval = 60 * time.Second
	// defaultLag is the trailing safety margin used when none is configured.
	// Flow logs can arrive late, so the scheduler queries up to now-Lag.
	defaultLag = 120 * time.Second
	// dedupCapacity bounds how many recently-seen connections are remembered for
	// boundary de-duplication. A window holds at most a couple of ticks' worth of
	// connections, so a few thousand keys covers the overlap with margin.
	dedupCapacity = 16384
	// replayKeyPrefix is the private checkpoint namespace used for durable replay
	// identities. Keys contain only a SHA-256 digest, never a raw flow identity.
	replayKeyPrefix = "flowlogs/replay/seen/"
)

// metricFeatureEnabled is the gauge reporting whether the network-flow-logging
// feature is enabled (1) or disabled (0) for the tailnet.
const metricFeatureEnabled = "tailscale.feature.enabled"

// featureName is the value of the tailscale.feature attribute on the
// feature.enabled gauge this collector emits.
const featureName = "network_flow_logging"

// opListNetworkFlowLogs is the upstream operationId of the window fetch.
const opListNetworkFlowLogs = "listNetworkFlowLogs"

// flowlogsDisposition is the ONE place 403 is read as "feature not enabled"
// rather than "the credential lacks the scope" (#420).
//
// Network flow logging is a documented Premium/Enterprise feature that must
// also be switched on for the tailnet, and upstream answers 403 when it is not
// — nothing in the response distinguishes that from a scope denial. Because the
// reading is declared HERE rather than baked into apistate.Classify, no other
// operation inherits it: every collector that does not opt in reads 403 as
// scope_denied, which is what keeps a real permission regression visible.
var flowlogsDisposition = apistate.Disposition{DisabledOn: []int{403}}

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	NetworkFlowLogs(ctx context.Context, start, end time.Time) (flowlog.NetworkResponse, error)
}

// FeatureCheck reports whether the network-flow-logging feature is currently
// enabled for the tailnet. A nil FeatureCheck means "always enabled". An error
// is treated as fail-open (proceed as enabled) by the collector.
type FeatureCheck func(ctx context.Context) (bool, error)

// Collector implements collector.WindowCollector for Tailscale network flow
// logs, fetching each window and delegating conversion to a shared
// flowlog.Processor.
type Collector struct {
	api          api
	proc         *flowlog.Processor
	interval     time.Duration
	lag          time.Duration
	seen         *dedup.Set
	featureCheck FeatureCheck
	// onIngest, when non-nil, is called once per successful poll window with
	// ("poll","flow", <records accepted>, 0). The app supplies it (gated on
	// self-observability); the collector stays agnostic to how it's emitted.
	onIngest func(source, signal string, records, bytes int)
	// acceptedObserver, when non-nil, observes each semantically valid,
	// collector-deduplicated source record after it is handed to the processor.
	acceptedObserver ingest.AcceptedObserver
	// tracker records per-operation availability for the admin status page
	// (#420). A nil *apistate.Tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time
	// replayStore holds digest-only identities across a process restart. It is
	// supplied already namespaced by the app for multi-tailnet isolation.
	replayStore    collector.CheckpointStore
	replayOverlap  time.Duration
	replayCapacity int
	replaySeen     map[string]time.Time // SHA-256 hex digest -> inclusive expiry
	replayLoadErr  error
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads.
func WithAPIState(t *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = t }
}

// WithAcceptedObserver observes each semantically valid, intra-source-deduped
// flow record after it is handed to the processor.
func WithAcceptedObserver(observer ingest.AcceptedObserver) Option {
	return func(c *Collector) { c.acceptedObserver = observer }
}

// WithReplay enables durable replay suppression for a bounded scheduler
// overlap. The supplied store must already be namespaced for this tailnet.
// Invalid settings deliberately disable the feature, preserving the legacy
// in-memory boundary-deduplication behavior.
func WithReplay(overlap time.Duration, capacity int, store collector.CheckpointStore) Option {
	return func(c *Collector) {
		if overlap <= 0 || capacity <= 0 || store == nil {
			return
		}
		c.replayOverlap = overlap
		c.replayCapacity = capacity
		c.replayStore = store
		c.replaySeen = make(map[string]time.Time, capacity)
	}
}

// withClock exists only to make replay retention tests deterministic.
func withClock(now func() time.Time) Option {
	return func(c *Collector) {
		if now != nil {
			c.now = now
		}
	}
}

// New returns a flowlogs Collector that fetches windows via a, converts them
// with proc, and uses interval/lag as its poll cadence and trailing safety
// margin (non-positive values fall back to 60s and 120s respectively).
//
// featureCheck, when non-nil, gates collection: if it reports the feature
// disabled the collector stays idle and emits tailscale.feature.enabled=0
// rather than fetching. A nil featureCheck preserves the always-enabled
// behavior. featureCheck errors fail open (the collector proceeds as enabled).
func New(a api, proc *flowlog.Processor, interval, lag time.Duration, featureCheck FeatureCheck, onIngest func(source, signal string, records, bytes int), opts ...Option) *Collector {
	c := &Collector{
		api:          a,
		proc:         proc,
		interval:     interval,
		lag:          lag,
		seen:         dedup.New(dedupCapacity),
		featureCheck: featureCheck,
		onIngest:     onIngest,
		now:          time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	c.loadReplayState()
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "flowlogs" }

// DefaultInterval returns the configured interval, or 60s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Lag returns the configured trailing safety margin, or 120s when unset.
func (c *Collector) Lag() time.Duration {
	if c.lag > 0 {
		return c.lag
	}
	return defaultLag
}

// ReplayOverlap opts this window collector into a warm replay only when
// durable replay state is fully configured. The scheduler keeps cold-start
// lookback unchanged and advances the forward high-water mark after a replay.
func (c *Collector) ReplayOverlap() time.Duration {
	if !c.replayEnabled() {
		return 0
	}
	return c.replayOverlap
}

// CollectWindow fetches flow logs for [from, to] and processes them.
//
// When a featureCheck is configured it runs first: a disabled feature emits
// tailscale.feature.enabled=0 and returns to with no error (idle, not a
// failure), while an enabled feature emits =1 and proceeds. A featureCheck
// error fails open (proceed without emitting the gauge).
//
// A fetch error carrying a genuine HTTP 403 (a *tsapi.StatusError with
// Code == 403, see isForbidden) is also treated as the feature being
// disabled: it emits =0 and returns to with no error so the scheduler
// advances instead of retrying. Any other fetch error — including one whose
// text merely contains "403" or "forbidden" — is ambiguous and returns the
// zero time so the scheduler does not advance the checkpoint and the window is
// retried.
//
// Connections already seen on a previous tick (boundary overlap) are filtered
// out before processing so their metrics are emitted only once.
func (c *Collector) CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (time.Time, error) {
	if c.replayLoadErr != nil {
		return time.Time{}, c.replayLoadErr
	}
	if c.featureCheck != nil {
		enabled, err := c.featureCheck(ctx)
		switch {
		case err != nil:
			// Fail open: proceed as enabled without emitting the gauge.
		case !enabled:
			c.emitFeature(e, false)
			return to, nil
		default:
			c.emitFeature(e, true)
		}
	}

	resp, err := c.api.NetworkFlowLogs(ctx, from, to)
	// Availability is a pure ADDITION here: the control flow below is unchanged,
	// keyed on a genuine typed 403 exactly as before. A 404 therefore still
	// returns an error and does not advance the checkpoint, even though its
	// availability state is `disabled` — the endpoint's absence is real, but a
	// window that was never read must still be retried.
	apistate.Observe(e, c.tracker, c.Name(), opListNetworkFlowLogs, flowlogsDisposition, err, c.now())
	if err != nil {
		if isForbidden(err) {
			// The feature requires Premium/Enterprise and being enabled; a 403
			// means it is off, not a transient failure. Report it and advance.
			c.emitFeature(e, false)
			return to, nil
		}
		return time.Time{}, err
	}

	// Expired durable identities must not suppress a late record. Deletions are
	// held until after processing so a failed API request does not turn startup
	// cleanup into a durable side effect.
	stale := c.pruneExpiredReplay(c.now())
	resp = c.validRecords(resp, e)
	deduped, additions := c.dedupe(resp, e)
	c.proc.ProcessAllCtx(ctx, deduped, e)
	if c.acceptedObserver != nil {
		acceptedAt := c.now()
		for i := range deduped.Logs {
			c.acceptedObserver(ingest.AcceptedEvent{
				Source:      semconv.IngestSourcePoll,
				Signal:      semconv.IngestSignalFlow,
				EventTime:   flowlog.EventTimestamp(deduped.Logs[i]),
				CaptureTime: flowlog.CaptureTimestamp(deduped.Logs[i]),
				AcceptedAt:  acceptedAt,
			})
		}
	}
	if c.onIngest != nil {
		c.onIngest(semconv.IngestSourcePoll, semconv.IngestSignalFlow, len(deduped.Logs), 0)
	}
	if err := c.persistReplay(additions, stale, to); err != nil {
		// The processor has already emitted accepted data. Returning an error is
		// intentional: the scheduler must not advance its high-water mark when
		// the durable replay guard was not persisted.
		return time.Time{}, err
	}
	return to, nil
}

func (c *Collector) validRecords(resp flowlog.NetworkResponse, e telemetry.Emitter) flowlog.NetworkResponse {
	out := resp
	out.Logs = make([]flowlog.FlowLog, 0, len(resp.Logs))
	for i := range resp.Logs {
		violations := flowlog.Validate(resp.Logs[i], flowlog.ValidationOptions{Now: c.now})
		if len(violations) != 0 {
			flowlog.ObserveDataQuality(e, semconv.IngestSourcePoll, violations)
			continue
		}
		out.Logs = append(out.Logs, resp.Logs[i])
	}
	return out
}

// dedupe returns a copy of resp with already-seen connections removed and any
// FlowLog left with zero connections dropped. A connection's identity is its
// node, window, bounded traffic class, protocol, and 5-tuple endpoints; the
// first sighting wins.
func (c *Collector) dedupe(resp flowlog.NetworkResponse, e telemetry.Emitter) (flowlog.NetworkResponse, map[string]struct{}) {
	out := flowlog.NetworkResponse{Logs: make([]flowlog.FlowLog, 0, len(resp.Logs))}
	additions := make(map[string]struct{})
	for i := range resp.Logs {
		fl := resp.Logs[i]
		filtered := fl
		filtered.VirtualTraffic = c.keepNew(fl, semconv.TrafficVirtual, fl.VirtualTraffic, e, additions)
		filtered.SubnetTraffic = c.keepNew(fl, semconv.TrafficSubnet, fl.SubnetTraffic, e, additions)
		filtered.ExitTraffic = c.keepNew(fl, semconv.TrafficExit, fl.ExitTraffic, e, additions)
		filtered.PhysicalTraffic = c.keepNew(fl, semconv.TrafficPhysical, fl.PhysicalTraffic, e, additions)
		if len(filtered.VirtualTraffic)+len(filtered.SubnetTraffic)+
			len(filtered.ExitTraffic)+len(filtered.PhysicalTraffic) == 0 {
			continue
		}
		out.Logs = append(out.Logs, filtered)
	}
	return out, additions
}

// keepNew returns the subset of counts whose connection key has not been seen
// before, marking each kept connection as seen.
func (c *Collector) keepNew(fl flowlog.FlowLog, trafficType string, counts []flowlog.ConnectionCounts, e telemetry.Emitter, additions map[string]struct{}) []flowlog.ConnectionCounts {
	if len(counts) == 0 {
		return nil
	}
	kept := make([]flowlog.ConnectionCounts, 0, len(counts))
	for i := range counts {
		digest := replayDigest(flowlog.ConnectionKey(fl, trafficType, counts[i]))
		if c.replayEnabled() && c.replayContains(digest) {
			// Across a restart only the connection identity is durable, not
			// counters. Suppress any replay of that identity intentionally and
			// do not seed the in-process value comparator from an event that was
			// not emitted in this process.
			continue
		}
		result := flowlog.CompareConnection(c.seen, fl, trafficType, counts[i])
		switch result {
		case flowlog.DuplicateNew:
			kept = append(kept, counts[i])
			if c.replayEnabled() {
				additions[digest] = struct{}{}
			}
		case flowlog.DuplicateConflict:
			flowlog.ObserveDedupConflict(e, flowlog.DedupScopePollBoundary, trafficType)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func (c *Collector) replayEnabled() bool {
	return c.replayStore != nil && c.replayOverlap > 0 && c.replayCapacity > 0 && c.replaySeen != nil
}

func replayDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func replayKey(digest string) string { return replayKeyPrefix + digest }

func (c *Collector) replayContains(digest string) bool {
	_, ok := c.replaySeen[digest]
	return ok
}

// loadReplayState restores only this collector's digest-only state, pruning
// malformed, expired, and over-capacity entries deterministically. New cannot
// return an error, so a cleanup failure is carried into CollectWindow where it
// prevents a scheduler checkpoint advance.
func (c *Collector) loadReplayState() {
	if !c.replayEnabled() {
		return
	}
	now := c.now()
	deletes := make([]string, 0)
	for _, key := range c.replayStore.Keys() {
		digest, ok := strings.CutPrefix(key, replayKeyPrefix)
		if !ok {
			continue
		}
		expiry, exists := c.replayStore.Get(key)
		if !exists || !validReplayDigest(digest) || expiry.Before(now) {
			deletes = append(deletes, key)
			continue
		}
		c.replaySeen[digest] = expiry
	}
	for _, digest := range c.trimReplayCapacity() {
		deletes = append(deletes, replayKey(digest))
	}
	if len(deletes) == 0 {
		return
	}
	if err := collector.UpdateCheckpointBatch(c.replayStore, nil, uniqueSorted(deletes)); err != nil {
		c.replayLoadErr = err
	}
}

func validReplayDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// pruneExpiredReplay drops identities strictly after their inclusive expiry.
// Equality remains valid so an overlap exactly at its configured horizon is
// still protected.
func (c *Collector) pruneExpiredReplay(now time.Time) []string {
	if !c.replayEnabled() {
		return nil
	}
	deletes := make([]string, 0)
	for digest, expiry := range c.replaySeen {
		if expiry.Before(now) {
			delete(c.replaySeen, digest)
			deletes = append(deletes, replayKey(digest))
		}
	}
	return deletes
}

// persistReplay records newly accepted identities only after the processor has
// emitted them. Built-in checkpoint stores apply the supplied updates and
// deletes with one durable rewrite through collector.UpdateCheckpointBatch.
func (c *Collector) persistReplay(additions map[string]struct{}, deletes []string, to time.Time) error {
	if !c.replayEnabled() {
		return nil
	}
	updates := make(map[string]time.Time, len(additions))
	expiry := to.Add(c.replayOverlap)
	for digest := range additions {
		if expiry.Before(c.now()) {
			continue
		}
		c.replaySeen[digest] = expiry
	}
	for _, digest := range c.trimReplayCapacity() {
		deletes = append(deletes, replayKey(digest))
	}
	for digest := range additions {
		if retained, ok := c.replaySeen[digest]; ok {
			updates[replayKey(digest)] = retained
		}
	}
	if len(updates) == 0 && len(deletes) == 0 {
		return nil
	}
	return collector.UpdateCheckpointBatch(c.replayStore, updates, uniqueSorted(deletes))
}

// trimReplayCapacity retains identities with the latest expiry, using the
// digest as a stable tie-breaker. That makes startup pruning independent of a
// checkpoint map's randomized iteration order.
func (c *Collector) trimReplayCapacity() []string {
	if !c.replayEnabled() || len(c.replaySeen) <= c.replayCapacity {
		return nil
	}
	type entry struct {
		digest string
		expiry time.Time
	}
	entries := make([]entry, 0, len(c.replaySeen))
	for digest, expiry := range c.replaySeen {
		entries = append(entries, entry{digest: digest, expiry: expiry})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].expiry.Equal(entries[j].expiry) {
			return entries[i].digest < entries[j].digest
		}
		return entries[i].expiry.After(entries[j].expiry)
	})
	dropped := make([]string, 0, len(entries)-c.replayCapacity)
	for _, entry := range entries[c.replayCapacity:] {
		delete(c.replaySeen, entry.digest)
		dropped = append(dropped, entry.digest)
	}
	return dropped
}

func uniqueSorted(keys []string) []string {
	if len(keys) < 2 {
		return keys
	}
	sort.Strings(keys)
	out := keys[:0]
	for _, key := range keys {
		if len(out) == 0 || key != out[len(out)-1] {
			out = append(out, key)
		}
	}
	return out
}

// emitFeature records the feature.enabled gauge for network-flow-logging.
func (c *Collector) emitFeature(e telemetry.Emitter, enabled bool) {
	var v float64
	if enabled {
		v = 1
	}
	e.Gauge(docFeatureEnabled.Name, docFeatureEnabled.Unit,
		docFeatureEnabled.Description,
		v, telemetry.Attrs{semconv.AttrFeature: featureName})
}

// isForbidden reports whether err is (or wraps) a *tsapi.StatusError with HTTP
// status 403, indicating the feature is disabled rather than a transient
// failure. This mirrors the logstream collector's precedent (see
// internal/collector/logstream/logstream.go) of classifying by the typed
// status code rather than by matching text in err.Error(): the flow-logs
// error text embeds the full request URL plus up to 16KB of response body, so
// a substring match on "403"/"forbidden" can misfire on unrelated content
// (e.g. a proxy port like 10.0.0.1:8403, or a 5xx error page whose body
// happens to mention "Forbidden") and would incorrectly advance the
// checkpoint, silently dropping the window. Only a genuine typed 403 is
// treated as "feature disabled"; every other error is ambiguous and must be
// retried.
//
// It reads the code through tsapi.StatusCode — the one shared accessor — rather
// than its own errors.As, so this predicate and apistate.Classify can never
// disagree about what a 403 is (#420).
func isForbidden(err error) bool {
	code, ok := tsapi.StatusCode(err)
	return ok && code == 403
}
