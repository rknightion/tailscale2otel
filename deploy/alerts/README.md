# tailscale2otel — Alerting rules

Prometheus-style alerting rules that complement the four Grafana dashboards in
[`../grafana/`](../grafana/). One file:

- [`tailscale2otel.rules.yaml`](tailscale2otel.rules.yaml) — standard
  `groups:` / `rules:` rule-group YAML (`alert:` / `expr:` / `for:` / `labels:`
  / `annotations:`).

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

## Alerts

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
