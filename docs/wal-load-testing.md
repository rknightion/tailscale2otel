# Local WAL drain comparison

Run `just load-wal`. No Tailscale account, credentials, Kubernetes cluster or
Grafana endpoint is needed. The harness uses synthetic HEC flows, the production
stream receiver and coordinator, a temporary disk WAL, the real flow processor
and telemetry provider, and a loopback OTLP HTTP sink.

```sh
# Original bodies, flows per original body, sink delay in milliseconds, repeats
just load-wal 16 256 2 2

# Larger backlog and slower receiver; still local only
just load-wal 32 512 10 2
```

Every case consumes the same flows and asserts the byte totals actually decoded
by the OTLP sink, separately for raw and rollup counters. Cumulative exports
replace each series' previous value; delta exports accumulate increments. Raw
and rollup totals are never summed together. A run fails if the receiver refuses
a body, replay fails, exported totals differ, or the WAL does not drain to zero.
The small accounting test runs in `just check`; wall-clock performance is an
explicit experiment, not a timing threshold in CI.

## Comparisons

| Case | Change from baseline | Tradeoff |
| --- | --- | --- |
| `baseline` | `metrics_mode: both`, external addresses retained, destination port enabled, cumulative temporality, 10,000-point OTLP batches | High-cardinality reference |
| `coalesced8_experiment` | Combine eight original HEC bodies before admission | Fewer flush barriers **and disk commits**; not a production group-commit implementation |
| `rollup_only` | `cardinality.flow.metrics_mode: rollup` | Removes raw connection metric families; totals retained |
| `collapse_external` | `cardinality.flow.collapse_external: true` | Removes individual unresolved endpoint addresses from metric dimensions |
| `otlp_batch1000` | `otlp.metric_export_batch_size: 1000` | Smaller individual requests, more serial requests per collection |
| `delta` | `otlp.metric_temporality: delta` | Requires compatible backend ingestion and query semantics; not a transparent production switch |

Flow logs are disabled to isolate metric-export amplification. No collectors,
enrichment lookups, scheduler, periodic rollup worker or Kubernetes lifecycle run.
Metric periodic export is set to one hour so the WAL controls the measured flush
cadence. Endpoint uniqueness is synthetic, not a replay of private captures.

## Read the output

- `flows/s` and `drain-s/op`: replay through the final successful export barrier
  and disk commit. Admission/setup/shutdown are excluded from these two numbers.
- `metric-requests/op` and `exported-points/op`: sink-observed export amplification
  during replay, before shutdown. Datapoints are not unique series.
- `peak-WAL-bytes/op` and `WAL-entries/op`: backlog after admission. It is the peak
  for this finite test because no additional traffic arrives while draining.
- `seed-s/op`: generating and durably admitting the workload. Go's default
  `ns/op` includes the whole setup/work/cleanup, not just replay.

Each benchmark operation starts fresh. `-benchtime=1x` avoids automatic workload
growth; repeats are independent runs. Bounds are 1–256 original bodies,
1–4,096 flows per body (at most 65,536 total), and 1–100 ms sink delay. The 256 MiB WAL capacity,
receiver limits and five-minute per-case deadline remain enforced: an excessive
combination fails rather than silently reducing the workload. Temporary stores
are removed by Go's test cleanup.

## Evidence boundaries

This measures **finite backlog drain**, not steady-state sustainable ingress,
recovery after exporter failure, Kubernetes probe latency, or production capacity.
It does not establish exactly-once delivery. CPU, filesystem, other local work
and sink delay all affect timings; do not convert a laptop result into a cluster
sizing guarantee.

Coalescing uses the unchanged production coordinator and durability protocol.
It also changes commit count and the rollup top-N selection window. Consequently,
its speedup cannot be attributed solely to sharing flushes. TSO-0148 tracks real
multi-entry batching, including entry/byte/latency limits, retries, mixed routes,
crash replay and cancellation. That implementation must be compared using
**identical admitted WAL entries** before claiming its throughput improvement.

No setting in this matrix is applied to a live deployment by this command.

## Local measurements, 2026-09-10

Apple M1 Max, darwin/arm64, Go 1.27.1; repository base
`a483b9289d217df767971dccf6d2bee464dbe0a3`. Harness source Git blob:
`62127d3fc35951b6dc3e2b2edb682688abc1c754`. These runs were made after the
repository gate and review completed, not concurrently with those checks.

`just load-wal 16 256 2 2`: 4,096 flows, approximately 1.23 MB initial WAL,
two independent runs per case. Every run preserved byte totals and drained to
zero. Request and datapoint counts agreed across repeats.

| Case | Drain seconds, observed range | Metric requests | Exported datapoints |
| --- | ---: | ---: | ---: |
| baseline | 2.015 to 2.024 | 37 | 278,800 |
| coalesced8 experiment | 0.380 to 0.385 | 5 | 30,618 |
| rollup only | 1.170 to 1.240 | 23 | 139,536 |
| collapse external | 0.512 to 0.516 | 16 | 400 |
| OTLP batch 1,000 | 2.691 to 2.721 | 288 | 278,800 |
| delta | 0.728 to 0.738 | 16 | 33,025 |

`just load-wal 32 512 10 2`: 16,384 flows, approximately 4.94 MB initial WAL,
two independent runs per case. All accounting and drain assertions passed.

| Case | Drain seconds, observed range | Metric requests | Exported datapoints |
| --- | ---: | ---: | ---: |
| baseline | 14.84 to 15.07 | 230 | 2,138,016 |
| coalesced8 experiment | 1.958 to 2.021 | 20 | 183,924 |
| rollup only | 7.925 to 8.655 | 125 | 1,056,672 |
| collapse external | 1.570 to 1.683 | 32 | 800 |
| OTLP batch 1,000 | 39.54 to 47.32 | 2,156 | 2,138,016 |
| delta | 2.432 to 2.471 | 32 | 130,177 |

External-address collapsing is particularly effective for this fixture because
its unique external destinations create most of the cardinality. Rollup-only
does not guarantee a fixed number of cumulative series over time: different
endpoint pairs can enter successive top-N windows. Coalescing also changes
those windows. Neither result is a recommendation to remove production detail
without considering its value.
