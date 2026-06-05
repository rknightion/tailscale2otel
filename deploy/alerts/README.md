# tailscale2otel — Alerting rules

Alerting + recording rules that complement the Grafana dashboards in
[`../grafana/`](../grafana/). Two delivery models, two files — pick one (don't
load overlapping rules into both, or they fire twice):

- [`tailscale2otel.grafana-rules.yaml`](tailscale2otel.grafana-rules.yaml) —
  **Grafana-managed** rules (Grafana evaluates them: `noDataState`/`execErrState`,
  `isPaused`, mixed Prometheus+Loki in one ruleset). Grafana *file-provisioning*
  format (`apiVersion: 1` + `groups:`). **Generated** — see
  [Grafana-managed rules](#grafana-managed-rules-recommended) below.
- [`tailscale2otel.rules.yaml`](tailscale2otel.rules.yaml) — the
  **datasource-managed** equivalent: standard Prometheus/Mimir/Loki *ruler*
  `groups:` / `rules:` YAML (`alert:` / `expr:` / `for:` …). Hand-maintained; the
  original baseline set.

> *Grafana-managed* rules are evaluated by Grafana itself and can span datasources;
> *datasource-managed* rules are loaded into the datasource's own ruler
> (Mimir/Cortex/Loki). The two files overlap in intent.

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

[`tailscale2otel.grafana-rules.yaml`](tailscale2otel.grafana-rules.yaml) is
**generated** by [`gen/build_rules.py`](gen/build_rules.py) (stdlib Python, no
PyYAML) — edit the generator, not the YAML:

```bash
python3 gen/build_rules.py --out tailscale2otel.grafana-rules.yaml
```

Every rule is the canonical Grafana 3-node pipeline — **A** (datasource query) →
**B** (reduce, `last`) → **C** (threshold), `condition: C` — so it round-trips
cleanly through the Grafana UI/API. Datasource UIDs are the portable Grafana Cloud
defaults (`grafanacloud-prom` / `grafanacloud-logs`); swap them for a self-hosted
stack. All rules carry `service: tailscale2otel` plus a `severity` label.

**Default-disabled by design.** Only a high-signal *starter set* ships enabled
(`isPaused: false`); the rest are `isPaused: true` — enable them in the UI when you
want them. Gated/optional signals (posture integrations, log streaming, services,
tailnet-lock, DERP rollups, node discovery) are *absent* until the tailnet has the
data, so their rules use `noDataState: OK` (absent ⇒ not firing).

### `tailscale2otel-health` — exporter self-health

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `CollectorScrapeStale` | warning | ✅ on | a collector hasn't completed a scrape in >1h (wedged; success gauge can stay stale at 1) |
| `MetricCardinalityCapped` | warning | ✅ on | a metric pinned at the 10k series cap → silent per-series loss |
| `SeriesBudgetHigh` | warning | ⏸ off | busiest metric > 8000 series (approaching the cap) |
| `TailscaleAPIAuthFailing` | critical | ✅ on | API returns 401/403 → credentials broken, all polling fails |
| `TailscaleAPIRateLimited` | warning | ⏸ off | API returns 429 (throttled) |
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
| `DeviceKeyExpiringCritical` | critical | ✅ on | a device node key expires within **48h** (critical tier above the 7-day warning) |
| `AuthKeyExpiringCritical` | critical | ✅ on | an auth/API key expires within **48h** |
| `PostureAutoUpdateCoverageLow` | warning | ✅ on | < 80% of devices have client auto-update enabled |
| `PostureEncryptionCoverageLow` | warning | ⏸ off | < 80% of devices report an encrypted state store |
| `DevicesNeedingUpdate` | info | ⏸ off | > 5 devices have a client update available |
| `TailnetContactUnverified` | warning | ✅ on | a tailnet contact is unverified (security notices may not be delivered) |

### `tailscale2otel-integrations` — integration & delivery health

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `PostureIntegrationSyncStale` | warning | ✅ on | an MDM/EDR posture integration hasn't synced in >24h |
| `LogStreamDeliveryFailing` | warning | ✅ on | SIEM log delivery is failing (`requests_failed` rate > 0) |
| `LogStreamStalled` | warning | ⏸ off | a configured stream has no delivery activity for >1h |
| `LogStreamBackpressure` | info | ⏸ off | delivery requests hitting the max body size |
| `LogStreamSpoofedEntries` | warning | ⏸ off | log entries rejected as spoofed |

### `tailscale2otel-network` — connectivity

| Rule | Severity | Default | Fires when |
|---|---|---|---|
| `HighDERPRelayUsage` | warning | ✅ on | > 50% of fleet bytes relayed via DERP (NAT-traversal problems) |
| `DERPRegionLatencyHigh` | info | ⏸ off | best latency to a DERP region > 150ms |
| `NoFlowData` | info | ⏸ off | ~0 flow records for an hour while flow logging is on |

### `tailscale2otel-recording` — recording rules

| Recorded metric | Default | Definition |
|---|---|---|
| `tailscale:devices_online:count` | ⏸ off | devices currently online (deploy-stable) |
| `tailscale:posture_autoupdate:ratio` | ✅ on | fraction of devices with auto-update on |
| `tailscale:posture_encrypted:ratio` | ✅ on | fraction of devices with encrypted state |
| `tailscale:derp_relay:byte_fraction` | ✅ on | fleet DERP byte fraction (precomputes the heavy 4-rate query) |
| `tailscale:flow_throughput:bytes:rate5m` | ⏸ off | total flow throughput (rollup or raw) |
| `tailscale2otel:series_active:sum` | ✅ on | total active series (ingest-cost proxy) |
| `tailscale:device_keys_expiring_7d:count` | ⏸ off | device keys expiring within 7 days |

> **Heads-up on recording rules:** Grafana-managed recording rules need the
> recording-rules feature + a writable Prometheus/Mimir target on your stack;
> they write `tailscale:*` series back to it. Leave them paused if your stack
> doesn't support them.

### Importing the Grafana-managed file

- **File provisioning** (self-hosted / Alloy): drop the file in
  `/etc/grafana/provisioning/alerting/` and restart Grafana. It creates a
  `tailscale2otel` folder and the rule groups.
- **HTTP provisioning API:** `POST /api/v1/provisioning/alert-rules` per rule (or
  use Terraform `grafana_rule_group` / [Grizzly](https://grafana.github.io/grizzly/)
  which consume this same model).
- **Grafana Cloud UI:** the file-provisioning format isn't importable via the UI's
  "Import alert rules" (that path takes Prometheus rules — use the
  `tailscale2otel.rules.yaml` file there instead). For Cloud, prefer Terraform/Grizzly
  or the provisioning API.

Wire the `severity` label (`critical` / `warning` / `info`) into your notification
policy. Thresholds, `for:` windows and the enabled/paused split all live in
`gen/build_rules.py`.

## Datasource-managed baseline (`tailscale2otel.rules.yaml`)

| Alert | Severity | Fires when | Dashboard |
|---|---|---|---|
| `ExporterDown` | critical | `tailscale2otel_up_ratio` absent or `0` for 5m | ts2otel-exporter-health |
| `CollectorScrapeFailing` | warning | `tailscale2otel_scrape_success_ratio == 0` (per `tailscale_collector`) for 15m | ts2otel-exporter-health |
| `CollectorScrapeErrorRateHigh` | warning | `rate(tailscale2otel_scrape_errors_total[5m]) > 0` (per collector) for 15m | ts2otel-exporter-health |
| `OTLPExportFailures` | warning | `rate(tailscale2otel_export_failures_total[10m]) > 0` for 15m | ts2otel-exporter-health |
| `DeviceKeyExpiringSoon` | warning | a device node key expires within 7 days (`tailscale_device_key_expiry_seconds - time()`) for 1h | ts2otel-fleet |
| `AuthKeyExpiringSoon` | warning | an auth/API key expires within 7 days (`tailscale_key_expiry_seconds - time()`) for 1h | ts2otel-fleet |
| `FlowLoggingDisabled` | warning | `tailscale_feature_enabled_ratio{tailscale_feature="network_flow_logging"} == 0` for 30m | ts2otel-network / ts2otel-audit-events |
| `NodeMetricsTargetDown` | warning | `tailscale_node_up_ratio == 0` (per `instance`) for 10m | docs/metrics.md (node scraper) |
| `FlowLogsDropped` | warning | `rate(tailscale_network_flow_logs_dropped_total[10m]) > 0` for 10m | ts2otel-audit-events |
| `NodeIPForwardingMisconfigured` | warning | `rate(tailscale_webhook_events_total{tailscale_webhook_type=~"exitNodeIPForwardingNotEnabled\|subnetIPForwardingNotEnabled"}[15m]) > 0` for 5m | — (webhook receiver) |

Notes:

- `ExporterDown` uses `absent()`, so it fires even when the exporter never came
  up. Scope it to your environment (add label matchers / a per-tenant
  external-label filter) if you run more than one instance.
- The key-expiry alerts intentionally exclude already-expired keys
  (`... > 0`) so an expired-and-abandoned key doesn't alert forever; adjust the
  `7 * 24 * 3600` threshold to taste.
- `FlowLogsDropped` surfaces the S4-7 volume guard
  (`max_log_records_per_window`) actually truncating flow **logs**. Metrics are
  never capped — only log records are dropped — so this alert means the flow log
  stream is incomplete, not that metric counters are wrong.
- `NodeIPForwardingMisconfigured` depends on the **webhook receiver** (off by
  default). The `exitNodeIPForwardingNotEnabled` / `subnetIPForwardingNotEnabled`
  health events have no log-streaming or polling equivalent, so they are emitted
  at INFO log severity and this alert is the way to surface them; without the
  webhook receiver the series never appears and the alert never fires. Subscribe
  to the events at <https://tailscale.com/kb/1213/webhooks#events>.

## Loading the rules

### Grafana-managed alerting (recommended on Grafana Cloud)

Grafana can import Prometheus-style rule groups as Grafana-managed (or
data-source-managed) alert rules.

- UI: **Alerting → Alert rules → New alert rule → Import** (or **More →
  Import alert rules from a Prometheus rules file**), upload
  `tailscale2otel.rules.yaml`, and pick the Prometheus/Mimir data source that
  holds the `tailscale*` series.
- API / IaC: convert with [`mimirtool`](https://grafana.com/docs/mimir/latest/manage/tools/mimirtool/)
  (`mimirtool rules ...`) against the Grafana Cloud ruler, or manage the file
  via Terraform (`grafana_rule_group` / cloud Mimir rules).

Routing/severity: the rules set a `severity` label (`critical` / `warning`);
wire those into your notification policy / contact points.

### Prometheus / Mimir / Cortex ruler

Drop the file in the ruler's rule path and reference it from the Prometheus
config, e.g.:

```yaml
# prometheus.yml
rule_files:
  - /etc/prometheus/rules/tailscale2otel.rules.yaml
```

then reload (`SIGHUP` or `POST /-/reload`). For Mimir/Cortex, load it with
`mimirtool rules load tailscale2otel.rules.yaml` (set `--address` /
`--id` for your tenant).

## Validating locally

The file is plain YAML plus the Prometheus rule schema.

```bash
# YAML well-formedness (no yamllint required)
python3 -c "import yaml; yaml.safe_load(open('deploy/alerts/tailscale2otel.rules.yaml'))"

# Prometheus rule schema + PromQL expression check (if promtool is installed)
promtool check rules deploy/alerts/tailscale2otel.rules.yaml
```

`promtool` is the authoritative check (it parses each `expr` as PromQL); run it
in CI if it isn't available locally.
