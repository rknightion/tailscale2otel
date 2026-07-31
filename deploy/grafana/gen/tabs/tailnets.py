"""tab_tailnets() — moved out of build.py in the module split."""

from builder import (merge, organize, panel, prom_t, raw_sentinel, RI, row, stat_opts,
                     ts_custom, ts_opts)
from builder import DASHBOARD  # #526: wave 1 leaves every sentinel dashboard-level


def tab_tailnets(scope):
    """MSP / multi-tailnet scorecard tab — gated by has_multitailnet (hidden on single-tailnet)."""
    # has_multitailnet declared here (moved from variables.py, #495): this is the tab it is
    # named after and gates (see build.py's tab_defs), even though the query itself is also
    # consumed by rows in overview.py and diagnostics.py — not by this function's own rows.
    # Not a plain metric-presence check (no single metric to hand to sentinel(, DASHBOARD)): it gates on
    # ">1 distinct tailnet observed", excluding "" and "-" (single-tailnet placeholder) so
    # placeholder/unnamed-tailnet series don't false-positive on single-tailnet deployments.
    raw_sentinel(
        "has_multitailnet",
        'query_result(count(count by (tailscale_tailnet) '
        '({__name__=~"tailscale_.+", tailscale_tailnet!="", tailscale_tailnet!="-"})) > 1)', DASHBOARD)

    # tailscale_tailnet is a real per-series label now (item L) — filter it directly, no join.
    _tn = 'tailscale_tailnet!="", tailscale_tailnet!="-"'

    scorecard = [
        (panel("Tailnets observed", "stat",
               [prom_t('count(count by (tailscale_tailnet) '
                       '({__name__=~"tailscale_.+", tailscale_tailnet!="", tailscale_tailnet!="-"}))')],
               unit="short", options=stat_opts(color="value"),
               desc="Number of distinct tailnets observed by this exporter instance (excluding placeholder '-')."), 6, 5),
        (panel("Tailnet scorecard", "table",
               [prom_t('sum by (tailscale_tailnet) (tailscale_device_online_ratio{%s} == 1)' % _tn,
                       instant=True, fmt="table"),
                prom_t('max by (tailscale_tailnet) (tailscale2otel_scrape_staleness_seconds{%s})' % _tn,
                       instant=True, fmt="table"),
                prom_t('sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total{http_response_status_code=~"4..|5..", %s}[%s]))' % (_tn, RI),
                       instant=True, fmt="table")],
               transformations=[
                   merge(),
                   organize(
                       exclude=["Time", "__name__", "job", "instance",
                                "service_instance_id", "service_name", "service_namespace"],
                       rename={"tailscale_tailnet": "Tailnet",
                               "Value #A": "Online devices",
                               "Value #B": "Max staleness (s)",
                               "Value #C": "API errors/s"})],
               overrides=[{"matcher": {"id": "byName", "options": "Max staleness (s)"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per-tailnet health scorecard: online device count, worst scrape staleness, and API error rate."), 24, 8),
    ]
    trends = [
        (panel("Per-tailnet online devices over time", "timeseries",
               [prom_t('sum by (tailscale_tailnet) (tailscale_device_online_ratio{%s} == 1)' % _tn,
                       legend="{{tailscale_tailnet}}")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts(placement="right"),
               desc="Count of online devices per tailnet over time."), 24, 7),
    ]
    return [row("MSP scorecard", scorecard), row("Per-tailnet trends", trends)]
