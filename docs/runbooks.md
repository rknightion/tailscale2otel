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
(`deploy/grafana/tailscale2otel.json`, uid `tailscale2otel`); most alerts also carry a
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
| `advisory` | `Ok` | `OK` | Hygiene. Neither absence nor a transient error is actionable at this severity. |

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

**First step.** Look at **Uptime** and **Build info** on the Exporter Health tab. If `Uptime` is
resetting repeatedly, it is a crash loop — go to the process logs. If there is no series at all,
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

**First step.** **Scrape success by collector** and **Last scrape age** on the Exporter Health tab
identify which collector. Then **Scrape errors/s by collector / type** gives the error class, and
**Scrape budget headroom** tells you whether it is erroring or simply too slow. Cross-check
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

**First step.** **Metrics overflowing now** and **Per-metric headroom (top-N)** on the Cardinality
tab name the offending metric family. Decide between raising `cardinality.metric_limit` (costs ingest)
and lowering the source cardinality (flow rollups, dropping `source_port`, narrowing collectors).

**Resolved when.** `tailscale2otel_series_overflowing_ratio` is `0` and the busiest family is back
under 80% of its budget. Note these rules are `optional`: setting `metric_limit` to `0`/unlimited
suppresses the gauges entirely, so the alert going quiet may mean "no longer measured" rather than
"fixed".

## Tailscale API health {#tailscale-api-health}

**Rules:** `ts2o-api-credential-rejected`, `ts2o-api-scope-denied`, `ts2o-api-rate-limited`,
`ts2o-api-server-errors`, `ts2o-api-retries-elevated`, `ts2o-tailnet-api-errors`

**What it means.** The exporter's calls to the Tailscale API are failing, keyed off the *classified*
availability state rather than the raw status code. `credential_rejected` (HTTP 401) is the
tailnet-wide emergency — the credential is invalid, expired or revoked, so every collector stops.
`scope_denied` (HTTP 403) is narrower: the credential works but is refused one operation, so exactly
one collector's signals go missing while everything else looks fine.

**Legitimate causes.** A 403 is **not** always a fault, and this is the trap the rules were rebuilt
around: upstream also reports "your tailnet does not have this feature" as a 403. That case is
classified `disabled`, not `scope_denied`, and does not fire. Rate limiting on a large multi-tailnet
deployment with aggressive poll intervals is expected and is a tuning signal. Occasional 5xx from
upstream is normal internet.

**Not legitimate.** Sustained 401. API keys expire at 90 days and are user-bound — if the user who
minted the key leaves, it dies with them. That is the most common cause of a sudden tailnet-wide
outage.

**First step.** **API requests/s by status** on the Exporter Diagnostics tab separates 401 from 403
from 5xx. For 401, rotate the OAuth client or API key. For 403, find which endpoint is being refused
and either widen the OAuth scope or disable that collector. For 429, raise poll intervals or reduce
enabled collectors. For multi-tailnet, **Per-tailnet API errors** isolates which tailnet's
credentials are at fault without the others masking it.

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
Exporter Diagnostics tab. Break down by signal — metrics and logs use different endpoints, and one
failing alone points at a per-signal endpoint path or a per-signal quota. Remember the exporter
appends `/v1/metrics` and `/v1/logs` itself: a bare gateway URL in `otlp.endpoint` 404s silently.

**Resolved when.** The failure rate is zero and p99 is back under the threshold for a full evaluation
window. `export-latency-high` is `core` — if the histogram disappears entirely, that is a `NoData`
alert and means exports stopped, not that they got fast.

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

**First step.** **Checkpoint persist errors/s** and **Checkpoint persist age** on the Exporter
Diagnostics tab. Then check the checkpoint path's existence, ownership (uid 65532 in the shipped
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

**First step.** **Component errors/s** on the Exporter Diagnostics tab, broken down by `component`,
then the process logs for that component. For the other three, confirm against the absolute-value
panel (**Dedup set fill**, **GC CPU fraction**, **Admin auth rejected/s**) before treating the rate
as a problem.

**Resolved when.** The component error rate returns to zero. The other three are `advisory` and are
tuning signals, not incidents.

## Enrichment and discovery {#enrichment-and-discovery}

**Rules:** `ts2o-enrich-cache-stale`, `ts2o-nodemetrics-discovery-failing`

**What it means.** The IP/nodeID → name cache has not refreshed, or dynamic node-metrics target
discovery is failing. Neither stops data flowing — both degrade it silently. Stale enrichment means
flow and audit records resolve to `unknown`/`external` instead of device names; stale discovery means
the node-metrics target list is frozen at its last-known state.

**Legitimate causes.** Both are gated. Enrichment age is only emitted when the **devices** collector
is enabled (it is the sole refresher), and discovery only when dynamic discovery is on. With either
disabled the series is absent and the rule cannot fire — that is why they are `optional`, and it is
also the trap: turning the devices collector off does not "fix" stale enrichment, it hides it.

**Not legitimate.** A stale cache while the devices collector is enabled and scraping. That means
devices scrapes are failing — go to [Collector scrape health](#collector-scrape-health) instead.

**First step.** **Enrich cache age** and **Enrich cache devices** on the Exporter Diagnostics tab.
If the age is climbing, check the devices collector's scrape success. For discovery, **Discovery OK**
and **Discovered targets** on the Node Metrics tab.

**Resolved when.** Cache age drops back to roughly the devices poll interval, and flow/audit panels
show device names rather than `unknown`.

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

**First step.** **Config warnings** and **Config valid** on the Exporter Health tab, then the startup
logs — the `Warnings()` output names each warning explicitly. `docs/configuration.md` is the
key-by-key reference.

**Resolved when.** `config_warnings_ratio` is `0` (or the remaining warnings are ones you have
accepted) and `config_valid_ratio` is `1`. These two are `core`: if they disappear the exporter is
gone, which is a `NoData` alert, not silence.

## Credential expiry {#credential-expiry}

**Rules:** `ts2o-device-key-expiring-critical`, `ts2o-auth-key-expiring-critical`,
`ts2o-device-keys-expiring-7d`, `ts2o-auth-keys-expiring-7d`,
`ts2o-device-attribute-expiring-14d`

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

**Not legitimate.** An auth/API key expiring under automation. API keys are capped at 90 days and are
**user-bound**: they die when the user who created them is offboarded, taking the exporter with them.
Prefer OAuth clients, which auto-refresh.

**First step.** **Device key expiry (time until)** and **Key expiry (time until)** on the Fleet and
Policy tabs list the specific devices and keys with their remaining time. For posture attributes,
note an expired attribute *silently* breaks posture-based ACLs — there is no error, the grant just
stops matching.

**Resolved when.** The key is rotated or the device re-authed. All of these are computed from
`expiry - now` gauges, not from cumulative histogram buckets, so they clear on their own once the
credential is renewed — no manual reset, and an already-expired-and-abandoned key does not alert
forever (the rules exclude negative remaining time).

## Device posture coverage {#device-posture-coverage}

**Rules:** `ts2o-posture-autoupdate-low`, `ts2o-posture-encryption-low`, `ts2o-posture-match-low`

**What it means.** A fleet-wide posture property has fallen below its coverage threshold: fewer than
80% of devices report client auto-update enabled, or an encrypted local state store, or an MDM/EDR
integration is matching fewer than 80% of the devices it could.

**Legitimate causes.** Platforms differ. Client auto-update is not available on every OS, and state
encryption depends on the platform keystore, so a mixed fleet has a structural ceiling below 100% —
tune the threshold to *your* fleet rather than chasing 100%. A low integration match rate right after
onboarding a new MDM is expected while enrolment catches up.

**Not legitimate.** A coverage ratio that *drops*. A sustained fall means devices are dropping out of
management, and for the match-rate rule specifically it means devices may be bypassing posture gates
entirely.

**First step.** **Auto-update coverage**, **State-encryption coverage** and **Posture match rate** on
the Security tab. **Device posture snapshot** lists which devices are missing the property. Note the
whole family is gated on `collect_posture`; with posture collection off the series are absent and
nothing fires.

**Resolved when.** The ratio is back above threshold, or you have concluded the threshold was wrong
for this fleet and adjusted it in the generator.

## Fleet version hygiene {#fleet-version-hygiene}

**Rules:** `ts2o-devices-needing-update`, `ts2o-device-version-skew-high`, `ts2o-devices-outdated`

**What it means.** Devices are running Tailscale clients behind the fleet's latest version.

**Legitimate causes.** Almost all of them. Pinned versions on appliances, devices that have been off
for a while, a staged rollout in progress, and platforms whose app-store updates lag. This family is
`advisory` — fail-open on both no-data and error — because it is drift reporting, not an incident.

**Not legitimate.** Nothing here is an incident. Treat a persistent high skew as a fleet-management
backlog item, not a page.

**First step.** **Most-behind devices (top-N)** and **Outdated (≥N behind)** on the Fleet tab name the
laggards. If you want auto-update coverage rather than a snapshot of drift, that lives in
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

**First step.** **Nodes with tailnet-lock errors** on the Security tab names the devices; sign them
from a signing node. For a disable event, **Security/lifecycle changes/s** and the audit trail show
the actor. Both rules are `optional` because tailnet lock is off by default on most tailnets, so the
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

**First step.** **Unrestricted ACL rules** and **Auto-approvers by kind** on the Policy tab, then
**ACL last changed** to correlate against a change. Pair with
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

**First step.** **Credential scopes (top-N)** on the Policy tab lists every credential and its scope
class. Note the historical trap here: an earlier version of this rule counted scopes, which inverted
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

**First step.** **Changes by actor type** and **Top $topn actors over time** on the Security & Audit
tab attribute the change. For schema drift, the collector emits a once-per-value digest warning naming
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

**First step.** **Flow logging**, **Tailnet scorecard**, **Streams configured** and **Contact needs
verification** show current state; **Recent configuration changes** on the Security & Audit tab shows
what moved.

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

**First step.** **Exit-node-granting shares** on the Security tab lists the outstanding invites. Check
each against who it was meant for and whether routing their traffic is intended.

**Resolved when.** The invite is revoked or reissued without exit-node permission, or it is confirmed
intentional.

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

**First step.** **Oldest sync age** and **Integration sync detail** on the Integrations tab identify
the provider and integration. Then re-authorize it in the Tailscale admin console — revoked or expired
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
error** on the Integrations tab. The last delivery error is usually explicit about whether it is the
endpoint, the credential or the sink rejecting the payload. Pair with `ts2o-logstream-config-changed`
in [Tailnet settings drift](#tailnet-settings-drift) — a stream that stopped delivering right after a
config change was probably reconfigured.

**Resolved when.** The failure rate is zero and delivery activity resumes inside the stream's normal
cadence.

## Ingest receivers {#ingest-receivers}

**Rules:** `ts2o-receiver-rejections`, `ts2o-receiver-latency-high`, `ts2o-ingest-data-stale`

**What it means.** The exporter's own inbound paths — the Splunk-HEC stream receiver and the
HMAC-verified webhook receiver — are rejecting events, responding slowly, or accepting nothing recent.

**Legitimate causes.** Both receivers are **off by default**, so all three series are absent in a
poll-only deployment (`optional`). `ingest-data-stale` in particular ships paused because quiet
tailnets and sparse webhook/audit sources are legitimately idle for hours — enable it only for
`source`/`signal` pairs you expect to deliver continuously, with label filters or a tuned threshold
for that workload. Firing it on everything produces noise, not coverage.

**Not legitimate.** A rejection rate above zero on a configured receiver. Rejections mean spoofed,
oversized or undecodable events — either the sender is misconfigured or the HEC/webhook secret does
not match.

**First step.** **Receiver rejected/s** (broken down by reason), **Receiver latency
p50/p95/p99 (stream)** and **Accepted event freshness** on the Events & Logs tab. The rejection reason
distinguishes an auth mismatch from a decode failure. Also confirm you have not enabled both poll and
stream for the same log type — that double-counts, and cross-source dedup is only a best-effort
failsafe.

**Resolved when.** Rejections return to zero and the newest accepted event timestamp is inside the
expected window for that source.

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

**First step.** **Fleet DERP share (now)** and **Traffic mix by path (direct / DERP / peer-relay)** on
the Node Metrics tab, then **Best latency per DERP region** and **Hard-NAT %**. All of these need the
node-metrics scraper — with it disabled the series are absent and nothing fires.

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

**First step.** **Flows/s (now)** and **Flow log stream** on the Flow View tab; **Reporter trust &
consistency** for the mismatch case; **Flow log records dropped/s** for the truncation case. Confirm
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

**First step.** **Subnet routes — advertised vs enabled** and **Subnet-route redundancy by CIDR** on
the Network tab; **Backing hosts by service** for VIP services. Approve or reject the route in the
Tailscale admin console. For IP forwarding, **Webhook events/s by type** identifies the event type;
fix it on the node itself — `net.ipv4.ip_forward=1` and `net.ipv6.conf.all.forwarding=1`, persisted
in `/etc/sysctl.d/`, not just set for the current boot.

**Resolved when.** Routes are approved (or the advertisement withdrawn), any CIDR/service that
warrants redundancy has a second router/host, and the IP-forwarding events stop arriving — this last
one clears by *absence* of new events, so allow a full `15m` rate window plus the `for` period before
calling it fixed.

## Node client health {#node-client-health}

**Rules:** `ts2o-node-health-warnings`

**What it means.** The `tailscaled` client on a node is self-reporting one or more active health
warnings — no DERP connection, key expiry approaching, network down, and similar. This is the client's
own opinion of itself, curated from the node-metrics scraper, and it often precedes an outage the
control-plane view has not noticed yet.

**Legitimate causes.** A laptop that has just woken, or a node mid-network-transition, will report a
transient warning that clears itself within a poll or two. The 15-minute `for` window is there to
absorb exactly that.

**Not legitimate.** A warning that persists across evaluation windows. `no-DERP-connection` in
particular means that node cannot fall back to relay, so it is one failed direct path away from being
unreachable.

**First step.** **Active health warnings by type** and **Health messages** on the Node Metrics tab
identify the node and the warning type. Then go to that node: `tailscale status` and
`tailscale netcheck` on the host give the client's own diagnosis directly. This rule needs the
node-metrics scraper — without it the series is absent and nothing fires.

**Resolved when.** The node reports no active health warnings for a full evaluation window.

## Node-metrics scrape targets {#nodemetrics-scrape-targets}

**Rules:** `ts2o-nodemetrics-target-down`

**What it means.** The node-metrics scraper could not reach `tailscaled`'s metrics endpoint on a
target, so `tailscale_node_up_ratio` is `0` for it and every forwarded `tailscaled_*` series from that
node is frozen at its last value. This is scrape *reachability*, not the client's opinion of itself —
a node can be perfectly healthy and simply not be scrapeable, and it can be unreachable while
reporting no health warnings at all. That is why it is a separate family from
[Node client health](#node-client-health), and separate again from
[Enrichment and discovery](#enrichment-and-discovery), which covers the target *list* rather than the
targets on it.

**Legitimate causes.** The scraper is off by default, so the gauge is absent in most deployments
(`optional`). Where it is on, a laptop or workstation target that sleeps overnight goes down every
night and comes back every morning; `tailscaled` also only serves `/metrics` when the node has the
debug metrics endpoint enabled and reachable over the tailnet. **This rule therefore ships paused** —
enable it once your target list is servers you expect to be up continuously.

**Not legitimate.** A server target that stays down while the device still shows online in the
control-plane view. That combination means the node is on the tailnet but its metrics endpoint is not
answering — a local `tailscaled`, firewall or bind-address problem, not a tailnet one.

**First step.** **Node up** and **Targets up** on the Node Metrics tab name the target
(`tailscale_node`). Then, from a host on the tailnet, curl that node's metrics endpoint directly: a
connection refused points at the endpoint, a timeout points at reachability. If *every* target is
down at once, suspect the scraper's config rather than the fleet, and check
[Enrichment and discovery](#enrichment-and-discovery) for a stale or empty target list.

**Resolved when.** `tailscale_node_up_ratio` is `1` for that target across a full evaluation window.
Note that decommissioning a target does **not** resolve it by clearing the alert to OK — the series
goes absent, which this rule treats as `Ok` (no alert) rather than as a fault; remove it from the
target list so it stops being expected.
