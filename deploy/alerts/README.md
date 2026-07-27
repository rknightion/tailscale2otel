# tailscale2otel — Alerting rules

Alerting + recording rules that complement the Grafana dashboards in
[`../grafana/`](../grafana/).

**One delivery model: Grafana-managed rules, as `rules.alerting.grafana.app`
manifests.** [`grafana-managed/`](grafana-managed/) holds one JSON manifest per
rule plus a folder manifest, and the whole directory is pushed with
[`gcx`](https://github.com/grafana/gcx):

```bash
gcx resources push -p deploy/alerts/grafana-managed
```

Grafana evaluates these itself, so they can span Prometheus and Loki in one
ruleset and carry `noDataState` / `execErrState` / `paused`.

> **This repo no longer ships a Prometheus/Mimir *ruler* rules file.** The old
> hand-maintained `tailscale2otel.rules.yaml` and the generated
> `tailscale2otel.grafana-rules.yaml` (Grafana *file-provisioning*,
> `apiVersion: 1`) are both gone. File provisioning is not what
> `gcx resources push` consumes, and maintaining a second, drifting,
> ruler-compatible copy of the same catalogue was not worth it. If you need
> datasource-managed rules, render them yourself from the generator — see
> [the test-only Prometheus rendering](#the-test-only-prometheus-rendering) —
> and own the result.

## Important: metric names assume OTLP → Prometheus normalization

Every `expr` queries the **normalized Prometheus names** that Grafana Cloud
(Mimir) produces when it ingests our OTLP metrics — *not* the raw OTEL source
names. The relevant rules (see [`../grafana/README.md`](../grafana/README.md)
and [`../../docs/metrics.md`](../../docs/metrics.md)):

- Dots become underscores, in both metric names **and** label keys.
- Monotonic counters get a `_total` suffix.
- Units are appended: `s` → `_seconds`, `By` → `_bytes`, `d` → `_days`.
- A gauge with unit `1` gets a `_ratio` suffix — so boolean 0/1 flag gauges
  (and even plain counts) land as `*_ratio`. That is why the liveness, scrape,
  feature and node-up gauges are queried as `*_up_ratio`, `*_success_ratio`,
  `*_enabled_ratio`, etc.

If you run a non-Grafana OTEL backend with different translation rules, adjust
the metric names accordingly.

## Grafana-managed rules (recommended)

[`grafana-managed/`](grafana-managed/) is **generated** by
[`gen/build_rules.py`](gen/build_rules.py) (stdlib Python, no PyYAML) — edit the
generator, not the JSON:

```bash
python3 gen/build_rules.py --out grafana-managed
```

One manifest per rule (`<uid>.json`), plus `_folder.json`. Every `*.json` in the
directory is **deleted before writing**, so a renamed or removed rule cannot
linger as a stale file that `gcx resources push` keeps re-creating. Output is
sorted-key JSON with a trailing newline, so two runs are byte-identical and the
CI drift gate is meaningful.

| kind | apiVersion | file |
|---|---|---|
| `Folder` | `folder.grafana.app/v1beta1` | `_folder.json` |
| `AlertRule` | `rules.alerting.grafana.app/v0alpha1` | `ts2o-*.json` |
| `RecordingRule` | `rules.alerting.grafana.app/v0alpha1` | `ts2o-rec-*.json` |

`metadata.name` is the rule uid — that becomes the Grafana UID, so it is capped
at 40 chars and `[A-Za-z0-9_-]`. The folder is named in `metadata.labels` **and**
`metadata.annotations` (`grafana.app/folder`); push the folder first.

Every rule is the canonical Grafana 3-node pipeline — **A** (datasource query) →
**B** (reduce, `last`) → **C** (threshold) — expressed as the `spec.expressions`
MAP keyed by refId, with `C` marked `"source": true`. This API has no separate
`condition` field: the `source` marker is what makes the threshold the verdict.
Datasource UIDs are the portable Grafana Cloud defaults (`grafanacloud-prom` /
`grafanacloud-logs`); swap them for a self-hosted stack. Every rule carries
`service: tailscale2otel`, a `severity`, and a **`domain`** label (`security` /
`infra` / `observability`); rules not worthy of automatic investigation
(non-critical, non-paging, non-security) also carry `skipinvestigation: "true"`
so IRM routing / auto-investigation stays focused. The generated set currently
has **91 alert rules + 12 recording rules** across five groups (`-health`,
`-security`, `-integrations`, `-network`, `-recording`); the tables below are an
illustrative guide — `gen/build_rules.py` is the source of truth.

**Default-disabled by design.** Only a high-signal *starter set* ships enabled
(`spec.paused: false`); the rest are `spec.paused: true` — enable them in the UI
when you want them.

### Format traps (why this is worth reading before editing)

Three of these are silent: the manifest looks fine everywhere locally and the API
rejects it — or accepts it and ignores the field — at push time.

- **`noDataState` spells its OK value `"Ok"`. `execErrState` spells its own
  `"OK"`.** The asymmetry is real. The old `apiVersion: 1` provisioning format
  used `"OK"` for both, so a mechanical port produces un-deployable rules.
- **Durations are Go-style strings** — `"30m0s"`, `"1h0m0s"`, `"0s"`. `"5m"` is
  not accepted, and `relativeTimeRange.from`/`to` are durations here rather than
  the integer seconds provisioning took.
- **Panel links are the paired `__dashboardUid__` / `__panelId__` annotations**,
  and `__panelId__` is a **string**. The top-level `dashboardUid`/`panelId`
  fields are provisioning-only; `additionalProperties: false` rejects them.
- **`RecordingRule` has no `annotations`, `for`, `condition`, `noDataState` or
  `execErrState`.** Its spec is exactly
  `{title, trigger, metric, expressions, targetDatasourceUID, labels, paused}`.
  Each recorded metric's written reason therefore lives in the generator's
  internal `_desc` and is deliberately not emitted.

[`gen/validate_manifests.py`](gen/validate_manifests.py) enforces all four (and
the UID charset/length rule) over the emitted directory, and runs as part of the
generator test suite.

**Every alert declares an evaluation policy** (#388), which fixes its
`noDataState`/`execErrState` pair. There is no global default — the generator's
`alert()` requires `policy=`, so a new rule cannot inherit someone else's
semantics:

| Policy | `noDataState` | `execErrState` | Used for | Count |
|---|---|---|---|---|
| `coverage_critical` | `Alerting` | `Alerting` | absence **is** the fault | 1 |
| `core` | `NoData` | `Error` | always emitted while the exporter runs | 7 |
| `optional` | `Ok` | `Error` | legitimately absent (gated collector, optional source, a counter that has not incremented) | 64 |
| `advisory` | `Ok` | `OK` | hygiene; neither absence nor a transient error is actionable | 19 |

Before this, *every* rule was fail-open on error, so a broken datasource read
as "healthy" across the whole pack. Only the `advisory` class still is, and that
is a per-rule decision.

**Every alert also carries a `runbook_url` annotation** pointing at a section of
[`docs/runbooks.md`](../../docs/runbooks.md), and 90 of the 91 carry the
`__dashboardUid__`/`__panelId__` annotation pair for their canonical panel in the
generated flagship dashboard. Both are resolved and validated **at generation
time**: an unknown runbook slug, an unreferenced runbook section, a missing panel
title, or a title matching more than one panel each **fail the build**. Panels
are resolved by **title**, never by a literal id — ids come from a counter in
`../grafana/gen/build.py` and renumber whenever a panel is inserted. This is why
`build.py` must run before `build_rules.py` (`scripts/regen-generated.sh
dashboards` already does, in that order).

Nothing in this repo monitors Grafana's own ruler or datasource health, and a
rule structurally cannot alert on its own non-evaluation. See
[the runbook page](../../docs/runbooks.md) for the honest version and the three
external mechanisms that do cover it.

### The test-only Prometheus rendering

`promtool test rules` is the only thing in the repo that **executes** a rule
against synthetic series — it already caught a rule that was valid PromQL and
semantically wrong (dropping the lower bound on the key-expiry window makes every
long-dead host alert forever). promtool understands no format but Prometheus's,
so the generator can re-render the same catalogue as plain rule groups:

```bash
python3 deploy/alerts/gen/build_rules.py --prom-out deploy/alerts/.generated-prom-rules.yaml
promtool test rules deploy/alerts/tests/*.yaml
```

**That file is a fixture, not a deliverable: gitignored, never committed, never
shipped.** Do not load it into a ruler. It omits the Loki-backed rules (promtool
cannot parse LogQL), carries only the `summary` and `runbook_url` annotations
(promtool matches annotations exactly, and panel ids renumber on any dashboard
edit), and renders `coverage_critical`'s "absence is the alert" semantics as an
explicit `or absent(...)` arm, which Grafana expresses as `noDataState: Alerting`.
Fixture alert names are the CamelCased rule titles.

Generator tests live in [`gen/test_rules.py`](gen/test_rules.py) — they pin the
manifest format (including the two state casings), the policy matrix, the
`coverage_critical` allowlist (with a written reason per member), the
recording-rule field contract, both link contracts, emit determinism, and the
Prometheus rendering:

```bash
python3 -m unittest discover -s deploy/alerts/gen -t deploy/alerts/gen
```

### `tailscale2otel-health` — exporter self-health

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `ExporterDown` | critical | ✅ on | `tailscale2otel_up_ratio` is `0` **or absent** for 5m — the exporter process died or stopped emitting entirely. The pack's only `coverage_critical` rule: a query error pages too |
| `CollectorScrapeFailing` | warning | ✅ on | `tailscale2otel_scrape_success_ratio == 0` (per collector) for 15m — the last scrape failed and hasn't recovered |
| `CollectorScrapeStale` | warning | ✅ on | a collector hasn't completed a scrape in >1h (wedged; success gauge can stay stale at 1) |
| `CollectorScrapeErrorRateHigh` | warning | ✅ on | `rate(scrape_errors_total[5m]) > 0` per collector for 15m — catches a **flapping** collector, whose last-scrape success gauge reads `1` at every evaluation so `CollectorScrapeFailing` never fires |
| `OTLPExportFailures` | warning | ✅ on | `rate(export_failures_total[10m]) > 0` by `error_type` for 15m — the OTEL SDK's global error handler is seeing export failures; broader than `ExportFailures` (which reads the per-signal export decorators) |
| `MetricCardinalityCapped` | warning | ✅ on | `series_overflowing_ratio` > 0 → a metric is collapsing excess series (silent per-series loss) |
| `SeriesBudgetHigh` | warning | ✅ on | a metric's active-series / `series_limit` headroom ratio > 0.8 (approaching its cap) |
| `TailscaleAPIAuthFailing` | critical | ✅ on | API returns 401/403 → credentials broken, all polling fails |
| `TailscaleAPIRateLimited` | warning | ⏸ off | API returns 429 (throttled) |
| `APIRateLimiterWaitHigh` | warning | ⏸ off | p95 client-side rate-limiter wait > 5s (placeholder threshold, tune from your baseline) — the exporter throttling *itself*, distinct from `TailscaleAPIRateLimited` (server-side 429) and genuine upstream latency |
| `TailscaleAPIServerErrors` | warning | ⏸ off | API 5xx rate > 0.05/s |
| `APIRetriesElevated` | warning | ⏸ off | API retry rate > 0.1/s |
| `CheckpointPersistErrors` | warning | ✅ on | a collector can't persist its high-water mark (replay/dup risk) |
| `ComponentErrors` | warning | ✅ on | a non-collector subsystem (receiver/admin/auto-configure) is erroring |
| `DedupSetSaturated` | warning | ⏸ off | a dedup set is evicting (undersized → double-count risk) |
| `EnrichCacheStale` | warning | ✅ on | enrichment cache > 1h old → flow/audit names degrade to `unknown` |
| `NodeMetricsDiscoveryFailing` | warning | ⏸ off | dynamic node-target discovery failing |
| `AdminAuthRejectionsHigh` | info | ⏸ off | elevated admin-auth rejections (probing/misconfig) |
| `GCCPUFractionHigh` | info | ⏸ off | GC CPU fraction > 0.25 (low value — near-idle service) |

### `tailscale2otel-security` — security & governance

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `TailnetLockErrors` | warning | ✅ on | a device has a tailnet-lock error (e.g. unsigned node) |
| `AuditConfigChangeWARN` (Loki) | warning | ✅ on | a `tailscale.config.audit` log was emitted at WARN (change carried an error) |
| `DeviceKeysExpiring7d` | warning | ✅ on | one or more device node keys expire within **7 days** — tagged **and** untagged; the only tier covering untagged devices |
| `DeviceKeyExpiringCritical` | critical | ✅ on | a **tagged** device's node key expires within **48h** (`tailscale_tags!=""`). Untagged/user-owned devices are excluded by design — see the note below |
| `AuthKeysExpiring7d` | warning | ✅ on | one or more auth/API keys expire within **7 days** (baseline warning tier below `AuthKeyExpiringCritical`) |
| `AuthKeyExpiringCritical` | critical | ✅ on | an auth/API key expires within **48h** |
| `PostureAutoUpdateCoverageLow` | warning | ✅ on | < 80% of devices have client auto-update enabled |
| `PostureEncryptionCoverageLow` | warning | ⏸ off | < 80% of devices report an encrypted state store |
| `DevicesNeedingUpdate` | info | ⏸ off | > 5 devices have a client update available |
| `TailnetContactUnverified` | warning | ✅ on | a tailnet contact is unverified (security notices may not be delivered) |
| `NewDeviceWithKeyExpiryDisabled` | warning | ⏸ off | a new device with key expiry disabled appeared in the last hour (delta, not level — a standing population of tag-owned never-expiring keys is normal) |
| `DeviceKeyUsedByMultipleClients` | warning | ⏸ off | one or more device node keys are in simultaneous use by more than one client — a key is being shared across machines |

> **Why `DeviceKeyExpiringCritical` only pages on tagged devices.** Node-key expiry is
> real either way — a Tailscale node key does **not** silently auto-renew, and the
> device drops off the tailnet at expiry until someone completes an interactive
> re-auth. What differs is who is there to do it. An untagged, user-owned device
> warns its signed-in user in the client (and Tailscale emails them); re-authing is
> routine, self-service, and recurs every key lifetime — not worth a page. A **tagged**
> device is typically headless: nobody sees the prompt, so expiry becomes an outage.
> Tailscale also disables key expiry on tagged devices by default, and the
> `tailscale.device.key.expiry` gauge is only emitted when key expiry is *enabled*
> (`internal/collector/devices/devices.go`) — so a tagged device with a live expiry
> series has had expiry explicitly re-enabled, which makes it the high-signal case.
> Untagged devices remain covered by the warning-tier `DeviceKeysExpiring7d`.

### `tailscale2otel-integrations` — integration & delivery health

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `PostureIntegrationSyncStale` | warning | ✅ on | an MDM/EDR posture integration hasn't synced in >24h |
| `PostureIntegrationSyncFailing` | warning | ✅ on | an MDM/EDR posture integration's `status.error` is set (fires even while `last_sync` stays fresh — see #99) |
| `LogStreamDeliveryFailing` | warning | ✅ on | SIEM log delivery is failing (`requests_failed` rate > 0) |
| `LogStreamStalled` | warning | ⏸ off | a configured stream has no delivery activity for >1h |
| `LogStreamBackpressure` | info | ⏸ off | delivery requests hitting the max body size |
| `LogStreamSpoofedEntries` | warning | ⏸ off | log entries rejected as spoofed |
| `AcceptedIngestDataStale` | warning | ⏸ off | newest accepted event timestamp is >1h old; enable only for source/signal pairs expected to be continuous |
| `ObjectStoreFeedUndecodable` | critical | ✅ on | one or more whole objects decoded zero records with at least one row failing — a broken feed (wrong framing), not a few bad rows |
| `ObjectStoreGapAging` | warning | ⏸ off | oldest failed-and-awaiting-retry object gap > 24h — tune to the bucket's own retention |
| `ObjectStoreExportStale` | warning | ⏸ off | newest discovered export object > 1h old (folds the `-1` no-discovery sentinel to a day of apparent staleness) |
| `ObjectStoreBacklogStuck` | warning | ⏸ off | backlog never reached zero across a full hour (`min_over_time`) |
| `RDNSCacheOverflowing` | warning | ⏸ off | non-zero rDNS cache overflow rate — the cache is too small for `enrichment.reverse_dns.max_entries` |
| `StreamRecordsSkipped` | warning | ⏸ off | non-zero rate of stream records skipped (`unclassified` or `unwrap_drop`) |
| `WebhookPayloadSchemaDrift` | warning | ⏸ off | a webhook payload field moved to `unknown` status — Tailscale changed the payload shape and whatever consumes that field goes quietly unpopulated |
| `NodeMetricsNameBudgetExhausted` | warning | ⏸ off | non-zero rate of forwarded metric names dropped against `node_metrics.max_distinct_metrics` |

### `tailscale2otel-network` — connectivity

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `HighDERPRelayUsage` | warning | ✅ on | > 50% of fleet bytes relayed via DERP (NAT-traversal problems) |
| `DERPRegionLatencyHigh` | info | ⏸ off | best latency to a DERP region > 150ms |
| `NoFlowData` | info | ⏸ off | ~0 flow records for an hour while flow logging is on |
| `FlowReporterIdentityMismatch` | warning | ⏸ off | the verified reporter node ID disagrees with the unverified embedded source reference |
| `AuditSchemaDriftDetected` | warning | ⏸ off | an audit enum field carries a value unknown to this collector version |
| `FlowLogsDropped` | warning | ✅ on | the per-window flow-log volume guard is truncating log records (metrics are never capped, so nothing else shows the loss) |
| `NodeMetricsTargetDown` | warning | ⏸ off | `tailscale_node_up_ratio == 0` for a target for 10m — tailscaled's metrics endpoint is unreachable. Paused: a sleeping laptop in the target list fires it nightly |
| `NodeIPForwardingMisconfigured` | warning | ✅ on | an exit node or subnet router reported IP forwarding disabled on the host. Webhook-only signal (no poll/log-stream equivalent) and emitted at INFO, so this rule is the only thing that surfaces it |
| `NodeErrorAndMalformedPacketDrops` | warning | ⏸ off | non-zero rate of `error`/`unknown_protocol`/`other` packet drops for 30m. `acl` is excluded by construction — an ACL drop is the packet filter working as intended |
| `PeerRelayEndpointsStuckConnecting` | warning | ⏸ off | one or more peer-relay endpoints stuck in `connecting` state for 1h |

### `tailscale2otel-recording` — recording rules

| Recorded metric | Default | Definition |
|---|---|---|
| `tailscale:devices_online:count` | ⏸ off | devices currently online (deploy-stable) |
| `tailscale:posture_autoupdate:ratio` | ✅ on | fraction of devices with auto-update on |
| `tailscale:posture_encrypted:ratio` | ✅ on | fraction of devices with encrypted state |
| `tailscale:derp_relay:byte_fraction` | ✅ on | fleet DERP byte fraction (precomputes the heavy 4-rate query) |
| `tailscale:flow_throughput:bytes:rate5m` | ⏸ off | total flow throughput (rollup or raw) |
| `tailscale2otel:series_active:sum` | ✅ on | total active series (ingest-cost proxy) |
| `tailscale2otel:ingest_event_freshness_seconds` | ⏸ off | seconds since the greatest accepted event timestamp per source/signal |
| `tailscale:device_keys_expiring_7d:count` | ⏸ off | device keys expiring within 7 days |

> **Heads-up on recording rules:** Grafana-managed recording rules need the
> recording-rules feature + a writable Prometheus/Mimir target on your stack;
> they write `tailscale:*` series back to it. Leave them paused if your stack
> doesn't support them.

### Deploying the manifests

```bash
# whole directory in one go (the folder manifest sorts first, so it lands first)
gcx resources push -p deploy/alerts/grafana-managed

# client-side sanity check before pushing
gcx resources validate -p deploy/alerts/grafana-managed
```

> `gcx resources validate` reports that `alertrules.rules.alerting.grafana.app`
> **does not support server-side dry-run** — it only confirms the manifests parse
> and that the kind is served by the target instance. It does not check the spec.
> `gen/validate_manifests.py` is what checks the spec, offline, and it is the one
> wired into CI.

Anything that speaks the Grafana app-platform API works too — `kubectl` against
the Grafana API server, or the Terraform
`grafana_apps_rules_alertrule_v0alpha1` / `..._recordingrule_v0alpha1` resources,
which take the same spec.

Wire the `severity` label (`critical` / `warning` / `info`) into your
notification policy. Thresholds, `for:` windows and the enabled/paused split all
live in `gen/build_rules.py`.

## Validating locally

```bash
# 1. regenerate + validate the shipped manifests
python3 deploy/alerts/gen/build_rules.py --out deploy/alerts/grafana-managed
python3 deploy/alerts/gen/validate_manifests.py

# 2. generator + manifest contract tests
python3 -m unittest discover -s deploy/alerts/gen -t deploy/alerts/gen

# 3. EXECUTE the rules (test-only rendering; the .yaml is gitignored)
python3 deploy/alerts/gen/build_rules.py --prom-out deploy/alerts/.generated-prom-rules.yaml
promtool check rules deploy/alerts/.generated-prom-rules.yaml
promtool test rules deploy/alerts/tests/*.yaml
```

Step 3 is the only one that runs the expressions rather than reading them. Add a
case to [`tests/`](tests/) whenever a rule's *semantics* — not just its threshold
— are the thing that could be wrong: absence handling, counter resets, label
preservation, and "healthy zero versus absent" are the four classes already
covered.
