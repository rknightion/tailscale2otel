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
// lines in that object are accepted and the object can be marked complete. GET,
// decompressor, and scanner failures are object-level gaps. Raw rows are staged
// until clean EOF, so these failures emit no signal rows. An unexpected signal
// processing failure can still occur after earlier staged rows were emitted;
// generic prepare/commit is outside this package's current boundary.
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

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/ingest"
	storeapi "github.com/rknightion/tailscale2otel/v3/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
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

// checkpoint key prefixes within the shared store. The cursor is one entry; the
// seen set is one entry per recently ingested object.
const (
	cursorKey  = "cursor"
	seenPrefix = "seen/"
)

// Options configures a Collector. Zero values select the defaults above.
type Options struct {
	// Prefix is the export's root within the bucket, above the YYYY/MM/DD
	// partitions.
	Prefix          string
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
	if err := migrateLegacyState(cp, opts.LegacyCheckpointNamespace, namespace); err != nil {
		return nil, err
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
func (c *Collector) Name() string { return "objectstore" }

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

	state, err := loadScanState(c.cp, c.opts.Prefix)
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
				telemetry.Attrs{attrReason: reasonReadError})
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
	cycleExhausted := false
	for _, gap := range gaps {
		if gap.Quarantined || now.Before(gap.NextAttempt) {
			continue
		}
		if remaining == 0 || cycleExhausted || !c.canAttempt(wireUsed, decoded, rows) {
			gapDeferred++
			continue
		}
		at, comp, parsed := parseKey(gap.Key)
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
	e.Gauge(docBacklog.Name, docBacklog.Unit, docBacklog.Description,
		float64(len(candidates)-durableCandidates), telemetry.Attrs{})
	if c.opts.OnIngest != nil && records > 0 {
		c.opts.OnIngest(semconv.IngestSourceObjectStore, c.signal.Signal(), records, int(fetched))
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
}

// enumerate lists the day partitions spanning [from, now] and returns the
// objects worth fetching, oldest first.
func (c *Collector) enumerate(
	ctx context.Context,
	from, now time.Time,
	seen map[string]struct{},
	gaps map[string]gapState,
	positions map[string]string,
	e telemetry.Emitter,
) (enumeration, error) {
	prefixes, err := listingPrefixes(positions, dayPrefixes(c.opts.Prefix, from, now, maxDayPrefixes))
	if err != nil {
		return enumeration{}, err
	}
	var result enumeration
	var unparsed, already, stale, future int
	for _, prefix := range prefixes {
		// Listing is bounded per cycle even before the ingest budget: a day's
		// partition on a large tailnet is hundreds of objects, and there is no
		// value in walking further than one cycle could ever consume.
		startAfter := positions[prefix]
		page, err := c.api.List(ctx, prefix, startAfter, c.opts.MaxObjects*4)
		if err != nil {
			return enumeration{}, fmt.Errorf("objectstore: list %s: %w", prefix, err)
		}
		progress := prefixProgress{
			prefix:     prefix,
			startAfter: startAfter,
			active:     hasPosition(positions, prefix),
			truncated:  page.Truncated,
		}
		for _, o := range page.Objects {
			at, comp, ok := parseKey(o.Key)
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
			case at.Before(from) && !progress.active:
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

// ingest fetches, decompresses, and stages every nonblank row before sending
// any row to the configured signal processor.
func (c *Collector) ingest(
	ctx context.Context,
	identity string,
	comp compression,
	e telemetry.Emitter,
	cycle ingestLimits,
) (result ingestResult, err error) {
	body, err := c.api.Get(ctx, identity)
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

	var bad, semanticBad int
	for _, line := range staged {
		timestamps, err := c.signal.ProcessRecord(ctx, line, c.now(), e)
		switch {
		case errors.Is(err, ErrRecordDecode):
			bad++
			continue
		case errors.Is(err, ErrRecordInvalid):
			semanticBad++
			continue
		case err != nil:
			return result, &objectIngestError{stage: "process", err: err}
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

// countSkips emits one skipped counter per non-zero reason.
func countSkips(e telemetry.Emitter, byReason map[string]int) {
	for reason, n := range byReason {
		if n > 0 {
			e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(n),
				telemetry.Attrs{attrReason: reason})
		}
	}
}
