# internal/collector

The polling framework plus one subpackage per data source. The framework owns scheduling, jitter,
time-window bookkeeping, checkpoints and per-tick self-observability; a subpackage owns "fetch from
the API and emit signals".

## The two interfaces (`collector.go`)

`SnapshotCollector` is a point-in-time read. `WindowCollector` adds `CollectWindow(ctx, from, to, e)
(highWaterMark, error)` and `Lag()`; the scheduler sets `to = now - Lag()` so the query never
reaches "now" and misses late records. **Return a zero high-water mark on failure** or the
checkpoint advances past a window that was never read. Add the compile-time assertion
(`var _ collector.SnapshotCollector = (*Collector)(nil)`) in the subpackage.

## Scheduling and checkpoints

- One goroutine per collector on its own ticker. Each runs its first tick promptly after a random
  stagger bounded by an **absolute** window (`defaultStaggerWindow`, 3s), not a fraction of the
  interval - a fraction would make a 600s collector wait minutes for first data. A panic or error
  in one tick is recovered, logged, still emits scrape metrics, and never stops the scheduler or
  another collector.
- The Tailscale window is **inclusive on both ends**, so boundary records repeat across ticks.
  Window collectors hold a bounded `dedup.Set` to suppress the overlap.
- **The atomic checkpoint write lives in `atomicfile.go`, not `checkpoint.go`.** Do not "simplify"
  `writeFileAtomic` back to a predictable `<path>.tmp`: that name can be pre-placed as a symlink and
  followed, and two concurrent savers sharing it lose a checkpoint outright. Writer and sweeper both
  derive names from `stagingNameBounds` so they cannot drift apart; the sweep is `Lstat`-based and
  never follows a symlink. `writeAndSync` and `readDirForSweep` are package vars purely so tests can
  inject failures.
- `StatusTracker` records every tick regardless of `WithSelfObs`, so the admin status page works
  even when `scrape.*` emission is suppressed. It is in-process introspection and emits no OTLP; a
  nil tracker is a no-op.

## Adding a collector

Declare signals as `metricdoc` descriptors in the subpackage's `catalog.go`, reference
`internal/semconv` constants for attribute keys and units (**never string literals**, or the
generated docs and Prometheus names drift silently), emit only through the passed
`telemetry.Emitter` referencing those descriptors, and take a narrow `api interface{ ... }` holding
only the methods you need so tests can fake it. Then wire it in two places outside this package:
`internal/app/collectors.go` (`registerCollectors`, gated on the config `Enabled` flag and, for a
window collector, `pollSource(cfg.Source)` so a `stream`-only deployment does not also poll) and the
source lists in `internal/catalog/catalog.go`.

**Record availability, or the collector ships dark.** Call
`apistate.Observe(e, c.tracker, c.Name(), opConst, apistate.Disposition{}, err, c.now())` right after
each API call and **before** branching on `err` - success is observed too. A collector that never
observes reads `unknown` on the status page forever and cannot fire either shipped availability
alert. `TestEveryRegisteredCollectorRecordsAPIState` fails if you skip it. Three traps:

- **The disposition is `apistate.Disposition{}`.** An ambiguous 403 must stay `scope_denied`.
  `flowlogs` is the ONLY operation allowed `DisabledOn: []int{403}`, because upstream documents its
  403 as the feature gate. Read the `internal/apistate` package doc before deviating.
- **The metric and the tracker are separate.** `Observe` emits the metric even with a nil tracker, so
  forgetting `WithAPIState` at registration alerts fine and still shows `unknown` on the capability
  matrix.
- **Operation naming** is the upstream operationId from `spec/tailscale-api.json`, except a
  per-entity subrequest, which records under its bounded subrequest name (`device_posture`,
  `device_invites`, `user_invites`) because `internal/app/capability.go` joins a subrequest row on
  `operation == sub.Name`. Aggregate a per-entity loop into ONE entry per tick (first error wins;
  emit nothing when there were zero attempts, so an unprobed operation stays honestly `unknown`).

## Gotchas

- The pollers and the `stream`/`webhook` receivers feed the **same** processor. Put emission logic
  in the processor, never in the collector, or the two paths drift.
- **Cross-source dedup keys are content-based and time-free** so the poll and stream copies of one
  record collapse: flow uses `nodeId|start|end|proto|src|dst`; audit, when an `eventGroupID` is
  present, uses `eventGroupID|action|target.id|target.property`. Poll carries an ns-precision
  `eventTime` while a streamed audit record has none. **Never put a timestamp in a cross-path dedup
  key.**
- A feature-gated collector treats a 403 or feature-off as **idle** - emit `feature.enabled=0`,
  advance the checkpoint, no error - not as a failure.
- Several device fields are derived rather than native: `device.online` from `LastSeen` recency
  (injectable clock via `export_test.go`), key "type" from `Capabilities`.
- **nodemetrics reachability:** the scraper reads tailscaled client metrics (`tailscale set
  --webclient`, port 5252, plain HTTP). Reaching them across the tailnet needs an ACL grant opening
  TCP 5252 to the scraping node. A node with no endpoint - tailscaled older than v1.78, or
  `--webclient` never set - reports `node.up=0`. Client metrics are unauthenticated by default;
  per-target bearer/TLS is supported.
- **nodemetrics discovery** runs on its own interval, separate from the scrape interval, and UNIONS
  its result with the static `targets` (dedup by URL, **static wins**). A discovery *failure* keeps
  the prior active set, so a flaky discoverer never empties the scrape set. The package stays
  provider-agnostic: `Discoverer` returns plain `Target`s and the concrete implementation lives in
  `internal/app/nodediscovery.go`, which also forces discovered instance labels unique -
  `instance_source: hostname` is not unique (many devices report `localhost`), so collisions are
  auto-suffixed with the node address and a WARN is logged. Prefer `name` or `address`.
- The `metric_allow`/`metric_deny` (anchored regex on the metric NAME) and `drop_labels` (`instance`
  is never dropped) filters apply ONLY to forwarded passthrough samples in `emitSample` - never to
  `tailscale.node.up` or the `discovery.*` gauges.
- **Per-entity gauge toggles are the main cardinality lever.** `cardinality.per_entity.{device,user,
  key}` gate the per-entity gauges; switching one off falls back to the aggregate count (the
  key-expiry WARN log still fires when `per_entity.key` is off). `cardinality.flow.collapse_external`
  buckets unresolved IPs as `external`/`unknown` and, when `cardinality.flow.node_dims` is on, also
  sets the src/dst node labels on flow *metrics*.
