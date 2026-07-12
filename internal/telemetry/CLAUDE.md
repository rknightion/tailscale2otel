# internal/telemetry (+ semconv, metricdoc, catalog)

The OTEL facade and the **code-as-documentation** metrics catalog. Collectors and processors never
touch the OTEL SDK directly — they emit through a small interface and declare their signals as data.

## telemetry — the Emitter facade

`telemetry.Provider` (`provider.go`) builds the OTEL SDK pipeline (metric + log exporters: grpc / http
/ stdout, cumulative temporality for Grafana Cloud) from `Options`, and hands out a `telemetry.Emitter`
(`types.go`). The Emitter is the only thing collectors/processors see:

```go
Counter(name, unit, desc string, add float64, attrs Attrs)        // monotonic counter
Gauge(name, unit, desc string, value float64, attrs Attrs)        // synchronous gauge
UpDownCounter(name, unit, desc string, value float64, attrs Attrs)
Histogram(name, unit, desc string, value float64, bounds []float64, attrs Attrs)
HistogramCtx(ctx context.Context, name, unit, desc string, value float64, bounds []float64, attrs Attrs) // like Histogram, but ctx lets the SDK attach a trace exemplar
LogEvent(ev Event)                                                // one OTEL log record
```

The concrete `otelEmitter` (`emitter.go`) caches instruments by name and converts the `Attrs` map to
OTEL key-values. Pass `name`/`unit`/`desc` straight from your `metricdoc` descriptor (below) so the
emitted signal can't drift from the declared one. The Emitter is safe for concurrent use.

## ProviderSet — one Provider per tailnet, fanned out from one process

A `Provider` is **per-tailnet**, not global. `app.New` never constructs a bare `Provider`; it always
builds a `telemetry.ProviderSet` (`providerset.go`): one **process** Provider (no `tailscale.tailnet`
attribute; carries process/global self-obs — `tailscale2otel.up`, build info, and any signal with no
tailnet dimension) plus one **tailnet** Provider per configured tailnet, each stamping
`tailscale.tailnet=<name>` as a per-signal **const attribute** (`constLabelAttrs` in `provider.go` —
NOT a Resource attribute) on every metric/log/span it emits. All providers export to the same
configured OTLP backend. This holds even for a single-tailnet deployment — it still goes through a
`ProviderSet` with exactly one tailnet entry; there is no separate single-Provider code path.

- `NewProviderSet(ctx, base Options, tailnets []PerTailnetOptions)` builds the process provider from
  `base` (`TailnetName` cleared), then one Provider per `PerTailnetOptions{Name, InstanceID}` entry
  (`base` with `TailnetName` + a **distinct** `InstanceID`). `InstanceID` must be unique per tailnet: on
  Grafana Cloud's OTLP→Prometheus mapping, Resource attributes other than job/instance/service_* live
  only in `target_info`, so two tailnet providers sharing one `service.instance.id` would collide.
- `.Process()` / `.Tailnet(name)` return the respective `*Provider`; `.TailnetNames()` returns
  construction order; `.PromGatherers()` merges every provider's Prometheus registry (process first,
  then each tailnet) into one `prometheus.Gatherers` for the single opt-in `/metrics` pull endpoint;
  `.Shutdown(ctx)` flushes/stops all of them, joining any errors.
- `provider: headscale` deployments have no tailnet fan-out: the single Headscale runtime shares the
  **process** provider/emitter directly (no `tailscale.tailnet` attribute), matching the pre-multi-tailnet
  single-Resource output — see `app.New`'s Headscale branch and `internal/provider`/`internal/hsapi`.

## semconv — attribute & unit constants

`internal/semconv` holds every attribute key and unit string used across collectors/processors (stable
OTEL names like `network.io.direction`, `source.address`, `host.name`; Tailscale-specific
`tailscale.*` keys; UCUM units `By`, `s`, `{packet}`, `1`, …). **Always reference these constants — no
string literals** for attribute keys or units, or the docs/Prometheus names drift silently.

## metricdoc + catalog — declare once, document automatically

- A signal is declared as a `metricdoc.Metric` / `metricdoc.LogEvent` descriptor (instrument type,
  name, unit, description, attributes, group) in its owning package's `catalog.go`.
- `internal/catalog` aggregates every package's `Catalog()` / `LogCatalog()` and `catalog.Render()`
  rewrites the tables between the `<!-- BEGIN GENERATED -->` / `<!-- END GENERATED -->` markers in
  `docs/metrics.md`. **Edit catalogs in code, then regenerate — never hand-edit the generated tables.**
  Adding a new emitting package means adding it to the source lists in `internal/catalog/catalog.go`.
  Exception: the **app layer's** self-obs descriptors live in the leaf package `internal/appcatalog`
  (not `internal/app`) so `internal/catalog` can aggregate them without importing `internal/app`, which
  itself imports `internal/catalog` to render the admin status page.

Regenerate (from repo root):
```sh
go run -C tools/metricscatalog . -write -file "$PWD/docs/metrics.md"
```
CI runs the same tool with `-check` and fails on drift. (See root `CLAUDE.md` for why the nested-module
invocation needs `-C` and an absolute `-file`.)

## PromName normalization — the naming gotcha

`metricdoc.PromName()` mirrors Grafana Cloud's OTLP→Prometheus rules, and the order/quirks matter when
you choose a metric's instrument + unit:

1. dots → underscores (`tailscale.network.io` → `tailscale_network_io`);
2. unit suffix: `By`→`_bytes`, `s`→`_seconds`, `d`→`_days`; annotation units like `{packet}`/`{flow}`
   are **dropped**;
3. a **unit-`"1"` gauge → `_ratio`** (a unit-`"1"` counter does *not* — it just gets `_total`);
4. monotonic counter → `_total`.

Consequence to remember: a plain integer **count** declared as a unit-`"1"` gauge becomes `*_ratio`
(e.g. `tailscale_devices_count_ratio`) even though it isn't a fraction. `*_seconds` expiry/last-seen
gauges hold **absolute epoch timestamps** (dashboards subtract `time()`). When in doubt, run the tool
and read the generated Prometheus-name column.

## Cardinality self-tracking — `tailscale2otel.series.active`

`cardinality.go`'s `CardinalityTracker` counts the distinct attribute combinations (time series) emitted
per source metric. `Observe(name, attrs)` is called from the emit hot path for **every** data point
(gated by self-obs — a nil tracker is a no-op); `Report(e)` runs once per OTLP export interval, emits one
`tailscale2otel.series.active` gauge per source metric (keyed by the `metric.name` attribute), then
**resets** the sets so each interval measures active-per-interval cardinality (a metric that stops
emitting drops out of *this tracker's own measurement* rather than lingering at a stale value).

> **Churning per-entity gauges must use `GaugeSnapshot`, not `Gauge` (#55).** A *synchronous* gauge
> (`Emitter.Gauge`) under the forced cumulative temporality never drops a series: the SDK's
> `cumulativeLastValue` keeps every attribute set it has ever seen and re-exports its last value forever
> (upstream otel-go #3006). An **observable** gauge is different — pin down the version, because it
> matters: in otel-go v1.44 an `ObservableGauge` routes to `PrecomputedLastValue`, whose cumulative
> collect *clears* stale sets each cycle, so it reports only the series observed in the current
> collection. `Emitter.GaugeSnapshot(name, unit, desc, []GaugePoint)` (emitter.go) wraps that: a series
> absent from a later snapshot drops out instead of ghosting. Collectors accumulate via
> `telemetry.GaugeSnapshotBuilder` (`gaugesnapshot.go`: hold one, `Add` points each `Collect`, then
> `Flush` — Flush re-emits every gauge it has ever seen, empty snapshots included, so an emptied metric
> clears). Every attribute-keyed (churning) gauge in the devices, nodemetrics, and dns collectors now
> uses this; only **nil-attr single-series** gauges stay synchronous (they can't churn). The one
> deliberate exception is nodemetrics' forwarded passthrough samples (`emitSample`) — dynamic names +
> monotonic counters, so snapshot semantics don't fit; documented in `docs/metrics.md`. The self-obs
> `series.active` tracker's own drop-out (line above) is unrelated — it resets its in-process map each
> interval, independent of instrument temporality.

Things to preserve when editing:

- **Self-exclusion:** the tracker never measures `series.active` itself — both to avoid skew and to break
  the `Report → Gauge → Observe` recursion. Keep the name-equality guard if you rename the metric.
- **Per-metric cap** (`defaultSeriesCap` = 10000): at the cap the reported value pins at the cap (a
  visible "≥ this" signal) and further distinct series stop being counted, bounding memory.
- `Snapshot()` returns the last interval's counts (sorted by count desc) for in-process introspection —
  the admin status page joins it with the catalog. It returns nil before the first `Report`.

## Testing & catalog gotchas

- `telemetrytest.Recorder` renders int64 **log** attributes as `""` (`Value.AsString` on the Int64
  kind); they emit correctly in prod. Assert the attribute's *presence*, then verify its value via the
  log `Body`.
- The per-package `catalog_test.go` consistency guard checks name membership + unit/instrument/
  description — **not attribute keys**. Attribute accuracy in the generated docs relies on the catalog's
  `Attributes` being correct, so review them by hand when you add or change a signal (this is exactly
  how a missing per-record attribute once slipped past the tests).
