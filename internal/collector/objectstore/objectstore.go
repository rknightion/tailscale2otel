// Package objectstore implements provider-neutral, multi-signal object-store
// ingestion. Providers supply immutable objects; signal adapters decode and
// hand records to the existing shared processors.
//
// It is the third source alongside poll (the API) and stream (the HEC receiver),
// and it converges with them completely — objects hold the SAME record shape the
// API returns, so they decode into flowlog.FlowLog and go through the same
// shared flowlog.Processor. There is no second record type and no second
// emission path.
//
// It is the cheapest ingestion for a large tailnet and the only practical way to
// backfill a long history: the objects are immutable, already batched, and cost
// no API quota.
//
// # At-least-once durability boundary
//
// With a file-backed checkpoint store, successful object identities, bounded
// listing progress, and failed-object gaps survive restart. Those state changes
// are persisted together before listing progress may pass an object. A memory
// checkpoint can replay objects after restart.
//
// Malformed or semantically invalid NDJSON lines are record-level failures: good
// lines in that object are accepted and the object can be marked complete. An
// object whose rows ALL fail that way accepted nothing, so it becomes an
// object-level gap instead — total row failure is a framing mismatch rather than
// corrupt records, and completing it would lose the object silently. A genuinely
// empty object has no row failures at all and still completes. GET,
// decompressor, scanner, and unexpected signal-preparation failures are
// object-level gaps. Raw rows are staged until clean EOF and then converted to
// prepared actions without side effects. Only after every row has prepared does
// the engine commit those actions in order. This makes object processing atomic
// across preparation failures, but it is not a crash, checkpoint, or OTLP
// transaction.
// OTLP/backend acknowledgement is outside this boundary.
package objectstore

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	storeapi "github.com/rknightion/tailscale2otel/v4/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

const (
	// defaultInterval is how often the bucket is listed. Tailscale writes an
	// object every few minutes, so a minute's cadence keeps latency low without
	// making the LIST call the dominant cost.
	defaultInterval = 60 * time.Second
	// defaultLookback is how far back past the cursor each listing reaches.
	// Objects can appear out of order relative to their embedded timestamp; this
	// is what finds one that landed late, and the seen set is what makes the
	// overlap free.
	defaultLookback = time.Hour
	// defaultInitialLookback bounds a cold start. Without it a first run against
	// a bucket holding months of exports would try to ingest all of it.
	defaultInitialLookback = 6 * time.Hour
	// defaultMaxObjects bounds one cycle's work. Exceeding it is not an error —
	// the remainder is counted, logged and picked up next cycle.
	defaultMaxObjects = 200
	// Expansion defaults bound both retained staging memory and per-cycle decode
	// work. They remain configurable for exports that legitimately exceed the
	// measured common-case object shape.
	defaultMaxObjectDecompressedBytes = 32 << 20
	defaultMaxObjectWireBytes         = 64 << 20
	defaultMaxObjectRecords           = 100_000
	defaultMaxCycleDecompressedBytes  = 256 << 20
	defaultMaxCycleWireBytes          = 512 << 20
	defaultMaxCycleRecords            = 500_000
	// maxDayPrefixes bounds how many day partitions one cycle enumerates, so a
	// corrupt or absurd cursor cannot turn a cycle into thousands of LIST calls.
	//
	// It is also the PERMANENT backfill ceiling of the partitioned layout, not just
	// a per-cycle bound: dayPrefixes walks backwards from the newest day so a
	// capped span keeps the recent days, and the cursor only ever moves forward, so
	// the days beyond the cap are never enumerated on a later cycle either. See
	// MaxDayPrefixes.
	maxDayPrefixes = 14
	// maxLineBytes bounds one NDJSON line. Flow-log records carrying a busy
	// node's whole window are large; bufio.Scanner's 64 KiB default fails on
	// them with a bare "token too long", which reads as corruption rather than
	// as a limit.
	maxLineBytes = 16 << 20
	// maxSeenKeys bounds the durable seen set regardless of what pruning by time
	// would keep, so a pathological bucket cannot grow the checkpoint file
	// without limit.
	maxSeenKeys  = 5000
	gapRetryBase = time.Minute
	gapRetryMax  = time.Hour
)

// requestDurationBucketsSeconds are the explicit bucket boundaries for
// tailscale2otel.objectstore.request.duration. They reach further than the
// receivers' HTTP buckets because an object-store GET is a network fetch of a
// multi-megabyte object rather than a local handler, and a clipped top bucket
// would hide a stalling provider behind a saturated +Inf.
var requestDurationBucketsSeconds = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// checkpoint key prefixes within the shared store. The cursor is one entry; the
// seen set is one entry per recently ingested object.
const (
	cursorKey  = "cursor"
	seenPrefix = "seen/"
)

// Layout is how objects are arranged under Prefix. It is an explicit operator
// choice, never autodetected: a flat and a partitioned bucket are
// indistinguishable without listing, and guessing wrong changes what the durable
// scan positions mean.
type Layout string

const (
	// LayoutPartitioned is Tailscale's own export shape and the default: objects
	// live under <Prefix>/YYYY/MM/DD/, and only the day partitions spanning the
	// listing window are enumerated. That bound is what keeps a first run against
	// a bucket holding months of history from walking all of it.
	LayoutPartitioned Layout = "partitioned"
	// LayoutFlat enumerates <Prefix> itself, with no delimiter, so objects whose
	// self-contained basename carries the whole timestamp are discovered even
	// though no YYYY/MM/DD directory sits above them. It exists for copied or
	// mirrored exports — Tailscale's own exports are always partitioned.
	//
	// It is a SUPERSET of LayoutPartitioned's reach: an undelimited listing of the
	// base prefix also returns everything beneath the day partitions, and parseKey
	// handles those keys too. What it costs is the day-partition bound: once caught
	// up, each cycle re-walks the prefix (still bounded per cycle by the page
	// request and resumable through the durable scan position, so no single cycle
	// scans an unbounded bucket).
	LayoutFlat Layout = "flat"
	// LayoutRecorder is what Tailscale's tsrecorder writes, verified against a live
	// bucket on 2026-07-29:
	//
	//	<stableID>/events/<RFC3339Nano>.event   -- one Kubernetes API request
	//	<stableID>/<RFC3339Nano>.cast           -- one terminal session
	//
	// It is NOT date-partitioned, so it enumerates like LayoutFlat: one undelimited
	// listing of the configured prefix, which is normally the bucket root because
	// <stableID> varies per recorder replica and is not known ahead of time.
	//
	// Its basenames are RFC3339Nano, and Go's RFC3339Nano TRIMS TRAILING ZEROS, so
	// key length varies (a live capture held 7-, 8- and 9-digit fractional seconds).
	// Lexicographic key order is therefore NOT chronological here, unlike every
	// other layout, whose fixed-width basenames make the two agree. That is why the
	// scan position for this layout is truncated to a second boundary before being
	// used as a listing lower bound -- see truncatedStartAfter.
	LayoutRecorder Layout = "recorder"
)

// Options configures a Collector. Zero values select the defaults above.
type Options struct {
	// Prefix is the export's root within the bucket, above the YYYY/MM/DD
	// partitions.
	Prefix string
	// Layout is how objects are arranged under Prefix. Empty means
	// LayoutPartitioned.
	Layout          Layout
	Interval        time.Duration
	Lookback        time.Duration
	InitialLookback time.Duration
	MaxObjects      int
	// MaxObjectWireBytes, MaxObjectDecompressedBytes, and MaxObjectRecords bound
	// one object's fetched and staged input. Blank rows consume both byte
	// budgets but not the record budget.
	MaxObjectWireBytes         int64
	MaxObjectDecompressedBytes int64
	MaxObjectRecords           int
	// MaxCycleWireBytes, MaxCycleDecompressedBytes, and MaxCycleRecords bound
	// aggregate fetch and decode work across attempts in one collection cycle.
	MaxCycleWireBytes         int64
	MaxCycleDecompressedBytes int64
	MaxCycleRecords           int
	Logger                    *slog.Logger
	// Now is injectable so cursor arithmetic is testable without sleeping.
	Now func() time.Time
	// OnIngest, when non-nil, is called once per cycle with
	// ("objectstore", "flow", <records accepted>, <bytes fetched>). The app
	// supplies it, gated on self-observability.
	OnIngest func(source, signal string, records, bytes int)
	// OnAccepted, when non-nil, observes each semantically valid flow record
	// immediately after it is handed to the shared processor.
	OnAccepted ingest.AcceptedObserver
	// Scope isolates the durable state for one tailnet/provider/signal/feed.
	Scope CheckpointScope
	// LegacyCheckpointNamespace is the pre-v1 namespace used by this runtime:
	// empty for single-tailnet mode, or the tailnet name for multi-tailnet mode.
	// New migrates that state atomically before the collector starts.
	LegacyCheckpointNamespace string
}

// Collector ingests exported flow-log objects and feeds them to the shared
// processor.
type Collector struct {
	api    storeapi.Backend
	signal SignalProcessor
	cp     collector.CheckpointStore
	opts   Options
	logger *slog.Logger
	now    func() time.Time
}

var _ collector.SnapshotCollector = (*Collector)(nil)

// New returns a Collector reading from api, converting records through signal,
// and keeping its state in a fully isolated checkpoint namespace.
func New(
	api storeapi.Backend,
	signal SignalProcessor,
	cp collector.CheckpointStore,
	opts Options,
) (*Collector, error) {
	if api == nil {
		return nil, fmt.Errorf("objectstore: backend is required")
	}
	if signal == nil {
		return nil, fmt.Errorf("objectstore: signal processor is required")
	}
	namespace, err := opts.Scope.Namespace()
	if err != nil {
		return nil, err
	}
	if opts.Scope.Signal != signal.Signal() {
		return nil, fmt.Errorf(
			"objectstore: checkpoint scope signal %q does not match processor signal %q",
			opts.Scope.Signal,
			signal.Signal(),
		)
	}
	// The pre-v1 layout exists for FLOW ONLY — its keys are literally
	// objectstore.flowlogs.* (see migration.go). Running the migration for any
	// other signal would have that signal adopt the flow cursor, seen set and
	// gaps as its own AND delete them out from under flow, so it is gated on the
	// signal here rather than trusted to every caller.
	if signal.Signal() == semconv.IngestSignalFlow {
		if err := migrateLegacyState(cp, opts.LegacyCheckpointNamespace, namespace); err != nil {
			return nil, err
		}
	}
	// An unrecognized layout is refused rather than defaulted: silently reading it
	// as partitioned would leave every flat object undiscovered while the config
	// says otherwise, and reading it as flat would change what the durable scan
	// positions mean. config.Validate rejects it first, so reaching this is a
	// wiring bug.
	switch opts.Layout {
	case "":
		opts.Layout = LayoutPartitioned
	case LayoutPartitioned, LayoutFlat, LayoutRecorder:
	default:
		return nil, fmt.Errorf("objectstore: layout %q is not supported: use %q, %q or %q",
			opts.Layout, LayoutPartitioned, LayoutFlat, LayoutRecorder)
	}
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.Lookback <= 0 {
		opts.Lookback = defaultLookback
	}
	if opts.InitialLookback <= 0 {
		opts.InitialLookback = defaultInitialLookback
	}
	if opts.MaxObjects <= 0 {
		opts.MaxObjects = defaultMaxObjects
	}
	if opts.MaxObjectDecompressedBytes <= 0 {
		opts.MaxObjectDecompressedBytes = defaultMaxObjectDecompressedBytes
	}
	if opts.MaxObjectWireBytes <= 0 {
		opts.MaxObjectWireBytes = defaultMaxObjectWireBytes
	}
	if opts.MaxObjectRecords <= 0 {
		opts.MaxObjectRecords = defaultMaxObjectRecords
	}
	if opts.MaxCycleDecompressedBytes <= 0 {
		opts.MaxCycleDecompressedBytes = defaultMaxCycleDecompressedBytes
	}
	if opts.MaxCycleWireBytes <= 0 {
		opts.MaxCycleWireBytes = defaultMaxCycleWireBytes
	}
	if opts.MaxCycleRecords <= 0 {
		opts.MaxCycleRecords = defaultMaxCycleRecords
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Collector{
		api:    api,
		signal: signal,
		cp:     collector.Namespaced(cp, namespace),
		opts:   opts,
		logger: logger,
		now:    now,
	}, nil
}

// Name implements collector.SnapshotCollector.
// Two signals can be registered on one runtime, and Name() keys BOTH the
// tailscale.collector attribute on the framework's scrape.* metrics and the admin
// status page's per-collector row. Returning one name for both would merge their
// durations, failures and last-error into a single indistinguishable series.
//
// The flow name is deliberately left as the bare "objectstore" rather than made
// symmetric: it is the value every existing deployment's scrape series and every
// dashboard query already carry, and renaming it to gain symmetry would be a
// silent breaking change for no functional gain (#288).
func (c *Collector) Name() string {
	if c.signal.Signal() == semconv.IngestSignalFlow {
		return "objectstore"
	}
	return "objectstore-" + c.signal.Signal()
}

// DefaultInterval implements collector.SnapshotCollector.
func (c *Collector) DefaultInterval() time.Duration { return c.opts.Interval }

// Collect runs one ingestion cycle: list from the cursor (with overlap), ingest
// what has not been seen, advance the cursor, prune the seen set.
//
// A failure to LIST is returned as an error and the cursor does not move, so the
// next cycle retries the same ground. A failure on ONE object is counted and
// logged but does not abort the cycle: a single corrupt object must not wedge
// ingestion behind it forever.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	now := c.now().UTC()
	cursor, ok := c.cp.Get(cursorKey)
	if !ok || cursor.IsZero() {
		cursor = now.Add(-c.opts.InitialLookback)
	}
	// Overlap backwards so an object that landed after the cursor passed its
	// timestamp is still found. The seen set is what makes this free.
	from := cursor.Add(-c.opts.Lookback)

	state, err := loadScanState(c.cp, c.opts.Prefix, c.opts.Layout)
	if err != nil {
		return err
	}
	gaps, err := loadGaps(c.cp)
	if err != nil {
		return err
	}
	gapsByIdentity := make(map[string]gapState, len(gaps))
	for _, gap := range gaps {
		gapsByIdentity[gap.Identity] = gap
	}

	batch := newCheckpointBatch()
	for _, key := range state.StaleKeys {
		batch.delete(key)
	}
	seen, err := c.seenKeys()
	if err != nil {
		return err
	}

	var (
		newest    = cursor
		objects   int
		records   int
		fetched   int64
		wireUsed  int64
		decoded   int64
		rows      int
		durable   = map[string]bool{}
		remaining = c.opts.MaxObjects
	)

	attempt := func(obj storeapi.Object, comp compression, at time.Time) bool {
		result, err := c.ingest(ctx, obj.Identity, comp, e, ingestLimits{
			wireBytes:         c.opts.MaxCycleWireBytes - wireUsed,
			decompressedBytes: c.opts.MaxCycleDecompressedBytes - decoded,
			records:           c.opts.MaxCycleRecords - rows,
		})
		fetched += result.wireBytes
		wireUsed += result.wireBytes
		decoded += result.decompressedBytes
		rows += result.rows
		if result.decompressedBytes > 0 {
			e.Counter(docDecompressedBytes.Name, docDecompressedBytes.Unit,
				docDecompressedBytes.Description, float64(result.decompressedBytes), telemetry.Attrs{})
		}
		if err != nil {
			var cycleErr *cycleLimitError
			if errors.As(err, &cycleErr) {
				e.Counter(docExpansionLimitFailures.Name, docExpansionLimitFailures.Unit,
					docExpansionLimitFailures.Description, 1,
					telemetry.Attrs{attrLimit: cycleErr.kind})
				logArgs := []any{
					"object_id", objectDigest(obj.Identity),
					"limit", cycleErr.kind,
					"configured_limit", cycleErr.limit,
				}
				logArgs = appendMeasured(logArgs, cycleErr.expansionLimitError)
				c.logger.Warn(
					"objectstore: per-cycle expansion budget reached; object deferred",
					logArgs...,
				)
				return false
			}
			failure := classifyObjectFailure(err)
			gap, exists := gapsByIdentity[obj.Identity]
			if exists {
				gap.Attempts++
			} else {
				gap = gapState{
					Identity:    obj.Identity,
					Key:         obj.Key,
					FirstFailed: now,
					Attempts:    1,
				}
			}
			gap.Quarantined = failure.quarantine
			if gap.Quarantined {
				gap.NextAttempt = time.Time{}
			} else {
				gap.NextAttempt = now.Add(gapRetryDelay(gap.Attempts))
			}
			gapsByIdentity[obj.Identity] = gap
			batch.persistGap(c.cp, gap)
			if gap.Quarantined {
				// Quarantine is terminal until an operator intervenes. Keep a
				// normal seen identity beside the gap so deleting only the gap
				// row is a durable manual acknowledgement rather than an
				// immediate re-quarantine on the next prefix wrap.
				seen[obj.Identity] = struct{}{}
				batch.updates[seenRow(obj.Identity)] = at
			}
			// The scan position may pass this object only because its retry
			// identity is being persisted in the same checkpoint batch.
			durable[obj.Identity] = true
			status := "pending"
			if gap.Quarantined {
				status = "quarantined"
			}
			logArgs := []any{
				"object_id", objectDigest(obj.Identity),
				"stage", failure.stage,
				"gap_status", status,
				"attempts", gap.Attempts,
			}
			if failure.limitKind != "" {
				e.Counter(docExpansionLimitFailures.Name, docExpansionLimitFailures.Unit,
					docExpansionLimitFailures.Description, 1,
					telemetry.Attrs{attrLimit: failure.limitKind})
				logArgs = append(logArgs,
					"limit", failure.limitKind,
					"configured_limit", failure.configured,
				)
				logArgs = appendMeasured(logArgs, failure.expansionLimit)
			}
			c.logger.Error("objectstore: ingest failed", logArgs...)
			e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, 1,
				telemetry.Attrs{attrReason: skipReasonForStage(failure.stage)})
			return true
		}
		if _, exists := gapsByIdentity[obj.Identity]; exists {
			delete(gapsByIdentity, obj.Identity)
			batch.resolveGap(c.cp, obj.Identity)
		}
		durable[obj.Identity] = true
		seen[obj.Identity] = struct{}{}
		objects++
		records += result.acceptedRecords
		if at.After(newest) {
			newest = at
		}
		batch.updates[seenRow(obj.Identity)] = at
		return true
	}

	gapDeferred := 0
	// retried counts attempts, not objects: one gap row attempted on three cycles
	// is three retries, which is what distinguishes an object recovering from one
	// failing over and over under the bounded backoff.
	retried := 0
	cycleExhausted := false
	for _, gap := range gaps {
		if gap.Quarantined || now.Before(gap.NextAttempt) {
			continue
		}
		if remaining == 0 || cycleExhausted || !c.canAttempt(wireUsed, decoded, rows) {
			gapDeferred++
			continue
		}
		at, comp, parsed := parseKey(gap.Key, c.opts.Layout)
		if !parsed {
			gap.Attempts++
			gap.Quarantined = true
			gap.NextAttempt = time.Time{}
			gapsByIdentity[gap.Identity] = gap
			batch.persistGap(c.cp, gap)
			c.logger.Error("objectstore: gap key no longer matches a supported export layout",
				"object_id", objectDigest(gap.Identity),
				"gap_status", "quarantined",
				"attempts", gap.Attempts)
			continue
		}
		remaining--
		// Counted here rather than inside attempt: this is the ONLY path that
		// re-attempts an object. A listed object carrying a pending gap row is
		// never made a candidate, so an object can be retried only from its gap.
		retried++
		if !attempt(storeapi.Object{Identity: gap.Identity, Key: gap.Key}, comp, at) {
			cycleExhausted = true
		}
	}

	listing, err := c.enumerate(ctx, from, now, seen, gapsByIdentity, state.Positions, e)
	if err != nil {
		return err
	}
	candidates := listing.candidates
	candidateBudget := min(len(candidates), remaining)
	durableCandidates := 0
	for _, cand := range candidates[:candidateBudget] {
		if cycleExhausted || !c.canAttempt(wireUsed, decoded, rows) {
			break
		}
		if !attempt(cand.obj, cand.comp, cand.at) {
			break
		}
		durableCandidates++
	}
	remaining -= durableCandidates

	if skipped := gapDeferred + len(candidates) - durableCandidates; skipped > 0 {
		// Never a silent truncation: an operator whose bucket is outrunning the
		// per-cycle cap needs to know before the backlog becomes days.
		c.logger.Warn("objectstore: per-cycle object budget reached; work remains for a later cycle",
			"attempted", c.opts.MaxObjects-remaining,
			"deferred", skipped,
			"budget", c.opts.MaxObjects)
		e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(skipped),
			telemetry.Attrs{attrReason: reasonBudget})
	}

	e.Counter(docObjects.Name, docObjects.Unit, docObjects.Description, float64(objects), telemetry.Attrs{})
	e.Counter(docRecords.Name, docRecords.Unit, docRecords.Description, float64(records), telemetry.Attrs{})
	e.Counter(docBytes.Name, docBytes.Unit, docBytes.Description, float64(fetched), telemetry.Attrs{})
	e.Counter(docRetries.Name, docRetries.Unit, docRetries.Description, float64(retried), telemetry.Attrs{})
	e.Gauge(docBacklog.Name, docBacklog.Unit, docBacklog.Description,
		float64(len(candidates)-durableCandidates), telemetry.Attrs{})
	// newest is the cursor this cycle leaves behind: it starts at the loaded
	// cursor (the initial lookback on a cold start, so there is always a value)
	// and advances only to an object actually ingested. Clamped because a key
	// inside the clock-skew allowance may legitimately sit ahead of now.
	e.Gauge(docCursorAge.Name, docCursorAge.Unit, docCursorAge.Description,
		max(0, now.Sub(newest).Seconds()), telemetry.Attrs{})
	discoveredAge := float64(ageNothingDiscovered)
	if !listing.newestKeyAt.IsZero() {
		discoveredAge = max(0, now.Sub(listing.newestKeyAt).Seconds())
	}
	e.Gauge(docDiscoveredNewestAge.Name, docDiscoveredNewestAge.Unit, docDiscoveredNewestAge.Description,
		discoveredAge, telemetry.Attrs{})
	// Candidates are chronological and consumed from the oldest, so the unhandled
	// tail begins at durableCandidates — exactly the population the backlog gauge
	// counts. A candidate that FAILED is durable (it became a gap) and is
	// therefore not pending: its age belongs to gap.oldest.age instead. Zero is a
	// real reading here, matching a zero backlog, because "nothing waiting" and
	// "waiting on something fresh" are both healthy.
	pendingOldestAge := 0.0
	if durableCandidates < len(candidates) {
		pendingOldestAge = max(0, now.Sub(candidates[durableCandidates].at).Seconds())
	}
	e.Gauge(docPendingOldestAge.Name, docPendingOldestAge.Unit, docPendingOldestAge.Description,
		pendingOldestAge, telemetry.Attrs{})
	if c.opts.OnIngest != nil && records > 0 {
		// bytes is the decompressed/decoded payload size, matching what the
		// stream and webhook receivers mean by tailscale2otel.ingest.size — NOT
		// the compressed wire bytes (`fetched`), which stay reported separately,
		// unchanged, by tailscale2otel.objectstore.bytes. See #450.
		c.opts.OnIngest(semconv.IngestSourceObjectStore, c.signal.Signal(), records, int(decoded))
	}

	if newest.After(cursor) {
		batch.updates[cursorKey] = newest
	}
	c.stagePruneSeen(batch, newest)

	scanTruncated := false
	for _, progress := range listing.prefixes {
		lastSafe := progress.startAfter
		allSafe := true
		for _, object := range progress.objects {
			if !object.safe && !durable[object.identity] {
				allSafe = false
				break
			}
			lastSafe = object.key
		}
		if allSafe && !progress.truncated {
			batch.clearScanPosition(c.cp, progress.prefix)
			continue
		}
		scanTruncated = true
		if lastSafe != progress.startAfter || !progress.active {
			batch.setScanPosition(c.cp, progress.prefix, lastSafe, now)
		}
	}
	e.Gauge(docScanTruncated.Name, docScanTruncated.Unit, docScanTruncated.Description,
		boolFloat(scanTruncated), telemetry.Attrs{})
	emitGapHealth(e, gapsByIdentity, now)

	if err := batch.apply(c.cp); err != nil {
		return fmt.Errorf("objectstore: persist collection state: %w", err)
	}
	return nil
}

func (c *Collector) canAttempt(wire, decoded int64, rows int) bool {
	return wire < c.opts.MaxCycleWireBytes &&
		decoded < c.opts.MaxCycleDecompressedBytes &&
		rows < c.opts.MaxCycleRecords
}

// candidate is one object that has passed the cursor and seen-set filters.
type candidate struct {
	obj  storeapi.Object
	at   time.Time
	comp compression
}

type listedObject struct {
	key      string
	identity string
	safe     bool
}

type prefixProgress struct {
	prefix     string
	startAfter string
	active     bool
	objects    []listedObject
	truncated  bool
}

type enumeration struct {
	candidates []candidate
	prefixes   []prefixProgress
	// newestKeyAt is the newest key timestamp across every object this cycle
	// listed whose key parsed and was not rejected as future-stamped, whatever
	// filter it then fell to. Zero when the cycle listed no such object, which is
	// a different state from "listed something stamped now" and is reported as
	// such.
	newestKeyAt time.Time
}

// scanPrefixes is the prefix set this cycle would list from scratch, before any
// durable scan position is folded in by listingPrefixes.
//
// LayoutFlat and LayoutRecorder both return exactly ONE prefix — the configured
// base — so the maxDayPrefixes span cap is irrelevant there and cannot misfire:
// one prefix can never exceed it, and the listing window plays no part in
// choosing it.
func (c *Collector) scanPrefixes(from, now time.Time) []string {
	if c.opts.Layout == LayoutFlat || c.opts.Layout == LayoutRecorder {
		return []string{flatPrefix(c.opts.Prefix)}
	}
	return dayPrefixes(c.opts.Prefix, from, now, maxDayPrefixes)
}

// flatPrefix normalizes the configured base prefix into the single listing prefix
// LayoutFlat walks. It trims a trailing slash and restores exactly one, the same
// way dayPrefixes joins the base onto a day partition, so the two layouts agree
// on what "the configured prefix" is. An empty base stays empty, which lists the
// whole bucket.
func flatPrefix(base string) string {
	base = scanBase(base)
	if base == "" {
		return ""
	}
	return base + "/"
}

// beforeLowerBound reports whether an object stamped at is skipped for sitting
// below the listing lower bound.
//
// A PARTITIONED prefix that already holds a scan position is committed ground:
// its span is one day, so an object behind the bound is at most that far behind
// it, and re-listing it is how a failed object at the start of a partition is
// retried immediately (see TestCollect_FailureAtPrefixStartKeepsScanGroundActive).
//
// A FLAT (or RECORDER) prefix spans the whole export, so the same exemption would
// admit every object in the bucket for as long as any scan position exists. That
// is not merely expensive: the seen set is pruned 2*Lookback behind the cursor, so
// an object too old to be inside the window is also too old to be remembered, and
// each wrap of the walk would re-fetch and re-emit the same history — permanent
// duplicate records. The bound is absolute in flat/recorder mode; a failed object
// there is still retried durably through its gap row, which needs no listing.
func (c *Collector) beforeLowerBound(at, from time.Time, active bool) bool {
	if !at.Before(from) {
		return false
	}
	return !active || c.opts.Layout == LayoutFlat || c.opts.Layout == LayoutRecorder
}

// enumerate lists the configured prefixes for this layout — the day partitions
// spanning [from, now], or the one flat base prefix — and returns the objects
// worth fetching, oldest first.
func (c *Collector) enumerate(
	ctx context.Context,
	from, now time.Time,
	seen map[string]struct{},
	gaps map[string]gapState,
	positions map[string]string,
	e telemetry.Emitter,
) (enumeration, error) {
	prefixes, err := listingPrefixes(positions, c.scanPrefixes(from, now))
	if err != nil {
		return enumeration{}, err
	}
	var result enumeration
	var unparsed, already, stale, future int
	for _, prefix := range prefixes {
		// Listing is bounded per cycle even before the ingest budget: a day's
		// partition on a large tailnet is hundreds of objects, and there is no
		// value in walking further than one cycle could ever consume.
		startAfter := truncatedStartAfter(positions[prefix], c.opts.Layout)
		listStarted := time.Now()
		page, err := c.api.List(ctx, prefix, startAfter, c.opts.MaxObjects*4)
		observeRequest(ctx, e, operationList, listStarted, err)
		if err != nil {
			return enumeration{}, fmt.Errorf("objectstore: list %s: %w", prefix, err)
		}
		progress := prefixProgress{
			prefix: prefix,
			// The untruncated value, not the listing lower bound above: this is what
			// persisted progress bookkeeping means, and truncating it here would
			// change what a recorder-layout scan position means on disk.
			startAfter: positions[prefix],
			active:     hasPosition(positions, prefix),
			truncated:  page.Truncated,
		}
		for _, o := range page.Objects {
			at, comp, ok := parseKey(o.Key, c.opts.Layout)
			item := listedObject{key: o.Key, identity: o.Identity}
			switch {
			case o.Identity == "":
				return enumeration{}, fmt.Errorf("objectstore: provider returned an object with an empty identity")
			case !ok:
				unparsed++
				item.safe = true
			case isFutureKey(at, now):
				// Checked before every other filter so a future timestamp can
				// never become a candidate: a candidate is what advances the
				// cursor, and the cursor is the next cycle's lower bound.
				future++
			case c.beforeLowerBound(at, from, progress.active):
				stale++
				item.safe = true
			default:
				if _, pending := gaps[o.Identity]; pending {
					item.safe = true
					break
				}
				if _, dup := seen[o.Identity]; dup {
					already++
					item.safe = true
					break
				}
				result.candidates = append(result.candidates, candidate{obj: o, at: at, comp: comp})
			}
			// Discovery is measured over what was LISTED, not over what will be
			// ingested, so an object skipped as already ingested or as stale still
			// counts: that is what keeps the signal alive on a caught-up feed. An
			// unparsed key has no timestamp to offer, and a future-stamped key is
			// not data, so neither may set this.
			if ok && !isFutureKey(at, now) && at.After(result.newestKeyAt) {
				result.newestKeyAt = at
			}
			progress.objects = append(progress.objects, item)
		}
		result.prefixes = append(result.prefixes, progress)
	}
	if future > 0 {
		// Bounded and name-free: the count is the signal, and an object key is
		// operator data that must never become a metric label.
		c.logger.Warn("objectstore: skipped objects timestamped beyond the clock-skew allowance; check the exporter's clock",
			"objects", future, "allowance", maxClockSkew)
	}
	countSkips(e, map[string]int{
		reasonUnparsedKey:  unparsed,
		reasonAlreadySeen:  already,
		reasonBeforeCursor: stale,
		reasonFutureKey:    future,
	})

	// Chronological order across day prefixes. Within one prefix the listing is
	// already ordered, but a stable sort over all of them is what makes the
	// cursor safe to advance to the newest ingested object.
	sortCandidates(result.candidates)
	return result, nil
}

type ingestLimits struct {
	wireBytes         int64
	decompressedBytes int64
	records           int
}

type ingestResult struct {
	acceptedRecords   int
	rows              int
	wireBytes         int64
	decompressedBytes int64
}

type preparedAction struct {
	record   PreparedRecord
	accepted bool
}

// ingest fetches, decompresses, and stages every nonblank row before preparing
// or committing any signal-specific action.
func (c *Collector) ingest(
	ctx context.Context,
	identity string,
	comp compression,
	e telemetry.Emitter,
	cycle ingestLimits,
) (result ingestResult, err error) {
	getStarted := time.Now()
	body, err := c.api.Get(ctx, identity)
	observeRequest(ctx, e, operationGet, getStarted, err)
	if err != nil {
		return result, &objectIngestError{stage: "fetch", err: err}
	}
	defer body.Close()

	wireLimit, wireLimitKind := lowerLimit(
		c.opts.MaxObjectWireBytes,
		cycle.wireBytes,
		"object_wire_bytes",
		"cycle_wire_bytes",
	)
	overWireLimit := wireLimit
	if overWireLimit < math.MaxInt64 {
		overWireLimit++
	}
	wireSource := &io.LimitedReader{R: body, N: overWireLimit}
	wire := &countingReader{r: wireSource}
	byteLimit, byteLimitKind := lowerLimit(
		c.opts.MaxObjectDecompressedBytes,
		cycle.decompressedBytes,
		"object_bytes",
		"cycle_bytes",
	)
	recordLimit, recordLimitKind := lowerLimit(
		int64(c.opts.MaxObjectRecords),
		int64(cycle.records),
		"object_records",
		"cycle_records",
	)
	defer func() {
		result.wireBytes = wire.n
	}()

	overByteLimit := byteLimit
	if overByteLimit < math.MaxInt64 {
		overByteLimit++
	}
	r, closeFn, err := decompress(wire, comp, byteLimit)
	if err != nil {
		if wire.n > wireLimit {
			return result, expansionError(wireLimitKind, wire.n, wireLimit)
		}
		if isZstdExpansionLimit(err) {
			return result, expansionLowerBoundError(
				byteLimitKind,
				overByteLimit,
				byteLimit,
			)
		}
		return result, &objectIngestError{stage: "decompress", quarantine: true, err: err}
	}
	defer closeFn()

	limited := &io.LimitedReader{R: r, N: overByteLimit}
	sc := bufio.NewScanner(limited)
	scanMax := int(min(int64(maxLineBytes), overByteLimit))
	sc.Buffer(make([]byte, 0, min(64<<10, scanMax)), scanMax)

	var staged [][]byte
	for sc.Scan() {
		result.decompressedBytes = overByteLimit - limited.N
		if result.decompressedBytes > byteLimit {
			return result, expansionError(
				byteLimitKind,
				result.decompressedBytes,
				byteLimit,
			)
		}
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		result.rows++
		if int64(result.rows) > recordLimit {
			return result, expansionError(
				recordLimitKind,
				int64(result.rows),
				recordLimit,
			)
		}
		staged = append(staged, bytes.Clone(line))
	}
	result.decompressedBytes = overByteLimit - limited.N
	if result.decompressedBytes > byteLimit {
		return result, expansionError(
			byteLimitKind,
			result.decompressedBytes,
			byteLimit,
		)
	}
	if err := sc.Err(); err != nil {
		if wire.n > wireLimit {
			return result, expansionError(wireLimitKind, wire.n, wireLimit)
		}
		if isZstdExpansionLimit(err) {
			return result, expansionLowerBoundError(
				byteLimitKind,
				max(result.decompressedBytes, overByteLimit),
				byteLimit,
			)
		}
		return result, &objectIngestError{stage: "read", err: err}
	}
	if wire.n > wireLimit {
		return result, expansionError(wireLimitKind, wire.n, wireLimit)
	}

	var (
		actions                        = make([]preparedAction, 0, len(staged))
		acceptedRows, bad, semanticBad int
	)
	for i, line := range staged {
		prepared, err := c.signal.PrepareRecord(ctx, line, c.now())
		// Release each raw row as soon as its bounded prepared representation
		// replaces it, so its payload can be reclaimed during preparation.
		staged[i] = nil
		if errors.Is(err, ErrRecordDecode) && errors.Is(err, ErrRecordInvalid) {
			return result, &objectIngestError{
				stage: "process",
				err:   errors.New("signal processor error matched both row-local sentinels"),
			}
		}
		switch {
		case errors.Is(err, ErrRecordDecode):
			if prepared != nil {
				return result, &objectIngestError{
					stage: "process",
					err:   errors.New("signal processor returned a prepared record with ErrRecordDecode"),
				}
			}
			bad++
			continue
		case errors.Is(err, ErrRecordInvalid):
			if prepared == nil {
				return result, &objectIngestError{
					stage: "process",
					err:   errors.New("signal processor returned nil with ErrRecordInvalid"),
				}
			}
			semanticBad++
			actions = append(actions, preparedAction{record: prepared})
			continue
		case err != nil:
			return result, &objectIngestError{stage: "process", err: err}
		case prepared == nil:
			return result, &objectIngestError{
				stage: "process",
				err:   errors.New("signal processor returned nil for an accepted record"),
			}
		}
		acceptedRows++
		actions = append(actions, preparedAction{record: prepared, accepted: true})
	}

	// Cancellation is an all-or-nothing boundary for row-derived effects. Once
	// commit starts it runs to completion without another context check, so a
	// retry cannot repeat an action prefix.
	if err := ctx.Err(); err != nil {
		return result, &objectIngestError{stage: "process", err: err}
	}
	// Zero accepted records with at least one row-level failure is the signature
	// of a FRAMING mismatch, not of corrupt records: the object is not
	// newline-delimited bare records, so every row failed the same way. One bad
	// row must stay row-local or a single malformed record wedges a feed forever
	// (#286, #448) — but an object that decoded nothing must never be recorded as
	// ingested, because that advances the cursor past permanently lost data while
	// every health signal reports success (#497). Decided before any commit, so
	// nothing is emitted for an object that fails here.
	//
	// A genuinely empty object has no failures and is NOT this case: Tailscale
	// writes zero-length objects, and those complete cleanly.
	if acceptedRows == 0 && bad+semanticBad > 0 {
		c.logger.Warn(
			"objectstore: no row in the object decoded; the export framing may not be newline-delimited records",
			"object_id", objectDigest(identity),
			"rows", result.rows,
			"decode_failures", bad,
			"validation_failures", semanticBad)
		return result, &objectIngestError{
			stage: stageUndecodableObject,
			err: fmt.Errorf(
				"no row decoded: %d row(s), %d decode failure(s), %d validation failure(s)",
				result.rows, bad, semanticBad,
			),
		}
	}
	for _, action := range actions {
		timestamps := action.record.Commit(e)
		if !action.accepted {
			continue
		}
		if c.opts.OnAccepted != nil {
			c.opts.OnAccepted(ingest.AcceptedEvent{
				Source:      semconv.IngestSourceObjectStore,
				Signal:      c.signal.Signal(),
				EventTime:   timestamps.EventTime,
				CaptureTime: timestamps.CaptureTime,
				AcceptedAt:  c.now(),
			})
		}
		result.acceptedRecords++
	}
	if bad > 0 {
		c.logger.Warn("objectstore: skipped malformed records",
			"object_id", objectDigest(identity),
			"records", bad)
		e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(bad),
			telemetry.Attrs{attrReason: reasonDecodeError})
	}
	if semanticBad > 0 {
		c.logger.Warn("objectstore: quarantined semantically invalid records",
			"object_id", objectDigest(identity),
			"records", semanticBad)
		e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(semanticBad),
			telemetry.Attrs{attrReason: reasonSemanticInvalid})
	}
	return result, nil
}

// decompress wraps r according to comp. The returned close function releases the
// decoder's own resources; it never closes r, which the caller owns.
func decompress(
	r io.Reader,
	comp compression,
	maxDecompressedBytes int64,
) (io.Reader, func(), error) {
	switch comp {
	case compZstd:
		if maxDecompressedBytes <= 0 {
			return nil, nil, fmt.Errorf("zstd: maximum decompressed bytes must be positive")
		}
		// The caller and guard above establish that this is positive.
		maxBytes := uint64(maxDecompressedBytes) //nolint:gosec // checked before conversion
		decoderMemory := max(uint64(zstd.MinWindowSize), maxBytes)
		zr, err := zstd.NewReader(
			r,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(decoderMemory),
			zstd.WithDecoderMaxWindow(decoderMemory),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("zstd: %w", err)
		}
		return zr, zr.Close, nil
	case compGzip:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip: %w", err)
		}
		return gz, func() { _ = gz.Close() }, nil
	default:
		return r, func() {}, nil
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

func lowerLimit(
	object, cycle int64,
	objectKind, cycleKind string,
) (int64, string) {
	if cycle < object {
		return cycle, cycleKind
	}
	return object, objectKind
}

func expansionError(kind string, measured, limit int64) error {
	return newExpansionError(kind, measured, limit, false)
}

func expansionLowerBoundError(kind string, measuredAtLeast, limit int64) error {
	return newExpansionError(kind, measuredAtLeast, limit, true)
}

func newExpansionError(
	kind string,
	measured, limit int64,
	lowerBound bool,
) error {
	err := &expansionLimitError{
		kind:       kind,
		measured:   measured,
		limit:      limit,
		lowerBound: lowerBound,
	}
	if strings.HasPrefix(kind, "cycle_") {
		return &cycleLimitError{expansionLimitError: err}
	}
	stage := "records_limit"
	switch kind {
	case "object_wire_bytes":
		stage = "wire_bytes_limit"
	case "object_bytes":
		stage = "decompressed_bytes_limit"
	}
	return &objectIngestError{
		stage:      stage,
		quarantine: true,
		err:        err,
	}
}

func isZstdExpansionLimit(err error) bool {
	return errors.Is(err, zstd.ErrDecoderSizeExceeded) ||
		errors.Is(err, zstd.ErrWindowSizeExceeded)
}

// seenKeys reads the durable set of recently ingested opaque identities.
func (c *Collector) seenKeys() (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, k := range c.cp.Keys() {
		if encoded, ok := strings.CutPrefix(k, seenPrefix); ok {
			identity, err := decodeIdentity(encoded)
			if err != nil {
				return nil, err
			}
			out[identity] = struct{}{}
		}
	}
	return out, nil
}

// pruneSeen drops entries older than the overlap window. Anything that old can
// no longer be re-listed, so remembering it costs storage and buys nothing.
func (c *Collector) stagePruneSeen(batch *checkpointBatch, cursor time.Time) {
	cutoff := cursor.Add(-2 * c.opts.Lookback)
	var kept []seenEntry
	for _, k := range c.cp.Keys() {
		if !strings.HasPrefix(k, seenPrefix) {
			continue
		}
		at, _ := c.cp.Get(k)
		if at.Before(cutoff) {
			batch.delete(k)
			continue
		}
		kept = append(kept, seenEntry{key: k, at: at})
	}
	for k, at := range batch.updates {
		if !strings.HasPrefix(k, seenPrefix) {
			continue
		}
		found := false
		for i := range kept {
			if kept[i].key == k {
				kept[i].at = at
				found = true
				break
			}
		}
		if !found {
			kept = append(kept, seenEntry{key: k, at: at})
		}
	}
	// A hard cap underneath the time-based pruning, so a bucket writing objects
	// faster than the window assumes cannot grow the checkpoint file without
	// limit. Oldest go first.
	if len(kept) > maxSeenKeys {
		sortEntriesByTime(kept)
		for _, ent := range kept[:len(kept)-maxSeenKeys] {
			batch.delete(ent.key)
		}
	}
}

// truncatedStartAfter lowers a persisted scan position to the start of its
// second before it is used as a listing lower bound.
//
// S3's StartAfter is a LEXICOGRAPHIC comparison, and the recorder layout's
// RFC3339Nano basenames have variable width because Go trims trailing zeros.
// So "...11.9115044Z" sorts AFTER "...11.91150441Z" while being 10ns earlier,
// and a position set from the latter would silently skip the former -- a
// permanent, invisible data loss, since a skipped key is never re-listed.
//
// Truncating to the second boundary costs at most one second of re-listing per
// cycle; the durable seen set already discards the re-read objects, so the only
// cost is a few extra keys walked. Every other layout has fixed-width basenames
// where lexicographic and chronological order agree, so this is a no-op there.
func truncatedStartAfter(pos string, layout Layout) string {
	if pos == "" || layout != LayoutRecorder {
		return pos
	}
	dir, base := "", pos
	if i := strings.LastIndexByte(pos, '/'); i >= 0 {
		dir, base = pos[:i+1], pos[i+1:]
	}
	dot := strings.IndexByte(base, '.')
	if dot < 0 {
		return pos
	}
	// "2026-07-29T11:51:11" + "" -- everything at or after this second is
	// re-listed and deduped against the seen set.
	return dir + base[:dot]
}

func listingPrefixes(positions map[string]string, current []string) ([]string, error) {
	if len(positions) > maxDayPrefixes {
		return nil, fmt.Errorf("objectstore: %d active scan prefixes exceed limit %d", len(positions), maxDayPrefixes)
	}
	seen := make(map[string]struct{}, maxDayPrefixes)
	out := make([]string, 0, maxDayPrefixes)
	for prefix := range positions {
		out = append(out, prefix)
		seen[prefix] = struct{}{}
	}
	sort.Strings(out)
	for _, prefix := range current {
		if len(out) == maxDayPrefixes {
			break
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		out = append(out, prefix)
	}
	sort.Strings(out)
	return out, nil
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

// stageUndecodableObject is the object-failure stage for an object that reached
// clean EOF but produced no accepted record while at least one row failed a
// row-local decode or validation check. It is NOT quarantining: like a fetch or
// read failure it retries under the existing bounded backoff, so an object lost
// to a framing bug is still recoverable once the framing is fixed.
const stageUndecodableObject = "undecodable_object"

type objectIngestError struct {
	stage      string
	quarantine bool
	err        error
}

func (e *objectIngestError) Error() string { return e.err.Error() }
func (e *objectIngestError) Unwrap() error { return e.err }

type expansionLimitError struct {
	kind       string
	measured   int64
	limit      int64
	lowerBound bool
}

func (e *expansionLimitError) Error() string {
	if e.lowerBound {
		return fmt.Sprintf(
			"%s measured value is at least %d and exceeds configured limit %d",
			e.kind,
			e.measured,
			e.limit,
		)
	}
	return fmt.Sprintf(
		"%s measured value %d exceeds configured limit %d",
		e.kind,
		e.measured,
		e.limit,
	)
}

type cycleLimitError struct {
	*expansionLimitError
}

type objectFailure struct {
	stage          string
	quarantine     bool
	limitKind      string
	configured     int64
	expansionLimit *expansionLimitError
}

// skipReasonForStage maps an object-failure stage onto its bounded skip reason.
// Every stage keeps the historical read_error reason except the one whose cause
// an operator cannot infer from it — an object that decoded nothing needs to be
// distinguishable from an object that could not be read at all.
func skipReasonForStage(stage string) string {
	if stage == stageUndecodableObject {
		return reasonUndecodableObject
	}
	return reasonReadError
}

func classifyObjectFailure(err error) objectFailure {
	var ingestErr *objectIngestError
	if errors.As(err, &ingestErr) {
		failure := objectFailure{stage: ingestErr.stage, quarantine: ingestErr.quarantine}
		var limitErr *expansionLimitError
		if errors.As(ingestErr, &limitErr) {
			failure.limitKind = limitErr.kind
			failure.configured = limitErr.limit
			failure.expansionLimit = limitErr
		}
		return failure
	}
	return objectFailure{stage: "unknown"}
}

func appendMeasured(args []any, limit *expansionLimitError) []any {
	if limit.lowerBound {
		return append(args, "measured_at_least", limit.measured)
	}
	return append(args, "measured", limit.measured)
}

func gapRetryDelay(attempts int) time.Duration {
	delay := gapRetryBase
	for attempt := 1; attempt < attempts && delay < gapRetryMax; attempt++ {
		if delay >= gapRetryMax/2 {
			return gapRetryMax
		}
		delay *= 2
	}
	if delay > gapRetryMax {
		return gapRetryMax
	}
	return delay
}

func emitGapHealth(e telemetry.Emitter, gaps map[string]gapState, now time.Time) {
	oldestAge := time.Duration(0)
	for _, gap := range gaps {
		age := now.Sub(gap.FirstFailed)
		if age > oldestAge {
			oldestAge = age
		}
	}
	e.Gauge(docGaps.Name, docGaps.Unit, docGaps.Description, float64(len(gaps)), telemetry.Attrs{})
	e.Gauge(docGapOldestAge.Name, docGapOldestAge.Unit, docGapOldestAge.Description,
		max(0, oldestAge.Seconds()), telemetry.Attrs{})
	e.Gauge(docGapHealthy.Name, docGapHealthy.Unit, docGapHealthy.Description,
		boolFloat(len(gaps) == 0), telemetry.Attrs{})
}

func hasPosition(positions map[string]string, prefix string) bool {
	_, ok := positions[prefix]
	return ok
}

// observeRequest records one provider call on the TRANSPORT axis: exactly one
// data point per Backend.List or Backend.Get call, whose outcome is that call's
// own return value and nothing else. A body that fails mid-read, a row that
// fails to decode, and an object that breaches a limit are all counted as a
// SUCCESSFUL request here — the call itself worked — and land on
// tailscale2otel.objectstore.skipped and the gap metrics instead. That is what
// keeps transport health separate from ingestion health and makes it impossible
// for one failure to be counted twice on either axis.
//
// The duration is measured with the monotonic clock rather than Options.Now: Now
// is the injectable LOGICAL clock the cursor arithmetic runs on, and a stepped or
// faked wall clock must not be able to produce a negative latency. It is recorded
// through HistogramCtx so the scheduler's per-cycle span links trace exemplars,
// the same reason api.duration does; the counter stays context-free because the
// SDK views give synchronous counters a no-op exemplar reservoir.
func observeRequest(
	ctx context.Context,
	e telemetry.Emitter,
	operation string,
	started time.Time,
	err error,
) {
	elapsed := time.Since(started)
	outcome := outcomeSuccess
	if err != nil {
		outcome = outcomeError
	}
	attrs := telemetry.Attrs{attrOperation: operation, attrOutcome: outcome}
	e.Counter(docRequests.Name, docRequests.Unit, docRequests.Description, 1, attrs)
	e.HistogramCtx(ctx, docRequestDuration.Name, docRequestDuration.Unit, docRequestDuration.Description,
		elapsed.Seconds(), requestDurationBucketsSeconds, attrs)
}

// countSkips emits one skipped counter per non-zero reason.
func countSkips(e telemetry.Emitter, byReason map[string]int) {
	for reason, n := range byReason {
		if n > 0 {
			e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(n),
				telemetry.Attrs{attrReason: reason})
		}
	}
}
