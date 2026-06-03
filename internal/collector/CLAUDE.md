# internal/collector

Polling framework + one subpackage per Tailscale data source. The framework here owns scheduling,
jitter, time-window bookkeeping, checkpoints, and per-tick self-observability; each subpackage owns
"fetch from the API and emit signals".

## The two collector interfaces (`collector.go`)

- **`SnapshotCollector`** — point-in-time reads. `Name()`, `DefaultInterval()`,
  `Collect(ctx, telemetry.Emitter) error`. Used by devices, users, keys, settings, acl, dns, nodemetrics.
  Stateless between ticks (exception: `acl` keeps an ETag to detect changes).
- **`WindowCollector`** — time-windowed reads. Adds `CollectWindow(ctx, from, to, e) (highWaterMark time.Time, err error)`
  and `Lag() time.Duration`. Used by flowlogs, auditlogs. The scheduler sets `to = now - Lag()` so the
  query never reaches "now" (late records). Return a zero high-water mark on failure so the checkpoint
  does **not** advance (the window is retried).

Both add a compile-time assertion in the subpackage, e.g. `var _ collector.SnapshotCollector = (*Collector)(nil)`.

## Scheduling, checkpoints, failure isolation

- `scheduler.go` runs one goroutine per registered collector, on its own ticker, with startup jitter
  (default ~10% of interval) to desync collectors. A panic or error in one tick is **recovered**, logged,
  and still emits scrape metrics — it never stops the scheduler or other collectors.
- Window collectors use `checkpoint.go` (`CheckpointStore`: file-backed JSON written atomically, or
  in-memory). `window.go` computes the next `[from, to]` from the last checkpoint, honoring
  `InitialLookback` (cold start) and `MaxWindow` (cap a long catch-up). The Tailscale window is
  **inclusive on both ends**, so boundary records repeat across ticks — window collectors hold a bounded
  `dedup.Set` to suppress the overlap.
- `selfobs.go` emits the framework `tailscale2otel.scrape.*` metrics (duration, success, errors, last
  timestamp), tagged with the `tailscale.collector` attribute, after every tick — classifying errors
  as `panic`/`timeout`/`error`.

## Adding a new collector

1. **Create `internal/collector/<name>/`** with `<name>.go`, `catalog.go`, `<name>_test.go`, `catalog_test.go`.
2. **Declare signals in `catalog.go`** using `metricdoc.Metric` / `metricdoc.LogEvent` descriptors
   (name, unit, description, attributes, group), and export `Catalog()` / `LogCatalog()`. Use
   `internal/semconv` constants for attribute keys and units — never string literals.
3. **Implement the collector in `<name>.go`:** a narrow `api interface{ ... }` (only the methods you
   need, for test fakes), a `Collector` struct, a `New(...)` constructor with sane defaults, `Name()`,
   `DefaultInterval()`, and `Collect`/`CollectWindow`. Emit only via the `telemetry.Emitter` argument,
   referencing your catalog descriptors so name/unit/description can't drift.
4. **Register in `internal/app/collectors.go`** (`registerCollectors`): `registry.Register(c, interval)`
   for snapshot, or `registry.RegisterWindow(c, interval, initialLookback, maxWindow)` for window. Gate
   on the collector's config `Enabled` flag (and, for window collectors, `pollSource(cfg.Source)` so a
   `stream`-only deployment doesn't also poll).
5. **Wire into the global catalog** (`internal/catalog/catalog.go`): add `<name>.Catalog` / `<name>.LogCatalog`.
6. **Test:** drive `Collect`/`CollectWindow` with a fake `api`; `catalog_test.go` should assert emitted
   signals match the declared catalog. Then regenerate docs:
   `go run -C ../../tools/metricscatalog . -write -file "$(git rev-parse --show-toplevel)/docs/metrics.md"`.

## Gotchas

- Collectors share one `config.CollectorConfig` union type; only window collectors read
  `Lag`/`InitialLookback`/`MaxWindow`, and only `flowlogs`/`auditlogs` interpret `Source`.
- `flowlogs`/`auditlogs` pollers and the `stream`/`webhook` receivers feed the **same** `flowlog.Processor`
  / `audit.Processor`. Don't duplicate emission logic in the collector — put it in the processor so both
  paths stay identical.
- Feature-gated collectors (e.g. flowlogs) treat a 403 / feature-off as "idle" (emit `feature.enabled=0`,
  advance the checkpoint, no error), not as a failure.
- **Rich device data needs `tsapi.DevicesRich()`** (raw `GET /devices?fields=all`), not the flat
  `tsclient.Device` (no online flag, per-DERP latency, routes, os.version, or nodeId). Several fields
  are *derived*, not native: `device.online` from `LastSeen` recency (injectable clock via
  `export_test.go`), key "type" from `Capabilities`. `go doc` the type before assuming a field exists.
- **Cross-source dedup keys are content-based / time-free** so the poll and stream copies of one record
  collapse to a single emission: flow uses `nodeId|start|end|proto|src|dst`; audit (when an
  `eventGroupID` is present) uses `eventGroupID|action|target.id|target.property` — deliberately
  time-free because poll carries an ns-precision `eventTime` while streamed audit records have none (the
  receiver threads the HEC envelope time onto `EventTime` only when it's zero). Never put a timestamp in
  a cross-path dedup key.
- **nodemetrics reachability:** the scraper reads tailscaled client metrics (`tailscale set --webclient`
  → `:5252`, plain HTTP); reaching them across the tailnet needs an ACL grant opening TCP 5252 to the
  scraping node. Nodes < v1.78 have no endpoint → `node.up=0`. Per-target bearer/TLS is supported, but
  tailnet client metrics are unauthenticated by default.
