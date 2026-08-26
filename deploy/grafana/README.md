# tailscale2otel Grafana dashboards

This directory contains the two generated Grafana 13+ dashboards shipped with tailscale2otel:

| Dashboard | UID | File | Use it for |
| --- | --- | --- | --- |
| Tailscale2OTel — Tailnet | `tailscale2otel-tailnet` | `tailscale2otel-tailnet.json` | Devices, network, security, and policy |
| Tailscale2OTel — Exporter health | `tailscale2otel-health` | `tailscale2otel-health.json` | Collection, ingestion, delivery, runtime, and cost |

The dashboards use Grafana's v2 schema (`dashboard.grafana.app/v2`) with tabs, nested navigation,
and conditional rendering. Grafana 13 or later is required. Grafana 12.4 accepts these resources but
renders them blank; Grafana 11.5 rejects them with the misleading error `Dashboard title cannot be
empty`.

Both dashboards are generated from `gen/build.py` and `gen/dashboards.py`. Edit the generator, not
the JSON. Regenerate both committed resources from this directory with:

```sh
python3 gen/build.py --out-dir .
```

For a focused preview of one dashboard or tab:

```sh
python3 gen/build.py --dashboard tailnet --tab "Network & Flows" --out /tmp/tailnet-network.json
```

## Publishing and importing

This repository publishes dashboard changes through `.github/workflows/grafana-sync.yml` into the
Grafana GitSync source. Do not push repository-owned dashboards directly with `gcx`; an API push is
an out-of-band edit that leaves the repository and Grafana disagreeing.

For your own Grafana 13+ deployment, import both resources with one of these supported paths:

```sh
gcx resources push -f tailscale2otel-tailnet.json
gcx resources push -f tailscale2otel-health.json
```

You can also use **Dashboards → New → Import → Upload JSON file** in Grafana, or place both JSON
files in a file-provisioned dashboard directory. The classic `POST /api/dashboards/db` endpoint
accepts v1 dashboard bodies and does not apply to these resources.

## Datasources and query names

Panels resolve their datasources through variables rather than pinned UIDs:

- `${DS_PROM}` / `ds_prometheus` selects a Prometheus datasource.
- `${DS_LOKI}` / `ds_loki` selects a Loki datasource.

Grafana Cloud normalizes OTLP metric names before they reach PromQL. Dashboard queries therefore use
normalized names: dots become underscores, counters gain `_total`, supported units add a suffix,
and unit-`1` gauges gain `_ratio`. See the
[metrics reference](../../docs/metrics.md) for the full mapping.

The operator-facing dashboard guide, including every tab and the legacy-dashboard migration map, is
in [docs/dashboards.md](../../docs/dashboards.md).
