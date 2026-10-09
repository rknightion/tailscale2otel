# Local WAL load comparison

Run `just load-wal`. No Tailscale account, credentials, Kubernetes cluster or
Grafana endpoint is needed. The harness uses synthetic HEC flows, the production
stream receiver and coordinator, a temporary disk WAL, the real flow processor
and telemetry provider, and a loopback OTLP HTTP sink. It never contacts a real
backend. A default run takes about 90 seconds on a laptop (about 20 seconds of
finite drains plus about one minute of sustained cases, each plus compile time).

```sh
# Defaults: finite drain 16 bodies x 256 flows, 2 ms sink delay, 1 repeat;
# sustained 60 bodies, one every 250 ms, slow sink 1500 ms, 2000 ms outage
just load-wal

# Original bodies, flows per body, sink delay (ms), repeats, then the
# sustained knobs: bodies, arrival gap (ms), slow-sink delay (ms), outage (ms)
just load-wal 32 512 10 2 80 250 1500 2000
```

`WAL_LOAD_INTERVAL_MS` (default 1000, the scheduled metric interval for every
case) is also honored as an environment variable. Bounds are 1 to 256 bodies, 1 to
4,096 flows per body (at most 65,536 flows per case), 1 to 100 ms sink delay,
1 to 10,000 ms between sustained arrivals (default a quarter of the interval, at
least 1 ms), and 1 ms to 60 s for the interval, slow delay and outage. The 256 MiB WAL capacity,
receiver limits and five-minute per-case deadline stay enforced: an excessive
combination fails rather than silently shrinking the workload.

Two benchmarks run:

- `BenchmarkIngressWALSustained` is the evidence for the scheduled-collection
  contract (TSO-0148). Read it first.
- `BenchmarkIngressWALDrain` is the original finite backlog drain.

## What changed in the contract, and what that does to timings

Ingress completion no longer forces a collection. An accepted body is staged into
a bounded preparation window, applied to the cumulative instruments, and
committed from the WAL only after a NORMAL scheduled collection that includes its
effect (and any required logs) has been delivered. Consequences you will see in
every number below:

- **Completion latency is bounded below by the collection schedule.** An entry
  waits for the next slot after it is applied, then for that collection to be
  exported, then for the commit. With a 1 s interval the measured median is about
  2.5 s from admission to commit (3 to 4 s with a slow sink). Nothing shortens it
  except a shorter `otlp.metric_interval`, which is also the sample cadence.
- **Cadence is the priority, delivery lag is the price.** Sample cadence stays at
  the configured rate no matter how fast bodies arrive. The cost is that a body is
  durable on disk, not yet "complete", for roughly two to three intervals.
- **Throughput is arrival-limited, not flush-limited.** The old per-body flush set
  the drain rate; now the scheduled interval does. Finite-drain `drain-s/op` is
  a few intervals regardless of the case, so cases differ far less than they did.
- **Earlier results are historical.** Every number measured before this change
  (the 2026-09-10 tables at the end, TSO-0149's coalesced-body comparison) used a
  per-body `ForceFlush` and is NOT comparable with anything here. Do not read a
  "speedup" off a new-versus-old pair.

## Sustained-arrival cases (criterion 7)

Every case admits the **same entries**: byte-identical bodies from one generator,
paced at a fixed gap through the production receiver while the production worker
drains them. The harness decorates the WAL (observation only) to see each
prepared entry and each commit request, and asserts every WAL entry equals the
generated body. The cases differ only in the sink:

| Case | Sink behavior | What it stresses |
| --- | --- | --- |
| `sustained_healthy` | 2 ms per request | Reference: completion lag, steady backlog, cadence |
| `sustained_slow_exporter` | 1.5x the interval per request | Deliveries overrun slots; collections queue and are dropped, never re-timed |
| `failing_then_recovering` | HTTP 503 for 2 s starting at the first export request, then healthy | Retained work across a failure; no catch-up samples; nothing lost after recovery |

Flow logs are off, a stable unrelated counter shares the provider, and the
default cumulative temporality and `metrics_mode: both` are used. The exporter
keeps its default retry policy (5 s initial backoff), so recovery lag includes it.
The outage window opens at the first export request, so that request is always
rejected; the run fails if the failing case rejected nothing, because it would
then have been a second healthy case.

### What fails the run

Each check is an assertion; the report is printed first, then the run fails.

| Property | Assertion |
| --- | --- |
| Bounded backlog | Peak pending WAL entries never exceed the configured WAL entry cap (`walSustainedBound`: entries arriving over 4 intervals plus twice the sink delay, plus the outage and 7.5 s of retry allowance when failing; capped at the total). Admission refused for a full WAL also fails. Peak bytes stay under the byte cap. The WAL must end at zero pending. At the defaults the failing case's cap (57 of 60) is the loosest; raise `sustained_entries` to make a cap bind harder. |
| Sample cadence | From the sink's decoded timestamps, not request counts: the stable metric and the ingress-driven metric never have two distinct samples of a series closer than 0.4 of the interval, average no more than 1.25 samples per series per interval, and the provider attempts no more scheduled collections than the clock allows (`floor(elapsed/interval)+1`), no terminal collection, and no more distinct stable samples than scheduled collections. Fewer samples than the schedule (dropped collections) is allowed and reported. The failing case must also see at least one rejected export. |
| Exported totals | Sum of the latest cumulative value per series at the sink equals the admitted bytes exactly, for the raw and the rollup family. |
| Commit safety | At every commit request the sink has already decoded, from successful exports, at least the bytes of every entry committed so far plus that one; every entry commits exactly once; the WAL ends empty after the sink recovers. |

The commit-safety check is a **necessary condition**, not a proof. Entries are the
same size and the counters additive, so k committed entries need k entries' bytes
delivered; flow series carry no per-entry identity (the attributes are
aggregated), so delivered bytes of a not-yet-committed entry could mask a single
early commit. Per-entry cover is exercised by the deterministic fixture tests, for example
`TestIngressWALCoordinator_ReplayAppliesThenCommitsOnlyAfterScheduledCover` and `TestIngressWALScheduledCadence_CommitRequiresMetricsAndLogs`, not here.

### Read the output

Each case logs a block (`BENCH` output) and reports metrics:

- `peak-pending-entries`, `peak-pending-bytes`, `steady-pending-entries`,
  `bound-entries`: WAL backlog sampled every 5 ms. Steady is the median after the
  first interval while arrivals continue.
- `ingress-samples/series/interval`, `stable-samples/interval`: distinct exported
  samples per series per scheduled interval over each series' lifetime. 1.00 is
  exactly the schedule; below 1.00 means collections were dropped or delayed by a
  slow or failing sink; it must never exceed the schedule.
- `exported-bytes` and `admitted-bytes`: equal or the run fails.
- `premature-commits`: always 0 on a passing run.
- `completion-lag-p50-s`, `completion-lag-max-s`: admission to WAL commit.
- `drain-after-last-arrival-s` and `flows/s`: end-to-end work divided by the span
  from first arrival to empty WAL. This is bounded by the arrival pace (a body per
  gap), so it is a delivery check, not a capacity figure.

### Local sample, 2026-10-09

Apple M1 Max, darwin/arm64, `just load-wal` defaults (60 bodies x 256 flows,
a body every 250 ms, 1 s interval). One run per case. Synthetic and local.

| Case | Peak / steady backlog (entries) | Configured bound | Stable samples per interval | Completion lag p50 / max | Exported vs admitted |
| --- | ---: | ---: | ---: | ---: | --- |
| healthy | 12 / 10 | 19 | 1.00 | 2.5 s / 2.9 s | 460,800 = 460,800 |
| slow exporter | 21 / 15 | 30 | 0.69 | 3.9 s / 5.2 s | 460,800 = 460,800 |
| failing then recovering | 19 / 10 | 57 | 0.87 | 2.6 s / 4.6 s | 460,800 = 460,800 |

Zero premature commits and zero terminal collections in all three. The failing
case varies with the exporter's randomized retry delay (peaks of 23 and 38
entries were seen on other runs, lag up to 9.5 s); the bound has to hold across
that spread.

## Finite backlog drain

`BenchmarkIngressWALDrain` seeds all bodies first, then replays them with no new
arrivals. It still asserts the exported byte totals per case, that the drain
completed on NORMAL scheduled collections (no terminal attempts), and that the WAL
ends at zero. It measures how fast a fixed backlog clears at the scheduled
interval, not cadence, steady state or recovery.

| Case | Change from baseline | Tradeoff |
| --- | --- | --- |
| `baseline` | `metrics_mode: both`, external addresses retained, destination port enabled, cumulative temporality, 10,000-point OTLP batches | High-cardinality reference |
| `coalesced8_experiment` | Combine eight original HEC bodies before admission | Fewer disk commits and fewer entries; not a production group-commit implementation |
| `rollup_only` | `cardinality.flow.metrics_mode: rollup` | Removes raw connection metric families; totals retained |
| `collapse_external` | `cardinality.flow.collapse_external: true` | Removes individual unresolved endpoint addresses from metric dimensions |
| `otlp_batch1000` | `otlp.metric_export_batch_size: 1000` | Smaller individual requests, more serial requests per collection |
| `delta` | `otlp.metric_temporality: delta` | Requires compatible backend ingestion and query semantics; not a transparent production switch, and not an acceptable fix for cadence |

Read `flows/s` and `drain-s/op` (replay to empty WAL), `metric-requests/op` and
`exported-points/op` (sink-observed, before shutdown), `peak-WAL-bytes/op` and
`WAL-entries/op` (backlog after admission) and `seed-s/op` (admission time).
Each operation starts fresh; `-benchtime=1x` avoids automatic workload growth.
With a 1 s interval every case drains in one to a few intervals, so the cases are
close: do not rank them from one run.

## Evidence limits

- **Synthetic, local, loopback.** Unique documentation-range IPv6 endpoints, a
  laptop filesystem and an in-process sink. CPU, disk, other local work and sink
  delay all move timings. This is not production sizing and not a cluster capacity
  guarantee.
- **Finite and short.** Sustained cases run tens of seconds, not hours. They show
  bounded backlog and exact cadence over a few dozen intervals, not long-run
  drift, leak or compaction behavior.
- **One failure shape.** A single full outage with the exporter's default retry,
  logs disabled, one provider, cumulative temporality, one run per case.
  Partial-failure splits, mixed routes with required logs, cancellation, crash
  replay, restart and leadership handover are covered by the deterministic tests
  in `internal/app/ingresswal_cadence_test.go`, not by this harness.
- **Commit safety is a necessary condition** (see above), and delivery is
  at-least-once: this never establishes exactly-once.
- **Timing floors are jitter-tolerant, not exact.** A collection delayed by load
  can shift one sample by a fraction of the interval (0.57 of it was seen after an
  outage), so the per-series gap floor is 0.4 of the interval. The average-rate and
  scheduled-collection-count checks are the tight cadence bounds. A regular extra
  collection at about half the interval would slip past the gap floor, but it
  doubles a series' rate (the slow and failing cases run at 0.7 to 0.95 of the
  schedule, so they would read about 1.4 to 1.9) and the scheduled-collection count,
  and trips those two checks.
- **Coalesced-body and delta results are directional only.** They change disk
  commit counts, rollup windows or backend semantics; neither is a substitute for
  the scheduled design, and none of the old per-flush timings below carries over.

No setting in this matrix is applied to a live deployment by this command. The
small accounting test (`TestIngressWALLoadAccounting`) and a short version of the
sustained cases (`TestIngressWALLoadSustainedAccounting`, about 8 s) run in
`just check`. The short test uses 30 bodies of 8 flows every 150 ms, a 400 ms
interval, a 700 ms slow sink, a 300 ms outage and a 100 ms exporter retry
(instead of the production 5 s). Its WAL entry caps (19, 28 and 23 of 30 for the
healthy, slow and failing cases) are strictly below the admitted count, so a
backlog that stops draining or a refused admission fails it. Every other
assertion above applies, including the rejected-export check and the 0.4 gap
floor. One margin is wider than the benchmark's: the cap allows six intervals of
lag, not four, because stressed 2-CPU `-race` runs reached 14 entries of a
16-entry healthy cap.
It is about 11 intervals long and exercises the shortened retry, not the
production one. Wall-clock performance is an explicit
experiment, not a timing threshold in CI.

## Historical measurements, 2026-09-10 (superseded)

These predate TSO-0148. The harness then forced a metric collection and flush
barrier for every accepted body, so they measure that mechanism and are **not
comparable** with anything above. Kept only to show what the earlier harness saw.

Apple M1 Max, darwin/arm64, Go 1.27.1; repository base
`a483b9289d217df767971dccf6d2bee464dbe0a3`. Harness source Git blob:
`62127d3fc35951b6dc3e2b2edb682688abc1c754`.

`just load-wal 16 256 2 2` (4,096 flows, approximately 1.23 MB initial WAL, two
independent runs per case):

| Case | Drain seconds, observed range | Metric requests | Exported datapoints |
| --- | ---: | ---: | ---: |
| baseline | 2.015 to 2.024 | 37 | 278,800 |
| coalesced8 experiment | 0.380 to 0.385 | 5 | 30,618 |
| rollup only | 1.170 to 1.240 | 23 | 139,536 |
| collapse external | 0.512 to 0.516 | 16 | 400 |
| OTLP batch 1,000 | 2.691 to 2.721 | 288 | 278,800 |
| delta | 0.728 to 0.738 | 16 | 33,025 |

`just load-wal 32 512 10 2` (16,384 flows, approximately 4.94 MB initial WAL, two
independent runs per case):

| Case | Drain seconds, observed range | Metric requests | Exported datapoints |
| --- | ---: | ---: | ---: |
| baseline | 14.84 to 15.07 | 230 | 2,138,016 |
| coalesced8 experiment | 1.958 to 2.021 | 20 | 183,924 |
| rollup only | 7.925 to 8.655 | 125 | 1,056,672 |
| collapse external | 1.570 to 1.683 | 32 | 800 |
| OTLP batch 1,000 | 39.54 to 47.32 | 2,156 | 2,138,016 |
| delta | 2.432 to 2.471 | 32 | 130,177 |

External-address collapsing was particularly effective for that fixture because
its unique external destinations created most of the cardinality. Rollup-only
does not guarantee a fixed number of cumulative series over time: different
endpoint pairs can enter successive top-N windows. Neither result is a
recommendation to remove production detail without considering its value.
