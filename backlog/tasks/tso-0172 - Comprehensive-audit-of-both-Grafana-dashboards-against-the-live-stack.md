---
id: TSO-0172
title: Comprehensive audit of both Grafana dashboards against the live stack
status: In Progress
assignee:
  - '@loop28'
created_date: '2026-10-11 10:33'
updated_date: '2026-10-11 17:15'
labels: []
dependencies: []
references:
  - deploy/grafana/gen
  - reference/grafana-delivery.md
priority: high
type: chore
ordinal: 172000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The last whole-dashboard live audit (TSO-0022) predates a long run of changes: the tab regroup (TSO-0092), new visualisation types (TSO-0150), HA coordination and ingress WAL panels, and many collector additions. TSO-0170 found the whole Node Metrics tab ignoring the tailnet and provider variables. Nothing caught it because the selector-scope test from TSO-0022 covered only the Policy and Config tabs, so similar gaps may exist on other tabs. This task audits every tab of the Tailnet and Exporter Health dashboards as rendered on the live stack. It produces a findings record and follow-up tasks. The fixes themselves belong in those follow-ups unless one is a single generated-query correction small enough to land here. Dashboards are delivered only through grafana-sync; never push them with gcx. Keep lab names, device names, addresses and raw telemetry values out of every tracked file, this task included.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every panel on every tab of both generated dashboards is inventoried with a verdict (correct, misleading, broken, redundant or missing-signal) and a one-line reason, recorded in the task notes without lab identifiers
- [ ] #2 Every metric, log and trace query is executed against the live stack over a stated window; each one either returns data or has its absence explained (feature disabled, prerequisite absent, or genuinely zero), and no panel shows a query error
- [ ] #3 Every query honours every dashboard variable that applies to it (tailnet, provider and any tab-local selector); a generated-dashboard test enforces this across all tabs, not only Policy and Config, and is seen failing on a deliberately unscoped query before it passes
- [ ] #4 Each signal the exporter emits (per internal/catalog) reaches a panel or has a documented disposition, and each panel or row whose question no operator would act on is flagged for removal or merge
- [x] #5 Every finding not fixed in this task is filed as its own Backlog task with observable acceptance criteria, and this task lists the IDs it filed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Inventory every generated panel and catalog signal; extend selector-scope proof across all tabs with a failing mutation; use root-provided live query and render evidence to assign verdicts and identify follow-ups; return staged candidate for guarded review, serialized gate and landing.

loop27: retained patch verified and applied cleanly to fresh base; finish live absence/control/render evidence, then root review, landing and exact-SHA gates.

loop28: apply verified retained candidate to fresh main worktree, update only D64 tailnet-matcher expectations, reprove selector negative mutation, complete live prerequisite/control/non-Overview evidence, then guarded review and sharded CodeRabbit, staged candidate gate, root landing and composed/CI/sync checks.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
# Sanitized exhaustive panel verdicts

Task: TSO-0172 (comprehensive audit of both Grafana dashboards against the live stack).

Active query window: 2026-10-10T16:24:57Z to 2026-10-10T22:24:57Z. Snapshot window: current now-6h (no recent exporter samples).

Verdicts are provisional where render or absence prerequisites remain outstanding. No non-Overview panel was declared visually correct from an Overview screenshot.

| Dashboard | Tab | Panel | Verdict | Reason |
|---|---|---|---|---|
| tailscale2otel-health | Overview | Exporter up | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Collectors OK | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-health | Overview | Goroutines (summary) | misleading | Current-window snapshot colors No data green; a health panel must distinguish missing telemetry from health. |
| tailscale2otel-health | Overview | GOMAXPROCS (summary) | misleading | Current-window snapshot colors No data green; a health panel must distinguish missing telemetry from health. |
| tailscale2otel-health | Overview | Build info | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Lease leadership | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Scrape success by collector (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Last scrape age (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | API requests/s by status (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Export outcome rate (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Export latency by signal (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Config valid | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Config warnings | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Uptime | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Checkpoint disk | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | Process CPU (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Overview | Checkpoint persist age | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | Scrape errors/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | Checkpoint persist errors/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | Component errors/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | Admin auth rejected/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | API 429 / retries (summary) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Overview | Leadership handovers (last 15m) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Node-metrics targets up | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Node-metrics targets total | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Node-metrics discovery OK | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Node-metrics discovered targets | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Node-metrics scrape health by target | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Node-metrics failed scrapes/s by reason | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Scrape duration p50/p95/p99 by collector | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Last scrape duration by collector | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Scrape success by collector | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Scrape duration distribution | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Last scrape age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Scrape errors/s by collector / type | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Scrape staleness | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Scrape budget headroom | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | API requests/s by status & endpoint | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | API retries/s by endpoint | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | API latency p50/p95/p99 by endpoint | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | API 429 / retries | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Provider rate-limit utilization by tailnet | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Rate-limiter wait p50/p95/p99 by endpoint | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Time waiting vs time on the wire | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Requests delayed by the rate limiter | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | API operation last-probe age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Capability state by collector | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | OAuth scope preflight by capability | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | API operation availability by state | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Capability availability history | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Pull-endpoint scrapes/s by outcome | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Pull-endpoint scrape duration p50/p95/p99 by outcome | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Pull-endpoint requests in flight | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Pull-endpoint gather errors/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Pull-endpoint auth rejections/s by reason | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Enrich cache age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | Enrich cache size | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | rDNS cache fill | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Collection | rDNS lookup hit-rate | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | rDNS upstream queries/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | rDNS cache overflows vs lookups/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Collection | Per-tailnet API errors | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Object-store age (cursor & newest object) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Object-store gaps clear | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Object listing complete | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Backlog & oldest pending object age | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Unresolved gaps & oldest gap age | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Objects & records ingested/s, bytes read vs decompressed | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Object ingestion loss (skipped / retried / limit-stopped) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Undecodable objects (broken feed) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Object-store provider requests/s & latency | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Ingress permanently rejected units lost | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Ingress WAL on-disk bytes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Ingress WAL byte capacity fill | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Ingress WAL entry capacity fill | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Subrequest attempts & failures/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Subrequest coverage (last pass) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Stream records/s by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Stream rejections & decode errors/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Webhook events by type & rejections by reason | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Receiver in-flight & latency (stream) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Receiver rejected/s (stream + webhook) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Fail-closed receiver misconfiguration | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Stream records accepted vs skipped/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Webhook request duration p50/p95/p99 | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Webhook accepted vs duplicates & schema drift/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Ingest records/s & rejections/s & bytes/s by source | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Accepted event freshness & age p95, timestamp skew/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Dedup hits & evictions/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Dedup set fill | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Youngest dedup eviction age & overlap horizon | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Processor queue fill by signal | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Processor queue drops/s by signal & reason | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Log records & bytes truncated/s by field | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Audit poll collector | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Audit events accepted (this range) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Audit log lines in Loki (this range) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Audit ingestion failures/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Tailscale deferred delay (p50/p95/p99) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Ingestion | Local processing delay (p50/p95/p99) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Ingestion | Audit schema drift | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Annotations published/s by category | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Annotations dropped/s by reason | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Annotation delivery degraded | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Export latency p50/p95/p99 by signal | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Export outcome rate | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Export failures/s by type | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Delivery | Export spans/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Export diagnostics suppressed/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Delivery | PII filter status | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Configured destinations by log type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Streams configured | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Last delivery error | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Delivery throughput by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Entries delivered/s by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Delivery requests/s by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Delivery failure ratio by type | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Delivery | Failed requests/s by type | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Delivery | Backpressure: spoofed & max-body/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Delivery | Last activity age by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Delivery errors | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Delivery | Scrape → API trace waterfall | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Span class activity | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | API p95 by endpoint (traces) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | Scrape cadence by collector (traces) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Delivery | stream.receive batch size (traces) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Runtime | Goroutines | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | GOMAXPROCS | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Process uptime | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Process CPU (user/system) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Memory breakdown | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Goroutines & stack | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | GC cycles/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | GC pause/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | GC CPU fraction | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | GC next-target vs live heap | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Heap alloc churn | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Live heap objects | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Profile upload attempts/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Profile upload failures/s by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Profile upload duration p50/p95/p99 | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Profile upload consecutive failures | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Profile upload last-success age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | CPU profile activity | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | In-use heap profile activity | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | CPU flame graph | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | In-use heap flame graph | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | TLS certificate expiry countdown | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Runtime | TLS certificate reload failures | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Runtime | TLS certificate age since issuance | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Runtime | TLS certificate reload freshness | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Runtime | Flow-store WAL journal size | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Flow-store checkpoint age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Runtime | Config advisories | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Cost & Cardinality | Metrics over cardinality cap | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Busiest metric — % of cap | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Total active series | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Metric families tracked | misleading | Description/count semantics contain a distinct-family or fixed historical-size/cap assumption; make multi-instance aggregation and configured-limit wording explicit. |
| tailscale2otel-health | Cost & Cardinality | Overflowing families | redundant | Overlapping overflowing-family inventory questions; consolidate the flag and overflow-label evidence in one diagnostic row rather than several separate rows. |
| tailscale2otel-health | Cost & Cardinality | Series limit | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Overflowing now | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Per-metric utilization (top-N) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Active series by group | misleading | Description/count semantics contain a distinct-family or fixed historical-size/cap assumption; make multi-instance aggregation and configured-limit wording explicit. |
| tailscale2otel-health | Cost & Cardinality | Metrics overflowing now | redundant | Overlapping overflowing-family inventory questions; consolidate the flag and overflow-label evidence in one diagnostic row rather than several separate rows. |
| tailscale2otel-health | Cost & Cardinality | Active series by metric family | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Active series vs cap (top $topn) | redundant | Same current utilization expression as Per-metric utilization (top-N); merge the current gauge/table choice, retain the history view. |
| tailscale2otel-health | Cost & Cardinality | Active series over time (top $topn) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Ingest vs export cost (per minute) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Forwarded metric-name drops/s by reason | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-health | Cost & Cardinality | Metric names dropped over the window | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Flow series: raw vs bounded rollup | misleading | Description/count semantics contain a distinct-family or fixed historical-size/cap assumption; make multi-instance aggregation and configured-limit wording explicit. |
| tailscale2otel-health | Cost & Cardinality | __other__ rollup share | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Flow log records dropped/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Dedup set size | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-health | Cost & Cardinality | Dedup evictions/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Devices online / total / offline | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-tailnet | Overview | Updates available | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Users | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-tailnet | Overview | Device keys ≤7d | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-tailnet | Overview | ACL change evidence age (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Flow logging | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Exporter up (summary) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Collectors OK (summary) | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-tailnet | Overview | Scrape errors/s (summary) | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-tailnet | Overview | Export failures/s | misleading | Current-window snapshot shows healthy-colored zero despite absent exporter samples; unanchored zero fallback conflates missing telemetry with healthy/empty population. |
| tailscale2otel-tailnet | Overview | Network throughput | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Audit & flow events/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Devices online over time | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Audit events/s by action | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Audit events/s by origin | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Tailnet features | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Tailnet settings | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Scrape errors/s by collector | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Overview | Last scrape age by collector | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Component errors/s (summary) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Overview | Tailnets observed | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Providers | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Tailnet scorecard | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Overview | Per-tailnet online devices over time | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Unrestricted ACL rules | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Auto-approved exit nodes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Unapproved subnet routes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Untagged devices | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Overview | Pending exit-node shares | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Overview | SSH wildcard enabled | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Online | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Total | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Offline | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Updates available | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Distinct users | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Organization tailnets | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Inventory & Hygiene | Devices by OS | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Authorized (internal) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Unauthorized (internal) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | External (shared-in) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Population split over time | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Device share invite log | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Device inventory changes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Online vs total | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Devices by OS over time | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Device online state by node | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Stale devices (>30d) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Untagged | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Ephemeral | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Outdated (≥N behind) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Exporter update available | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Latest stable | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Clients by version | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Fleet tags (rollup) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Device age (p50 / p90) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Devices by distribution | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Devices by distribution (per-device) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Devices by country | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Updates available | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Device key expiry (time until) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Last seen (time since) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Device security flags (per device) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Inventory & Hygiene | Most-behind devices (top-N) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Tailscale SSH enabled (fleet) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Key expiry disabled (fleet) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Tailscale SSH enabled (devices) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Key expiry disabled (devices) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Blocking incoming connections | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Posture & Security | Multiple simultaneous connections | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Posture & Security | Posture identity disabled | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Posture attribute keys dropped | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Keys already expired | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Median days-to-expiry | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Devices by days-to-expiry bucket | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Posture overview | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Clients by version | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Security | Compliance failures | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Connectivity & Routing | Direct-capable % | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Hard-NAT % | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Client capability support | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Direct-capable coverage (per-device) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | IPv6 support coverage | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | UDP blocked (DERP-forced) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Endpoint candidates per device | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Hard-NAT fraction | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Configured peer-relay endpoints | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Connectivity by device | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Needs relay (hard-NAT) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Exit nodes advertised | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Exit nodes enabled | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Unapproved subnet routes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Enabled subnet routes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Advertised subnet routes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Exit-node inventory | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Subnet routes — advertised vs enabled | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Subnet-route redundancy by CIDR | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | DERP latency by host / region | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Connectivity & Routing | Preferred DERP regions | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput (now) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Packets/s (now) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Flows/s (now) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Flows/s by transport | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Flows/s by traffic type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Current flow share by transport | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Current flow share by traffic type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Reporter trust & consistency | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Observed field omissions | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Rejected flow records/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Dedup counter conflicts/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Flow-view store drops/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Exit-node throughput | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Exit-node packets/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Observed bytes (flow logs) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput by direction | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput by transport | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput by traffic type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | __other__ rollup share | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput by path — ROLLUP | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput by DERP region ID — ROLLUP | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | DERP-relayed share of rollup bytes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Top $topn source nodes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Top $topn destination nodes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Top destination services | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Traffic topology - ROLLUP | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Unique dst peers per src | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Unique dst ports per src | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Throughput by direction (raw) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Packets by direction (raw) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Throughput by transport (raw) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Top $topn source nodes (raw) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Top $topn destination nodes (raw) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Top $topn destination services (raw) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Traffic topology - RAW | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Network & Flows | Top node-pair talkers (flow logs) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Network & Flows | Flow log stream | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Targets up / total | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Discovery OK | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Inbound bytes/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Outbound bytes/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Inbound packets/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Outbound packets/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Outbound dropped packets/s by node | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | % traffic via DERP relay by node | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Throughput by path | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Fleet DERP share (now) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Path mix (DERP / IPv4 / IPv6) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Active health warnings by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Traffic mix by path (direct / DERP / peer-relay) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Packets/s by path (curated) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Path mix (now) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Home DERP region (curated) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Service throughput by node | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Node Metrics | Policy drops (ACL) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Node Metrics | Error & malformed drops by reason | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Drop share by reason | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Peer-relay throughput by node | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Node Metrics | Peer-relay throughput by transport | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Node Metrics | Peer-relay packets/s by transport | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Node Metrics | Peer-relay endpoints by state | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Peer-relay endpoint share by state | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Best latency per DERP region | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Devices per DERP region | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Preferred DERP region distribution | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Advertised routes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Approved routes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Health messages | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Home DERP region | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Node Metrics | Peer relay endpoints | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Security/lifecycle changes/s | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Device churn | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Changes by actor type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Audit actions over time | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Audit events (range) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Failed changes — WARN (range) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Audit Trail | Audit events by target type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Audit action breakdown (logs) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Top $topn actors over time | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Audit Trail | Top $topn targets over time | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Audit: metric vs log | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Recent configuration changes | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Log stream — $log_event | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Audit Trail | Log volume by event | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Wildcard rules | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Unrestricted rules (grants) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Unrestricted rules (acls) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | SSH wildcard | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Auto-approvers by kind | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Posture-gated rules | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Unrestricted ACL/grant rules (log detail) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Risk & ACL | Tailnet-lock errors | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Risk & ACL | Nodes with tailnet-lock errors | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Posture & Compliance | Integrations configured | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Devices matched by integration | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Oldest sync age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Posture match rate | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Integration sync detail | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Encryption posture population | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Compliance distribution | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Posture attribute values by device | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Posture attribute expiry — time remaining | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Posture & Compliance | Expiring posture attributes (log detail) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Posture & Compliance | Client posture population | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Devices needing update | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Release track split | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Client version distribution | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Posture & Compliance | Device posture snapshot | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Device keys ≤7d | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Device keys ≤30d | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | API/auth keys ≤7d | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | API/auth keys ≤30d | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Device shares: pending vs accepted | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Exit-node-granting shares | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Multi-use shares | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Expiring device keys (log detail) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Keys | Expiring auth keys & credentials (log detail) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Keys | Key & user-invite lifecycle timeline | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Keys | Key age (p50 / p90) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Keys by owner and type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Unrestricted OAuth clients | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | OAuth client tag restrictions | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Credential privilege class (blast radius) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | OAuth client tag restriction class | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | User account age (p50 / p90) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Keys | Pending user-invite age (p50 / p90) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Keys | Pending device-invite age (p50 / p90) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Keys | Contact needs verification | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Kubernetes Audit | Kubernetes API requests/s | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Requests by verb | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Requests by namespace | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Requests by resource (top $topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Requests by user agent (top $topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Kubernetes request paths | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Sensitive reads/s by resource | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Sensitive reads by namespace | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Sensitive reads by user agent (top $topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Users reading sensitive resources ($topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Exec sessions/s by command class | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Exec sessions by namespace | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Exec sessions by session type | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Users by exec/attach/portforward sessions ($topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Mutations/s by verb | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Mutations by resource (top $topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Mutations by namespace | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Users making mutating requests ($topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | RBAC probes/s by resource | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | RBAC probes by namespace | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Users making RBAC/permission probes ($topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Sessions started/s by type | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Sessions by namespace | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Sessions by command class | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Users starting terminal sessions ($topn) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Schema drift events/s by field | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Schema drift events (range) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Kubernetes Audit | Recent Kubernetes API requests | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Recent terminal sessions | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Secret & sensitive resource reads | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Permission enumeration probes | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Kubernetes Audit | Reads of the recorder's own logs (anti-forensics) | misleading | Query selects any log-subresource read in a fixed namespace, not the recorder object identity; it can miss recorder installs elsewhere and falsely accuse unrelated pod log reads. |
| tailscale2otel-tailnet | Kubernetes Audit | Exec / attach / port-forward requests | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Access & ACL | ACL change evidence age | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | ACL size | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | ACL rules by section | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | Auto-approvers (inventory) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | Posture-gated rules (inventory) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | ACL policy validated | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | Validation issues (last check) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Access & ACL | Validation issues by kind (logs) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Access & ACL | Policy snapshots (chunked) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Access & ACL | Policy changes (unified diffs) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | DNS & Settings | MagicDNS | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Nameservers | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Search paths | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Split-DNS zones | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Override local DNS | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Exit-node resolvers | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Search domains | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Resolvers | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Tailnet settings | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Device key duration | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Tailnet features | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | External-tailnets role | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | Webhook endpoints | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | DNS & Settings | DNS configuration snapshots | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | DNS & Settings | Tailnet settings snapshots | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Credentials | Stale users (>30d) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Credentials | User invites | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Credentials | Users by role | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Users by status | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Users by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Per-user detail | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Keys by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Credential scopes (top-N) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Key scope inventory (logs) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Key expiry (time until) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | Preauthorized auth keys | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | OAuth applications | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Identity & Credentials | OAuth apps by privilege class | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Credentials | OAuth app age (p50 / p90) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Credentials | OAuth app scope & attribute counts | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Identity & Credentials | OAuth application inventory (logs) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | Services (VIP) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Backing hosts by service | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Service tags (rollup) | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Port rules per service | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | VIP service health | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Backing host identity | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | VIP service topology | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Desired-event coverage | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | Unrecognized desired events | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | Subscribers per event category | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Per-endpoint subscriptions | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | Webhook endpoint age (p50 / p90) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | Desired-event mismatches (logs) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | GeoIP database age since build | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | GeoIP database downloads | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | GeoIP lookups by database and result | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | GeoIP database reloads | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Posture integration last-sync errors | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | Integrations | Webhook configuration snapshots | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | Integrations | Posture-integration configuration snapshots | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Connectors | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Services by type | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Policies | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Identities by kind and role | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Connector health | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Connector builds | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Service health | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Service settings | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Policy settings | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Organization settings | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Plan | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Subscription limits | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |
| tailscale2otel-tailnet | PAM | Authorized sessions | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | PAM | Active sessions | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | PAM | Killed sessions | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | PAM | Session duration (p50 / p90 / p99) | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | PAM | Session events | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | PAM | PAM authorized session records | missing-signal | One or more query populations are empty; see per-query prerequisite explanation. |
| tailscale2otel-tailnet | PAM | PAM configuration snapshots | correct | Every query executes successfully and returns data in the frozen active window; rendered semantics beyond Overview remain unverified. |

Filed follow-ups (not admitted into loop26):
- TSO-0178 (Anchor fleet summary zeros to observed source presence).
- TSO-0179 (Separate absent exporter telemetry from zero health counters in summary tiles).
- TSO-0180 (Make absent runtime summary data visually distinct from healthy process telemetry).
- TSO-0181 (Merge redundant current-utilization and overflowing-family dashboard inventory).
- TSO-0182 (Count distinct metric families rather than reporter series in the family-count tile).
- TSO-0183 (Remove fixed historical cap and live-population claims from cardinality panel descriptions).
- TSO-0184 (Emit tailnet ownership on runtime traces before offering tailnet-scoped trace panels).
- TSO-0185 (Bind the anti-forensics log-read investigation to recorder identity, not a fixed namespace).
- TSO-0186 (Add retained browser evidence for every dashboard tab and selector state).
- TSO-0187 (Make automatic rule publication explicit in dashboard-only loop authority).

Root execution note: candidate-delta live queries and guarded independent review are pending. No dashboard publication or rule mutation occurred. The full task is not accepted, and provisional verdicts do not imply per-tab visual proof.

Additional filed follow-up: TSO-0188 (document and prove optional single-tailnet profile selector behavior), discovered in guarded review. The checkpoint isolation finding was repaired in implementation attempt 2 with independent rendered-panel failure/pass/mutation proof (exits 1/0/1); delta security review passed repaired code but reaffirmed publication is outside frozen authority. Exact reviewed patch SHA256 5190191b0a06461892a02a9bb71a607486b2dfba4346345354f85d2f9a4d5b07 is preserved. Baseline 815 query identities and changed-candidate 520 identities executed with no remaining backend query errors; absence/control/render evidence remains partial. CodeRabbit completed all eight candidate files, zero critical/major; follow-up-list minor corrected here. Absolute tracker references retained to respect owner path conventions.

loop26: attempts 2; review-repair rounds 1. Parked for publication authority: dashboard-only pushes trigger unconditional Grafana rule pruning/pushing, which the frozen goal forbids outside the root-only TSO-0176 (per-device posture status and its rule push) exception. Root and independent security review agree this is missing authority, not a tool limitation; no dashboard code was committed, pushed or delivered. Exact repaired patch SHA256 5190191b0a06461892a02a9bb71a607486b2dfba4346345354f85d2f9a4d5b07 retained in /Users/rob/repos/tailscale2otel/codex/loop26/dashboard-evidence/root-repaired-candidate.patch. Fresh staged verification tree f8e425a03444d2b83c1fb57631b56c556204ecae on base 2224ad2889feb252e9a114d2e4bdb90618954b53 passed exact full G exit 0 in 369.942s; repaired code passed guarded delta review and CodeRabbit reviewed all eight paths, zero major/critical. Eleven dashboard follow-ups filed individually with criteria; AC #5 checked only. AC #1-4 remain unchecked because provisional panel verdicts, absent-series prerequisites, non-All/ad-hoc/browser coverage and optional profile contract are not complete. Resume with explicit authority covering existing automation (recommended), then complete 136 absence prerequisites and 488 non-Overview panel renders/control checks, rebase retained patch onto accepted client migration, rerun changed proof at the new identity, land and verify exact ci-success/grafana-sync and far-side dashboard source.

loop27: 3 cumulative implementation attempts of owner-raised 5 (2 in loop26, 1 here). Retained candidate applies cleanly but unchanged tests test_cap_panels_use_the_exported_limit_not_a_10k_literal and test_active_series_treemap_is_bounded_and_keeps_plugin_identity require unscoped exact substrings; adding required tailnet filters produces two assertion failures among 251 tests. Per-tailnet emitter evidence rules out a global-only exemption. Explicit shared-test editing fence prevents correction. Resume when owner authorizes updating only those two expectations while preserving positive-limit and bounded-plugin contracts. Candidate remains staged/uncommitted; live absence, selected-control and render acceptance evidence remains incomplete. No source land or publication.

loop28 fresh non-Overview published-panel render: independent inspection found Devices by OS in-chart labels overlapping/clipped at 800x600, while the legend remained readable and no query/loading/no-data error was visible. Filed TSO-0189 (keep device OS pie-chart labels readable at snapshot dimensions); excluded from loop28 implementation. Representative published panel only, not candidate selector or whole-tab proof.
<!-- SECTION:NOTES:END -->
