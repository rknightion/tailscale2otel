// Package objectstore implements the "objectstore" ingestion path for Tailscale
// network flow logs: the export Tailscale writes into an S3-compatible bucket.
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
// # Exactly-once, and what guarantees it
//
// Three mechanisms, each covering what the others cannot:
//
//   - A durable CURSOR, the timestamp of the newest object ingested. It advances
//     only over ground actually covered.
//   - A durable SEEN SET of recently ingested object keys, held in the same
//     checkpoint store. Listing overlaps backwards past the cursor by Lookback so
//     late-arriving objects are found, and this set is what keeps that overlap
//     from re-ingesting everything inside it.
//   - The processor's own connection-level de-duplication, shared with the poll
//     and stream paths, as the failsafe underneath both.
//
// The seen set is pruned to the overlap window on every cycle, so it stays a few
// dozen entries rather than growing with the bucket.
package objectstore

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/s3"
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
	maxSeenKeys = 5000
)

// checkpoint key prefixes within the shared store. The cursor is one entry; the
// seen set is one entry per recently ingested object.
const (
	cursorKey  = "objectstore.flowlogs.cursor"
	seenPrefix = "objectstore.flowlogs.seen/"
)

// objectAPI is the subset of the object store this collector needs. Narrow by
// design so tests can fake it without an S3 server. *s3.Client satisfies it.
type objectAPI interface {
	List(ctx context.Context, prefix, startAfter string, limit int) ([]s3.Object, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Options configures a Collector. Zero values select the defaults above.
type Options struct {
	// Prefix is the export's root within the bucket, above the YYYY/MM/DD
	// partitions.
	Prefix          string
	Interval        time.Duration
	Lookback        time.Duration
	InitialLookback time.Duration
	MaxObjects      int
	Logger          *slog.Logger
	// Now is injectable so cursor arithmetic is testable without sleeping.
	Now func() time.Time
	// OnIngest, when non-nil, is called once per cycle with
	// ("objectstore", "flow", <records accepted>, <bytes fetched>). The app
	// supplies it, gated on self-observability.
	OnIngest func(source, signal string, records, bytes int)
}

// Collector ingests exported flow-log objects and feeds them to the shared
// processor.
type Collector struct {
	api    objectAPI
	proc   *flowlog.Processor
	cp     collector.CheckpointStore
	opts   Options
	logger *slog.Logger
	now    func() time.Time
}

var _ collector.SnapshotCollector = (*Collector)(nil)

// New returns a Collector reading from api, converting with proc, and keeping
// its cursor and seen set in cp.
func New(api objectAPI, proc *flowlog.Processor, cp collector.CheckpointStore, opts Options) *Collector {
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
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Collector{api: api, proc: proc, cp: cp, opts: opts, logger: logger, now: now}
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

	seen := c.seenKeys()
	candidates, listed, err := c.enumerate(ctx, from, now, seen, e)
	if err != nil {
		return err
	}

	// Oldest first, so the cursor only ever advances over ground covered.
	var (
		newest   = cursor
		objects  int
		records  int
		fetched  int64
		budgeted = candidates
	)
	if len(budgeted) > c.opts.MaxObjects {
		budgeted = budgeted[:c.opts.MaxObjects]
	}
	for _, cand := range budgeted {
		n, err := c.ingest(ctx, cand.obj.Key, cand.comp, e)
		if err != nil {
			// Counted and logged, never fatal: one unreadable object must not
			// stop every object behind it.
			c.logger.Error("objectstore: ingest failed", "key", cand.obj.Key, "error", err)
			e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, 1,
				telemetry.Attrs{attrReason: reasonReadError})
			continue
		}
		objects++
		records += n
		fetched += cand.obj.Size
		if cand.at.After(newest) {
			newest = cand.at
		}
		if err := c.cp.Set(seenPrefix+cand.obj.Key, cand.at); err != nil {
			c.logger.Warn("objectstore: could not record an ingested object; it may be re-read after a restart",
				"key", cand.obj.Key, "error", err)
		}
	}

	if skipped := len(candidates) - len(budgeted); skipped > 0 {
		// Never a silent truncation: an operator whose bucket is outrunning the
		// per-cycle cap needs to know before the backlog becomes days.
		c.logger.Warn("objectstore: per-cycle object budget reached; the remainder will be picked up next cycle",
			"ingested", len(budgeted), "deferred", skipped, "budget", c.opts.MaxObjects)
		e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(skipped),
			telemetry.Attrs{attrReason: reasonBudget})
	}

	e.Counter(docObjects.Name, docObjects.Unit, docObjects.Description, float64(objects), telemetry.Attrs{})
	e.Counter(docRecords.Name, docRecords.Unit, docRecords.Description, float64(records), telemetry.Attrs{})
	e.Counter(docBytes.Name, docBytes.Unit, docBytes.Description, float64(fetched), telemetry.Attrs{})
	e.Gauge(docBacklog.Name, docBacklog.Unit, docBacklog.Description,
		float64(len(candidates)-len(budgeted)), telemetry.Attrs{})
	if c.opts.OnIngest != nil && records > 0 {
		c.opts.OnIngest("objectstore", "flow", records, int(fetched))
	}

	if newest.After(cursor) {
		if err := c.cp.Set(cursorKey, newest); err != nil {
			return fmt.Errorf("objectstore: persist cursor: %w", err)
		}
	}
	c.pruneSeen(newest)
	_ = listed
	return nil
}

// candidate is one object that has passed the cursor and seen-set filters.
type candidate struct {
	obj  s3.Object
	at   time.Time
	comp compression
}

// enumerate lists the day partitions spanning [from, now] and returns the
// objects worth fetching, oldest first.
func (c *Collector) enumerate(ctx context.Context, from, now time.Time, seen map[string]struct{}, e telemetry.Emitter) (out []candidate, listed int, err error) {
	var unparsed, already, stale int
	for _, prefix := range dayPrefixes(c.opts.Prefix, from, now, maxDayPrefixes) {
		// Listing is bounded per cycle even before the ingest budget: a day's
		// partition on a large tailnet is hundreds of objects, and there is no
		// value in walking further than one cycle could ever consume.
		objs, err := c.api.List(ctx, prefix, "", c.opts.MaxObjects*4)
		if err != nil {
			return nil, 0, fmt.Errorf("objectstore: list %s: %w", prefix, err)
		}
		listed += len(objs)
		for _, o := range objs {
			at, comp, ok := parseKey(o.Key)
			switch {
			case !ok:
				unparsed++
			case at.Before(from):
				stale++
			default:
				if _, dup := seen[o.Key]; dup {
					already++
					continue
				}
				out = append(out, candidate{obj: o, at: at, comp: comp})
			}
		}
	}
	countSkips(e, map[string]int{
		reasonUnparsedKey:  unparsed,
		reasonAlreadySeen:  already,
		reasonBeforeCursor: stale,
	})

	// Chronological order across day prefixes. Within one prefix the listing is
	// already ordered, but a stable sort over all of them is what makes the
	// cursor safe to advance to the newest ingested object.
	sortCandidates(out)
	return out, listed, nil
}

// ingest fetches, decompresses and decodes one object, feeding every record to
// the shared processor. It returns the number of records handed over.
func (c *Collector) ingest(ctx context.Context, key string, comp compression, e telemetry.Emitter) (int, error) {
	body, err := c.api.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	defer body.Close()

	r, closeFn, err := decompress(body, comp)
	if err != nil {
		return 0, err
	}
	defer closeFn()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	var records, bad int
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var fl flowlog.FlowLog
		if err := json.Unmarshal(line, &fl); err != nil {
			// One malformed line does not condemn the object: the export is
			// line-delimited precisely so a bad record costs one record.
			bad++
			continue
		}
		c.proc.Process(fl, e)
		records++
	}
	if err := sc.Err(); err != nil {
		// Partial ingestion has already happened and its records are real. The
		// error is returned so the object is NOT marked seen and is retried,
		// which the processor's connection de-duplication makes safe.
		return records, fmt.Errorf("read %s: %w", key, err)
	}
	if bad > 0 {
		c.logger.Warn("objectstore: skipped malformed records", "key", key, "records", bad)
		e.Counter(docSkipped.Name, docSkipped.Unit, docSkipped.Description, float64(bad),
			telemetry.Attrs{attrReason: reasonDecodeError})
	}
	return records, nil
}

// decompress wraps r according to comp. The returned close function releases the
// decoder's own resources; it never closes r, which the caller owns.
func decompress(r io.Reader, comp compression) (io.Reader, func(), error) {
	switch comp {
	case compZstd:
		zr, err := zstd.NewReader(r)
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

// seenKeys reads the durable set of recently ingested object keys.
func (c *Collector) seenKeys() map[string]struct{} {
	out := map[string]struct{}{}
	for _, k := range c.cp.Keys() {
		if key, ok := strings.CutPrefix(k, seenPrefix); ok {
			out[key] = struct{}{}
		}
	}
	return out
}

// pruneSeen drops entries older than the overlap window. Anything that old can
// no longer be re-listed, so remembering it costs storage and buys nothing.
func (c *Collector) pruneSeen(cursor time.Time) {
	cutoff := cursor.Add(-2 * c.opts.Lookback)
	var kept []seenEntry
	for _, k := range c.cp.Keys() {
		if !strings.HasPrefix(k, seenPrefix) {
			continue
		}
		at, _ := c.cp.Get(k)
		if at.Before(cutoff) {
			_ = c.cp.Delete(k)
			continue
		}
		kept = append(kept, seenEntry{key: k, at: at})
	}
	// A hard cap underneath the time-based pruning, so a bucket writing objects
	// faster than the window assumes cannot grow the checkpoint file without
	// limit. Oldest go first.
	if len(kept) > maxSeenKeys {
		sortEntriesByTime(kept)
		for _, ent := range kept[:len(kept)-maxSeenKeys] {
			_ = c.cp.Delete(ent.key)
		}
	}
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
