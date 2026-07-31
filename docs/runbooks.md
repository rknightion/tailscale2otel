---
title: Alert runbooks
description: What each tailscale2otel alert means, what legitimately causes it, and how to clear it
tags:
  - Alerting
  - Troubleshooting
---

# Alert runbooks

Every alert rule in
[`deploy/alerts/grafana-managed/`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/alerts/grafana-managed)
carries a `runbook_url` annotation pointing at a section of this page. The sections are per **rule
family**, not per rule — rules in a family share a cause, a first diagnostic step and a definition
of "resolved", and splitting them would produce twenty-five near-identical pages nobody maintains.

This page and the generator are kept in sync mechanically: `deploy/alerts/gen/build_rules.py`
parses the `{#anchor}` on every `##` heading below and **fails the build** if a rule links a slug
that has no section, or if a section is referenced by no rule. A dead runbook link is not possible
without a red CI run.

## How to read a section

Each family states what the alert means, what causes it *legitimately* (the reasons not to page
anyone), what causes it illegitimately, the first thing to look at, and what "resolved" looks like.
Panel names in the **First step** lines refer to the flagship dashboard
(`deploy/grafana/tailscale2otel-tailnet.json` and `-health.json`, uids
`tailscale2otel-tailnet` / `tailscale2otel-health`); most alerts also carry a
`__dashboardUid__`/`__panelId__` annotation pair so Grafana links you straight there from the
alert itself.

## Evaluation policy: what "no data" and "error" mean here

Every alert declares one of four evaluation policies, which fixes its `noDataState` and
`execErrState`:

| Policy | `noDataState` | `execErrState` | Meaning |
|---|---|---|---|
| `coverage_critical` | `Alerting` | `Alerting` | Absence **is** the fault. A query error must never read as healthy. |
| `core` | `NoData` | `Error` | The exporter always emits this while running, so absence is abnormal and surfaces as a distinct `DatasourceNoData` alert. |
| `optional` | `Ok` | `Error` | The series is legitimately absent in a healthy deployment (gated collector, optional source, a counter that has not incremented). Absence is fine; a datasource error is not. |
| `advisory` | `Ok` | `Ok` | Hygiene. Neither absence nor a transient error is actionable at this severity. |

If you see a `DatasourceError` alert instead of the rule you expected, the rule did not evaluate —
treat it as an outage of that signal, not as an all-clear.

## Who watches the watcher: datasource and ruler health

**Nothing in this project monitors Grafana's own alerting stack, and it structurally cannot.** This
deserves stating plainly rather than papering over:

- `ts2o-exporter-down` is `coverage_critical` so a *query error* pages instead of reading as
  healthy. But if the Grafana **ruler** stops evaluating altogether — the unified-alerting scheduler
  wedges, the rule group is deleted, the whole stack is down — then no rule evaluates, including
  that one. A rule cannot alert on its own non-evaluation.
- Likewise, if the Prometheus/Loki datasource these rules point at is removed or its UID changes,
  every rule moves to `DatasourceError`. The `execErrState: Error` policy makes that *visible* as an
  error state, but it is still Grafana telling you, from inside Grafana.

The only real answers are external to this repo, and are the operator's to choose:

1. **Grafana Cloud** exposes its own alerting meta-metrics to your stack —
   `grafanacloud_instance_alerts_*`, `grafanacloud_instance_rule_evaluations_*` and the
   `grafanacloud_instance_samples_per_second` ingest signals. An alert on rule evaluations dropping
   to zero, or on evaluation failures, is the standard ruler-health check. Self-hosted Grafana
   exposes the equivalent `grafana_alerting_*` / `grafana_rule_evaluation_*` series on its own
   `/metrics`.
2. **A dead-man's switch.** Add an always-firing rule routed to a contact point that expects it
   (Grafana OnCall heartbeat, Alertmanager `Watchdog`, Dead Man's Snitch, Healthchecks.io). If the
   heartbeat stops arriving, alerting itself is broken. This is the only mechanism that survives the
   whole stack going away, and it lives outside Grafana by definition.
3. **Synthetic Monitoring** against the exporter's admin `/healthz` gives an exporter-liveness signal
   that does not depend on the metrics pipeline at all.

None of these ship in this repo, because all three depend on the operator's stack, contact points
and external heartbeat provider. Do not read the absence of a ruler-health rule here as "covered".

## Exporter down {#exporter-down}

**Rules:** `ts2o-exporter-down`

**What it means.** `tailscale2otel_up_ratio` is `0`, absent, or unqueryable for 5 minutes. The
exporter emits this gauge unconditionally while running, on every provider, with no collector or
feature gate — so absence has exactly one meaning: nothing is running, or nothing it produces is
reaching the backend. Every other signal in this pack is downstream of it.

**Legitimate causes.** A deliberate restart, redeploy or scale-to-zero inside the 5-minute window.
That is the only one.

**Not legitimate.** OOM-kill (the usual cause of a clean disappearance), a crash loop, an OTLP
endpoint or credential that stopped working, a `for`-window that outlasted the deploy.

**First step.** Look at **Uptime** and **Build info** on the **Overview** tab of `tailscale2otel-health`.
If `Uptime` is resetting repeatedly, it is a crash loop — go to the process logs. If there is no series at all,
the process is gone or its exports are not landing; check the container/pod state first, then the
OTLP endpoint and credentials, then whether the backend is rejecting writes.

**Resolved when.** `tailscale2otel_up_ratio` is `1` and stays `1` across at least two evaluation
intervals — a single scrape recovering during a crash loop is not a resolution.

## Collector scrape health {#collector-scrape-health}

**Rules:** `ts2o-collector-scrape-failing`, `ts2o-collector-scrape-stale`,
`ts2o-scrape-staleness-high`, `ts2o-scrape-budget-overrun`, `ts2o-collector-scrape-error-rate`

**What it means.** One collector is not completing successful scrapes. The five rules attack it from
different angles because a wedged collector can look healthy from any single one: *failing* is the
last scrape erroring, *stale* / *staleness-high* is no scrape completing at all (the success gauge
can sit at `1` forever while nothing runs), *budget overrun* is scrapes taking longer than their
interval, so the collector can never catch up, and *scrape-error-rate* is the flapping case: a
collector that fails and recovers on alternating scrapes leaves `scrape_success` sitting at `1`
whenever the rule happens to evaluate, so `scrape-failing` never sees a sustained zero — but the
error counter keeps climbing and half the data is missing. `scrape-failing` answers "is the last
scrape broken", `scrape-error-rate` answers "how often is it breaking"; a flapping collector fires
only the second.

**Legitimate causes.** A collector whose API endpoint the tailnet plan does not include will show as
failing until you disable it. A large tailnet can legitimately overrun a short poll interval — that
is a tuning problem, not a fault.

**Not legitimate.** A collector silently dropping out while the exporter stays up. That is exactly
the silent-coverage-loss case these rules exist for, which is why they are `core`: if the per-collector
series disappears entirely you get a `DatasourceNoData` alert rather than silence.

**First step.** **Scrape success by collector** and **Last scrape age** on the **Collection** tab of
`tailscale2otel-health` identify which collector. Then **Scrape errors/s by collector / type** gives the
error class, and **Scrape budget headroom** tells you whether it is erroring or simply too slow. Cross-check
[Tailscale API health](#tailscale-api-health) — a single collector failing with a scope error is an
API permission problem, not a collector bug.

**Resolved when.** That collector's `scrape_success` is `1` and its `scrape_staleness` is back below
its poll interval. If you fixed it by disabling the collector, the series goes absent — which these
rules treat as `NoData`, so silence the rule or accept the `NoData` alert for that instance.

## Cardinality and series budget {#cardinality-and-series-budget}

**Rules:** `ts2o-metric-cardinality-capped`, `ts2o-series-budget-high`, `ts2o-export-volume-high`

**What it means.** A metric family is at or approaching its per-metric series cap
(`cardinality.metric_limit`). Past the cap, excess series are collapsed into `otel_metric_overflow`
— **silent per-series loss**: the metric still exists, the specific series you care about quietly
stops being distinguishable. `export-volume-high` is the cost-side companion.

**Legitimate causes.** A genuinely large tailnet. High flow-log cardinality driven by ephemeral
`source_port` is the classic driver and is normal traffic, not a defect.

**Not legitimate.** A newly-added label with unbounded values. If overflow appeared right after an
upgrade or a config change, suspect that first.

**First step.** **Metrics overflowing now** and **Per-metric headroom (top-N)** on the **Cost &
Cardinality** tab of `tailscale2otel-health` name the offending metric family. Decide between raising `cardinality.metric_limit` (costs ingest)
and lowering the source cardinality (flow rollups, dropping `source_port`, narrowing collectors).

**Resolved when.** `tailscale2otel_series_overflowing_ratio` is `0` and the busiest family is back
under 80% of its budget. Note these rules are `optional`: setting `metric_limit` to `0`/unlimited
suppresses the gauges entirely, so the alert going quiet may mean "no longer measured" rather than
"fixed".

## Tailscale API health {#tailscale-api-health}

**Rules:** `ts2o-api-credential-rejected`, `ts2o-api-scope-denied`, `ts2o-api-rate-limited`,
`ts2o-api-server-errors`, `ts2o-api-retries-elevated`, `ts2o-tailnet-api-errors`,
`ts2o-api-rate-limit-wait-high`

**What it means.** The exporter's calls to the Tailscale API are failing, keyed off the *classified*
availability state rather than the raw status code. `credential_rejected` (HTTP 401) is the
tailnet-wide emergency — the credential is invalid, expired or revoked, so every collector stops.
`scope_denied` (HTTP 403) is narrower: the credential works but is refused one operation, so exactly
one collector's signals go missing while everything else looks fine.

**Three distinct latency-shaped faults, easy to conflate.** `ts2o-api-rate-limit-wait-high` is the
exporter's own client-side rate limiter making requests wait before they are sent — self-throttling,
not a failure. That is a different fault from `ts2o-api-rate-limited`, which is the server telling
you with an HTTP 429 that you have exceeded its limit, and different again from
`ts2o-export-latency-high` in [OTLP export health](#otlp-export-health), which is genuine upstream
API/network slowness. All three look identical on a naive latency chart, and each has a completely
different fix: tune the client-side limiter's rate, back off the poll interval, or investigate the
network path to Tailscale's API. The `ts2o-api-rate-limit-wait-high` threshold (5s p95) is a
**placeholder** — tune it from your own observed baseline rather than treating it as authoritative.

**Legitimate causes.** A 403 is **not** always a fault, and this is the trap the rules were rebuilt
around: upstream also reports "your tailnet does not have this feature" as a 403. That case is
classified `disabled`, not `scope_denied`, and does not fire. Rate limiting on a large multi-tailnet
deployment with aggressive poll intervals is expected and is a tuning signal. Occasional 5xx from
upstream is normal internet.

**Not legitimate.** Sustained 401. API keys expire at 90 days and are user-bound — if the user who
minted the key leaves, it dies with them. That is the most common cause of a sudden tailnet-wide
outage.

**First step.** **API requests/s by status & endpoint** on the **Collection** tab of
`tailscale2otel-health` separates 401 from 403 from 5xx. For 401, rotate the OAuth client or API key.
For 403, find which endpoint is being refused and either widen the OAuth scope or disable that
collector. For 429, raise poll intervals or reduce enabled collectors. For multi-tailnet, **Per-tailnet
API errors** (same tab) isolates which tailnet's credentials are at fault without the others masking it.

**Resolved when.** The relevant `tailscale2otel_api_availability_ratio` state series is absent again
(these are `optional` precisely because absence is the healthy state) and the affected collectors
have completed a successful scrape.

## OTLP export health {#otlp-export-health}

**Rules:** `ts2o-export-latency-high`, `ts2o-export-failures`, `ts2o-otlp-export-failures`

**What it means.** Data is being collected but is not reaching the OTLP backend, or is reaching it
slowly enough to back up. Everything upstream can look perfectly healthy while this is broken.

**Two failure counters, deliberately.** `ts2o-export-failures` reads
`tailscale2otel_export_duration_seconds_count{outcome="failure"}` — the export *decorators*, one
observation per `Export()` call per signal, so it tells you which signal is failing.
`ts2o-otlp-export-failures` reads `tailscale2otel_export_failures_total`, incremented from the OTEL
SDK's **global error handler**, so it also catches errors that never came from a decorated export
call, and breaks them down by `error_type` (`timeout` vs `export`) instead of by signal. The handler
counter is the canonical one — `internal/telemetry/selfobs.go` is written against the assumption that
alerts watch it, and it deliberately excludes `ErrInstrumentName`, which is not a lost datapoint.
Neither is a superset of the other, so widening one to cover both would drop a dimension a responder
needs; if only one fires, that difference is itself the diagnosis.

**Legitimate causes.** A brief spike during a backend deploy or a network blip. Sustained high
latency on a deliberately distant/underprovisioned collector endpoint.

**Not legitimate.** A steady failure rate above zero. Exports are retried, but a persistent failure
means datapoints are being dropped, and nothing else in the pack will tell you.

**First step.** **Export failures/s by type** and **Export latency p50/p95/p99 by signal** on the
**Delivery** tab of `tailscale2otel-health`. Break down by signal — metrics and logs use different endpoints, and one
failing alone points at a per-signal endpoint path or a per-signal quota. Remember the exporter
appends `/v1/metrics` and `/v1/logs` itself: a bare gateway URL in `otlp.endpoint` 404s silently.

**Resolved when.** The failure rate is zero and p99 is back under the threshold for a full evaluation
window. `export-latency-high` is `core` — if the histogram disappears entirely, that is a `NoData`
alert and means exports stopped, not that they got fast.

## SLO burn rate {#slo-burn-rate}

**Rules:** `ts2o-slo-availability-fast-burn`, `ts2o-slo-availability-slow-burn`,
`ts2o-slo-freshness-fast-burn`, `ts2o-slo-delivery-fast-burn`

**What it means.** Three separate SLIs, each recorded on its own and never blended:
**availability** (`tailscale2otel:sli_availability:ratio`, `max(tailscale2otel_up_ratio)`) is the
exporter process running at all; **freshness** (`tailscale2otel:sli_freshness:ratio`,
`avg(tailscale2otel_scrape_success_ratio)`) is whether collection is current; **delivery**
(`tailscale2otel:sli_delivery:ratio`, the export-success ratio) is whether the OTLP backend is
accepting what the exporter sends. Targets are 99.9% for availability and 99% for freshness and
delivery. Keeping delivery separate is the reason this section exists: **a firing delivery burn is
a backend fault, not a tailnet fault.** The exporter is running, the tailnet is fine, and Grafana
Cloud (or whatever OTLP endpoint is configured) is rejecting or timing out. Do not route it to
tailnet owners — there is nothing on the tailnet side for them to fix.

**Multi-window burn rate, briefly.** Each alert only fires when BOTH a short window and a long
window breach the burn threshold at once — a single blip that clears within the short window never
lights up the long one, so it never fires alone. `ts2o-slo-availability-fast-burn` (5m + 1h at
14.4x) exhausts a 30-day error budget in about two days if sustained, and is the critical-severity
tripwire for a fast, real outage. `ts2o-slo-availability-slow-burn` (30m + 6h at 6x) is the slower,
lower-severity companion that catches a burn too gradual to trip the fast pair. The freshness and
delivery rules use the same 14.4x/5m+1h fast-burn shape against their own SLI.

**These alerts depend on recording rules that must not be paused.** All four burn-rate alerts query
the recorded `tailscale2otel:sli_*:ratio` metrics rather than raw series, and those three recording
rules ship enabled for exactly that reason. If someone pauses `ts2o-rec-sli-availability`,
`ts2o-rec-sli-freshness` or `ts2o-rec-sli-delivery` in the Grafana UI, the matching burn-rate
alerts get no series at all to evaluate — not a false green, an absent one — and under `core` policy
that reads as `NoData`, not as "healthy". Check the recording rule is still enabled before assuming
a quiet burn-rate alert means a quiet system.

**Legitimate causes.** A deliberate exporter restart or redeploy burning availability budget for a
few minutes. A backend maintenance window burning delivery budget while the exporter keeps queuing
and retrying. A single collector failing repeatedly, which drags the freshness SLI down on its own
while every other collector and the exporter process itself are fine.

**Not legitimate.** A sustained burn with no known restart, deploy, or backend maintenance in
progress. A delivery burn attributed to "the tailnet" — the delivery SLI is deliberately backend-only
and cannot be caused by tailnet state.

**First step.** All three panels are on `tailscale2otel-health`: **Exporter up** (availability) is on
the **Overview** tab; **Scrape success by collector** and **Scrape staleness** (freshness) are on the
**Collection** tab; **Export outcome rate** (delivery) is on the **Delivery** tab. Start with whichever
SLI's alert fired, and for a delivery burn check the OTLP backend's own status before touching the
exporter or tailnet configuration at all.

**Resolved when.** The fired SLI's recorded ratio is back above its target for both the short and
the long window the alert reads.

## Checkpoint health {#checkpoint-health}

**Rules:** `ts2o-checkpoint-persist-errors`, `ts2o-checkpoint-stalled`

**What it means.** The high-water-mark checkpoint is not being saved. The scrape window itself
succeeded, so no data is missing *now* — the risk is on restart, where a stale or missing checkpoint
causes the log collectors to replay a window and emit duplicates.

**Legitimate causes.** Running with `checkpoint.store: memory` on purpose (in which case these gauges
are absent and the rules never fire — they are `optional` for exactly this reason). A read-only
filesystem in a hardened container where you have accepted the replay risk.

**Not legitimate.** Persist errors on a deployment that believes it has durable checkpoints. In
Kubernetes, an `emptyDir`-backed checkpoint directory survives container restarts but **not** pod
rescheduling; if you need durability across rescheduling, `persistence.enabled=true` is the fix.

**First step.** **Checkpoint persist errors/s** and **Checkpoint persist age** on the **Overview**
tab of `tailscale2otel-health`. Then check the checkpoint path's existence, ownership (uid 65532 in the shipped
image) and writability. The app falls back to in-memory with a WARN rather than crashing, so the
startup log is where the real reason is.

**Resolved when.** `checkpoint_persist_age` is back below the poll interval and the error rate is
zero.

## Exporter internal errors {#exporter-internal-errors}

**Rules:** `ts2o-component-errors`, `ts2o-dedup-set-saturated`, `ts2o-gc-cpu-fraction-high`,
`ts2o-admin-auth-rejections-high`

**What it means.** A non-collector subsystem — receivers, the admin server, streaming
auto-configure — is logging errors, or a runtime/hygiene threshold has been crossed.

**Legitimate causes.** Read these carefully before acting; most of this family is deliberately
low-signal.

- **Dedup evictions are normal at steady state.** Dedup keys are effectively unique, so a full
  fixed-size set evicts one key per insert forever in a perfectly healthy deployment. A raw
  evictions rate `> 0` is *not* actionable, which is why that rule ships paused. The real overflow
  signal is evictions approaching the set's capacity within one poll interval.
- **GC CPU fraction is misleading on an idle process.** This exporter is near-idle, so GC can be a
  large fraction of a tiny absolute CPU number. Check absolute CPU before reacting.
- **Admin auth rejections** are expected if anything on the network probes the admin port.

**Not legitimate.** A sustained `component_errors` rate. That is a real subsystem failing.

**First step.** **Component errors/s** on the **Overview** tab of `tailscale2otel-health`, broken down
by `component`, then the process logs for that component. For the other three, confirm against the
absolute-value panel before treating the rate as a problem: **Admin auth rejected/s** is on the same
Overview tab, **Dedup set fill** is on the **Ingestion** tab, and **GC CPU fraction** is on the
**Runtime** tab.

**Resolved when.** The component error rate returns to zero. The other three are `advisory` and are
tuning signals, not incidents.

## Enrichment and discovery {#enrichment-and-discovery}

**Rules:** `ts2o-enrich-cache-stale`, `ts2o-nodemetrics-discovery-failing`,
`ts2o-rdns-cache-overflowing`, `ts2o-geoip-database-stale`

**What it means.** The IP/nodeID → name cache has not refreshed, or dynamic node-metrics target
discovery is failing. Neither stops data flowing — both degrade it silently. Stale enrichment means
flow and audit records resolve to `unknown`/`external` instead of device names; stale discovery means
the node-metrics target list is frozen at its last-known state. `ts2o-rdns-cache-overflowing`
covers a third, related cache: the reverse-DNS (PTR) cache that resolves external IPs seen in flow
logs. A non-zero overflow rate means the cache is too small for the traffic it is seeing — the fix
is raising `enrichment.reverse_dns.max_entries`, not investigating a fault.

**Legitimate causes.** Both the enrich cache and discovery rules are gated. Enrichment age is only
emitted when the **devices** collector is enabled (it is the sole refresher), and discovery only when
dynamic discovery is on. With either disabled the series is absent and the rule cannot fire — that is
why they are `optional`, and it is also the trap: turning the devices collector off does not "fix"
stale enrichment, it hides it. The rDNS cache is likewise absent unless reverse-DNS enrichment is
enabled.

**Not legitimate.** A stale cache while the devices collector is enabled and scraping. That means
devices scrapes are failing — go to [Collector scrape health](#collector-scrape-health) instead. A
sustained rDNS overflow rate that does not clear after raising `max_entries` means the working set of
distinct external IPs is larger than expected — check the flow-log volume before raising the limit
further.

### A stale GeoIP database

`ts2o-geoip-database-stale` is the fourth signal in this family and the one whose failure mode is
easiest to miss. It measures `time() - tailscale_geoip_database_build_time_seconds`, i.e. how old
**MaxMind's build** is — not how recently anything was downloaded. That distinction is the whole
point: an updater that runs on schedule and fails every fetch keeps its timer green, keeps logging
activity, and looks perfectly healthy to any "did we sync recently" check. Only the build date
exposes it. Enrichment does not fail meanwhile; flow records still get country and ASN attributes,
just increasingly answered from allocations that have since moved.

Triage in this order:

1. **`sum by (result) (rate(tailscale_geoip_downloads_total[1h]))`.** A run of `failure` is expired
   or revoked MaxMind credentials, or blocked egress to `download.maxmind.com` — the WARN log names
   the HTTP status. All `unmodified` means the endpoint genuinely has nothing newer, which for
   GeoLite2 (rebuilt twice a week) is only plausible for a few days.
2. **No `downloads` series at all** means `enrichment.geoip.download.enabled` is off, so something
   external supplies the files. Check that whatever writes them still runs, and that the file's mtime
   actually changed — the process reloads on `(mtime, size)`, so a rewrite that preserves both is
   invisible.
3. **`enrichment.geoip.reload_interval: 0`** disables reloading entirely, so a database refreshed on
   disk is never picked up until a restart. The status page shows the loaded build time, which will
   disagree with the file on disk in that case.

Reloading is failure-tolerant by design: a database that cannot be read leaves the previously loaded
one serving, and `tailscale_geoip_reloads_total{result="failure"}` plus a WARN naming the file is the
only symptom. That is deliberate — degraded enrichment must never become a degraded exporter — but it
does mean a broken file can sit there indefinitely while everything looks fine except this rule.

**First step.** **Enrich cache age** and **Enrich cache size** on the **Collection** tab of
`tailscale2otel-health`. If the age is climbing, check the devices collector's scrape success. For
discovery, **Node-metrics discovery OK** and **Node-metrics discovered targets** on the same
Collection tab. For the rDNS cache, **rDNS cache overflows vs lookups/s** (also Collection) shows
whether overflow is tracking lookup volume or a step change.

**Resolved when.** Cache age drops back to roughly the devices poll interval, flow/audit panels show
device names rather than `unknown`, and the rDNS overflow rate returns to zero.

## Exporter config health {#exporter-config-health}

**Rules:** `ts2o-config-warnings`, `ts2o-config-invalid`, `ts2o-exporter-update-available`

**What it means.** The loaded config produced advisory warnings, failed validation at runtime, or a
newer release exists.

**Legitimate causes.** Warnings are advisory by design and several are permanent choices: API-key
auth instead of OAuth, a Pyroscope target without a `profiles:write` token, poll+stream overlap you
have decided to accept. A warning you have read and accepted is fine — but leave the rule enabled so
a *new* warning after a config change is visible.

**Not legitimate.** `config_valid_ratio < 1` at runtime. Validation normally fails at startup, so
seeing an invalid config in a running process is rare and serious.

**First step.** **Config warnings** and **Config valid** on the **Overview** tab of
`tailscale2otel-health`, then the startup
logs — the `Warnings()` output names each warning explicitly. `docs/configuration.md` is the
key-by-key reference.

**Resolved when.** `config_warnings_ratio` is `0` (or the remaining warnings are ones you have
accepted) and `config_valid_ratio` is `1`. These two are `core`: if they disappear the exporter is
gone, which is a `NoData` alert, not silence.

## Credential expiry {#credential-expiry}

**Rules:** `ts2o-device-key-expiring-critical`, `ts2o-auth-key-expiring-critical`,
`ts2o-device-keys-expiring-7d`, `ts2o-auth-keys-expiring-7d`,
`ts2o-device-attribute-expiring-14d`, `ts2o-device-key-expiry-disabled-new`

**What it means.** A node key, auth/API key or posture attribute is about to expire. Tailscale node
keys do **not** silently auto-renew: at expiry the device drops off the tailnet until someone
completes a re-auth.

**Legitimate causes.** Untagged, user-owned devices expiring is routine — the Tailscale client warns
the signed-in user, Tailscale emails them, and re-auth is a self-service browser click that recurs
every key lifetime. That is why the critical 48-hour device tier is restricted to **tagged** devices
and untagged ones only reach the 7-day warning tier. Tagged devices are typically headless: nobody
sees the prompt, so expiry becomes an outage. Note Tailscale disables key expiry on tagged devices by
default, so a tagged device with a live expiry has had it explicitly re-enabled — that is the
high-signal case.

`ts2o-device-key-expiry-disabled-new` is a different question from the rest of this family: it
alerts on the **delta**, not the level. A standing population of never-expiring keys is normal on
most tailnets — tag-owned servers routinely have key expiry disabled on purpose, and alerting on
their mere existence would be noise nobody acts on. What is actionable is a **new** device joining
that population: that is either a deliberate new headless deployment (fine) or a device that should
have had key expiry left on and did not (a mistake worth catching while it is recent).

**Not legitimate.** An auth/API key expiring under automation. API keys are capped at 90 days and are
**user-bound**: they die when the user who created them is offboarded, taking the exporter with them.
Prefer OAuth clients, which auto-refresh.

**First step.** On `tailscale2otel-tailnet`: **Device key expiry (time until)** is on the **Fleet &
Network > Devices > Inventory & Hygiene** tab, and **Key expiry (time until)** is on the **Security &
Policy > Policy & Config > Identity & Credentials** tab. Together they list the specific devices and
keys with their remaining time. For posture attributes,
note an expired attribute *silently* breaks posture-based ACLs — there is no error, the grant just
stops matching.

**Resolved when.** The key is rotated or the device re-authed. All of these are computed from
`expiry - now` gauges, not from cumulative histogram buckets, so they clear on their own once the
credential is renewed — no manual reset, and an already-expired-and-abandoned key does not alert
forever (the rules exclude negative remaining time).

## Device posture coverage {#device-posture-coverage}

**Rules:** `ts2o-posture-autoupdate-low`, `ts2o-posture-encryption-low`, `ts2o-posture-match-low`,
`ts2o-device-multiple-connections`

**What it means.** A fleet-wide posture property has fallen below its coverage threshold: fewer than
80% of devices report client auto-update enabled, or an encrypted local state store, or an MDM/EDR
integration is matching fewer than 80% of the devices it could. `ts2o-device-multiple-connections`
is a per-device fact rather than a fleet coverage ratio: the flag means more than one client has
connected simultaneously using the same node key — that is, a key is being shared across machines
rather than one key per device.

**Legitimate causes.** Platforms differ. Client auto-update is not available on every OS, and state
encryption depends on the platform keystore, so a mixed fleet has a structural ceiling below 100% —
tune the threshold to *your* fleet rather than chasing 100%. A low integration match rate right after
onboarding a new MDM is expected while enrolment catches up.

**Not legitimate.** A coverage ratio that *drops*. A sustained fall means devices are dropping out of
management, and for the match-rate rule specifically it means devices may be bypassing posture gates
entirely. A device flagged with multiple simultaneous connections: a shared node key means Tailscale
cannot tell the two clients apart, which undermines per-device posture and audit attribution for
both.

**First step.** On `tailscale2otel-tailnet`'s **Security & Policy > Security & Audit > Posture &
Compliance** tab: **Auto-update coverage**, **State-encryption coverage** and **Posture match rate**,
plus **Device posture snapshot**, which lists which devices are missing the property. Note the whole
family is gated on `collect_posture`; with posture collection off the series are absent and nothing
fires. For the shared-key case, **Multiple simultaneous connections** on the **Fleet & Network >
Devices > Posture & Security** tab identifies the affected `host_id`; re-key one of the clients so
each machine has its own node key.

**Resolved when.** The ratio is back above threshold, or you have concluded the threshold was wrong
for this fleet and adjusted it in the generator. For the shared-key rule: the device no longer shows
more than one simultaneous connection on the same key.

## Fleet version hygiene {#fleet-version-hygiene}

**Rules:** `ts2o-devices-needing-update`, `ts2o-device-version-skew-high`, `ts2o-devices-outdated`

**What it means.** Devices are running Tailscale clients behind the fleet's latest version.

**Legitimate causes.** Almost all of them. Pinned versions on appliances, devices that have been off
for a while, a staged rollout in progress, and platforms whose app-store updates lag. This family is
`advisory` — fail-open on both no-data and error — because it is drift reporting, not an incident.

**Not legitimate.** Nothing here is an incident. Treat a persistent high skew as a fleet-management
backlog item, not a page.

**First step.** **Most-behind devices (top-N)** and **Outdated (≥N behind)** on the **Fleet & Network >
Devices > Inventory & Hygiene** tab name the laggards. If you want auto-update coverage rather than a snapshot of drift, that lives in
[Device posture coverage](#device-posture-coverage).

**Resolved when.** The count falls below your threshold. Consider raising the threshold instead of
chasing it if your fleet has a permanent tail of pinned devices.

## Tailnet lock {#tailnet-lock}

**Rules:** `ts2o-tailnet-lock-errors`, `ts2o-tailnet-lock-disabled`

**What it means.** Tailnet lock enforces that node keys are signed by trusted signing nodes. An
*error* means one or more devices have a non-empty tailnet-lock error — usually an unsigned node that
cannot participate until a signing node signs its key. *Disabled* means an audit event turned the
whole mechanism off, weakening the tailnet's trust model.

**Legitimate causes.** A newly joined node before a signing node has signed it will show an error
briefly. A deliberate, authorized decision to disable tailnet lock — but that should be a change you
recognise, not one you discover here.

**Not legitimate.** A persistent unsigned node (it is effectively cut off), and any disable event
nobody can account for.

**First step.** **Nodes with tailnet-lock errors** on the **Security & Policy > Security & Audit >
Risk & ACL** tab names the devices; sign them from a signing node. For a disable event,
**Security/lifecycle changes/s** on the **Security & Policy > Security & Audit > Audit Trail** tab and
the audit trail show the actor. Both rules are `optional` because tailnet lock is off by default on
most tailnets, so the
series are absent unless it is in use.

**Resolved when.** The error gauge returns to `0`, or — for a disable — the change is confirmed
authorized, or tailnet lock is re-enabled and nodes re-signed.

## ACL policy hygiene {#acl-policy-hygiene}

**Rules:** `ts2o-acl-unrestricted`, `ts2o-acl-autoapprove-exit`, `ts2o-acl-changed`

**What it means.** The tailnet policy file contains wide-open grants (`*` to `*`), auto-approves exit
nodes without manual review, or has simply been modified.

**Legitimate causes.** A small single-owner tailnet may run a deliberately permissive default policy;
that is a choice, not a defect, and the rule is there so the choice stays visible. Auto-approving exit
nodes is a reasonable convenience in a tagged, controlled fleet — confirm it is intended once and move
on. `acl-changed` is pure change tracking and is `advisory`.

**Not legitimate.** An unrestricted rule appearing in a tailnet that had none. The `acl-changed`
signal is what tells you when, and the audit trail tells you who.

**First step.** **Unrestricted ACL rules** is on `tailscale2otel-tailnet`'s **Overview** tab;
**Auto-approvers by kind** is on the **Security & Policy > Security & Audit > Risk & ACL** tab. Then
**ACL last changed** on the **Security & Policy > Policy & Config > Access & ACL** tab to correlate
against a change. Pair with
[Audit events](#audit-events) to attribute it.

**Resolved when.** The policy is tightened, or the finding is explicitly accepted and the rule paused
for this tailnet.

## Credential scope hygiene {#credential-scope-hygiene}

**Rules:** `ts2o-key-broad-scope`, `ts2o-key-unrestricted-tags`

**What it means.** A credential holds the `all` scope — unrestricted read **and** write across the
entire tailnet, including APIs Tailscale has not shipped yet — or an OAuth client carries no
top-level tag restriction, so auth keys it mints are not confined to a tag it owns.

**Legitimate causes.** A genuine administrative automation credential may need broad scope. The point
of the rule is that it is a conscious, reviewed decision rather than a default nobody noticed. Both
rules are `advisory` and ship paused.

**Not legitimate.** A read-only integration holding `all`. `all:read` is the least-privilege
equivalent and is what a monitoring integration — including this exporter — should use.

**First step.** **Credential scopes (top-N)** on the **Security & Policy > Policy & Config > Identity &
Credentials** tab lists every credential and its scope class. Note the historical trap here: an earlier version of this rule counted scopes, which inverted
the answer — a single `all` scored `1` and never fired while eleven narrow `*:read` scopes scored
`11` and did. The current rule keys off the privilege **class**, not the count.

**Resolved when.** The credential is re-scoped, or its breadth is documented and accepted.

## Audit events {#audit-events}

**Rules:** `ts2o-audit-config-change-warn`, `ts2o-secret-scanner-fired`,
`ts2o-user-role-escalation`, `ts2o-audit-schema-drift`

**What it means.** Something happened in the tailnet's configuration-audit stream. `secret-scanner`
is the sharp one: Tailscale's scanner acted on a **leaked credential** it found in public — usually
by revoking it. `user-role-escalation` is a privilege change (member → admin/owner).
`audit-config-change-warn` is a change that carried an error. `audit-schema-drift` means the audit
stream contains enum values this collector version does not classify.

**Legitimate causes.** Role changes during onboarding/offboarding are routine — the rule exists so
they are *reviewed*, not so they are prevented. Schema drift is expected after Tailscale ships a new
audit field, and is a signal to refresh the vendored API contract, not an incident: metrics stay
bounded and raw values never reach labels.

**Not legitimate.** A secret-scanner event, ever. Treat it as a live credential leak: find where the
credential was exposed, confirm the revocation, and rotate anything derived from it.

**First step.** **Changes by actor type** and **Top $topn actors over time** on the **Security &
Policy > Security & Audit > Audit Trail** tab attribute the change. For schema drift, the collector
emits a once-per-value digest warning naming
the unclassified field — that log line is what you feed into the contract refresh.

**Resolved when.** The change is confirmed authorized (or reverted), the leaked credential is rotated,
or the vendored spec is refreshed for a drift finding.

## Tailnet settings drift {#tailnet-settings-drift}

**Rules:** `ts2o-flow-logging-disabled`, `ts2o-device-approval-disabled`,
`ts2o-logstream-config-changed`, `ts2o-contact-unverified`

**What it means.** A tailnet-level setting is in a state that weakens forensics, admission control or
security notification: network flow logging off, device approval off, a SIEM log-streaming endpoint
added/changed/removed, or a tailnet contact left unverified.

**Legitimate causes.** Most of these are defaults, and the rules ship paused for that reason.

- Flow logging is a **paid** feature; many tailnets legitimately run without it.
- Device approval is **off by default** in Tailscale and many tailnets intentionally run that way.
- A log-streaming change may be a planned SIEM migration.

Enable each rule only where the tailnet's policy says that setting must be on. The rules are
`optional`, so with the relevant collector disabled the series is absent and nothing fires — again,
absence is not proof of compliance.

**Not legitimate.** A log-streaming endpoint being **removed** or disabled is a forensics and
compliance gap. The rule is audit-driven so it fires on the change itself, catching a disable even if
it is quickly reverted. An unverified contact means Tailscale's security notifications may never
reach anyone.

**First step.** **Flow logging** and **Tailnet scorecard** on `tailscale2otel-tailnet`'s **Overview**
tab, and **Contact needs verification** on its **Security & Policy > Security & Audit > Identity &
Keys** tab, show current state. **Streams configured** moved to the **Delivery** tab of
`tailscale2otel-health`. **Recent configuration changes** on the tailnet dashboard's **Security &
Policy > Security & Audit > Audit Trail** tab shows what moved.

**Resolved when.** The setting is restored, or the deviation is a documented decision for this
tailnet and the rule is paused accordingly.

## Device sharing {#device-sharing}

**Rules:** `ts2o-device-share-exit-node`

**What it means.** An outstanding device invite or share allows the recipient to use the device as an
exit node — that is, to route their traffic through your network.

**Legitimate causes.** Deliberately sharing an exit node with a contractor, a partner tailnet, or your
own second tailnet. Perfectly normal if intended.

**Not legitimate.** A share created with exit-node permission by accident. The permission is a
checkbox at invite time and is easy to leave on.

**First step.** **Exit-node-granting shares** on the **Security & Policy > Security & Audit > Identity
& Keys** tab lists the outstanding invites. Check
each against who it was meant for and whether routing their traffic is intended.

**Resolved when.** The invite is revoked or reissued without exit-node permission, or it is confirmed
intentional.

## Device and user approval {#device-and-user-approval}

**Rules:** `ts2o-devices-unauthorized`, `ts2o-user-invites-stale`

**What it means.** `ts2o-devices-unauthorized` fires on a sustained count of devices joined to the
tailnet but not yet authorized — it consumes the `tailscale:devices_unauthorized:count` recording
rule, which sums `tailscale_devices_count_ratio` filtered on `tailscale_authorized="false"` **and**
`tailscale_external="false"`. That second filter is deliberate: **an external, shared-in device from
another tailnet is not an unauthorized device of yours to approve.** It belongs to someone else's
admin console. Counting it here would produce an alert nobody on this tailnet can action.
`ts2o-user-invites-stale` fires on the p90 age of pending user invites crossing 7 days.

**Why the 2-hour `for` on the unauthorized-devices rule.** Every device that joins goes through a
short window — seconds to minutes — before an admin (or auto-approval) authorizes it. Firing
immediately would page on every normal join. The 2-hour window reports a device that has been
*waiting*, not one that merely appeared.

**Why 7 days on invite age, not sooner.** It is a "nobody is going to accept this" horizon, not an
SLA — most invites are accepted within hours or days. A pending invite still open after a week is
better treated as an access-review and offboarding question (an invite sent to someone who has since
left, or who never needed access) than as something to chase; usually the right action is to revoke
it, not to remind the invitee.

**Legitimate causes.** Device approval genuinely requiring a human, on a tailnet that has device
approval enabled deliberately (`devices.approve`). Invites outstanding to contractors or infrequent
users who have not yet logged in.

**Not legitimate.** A large or growing count of internal devices sitting unauthorized with no admin
action pending. An invite aged well past a week with no plan to revoke or re-send it.

**First step.** **Unauthorized (internal)** on the **Fleet & Network > Devices > Inventory & Hygiene**
tab for the devices alert; **Pending user-invite age (p50 / p90)** on the **Security & Policy >
Security & Audit > Identity & Keys** tab for the invites alert. Neither rule is page-tier by design — both ship
`warning` severity with `page=false`, since neither represents an active outage.

**Resolved when.** The unauthorized-devices count returns to zero (or every remaining device has a
tracked approval in progress). The pending-invite p90 age drops back under 7 days, or the stale
invites are revoked.

## Posture integrations {#posture-integrations}

**Rules:** `ts2o-posture-integration-stale`, `ts2o-posture-integration-error`

**What it means.** A device-posture (MDM/EDR) integration is not working. These two rules are
deliberately not redundant, and the distinction matters: Tailscale updates `last_sync` on every sync
**attempt**, including failed ones. A persistently-failing-but-still-retrying integration — revoked
credentials, an expired OAuth grant — keeps `last_sync` fresh forever, so the staleness rule
structurally cannot see it. The error rule reads `status.error` and catches exactly that case.

**Legitimate causes.** Both series are absent until an integration exists, so a tailnet with no
MDM/EDR integration never fires either rule (they are `optional`). A brief error during credential
rotation on the MDM side.

**Not legitimate.** Sustained `status.error`. Posture data feeding your ACLs is stale, which means
posture gates are being evaluated against out-of-date facts.

**First step.** **Oldest sync age** and **Integration sync detail** on the **Security & Policy >
Security & Audit > Posture & Compliance** tab identify the provider and integration. Then
re-authorize it in the Tailscale admin console — revoked or expired
credentials are the usual cause.

**Resolved when.** `posture_integration_error_ratio` is `0` and the sync age is back inside the
integration's normal cadence.

## Log streaming {#log-streaming}

**Rules:** `ts2o-logstream-delivery-failing`, `ts2o-logstream-stalled`,
`ts2o-logstream-backpressure`, `ts2o-logstream-spoofed`

**What it means.** Tailscale's own log streaming to your SIEM sink is unhealthy: delivery requests are
failing, no delivery activity has happened for over an hour while a stream is configured, requests are
hitting the maximum body size, or entries are being rejected as spoofed.

**Legitimate causes.** All four series are absent until a log stream is configured, so a tailnet not
using log streaming never fires them. Backpressure (max-body) is `advisory` — it means the SIEM is
slow to accept, not that data is lost. A quiet tailnet can legitimately have low delivery volume, but
*zero activity for an hour* while a stream exists is not the same as quiet.

**Not legitimate.** Sustained delivery failures or a stalled stream: this is a compliance and
forensics gap, and it is entirely upstream of this exporter — Tailscale is failing to deliver to your
sink. Spoofed entries mean something is sending forged log traffic at your streaming endpoint;
investigate the source.

**First step.** **Failed requests/s by type**, **Last activity age by type** and **Last delivery
error**, all on the **Delivery** tab of `tailscale2otel-health`. The last delivery error is usually explicit about whether it is the
endpoint, the credential or the sink rejecting the payload. Pair with `ts2o-logstream-config-changed`
in [Tailnet settings drift](#tailnet-settings-drift) — a stream that stopped delivering right after a
config change was probably reconfigured.

**Resolved when.** The failure rate is zero and delivery activity resumes inside the stream's normal
cadence.

## Ingest receivers {#ingest-receivers}

**Rules:** `ts2o-receiver-rejections`, `ts2o-receiver-latency-high`, `ts2o-ingest-data-stale`,
`ts2o-stream-records-skipped`, `ts2o-webhook-schema-drift`

**What it means.** The exporter's own inbound paths — the Splunk-HEC stream receiver and the
HMAC-verified webhook receiver — are rejecting events, responding slowly, accepting nothing recent,
skipping records it cannot classify, or seeing a payload field drift out from under its schema.

`ts2o-stream-records-skipped` counts stream records skipped by reason. The two documented reasons
are `unclassified` (the record matched neither the flow-log nor the audit-log shape) and
`unwrap_drop` (a non-object value was dropped while unwrapping the HEC envelope). Either means
records are being silently discarded rather than processed.

`ts2o-webhook-schema-drift` is the quiet one. A field moving to `unknown` status means Tailscale
changed the webhook payload shape — the receiver keeps accepting events, nothing else in the pack
goes red, and whatever downstream signal that field used to feed is quietly no longer populated.
That silence is the entire reason this rule exists: without it, a schema change is invisible until
someone notices a panel has gone empty.

**Legitimate causes.** Both receivers are **off by default**, so all five series are absent in a
poll-only deployment (`optional`). `ingest-data-stale` in particular ships paused because quiet
tailnets and sparse webhook/audit sources are legitimately idle for hours — enable it only for
`source`/`signal` pairs you expect to deliver continuously, with label filters or a tuned threshold
for that workload. Firing it on everything produces noise, not coverage. A brief burst of
`unclassified` skips right after Tailscale ships a payload change is expected until the collector is
updated.

**Not legitimate.** A rejection rate above zero on a configured receiver. Rejections mean spoofed,
oversized or undecodable events — either the sender is misconfigured or the HEC/webhook secret does
not match. A sustained skip rate for either reason, or a webhook field parked at `unknown` for more
than a brief window: something now depends on stale or missing data and nobody has noticed.

**First step.** All on the **Ingestion** tab of `tailscale2otel-health`: **Receiver rejected/s (stream
+ webhook)** (broken down by reason), **Receiver in-flight & latency (stream)** and **Accepted event
freshness & age p95, timestamp skew/s**. The rejection reason distinguishes an auth mismatch from a
decode failure. **Stream records accepted vs skipped/s** shows which reason is climbing; **Webhook
accepted vs duplicates & schema drift/s** names when a field drifted — cross-check the vendored
OpenAPI spec (`spec/tailscale-api.json`) for whether it should be refreshed. Also confirm you have not
enabled both poll and stream for the same log type — that double-counts, and cross-source dedup is
only a best-effort failsafe.

**Resolved when.** Rejections return to zero, the newest accepted event timestamp is inside the
expected window for that source, the skip rate for both reasons returns to zero, and no field remains
at `unknown` status.

## Object-store ingestion {#object-store-ingestion}

**Rules:** `ts2o-objectstore-undecodable`, `ts2o-objectstore-gap-aging`,
`ts2o-objectstore-export-stale`, `ts2o-objectstore-backlog-stuck`

**What it means.** The object-store path ingests Tailscale's S3/bucket log export — a third
ingestion path alongside poll and stream. It is **off by default**, so all four series are absent
in a normal deployment and all four rules are `optional`.

`ts2o-objectstore-undecodable` is the important one, and the only one of the four that ships
enabled. It counts whole objects that decoded **zero** records while at least one row failed. That
combination is the signature of an export whose framing is not newline-delimited records — a
non-zero value means a broken **feed**, not a batch of corrupt data, and should be treated as a
feed-level fault rather than a few bad rows. There is a reading subtlety worth stating plainly: for
a wholly-failed object, the row-local `decode_error`/`semantic_invalid` reasons are deliberately
**not** emitted, so the case shows up as one `undecodable_object`, not as N `decode_error` counts. A
reader expecting a proportional row count will wrongly conclude the problem is small.

`ts2o-objectstore-gap-aging` reads `gap.oldest.age`, which ages **failed** objects awaiting retry.
Distinguish it from `pending.oldest.age`, which ages objects merely **not yet ingested** — the two
are not the same signal. A gap aging while the gap count holds steady means the same object is
failing repeatedly, not that ingestion is slowly catching up. The 24-hour threshold is about
permanent loss: once an object ages out of the bucket's own retention window it can never be
recovered, so tune the threshold to that retention.

`ts2o-objectstore-export-stale` reads `discovered.newest.age`, which is how fresh the export's own
writes are, independent of whether anything downstream was ingested. `-1` is a **no-discovery
sentinel, not an age** — the rule folds it to a full day of apparent staleness precisely so "nothing
was listed at all" is not silently excluded. This is the single easiest thing to get wrong when
reading the panel: a `-1` is not a healthy low number.

`ts2o-objectstore-backlog-stuck` fires when the backlog never reaches zero across a whole hour
(`min_over_time`), which is what distinguishes a genuinely stuck backlog from the normal
fill-and-drain of a busy bucket. Both the backlog and the pending object age are **lower bounds**
while `objectstore.scan.truncated` is `1`, because the listing itself was cut short — treat the
numbers as a floor, not a precise count, whenever that flag is set.

**Legitimate causes.** The path being unconfigured (absence, not a fault). A large initial backfill
that drains over several hours. A per-cycle object budget (`per_cycle_budget` skip reason)
deliberately holding ingestion behind what the bucket has available.

**Not legitimate.** Any non-zero `undecodable_object` count — treat it as a broken feed. A gap
aging without the gap count dropping. A `-1`/stale `discovered.newest.age` while the export is
believed to be running. A backlog that never drains across an hour with no budget skip in play.

**First step.** The **Ingestion** tab of `tailscale2otel-health` carries the panels for this family:
**Undecodable objects (broken feed)**, **Unresolved gaps & oldest gap age**, **Object-store age
(cursor & newest object)** (cursor age and newest-object age are now one combined panel),
**Backlog & oldest pending object age**, **Object ingestion loss (skipped / retried / limit-stopped)**,
and **Object listing complete**. Start with whichever rule fired, then check **Object ingestion loss
(skipped / retried / limit-stopped)** for a `per_cycle_budget` skip before assuming a fault, and
**Object listing complete** to see whether truncation is why backlog/gap numbers look wrong.

**Resolved when.** `ts2o-objectstore-undecodable`'s count returns to zero for a full evaluation
window. For the other three: the gap's oldest age drops back under threshold *and* the gap count is
falling, the export's newest-discovered age is back under an hour (and is not `-1`), and the backlog
reaches zero at least once within the hour.

## DERP and connectivity {#derp-and-connectivity}

**Rules:** `ts2o-high-derp-relay-usage`, `ts2o-derp-region-latency-high`, `ts2o-hard-nat-high`,
`ts2o-node-dropped-packets`

**What it means.** Traffic is being relayed through Tailscale's DERP servers instead of going
peer-to-peer, latency to a DERP region is poor, or nodes are behind hard NAT / dropping outbound
packets. Relayed traffic works — it is just slower and adds a dependency on DERP capacity.

**Legitimate causes.** Some networks simply cannot do NAT traversal: symmetric/hard NAT, restrictive
corporate firewalls, CGNAT. A fleet of mobile devices on carrier networks has a permanently high hard-NAT
fraction, and that is the network's fault, not a regression. Geographic distance to the nearest DERP
region legitimately exceeds 150 ms in some places.

**Not legitimate.** A *rise* in relay share on a fleet that previously went direct. That points at a
firewall change (UDP 41641 blocked, or outbound UDP restricted) rather than at Tailscale.

**First step.** On `tailscale2otel-tailnet`'s **Fleet & Network > Node Metrics** tab: **Fleet DERP
share (now)** and **Traffic mix by path (direct / DERP / peer-relay)**, then **Best latency per DERP
region**. **Hard-NAT %** is on the **Fleet & Network > Devices > Connectivity & Routing** tab. All of
these need the node-metrics scraper — with it disabled the series are absent and nothing fires.

**Resolved when.** The relay share returns to its baseline. Note the baseline is fleet-specific: pick
a threshold from your own history rather than assuming 50% is meaningful for you.

## Flow data pipeline {#flow-data-pipeline}

**Rules:** `ts2o-no-flow-data`, `ts2o-flow-reporter-mismatch`, `ts2o-flow-logs-dropped`

**What it means.** No network flow records have arrived for an hour while flow logging is on, flow
records are arriving where the Tailscale-**verified** reporter node ID disagrees with the unverified
embedded source reference, or the exporter's own per-window volume guard is **truncating** flow log
records.

**Dropped records are a local cap, not an upstream fault.** `flow-logs-dropped` counts records
suppressed by `collectors.flowlogs.max_log_records_per_window`. Only *log records* are ever dropped —
flow **metrics** are never capped, so the throughput panels stay complete and correct while the log
stream silently loses records. That asymmetry is the reason this rule exists at all: nothing else in
the pack makes the truncation visible, and the guard is doing exactly what it was configured to do,
so there is no error anywhere to find.

**Legitimate causes.** A genuinely idle tailnet produces no flows — which is why `no-flow-data` is
info-tier, paused and `advisory`. Reporter/source disagreement can be a benign artefact of how a
particular relay path attributes a record; the rule ships paused so you enable it only where agreement
is actually expected. Dropping is legitimate on a deliberately tight cap on a busy tailnet — but it
should be a decision you made, not a surprise.

**Not legitimate.** Zero flows on a tailnet you know is busy. That means the flow pipeline is stalled
— check whether flow logging is still enabled upstream (see
[Tailnet settings drift](#tailnet-settings-drift)) before suspecting the exporter. Sustained dropping
you did not intend is likewise not legitimate: your flow log search results are incomplete and
nothing in the query says so.

**First step.** **Flows/s (now)**, **Flow log stream** and **Reporter trust & consistency** (mismatch
case) are on `tailscale2otel-tailnet`'s **Fleet & Network > Network & Flows** tab. **Flow log records
dropped/s** (truncation case) moved to the **Cost & Cardinality** tab of `tailscale2otel-health`.
Confirm
the ingestion path: for `flowlogs` you must choose *exactly one* of `source: poll` or
`source: stream` — running both double-counts.

**Resolved when.** Flow records resume at the expected rate, or the tailnet's idleness is confirmed
and the threshold tuned. For dropping: the dropped rate is back to zero, either by raising
`max_log_records_per_window` (costs log ingest) or by narrowing the flow-log scope
(`per_connection` instead of `per_record`).

## Subnet routing and services {#subnet-routing-and-services}

**Rules:** `ts2o-subnet-routes-unapproved`, `ts2o-exit-node-no-failover`, `ts2o-vip-service-no-ha`,
`ts2o-node-ip-forwarding`

**What it means.** A device is advertising subnet routes an admin has not approved (so those subnets
are **not reachable** — the advertisement is inert until approved), a CIDR is served by exactly one
router (no failover), a Tailscale VIP service is backed by a single host (no HA), or a node that is
supposed to route traffic has **IP forwarding disabled on the host OS**, so its routing is broken
even though the tailnet side of the configuration is correct.

**The IP-forwarding case is webhook-only.** Tailscale reports
`exitNodeIPForwardingNotEnabled` / `subnetIPForwardingNotEnabled` as node-health *events*, delivered
**only** through the webhook receiver — there is no polling or log-streaming equivalent — and the
exporter emits them at INFO severity, so nothing surfaces them by default. That makes
`ts2o-node-ip-forwarding` the only mechanism in the pack that reports a silently broken exit node or
subnet router. The receiver is off by default, so `tailscale_webhook_events_total` is absent (and the
rule cannot fire) unless you have configured it.

**Legitimate causes.** Single-router and single-host are correct for a lab, a home network, or any
service whose availability target does not justify a second node. These two are `advisory` for that
reason — they report a redundancy fact, not a fault. An unapproved route may simply be waiting for a
change window. An IP-forwarding event during a node's first few minutes of provisioning is normal and
stops once the sysctl is applied.

**Not legitimate.** An unapproved route that has been pending for days: someone is being told "the
subnet is broken" while the fix is one click in the admin console. Nor is a repeating IP-forwarding
event: the route is advertised, approved and *dead*, which looks like a network fault to everyone
downstream.

**First step.** On `tailscale2otel-tailnet`: **Subnet routes — advertised vs enabled** and
**Subnet-route redundancy by CIDR** are on the **Fleet & Network > Devices > Connectivity & Routing**
tab; **Backing hosts by service** for VIP services is on the **Security & Policy > Policy & Config >
Integrations** tab. Approve or reject the route in the Tailscale admin console. For IP forwarding,
**Webhook events by type & rejections by reason** on the **Ingestion** tab of `tailscale2otel-health`
identifies the event type; fix it on the node itself — `net.ipv4.ip_forward=1` and
`net.ipv6.conf.all.forwarding=1`, persisted in `/etc/sysctl.d/`, not just set for the current boot.

**Resolved when.** Routes are approved (or the advertisement withdrawn), any CIDR/service that
warrants redundancy has a second router/host, and the IP-forwarding events stop arriving — this last
one clears by *absence* of new events, so allow a full `15m` rate window plus the `for` period before
calling it fixed.

## Node client health {#node-client-health}

**Rules:** `ts2o-node-health-warnings`, `ts2o-node-error-drops`, `ts2o-peer-relay-stuck`

**What it means.** The `tailscaled` client on a node is self-reporting one or more active health
warnings — no DERP connection, key expiry approaching, network down, and similar. This is the client's
own opinion of itself, curated from the node-metrics scraper, and it often precedes an outage the
control-plane view has not noticed yet. `ts2o-node-error-drops` and `ts2o-peer-relay-stuck` add two
more angles on the same client: packets it is discarding, and peer-relay connections that never
finish setting up.

`ts2o-node-error-drops` counts packet drops by reason, but it **excludes `acl` by construction**.
An ACL drop is the packet filter doing its job — the tailnet policy said no, and alerting on that
teaches operators to ignore drop signals, which is the opposite of what this rule is for. The
bounded reason set `tailscaled` emits is exactly
`acl, multicast, link_local_unicast, too_short, fragment, unknown_protocol, error, other`
(`internal/semconv/attrs.go`); this rule watches `error`, `unknown_protocol` and `other`. `other` is
the fold bucket for a reason value `tailscaled` emits that this exporter version does not recognise
— treat a rise in `other` as a sign the reason list needs refreshing, not as a single fault class.

`ts2o-peer-relay-stuck` watches endpoints stuck in the `connecting` state for peer-relay, Tailscale's
newer relay mechanism — a peer-relay endpoint that never leaves `connecting` behaves like a
direct-path negotiation that never completes, so that peer falls back to DERP or fails outright.

**Legitimate causes.** A laptop that has just woken, or a node mid-network-transition, will report a
transient warning that clears itself within a poll or two. The 15-minute `for` window is there to
absorb exactly that. A brief burst of `error`/`unknown_protocol` drops during a network transition is
similarly transient.

**Not legitimate.** A warning that persists across evaluation windows. `no-DERP-connection` in
particular means that node cannot fall back to relay, so it is one failed direct path away from being
unreachable. A sustained non-zero error/unknown-protocol drop rate, or a peer-relay endpoint stuck in
`connecting` for the full hour window, means the connection attempt is not going to resolve on its
own.

**First step.** **Active health warnings by type** and **Health messages** on the **Fleet & Network >
Node Metrics** tab of `tailscale2otel-tailnet` identify the node and the warning type. Then go to that
node: `tailscale status` and `tailscale netcheck` on the host give the client's own diagnosis directly.
For drops, **Error & malformed drops by reason** (same tab) breaks down which reason is climbing. For
peer-relay, **Peer-relay endpoints by state** (same tab) shows how many are stuck versus connected.
This family needs the node-metrics scraper — without it the series are absent and nothing fires.

**Resolved when.** The node reports no active health warnings for a full evaluation window, the
error/unknown-protocol drop rate returns to zero, and no peer-relay endpoint remains in `connecting`
past the window.

## Node-metrics scrape targets {#nodemetrics-scrape-targets}

**Rules:** `ts2o-nodemetrics-target-down`, `ts2o-nodemetrics-name-budget`

**What it means.** The node-metrics scraper could not reach `tailscaled`'s metrics endpoint on a
target, so `tailscale_node_up_ratio` is `0` for it and every forwarded `tailscaled_*` series from that
node is frozen at its last value. This is scrape *reachability*, not the client's opinion of itself —
a node can be perfectly healthy and simply not be scrapeable, and it can be unreachable while
reporting no health warnings at all. That is why it is a separate family from
[Node client health](#node-client-health), and separate again from
[Enrichment and discovery](#enrichment-and-discovery), which covers the target *list* rather than the
targets on it.

`ts2o-nodemetrics-name-budget` is a different fault on the same scraper: a target is *reachable* but
presents more distinct metric names than `node_metrics.max_distinct_metrics` allows, so some are
silently never forwarded. A sustained non-zero rate means that target's metric surface has grown past
the configured budget.

**Legitimate causes.** The scraper is off by default, so the gauge is absent in most deployments
(`optional`). Where it is on, a laptop or workstation target that sleeps overnight goes down every
night and comes back every morning; `tailscaled` also only serves `/metrics` when the node has the
debug metrics endpoint enabled and reachable over the tailnet. **This rule therefore ships paused** —
enable it once your target list is servers you expect to be up continuously. The name-budget rule
also ships paused: a target legitimately exposing more metric names than the current budget (a newer
`tailscaled` version, for instance) is a tuning decision, not an incident by itself.

**Not legitimate.** A server target that stays down while the device still shows online in the
control-plane view. That combination means the node is on the tailnet but its metrics endpoint is not
answering — a local `tailscaled`, firewall or bind-address problem, not a tailnet one. For the
name-budget rule: drops that persist after you have deliberately raised
`node_metrics.max_distinct_metrics` and confirmed the metric surface is expected mean something else
is generating unbounded metric names.

**First step.** **Node-metrics targets up** and **Node-metrics scrape health by target** on the
**Collection** tab of `tailscale2otel-health` name the target (`tailscale_node`); the second breaks
the aggregate down per target. Then, from a host on the tailnet, curl that node's metrics endpoint
directly: a connection refused points at the endpoint, a timeout points at reachability. If *every*
target is down at once, suspect the scraper's config rather than the fleet, and check
[Enrichment and discovery](#enrichment-and-discovery) for a stale or empty target list. For the
name-budget rule, **Forwarded metric-name drops/s by reason** on the **Cost & Cardinality** tab of
`tailscale2otel-health` identifies which target is over budget.

**Resolved when.** `tailscale_node_up_ratio` is `1` for that target across a full evaluation window.
Note that decommissioning a target does **not** resolve it by clearing the alert to OK — the series
goes absent, which this rule treats as `Ok` (no alert) rather than as a fault; remove it from the
target list so it stops being expected. For the name-budget rule: the drop rate returns to zero,
either because the metric surface shrank or because `max_distinct_metrics` was deliberately raised.

## TLS certificate rotation {#tls-certificate-rotation}

The exporter's inbound listeners (admin, Prometheus, streaming, webhook) reload their certificate
without a restart: on a handshake, if the cached pair is older than the re-check interval, the cert
and key files are stat'd and reloaded when either changed. There is no SIGHUP and no reload
endpoint — config hot reload is a parked decision (#486), and a certificate does not need one.

**A reload FAILURE is not an outage.** A broken or half-written replacement leaves the previous
certificate in service, which is deliberate: a cert-manager or certbot writing two files
non-atomically will be observed mid-write, so eager reloading into a partial file would turn a
routine rotation into a real outage. That is why a failure is a warning rather than a page — the
listener is still serving.

It becomes urgent when the old certificate is close to expiring, because that is when "still
serving the previous one" stops being a safe fallback.

**Certificate expiring** (`tailscale2otel_tls_cert_not_after_seconds`)

1. Identify the listener from the `component` label (admin, metrics, stream, webhook).
2. Check whether rotation is happening at all:
   `tailscale2otel_tls_cert_reload_failures_total` and the TLS panel on the status page, which shows
   the last successful reload and the last failure reason per listener.
3. If reloads are failing, fix the source of the files (the issuer, the mount, the file mode) — the
   exporter is reading whatever is on disk and telling you it could not use it.
4. If reloads are succeeding but the expiry is not moving, the issuer is renewing into a different
   path than the one configured. Compare the configured `*.tls.cert_file` against what the issuer
   writes; a relative path resolves against the CONFIG FILE's directory (#310), not the working
   directory.

**Reload failing** (`tailscale2otel_tls_cert_reload_failures_total`)

The status page carries the last failure reason verbatim. The common causes are a partially written
file (harmless if it resolves on the next attempt), a cert and key that are not a matching pair
(the issuer wrote one of the two), and a permissions change on rotation. The listener keeps serving
the old certificate throughout, so treat this as "fix before the current cert expires", not as an
active outage.

