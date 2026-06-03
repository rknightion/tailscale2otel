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
LogEvent(ev Event)                                                // one OTEL log record
```

The concrete `otelEmitter` (`emitter.go`) caches instruments by name and converts the `Attrs` map to
OTEL key-values. Pass `name`/`unit`/`desc` straight from your `metricdoc` descriptor (below) so the
emitted signal can't drift from the declared one. The Emitter is safe for concurrent use.

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
