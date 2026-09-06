# internal/telemetry (+ semconv, metricdoc, catalog)

The OTEL facade and the code-as-documentation metrics catalog. Pass `name`/`unit`/`desc` straight
from the `metricdoc` descriptor so an emitted signal cannot drift from the declared one.

## ProviderSet - one Provider per tailnet, fanned out from one process

A `Provider` is per-tailnet, never global. `app.New` never constructs a bare `Provider`; it always
builds a `ProviderSet` with one **process** provider (no `tailscale.tailnet`; carries process-global
self-obs such as `tailscale2otel.up` and build info) plus one **tailnet** provider per configured
tailnet, each stamping `tailscale.tailnet` as a per-signal **const attribute** (`constLabelAttrs` in
`resource.go`), NOT a Resource attribute. This holds for a single-tailnet deployment too - there is
no separate single-Provider code path.

- **`InstanceID` must be unique per tailnet.** On Grafana Cloud's OTLP to Prometheus mapping, a
  Resource attribute other than job/instance/service_* lives only in `target_info`, so two tailnet
  providers sharing one `service.instance.id` collide.
- `PromGatherer()` returns a `mergedGatherer`, deliberately **not** a bare `prometheus.Gatherers`:
  providers built from an identical Resource emit an identical `target_info`, which the plain merge
  reports as a duplicate on every scrape. It is nil when the Prometheus reader is off, which is how
  `app.New` decides whether to stand the endpoint up at all.
- `Shutdown` runs the providers concurrently under one shared deadline, and each provider shuts its
  own pipelines concurrently inside that. One tailnet's blocked exporter must never consume the
  whole budget.
- A `provider: headscale` deployment has no fan-out: the single Headscale runtime shares the
  **process** provider directly, with no `tailscale.tailnet` attribute.

## What may go on the metrics Resource

The OTLP to Prometheus convention promotes only `service.name` (+`service.namespace`) to `job` and
`service.instance.id` to `instance`; every other resource attribute belongs on `target_info`.
Grafana Cloud deviates and promotes the whole `service.*` namespace, so anything added there becomes
a label on **every series**.

**Never put a value that changes per build or deploy on the metrics Resource.** Each new value mints
a whole new series set, and an OTLP push carries no staleness signal, so after a redeploy old and
new coexist for the query lookback window: any panel summing across a bounded dimension transiently
doubles, and active-series cardinality grows by the number of values ever seen.

- `buildResource(ctx, opts, includeServiceVersion)` (`resource.go`) builds two resources: metrics get
  `false`, logs and traces get `true`. Logs are never summed and have no per-series label surface.
- **Traces are a deliberate, eyes-open exception.** Grafana Cloud's Tempo metrics-generator derives
  Prometheus series from spans and promotes span resource attributes, so `service.version` on the
  traces resource does reach a per-series surface as `service_version` on `traces_spanmetrics_*`.
  Accepted: version-on-RED is a legitimate canary dimension and the cardinality is trivial next to
  the flow series the metrics rule protects. **Do not "fix" this in the resource builder** - if the
  redeploy artifact ever matters, the surgical fix is backend-side, excluding `service_version` from
  the Tempo metrics-generator's dimensions.
- The version's metrics home is the `tailscale2otel.build_info` gauge. Join with `group_left`
  **on `job`, not `instance`**: `build_info` comes from the process provider while tailnet signals
  come from providers that each carry a distinct `service.instance.id`, so `on (job, instance)`
  matches nothing.
  ```promql
  tailscale_devices_count_ratio * on (job) group_left(version) tailscale2otel_build_info_ratio
  ```
- Its attribute key is **`version`, not `service.version`** - the latter normalizes to the
  `service_version` label and re-enters the promoted namespace.
- `reservedPromotedLabels` (`resource.go`) lists what Grafana Cloud promotes so the Emitter can drop
  a colliding data-point attribute; a duplicate label gets the whole sample rejected as
  `otlp_parse_error`. Keep it in sync with what the **metrics** resource actually carries -
  `service_version` is deliberately absent.

## semconv

`internal/semconv` holds every attribute key and unit string used across collectors and processors.
**Always reference these constants, never a string literal**, or the generated docs and the
Prometheus names drift silently.

## PromName - the naming trap

`metricdoc.PromName()` delegates to `otlptranslator` with `UnderscoreEscapingWithSuffixes`, so the
same name comes out on both the pull path and the Grafana Cloud push path. Beyond the base
normalization: **a suffix token already present anywhere in the name is not appended again**
(`tailscale2otel.objectstore.bytes` with unit `By` stays `..._bytes_total`), and a `total`/`ratio`
token found mid-name is MOVED to the end rather than duplicated. `*_seconds` expiry and last-seen
gauges hold **absolute epoch timestamps**; dashboards subtract `time()`.

## Cardinality self-tracking - `tailscale2otel.series.active`

`CardinalityTracker.Observe(name, attrs)` runs on the emit hot path for every data point (gated by
self-obs; a nil tracker is a no-op). `Report(e)` runs once per export interval, emits one gauge per
source metric, then **resets** the sets, so each interval measures active-per-interval cardinality.
When editing it, preserve:

- **Self-exclusion.** The tracker never measures `series.active` itself, both to avoid skew and to
  break the `Report` to `Gauge` to `Observe` recursion. Keep the name-equality guard if you rename.
- **The per-metric cap** (`defaultSeriesCap`): at the cap the reported value pins at the cap, a
  visible "at least this" signal, and further distinct series stop being counted.
- `Snapshot()` returns the previous interval's counts and is nil before the first `Report`.

## Churning gauges must use `GaugeSnapshot`, not `Gauge`

A **synchronous** gauge under the forced cumulative temporality never drops a series: the SDK's
`cumulativeLastValue` keeps every attribute set it has ever seen and re-exports its last value
forever. An **observable** gauge routes to `PrecomputedLastValue`, whose cumulative collect clears
stale sets each cycle. `Emitter.GaugeSnapshot` wraps that, and collectors accumulate through
`telemetry.GaugeSnapshotBuilder` - hold one, `Add` points each `Collect`, then `Flush`, which
re-emits every gauge it has ever seen, empty snapshots included, so an emptied metric clears.

Every attribute-keyed (churning) gauge must use it. Only **nil-attr single-series** gauges stay
synchronous, because they cannot churn. The one deliberate exception is nodemetrics' forwarded
passthrough samples: dynamic names plus monotonic counters, so snapshot semantics do not fit.

## Testing

`telemetrytest.AssertCatalogAttrs` is the attribute-drift guard: it asserts every emitted attribute
key is declared in the matching catalog entry. It deliberately does **not** require every declared
attribute to be observed - one test rarely exercises every conditional attribute, and a
declared-but-unemitted attribute does not corrupt the docs - so a wrongly declared attribute name
still reaches `docs/metrics.md` unchallenged. It is also opt-in: a package's `catalog_test.go` has
to call it, and half of them still do not, so those signals' attribute keys are guarded by review
alone. Add the call when you add a signal.
