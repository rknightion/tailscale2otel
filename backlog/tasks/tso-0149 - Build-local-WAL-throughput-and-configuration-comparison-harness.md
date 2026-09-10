---
id: TSO-0149
title: Build local WAL throughput and configuration comparison harness
status: Done
assignee:
  - '@codex'
created_date: '2026-09-10 21:02'
updated_date: '2026-09-10 21:18'
labels: []
dependencies: []
priority: medium
type: spike
ordinal: 150000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Provide reproducible local evidence for TSO-0148 and operator tuning after TSO-0147. Synthetic traffic and a loopback OTLP sink must exercise real disk storage, flow processing and metric exports without tailnet or Grafana access. Grouped flushing remains a test-only experiment, not a shipped durability implementation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A documented just command runs bounded synthetic WAL drain workloads with configurable workload size and sink latency
- [x] #2 Checks exported flow totals and drained WAL state so faster runs cannot silently skip work
- [x] #3 Records repeatable measurements and limitations without private data or production changes
- [x] #4 Reports wall-clock drain throughput, WAL backlog, export requests and datapoints for baseline, coalesced-body fewer-barrier experiment and optional settings
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Reuse real receiver, processor, file WAL and telemetry provider with an httptest OTLP sink. 2. Seed fixed synthetic HEC envelopes; run production per-entry replay and a test-only single-route grouped barrier experiment. 3. Compare metrics modes and OTLP datapoint batch sizes, assert sink-observed totals and pending state, report timings. 4. Document limits, run targeted and full gates and CodeRabbit, then commit evidence.

The existing Store.Replay API commits immediately after each successful handler. Do not bypass durability for a benchmark. The fewer-barrier experiment coalesces synthetic HEC bodies before receiver admission, using unchanged production replay. It therefore reduces both flush barriers and disk commit operations, and is not a production multi-entry group-commit implementation. Record that limitation explicitly.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Six accounting cases pass under the race detector. Final CodeRabbit review completed with zero findings for internal/app/ingresswal_load_test.go. Initial local runs preserve exported raw/rollup byte totals and expose cumulative export amplification; final measurements await the repository gate to avoid competing with local checks. The experiment deliberately changes admission grouping, not production replay or commit semantics.

Full just check passed on the final harness source (Git blob 62127d3fc35951b6dc3e2b2edb682688abc1c754), including all-module lint/race/vulnerability and generated-drift gates. Small accounting matrix separately passed -race. CodeRabbit completed with zero findings and reviewed the new Go file. Existing LogQL/TraceQL checks still only verify extracted variables, not syntax; no such expressions changed. Final timed runs execute after the gate and review complete.

Final independent measurements after checks: just load-wal 16 256 2 2 and just load-wal 32 512 10 2 both passed all six cases twice. Larger 16384-flow case drained in 14.84-15.07s baseline, 1.958-2.021s coalesced-body experiment, 7.925-8.655s rollup-only, 1.570-1.683s external collapsing, 39.54-47.32s with 1000-point OTLP batches, and 2.432-2.471s delta. All sink-observed byte totals matched and every WAL drained to zero. Hardware, base SHA, source blob, commands, both tables and confounders are documented in docs/wal-load-testing.md. Docs-check and just format validation passed after adding results. Generated artifact inputs did not change; no regeneration required. Production batching and live configuration remain untouched.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added just load-wal, a real-path local finite-WAL-drain harness using synthetic HEC, disk WAL, production coordinator/processor/provider and a loopback OTLP protobuf sink. Six scenarios compare the baseline, coalesced-body fewer-barrier experiment, rollup-only, external-address collapsing, 1000-point OTLP batches and delta temporality. Accounting checks verify exported raw and rollup byte totals independently and require zero pending WAL state. Small matrix passes race checks; full just check passed; CodeRabbit reviewed the new Go file with zero findings; two workload sizes with two repetitions passed. Results and limitations are documented. The experiment changes admission grouping and rollup windows, so actual group-commit implementation and identical-entry performance proof remain TSO-0148, not claimed complete here.
<!-- SECTION:FINAL_SUMMARY:END -->
