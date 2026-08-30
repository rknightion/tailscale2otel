# tailscale2otel Grafana dashboards

This directory contains the two generated Grafana 13+ dashboards shipped with tailscale2otel:

| Dashboard | UID | File | Use it for |
| --- | --- | --- | --- |
| Tailscale2OTel — Tailnet | `tailscale2otel-tailnet` | `tailscale2otel-tailnet.json` | Devices, network, security, and policy |
| Tailscale2OTel — Exporter health | `tailscale2otel-health` | `tailscale2otel-health.json` | Collection, ingestion, delivery, runtime, and cost |

The dashboards use Grafana's v2 schema. The operator guide owns the exact Grafana-version,
import-path, datasource, and OTLP-to-Prometheus naming contracts; read
[docs/dashboards.md](../../docs/dashboards.md) before importing them.

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

For your own deployment, follow the supported import paths in the
[operator guide](../../docs/dashboards.md#importing). Repository publication remains different:

```sh
gh workflow run grafana-sync.yml
```

The operator guide also owns datasource-variable and normalized-query-name details, avoiding a
second near-verbatim contract in this generator-oriented README.
