"""tab_policy_integrations() — the "Integrations" leaf of the Policy & Config sub-tab
group (#526).

Split out of tabs/policy.py: Tailscale Services (VIP) and the webhook-endpoint inventory,
plus the two integration families that had NO panel anywhere before #526's pending-panel
ledger — the GeoIP enrichment databases and device-posture integration sync health.

Five signals land here off the shrink-only ledger, two of them ALERTABLE-ONLY (an operator
could be paged by them today and then find nothing on any dashboard):

  * tailscale.geoip.database.build_time  -> "GeoIP database age since build"   (ALERTABLE-ONLY)
  * tailscale.geoip.downloads            -> "GeoIP database downloads"
  * tailscale.geoip.lookups              -> "GeoIP lookups by database and result"
  * tailscale.geoip.reloads              -> "GeoIP database reloads"
  * tailscale.posture_integration.error  -> "Posture integration last-sync errors" (ALERTABLE-ONLY)

Those two titles are load-bearing: alert rules are pointed at them by title.

GeoIP is process-wide shared infrastructure (internal/app/geoip.go hands it the PROCESS
emitter, same treatment as the reverse-DNS cache), so its series carry no
tailscale_tailnet / tailscale2otel_provider label and the tailnet filter is deliberately
NOT applied to them — applying it would blank the row whenever $tailnet is pinned to a
single tailnet. Posture-integration series come from a per-tailnet collector and are
filtered normally.

Sentinels declared at TAB scope: has_services and has_svc, both consumed only by the two
VIP rows here and both DASHBOARD-scoped in policy.py. The GeoIP and posture rows are
deliberately ungated with their own noValue prose: neither has an always-present
prerequisite metric distinct from the series it owns, and a row gated on a sentinel that
nothing declares renders permanently hidden — on screen, identical to a correctly-gated
empty row.
"""

from builder import (autogrid_row, bargauge_opts, loki_t, lot, merge, organize, panel,
                     prom_t, RI, row, sentinel, stat_opts, thr, ts_custom, ts_opts,
                     WIN_SLOW)
from maps import BOOL_HEALTHY_OFF, BOOL_HEALTHY_ON

DOCS = "https://m7kni.io/tailscale2otel"
CFG_DOC = DOCS + "/configuration/"

TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'
LOKI_TN = ('{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet" '
           '| tailscale2otel_provider=~"$provider"')

_INFRA_TBL = ["Time", "__name__", "job", "instance",
              "service_instance_id", "service_name", "service_namespace",
              "deployment_environment_name", "otel_scope_name", "otel_scope_version"]

# Shared empty state for the GeoIP family. Every series here additionally rides
# self_observability.enabled — the updater is only handed an Emitter then.
_GEO_EMPTY = ("No GeoIP series. Prerequisites: enrichment.geoip.enabled with a country or "
              "ASN database on disk, and self_observability.enabled. See " + CFG_DOC)
_GEO_DL_EMPTY = ("No GeoIP download series. Prerequisites: enrichment.geoip.download.enabled "
                 "with MaxMind credentials, and self_observability.enabled. Databases supplied "
                 "by an external geoipupdate cron never produce these. See " + CFG_DOC)


def sel(metric, extra=""):
    """`<metric>{<tailnet/provider filter>[, <extra>]}` — the filtered selector."""
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


def q_hist(quantile, metric):
    """histogram_quantile over a tailnet-filtered `<metric>_bucket`."""
    return ("histogram_quantile(%s, sum by (le) (rate(%s[%s])))"
            % (quantile, sel(metric + "_bucket"), RI))


def tab_policy_integrations(scope):
    sentinel("has_services", "tailscale_services_count_ratio", scope)
    sentinel("has_svc", "tailscale_service_ports", scope)

    services_vip = [
        (panel("Services (VIP)", "stat",
               [prom_t("max(%s) or vector(0)" % lot(sel("tailscale_services_count_ratio"), WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Tailscale Services (VIP services) advertised in the tailnet."), 6, 6),
        (panel("Backing hosts by service", "table",
               [prom_t(lot(sel("tailscale_service_hosts_ratio"), WIN_SLOW), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=_INFRA_TBL,
                                                       rename={"tailscale_service_name": "Service",
                                                               "tailscale_service_approval": "Approval",
                                                               "tailscale_service_configured": "Configured",
                                                               "Value": "Hosts"})],
               desc="Backing-host count per Service, bucketed by approval + configured state. "
                    "Gated by collect_hosts + cardinality.per_entity.service."), 18, 6),
    ]

    # has_svc wired (#495): both panels below read tailscale_service_ports, the
    # per-service gauge cardinality.per_entity.service switches off. The rest of
    # the Services row survives that toggle, so gating the whole row on it would
    # hide working panels.
    services_detail = [
        (panel("Port rules per service", "bargauge",
               [prom_t("max by (tailscale_service_name) (%s)" % lot(sel("tailscale_service_ports"), WIN_SLOW),
                       legend="{{tailscale_service_name}}")],
               unit="short", options=bargauge_opts(),
               desc="Port rules exposed by each Service. Gated by cardinality.per_entity.service."), 24, 6),
        # Task 1H.8 — VIP service health (merged hosts + port-rules)
        (panel("VIP service health", "table",
               [prom_t(lot(sel("tailscale_service_hosts_ratio"), WIN_SLOW), instant=True, fmt="table", refid="A"),
                prom_t("max by (tailscale_service_name) (%s)" % lot(sel("tailscale_service_ports"), WIN_SLOW),
                       instant=True, fmt="table", refid="B")],
               transformations=[merge(),
                                organize(
                                    exclude=_INFRA_TBL,
                                    rename={"tailscale_service_name": "Service",
                                            "tailscale_service_approval": "Approval",
                                            "tailscale_service_configured": "Configured",
                                            "Value #A": "Hosts",
                                            "Value #B": "Port rules"})],
               desc="Merged view: hosts + port-rule count per VIP service. "
                    "Services with 1 host have no HA. Requires collect_hosts + per_entity.service."), 24, 7),
    ]

    # ------------------------------------------------------------------
    # #403 — webhook endpoint inventory and desired-state drift
    # ------------------------------------------------------------------
    # desired_unrecognized and event_desired_covered ARE the drift pair: what the
    # operator asked Tailscale to send (collectors.webhooks.desired_events) versus
    # what Tailscale recognises as a category and what an endpoint actually covers.
    webhookinv = [
        (panel("Desired-event coverage", "table",
               [prom_t("min by (tailscale_webhook_event) (%s)"
                       % lot(sel("tailscale_webhook_endpoints_event_desired_covered_ratio"), WIN_SLOW),
                       instant=True, fmt="table")],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               transformations=[organize(exclude=_INFRA_TBL + ["Time"],
                                         rename={"tailscale_webhook_event": "Event category",
                                                 "Value": "Covered"})],
               novalue="No desired-event expectation. Prerequisites: collectors.webhooks.enabled "
                       "and a non-empty collectors.webhooks.desired_events list. See " + CFG_DOC,
               desc="1 when a category listed in collectors.webhooks.desired_events has at least "
                    "one subscribed endpoint. Emitted only for recognised desired categories — a "
                    "misspelled entry is counted by Unrecognized desired events instead of "
                    "reading as permanently uncovered."), 8, 7),
        (panel("Unrecognized desired events", "stat",
               [prom_t("max(%s)"
                       % lot(sel("tailscale_webhook_endpoints_desired_unrecognized_ratio"), WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Entries in collectors.webhooks.desired_events that are not documented "
                    "subscription categories — almost always a typo. The offending strings ride "
                    "the webhook_endpoints.event_mismatch log event, never a metric label, so "
                    "the count is what a panel can show."), 4, 7),
        (panel("Subscribers per event category", "bargauge",
               [prom_t("max by (tailscale_webhook_event) (%s)"
                       % lot(sel("tailscale_webhook_endpoints_event_subscriptions_ratio"), WIN_SLOW),
                       legend="{{tailscale_webhook_event}}")],
               unit="short", options=bargauge_opts(),
               desc="Endpoints subscribed to each documented category. The full 18-value "
                    "vocabulary is zero-seeded every tick (plus `other`), so a category nobody "
                    "listens for reads as an explicit 0 rather than a missing bar."), 12, 7),
        (panel("Per-endpoint subscriptions", "table",
               [prom_t(lot(sel("tailscale_webhook_endpoint_subscriptions_ratio"), WIN_SLOW),
                       instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA_TBL + ["Time"],
                                         rename={"tailscale_webhook_endpoint_id": "Endpoint",
                                                 "tailscale_webhook_endpoint_provider": "Provider",
                                                 "Value": "Subscriptions"})],
               novalue="No per-endpoint data. Prerequisites: collectors.webhooks.enabled and "
                       "cardinality.per_entity.webhook. See " + CFG_DOC,
               desc="How many categories each endpoint subscribes to, by endpoint id and "
                    "provider. The destination address, its signing credential and its creator are "
                    "never emitted, so this identifies an endpoint only by its id."), 14, 7),
        (panel("Webhook endpoint age (p50 / p90)", "timeseries",
               [prom_t(q_hist("0.5", "tailscale_webhook_endpoint_age_seconds"), legend="p50"),
                prom_t(q_hist("0.9", "tailscale_webhook_endpoint_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Age distribution of configured endpoints. Fleet-level with no per-endpoint "
                    "attributes; an endpoint with no creation timestamp is omitted rather than "
                    "recorded as age 0, and nothing is emitted when none reports one."), 10, 7),
        (panel("Desired-event mismatches (logs)", "table",
               [loki_t("sum by (tailscale_webhook_event) (count_over_time("
                       "%s | event_name=`tailscale.webhook_endpoints.event_mismatch` [$__range]))"
                       % LOKI_TN, instant=True)],
               transformations=[organize(exclude=["Time"],
                                         rename={"tailscale_webhook_event": "Event category",
                                                 "Value": "Observations"})],
               novalue="0",
               desc="One WARN per desired category that is uncovered or unrecognized. The body "
                    "names which of the two; the category label is bounded, reading `other` for "
                    "an unrecognized entry."), 24, 7),
    ]

    # ------------------------------------------------------------------
    # #526 — GeoIP enrichment (four ledger signals, no panel anywhere before this)
    # ------------------------------------------------------------------
    # No tailnet/provider filter: these ride the PROCESS emitter (see module docstring).
    geo_age = panel(
        "GeoIP database age since build", "bargauge",
        [prom_t("time() - max by (geoip_database) (%s)"
                % lot("tailscale_geoip_database_build_time_seconds", WIN_SLOW),
                legend="{{geoip_database}}")],
        unit="s", thresholds=thr([(None, "green"), (7 * 86400, "yellow"), (14 * 86400, "red")]),
        options=bargauge_opts(), novalue=_GEO_EMPTY,
        desc="How old the LOADED database's build is, per database (country / asn). This is "
             "MaxMind's build date, not when the file was fetched, which is exactly why it is "
             "the right staleness signal: an updater that has silently stopped still leaves a "
             "recent file mtime, but the build date keeps aging. Charted as age rather than the "
             "raw epoch so it matches the alert, "
             "`time() - tailscale_geoip_database_build_time_seconds > 14 * 86400` — the red band "
             "here IS that threshold. A database that is not loaded is absent, not stale.")
    geo_downloads = panel(
        "GeoIP database downloads", "timeseries",
        [prom_t("sum by (geoip_edition, result) (rate(tailscale_geoip_downloads_total[%s]))" % RI,
                legend="{{geoip_edition}} / {{result}}")],
        unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
        novalue=_GEO_DL_EMPTY,
        desc="MaxMind downloads by edition and result. A panel that is almost entirely "
             "`unmodified` is the GOOD state, not a stalled one: each check is conditional and a "
             "304 means the local build is already current, which is what keeps a daily updater "
             "inside MaxMind's download quota. `updated` means a newer build was fetched, "
             "SHA-256 verified and installed; `failure` means neither happened and the previous "
             "database keeps serving.")
    geo_lookups = panel(
        "GeoIP lookups by database and result", "timeseries",
        [prom_t("sum by (geoip_database, result) (rate(tailscale_geoip_lookups_total[%s]))" % RI,
                legend="{{geoip_database}} / {{result}}")],
        unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
        novalue=_GEO_EMPTY,
        desc="Enrichment lookups by database (country / asn / skipped) and result. A high "
             "`skipped` share is NORMAL on a tailnet and is not a fault: those are addresses "
             "that never reached a database at all, having been rejected as not globally "
             "routable — loopback, RFC 1918, link-local, and in particular the Tailscale CGNAT "
             "range and ULA, which are never geolocated. `hit` found a record; `miss` means the "
             "loaded database has none for that address.")
    geo_reloads = panel(
        "GeoIP database reloads", "timeseries",
        [prom_t("sum by (geoip_database, result) (rate(tailscale_geoip_reloads_total[%s]))" % RI,
                legend="{{geoip_database}} / {{result}}")],
        unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
        novalue=_GEO_EMPTY,
        desc="Hot-swaps of a database file that changed on disk, by database and result. "
             "`success` means the new file was read and swapped in atomically. A `failure` is "
             "degraded-but-working rather than an outage: the previously loaded database keeps "
             "serving lookups, so pair it with the age panel to see whether enrichment is "
             "quietly running on a frozen copy. An unchanged file is not counted at all, so a "
             "flat zero line is the steady state.")

    # ------------------------------------------------------------------
    # #526 — device-posture integration sync health (ledger signal, ALERTABLE-ONLY)
    # ------------------------------------------------------------------
    posture = [
        (panel("Posture integration last-sync errors", "table",
               [prom_t(lot(sel("tailscale_posture_integration_error_ratio"), WIN_SLOW),
                       instant=True, fmt="table", refid="A"),
                prom_t("time() - %s"
                       % lot(sel("tailscale_posture_integration_last_sync_seconds"), WIN_SLOW),
                       instant=True, fmt="table", refid="B")],
               mappings=BOOL_HEALTHY_OFF, thresholds=thr([(None, "green"), (1, "red")]),
               transformations=[merge(),
                                organize(exclude=_INFRA_TBL + ["Time"],
                                         rename={"tailscale_posture_provider": "Provider",
                                                 "tailscale_posture_integration": "Integration",
                                                 "Value #A": "Last sync errored",
                                                 "Value #B": "Last sync age"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last sync age"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               novalue="No posture-integration series. Prerequisites: "
                       "collectors.posture_integrations.enabled and a credential that can read "
                       "device-posture integrations. See " + CFG_DOC,
               desc="1 when an MDM/EDR posture integration's last sync reported an error, per "
                    "provider and integration. This is the ONLY failure signal these "
                    "integrations have: `lastSync` advances on every ATTEMPT, successful or not, "
                    "so the age column beside it goes stale only when syncing stops entirely and "
                    "stays perfectly fresh through an integration that fails every single run. "
                    "The raw error text is deliberately never emitted as a label, so read the "
                    "provider's own console for what actually broke. Age is repeated here from "
                    "the Security & Audit staleness view so both halves of the pair are on one "
                    "row."), 24, 7),
    ]

    return [
        row("Services / VIP", services_vip, present="has_services"),
        # has_svc wired (#495): per-service port gauges only
        row("VIP service detail", services_detail, present="has_svc"),
        row("Webhook endpoint inventory", webhookinv),
        # Four same-size panels -> AutoGrid, which reflows to the viewport (#526 decision 6).
        autogrid_row("GeoIP enrichment", [geo_age, geo_downloads, geo_lookups, geo_reloads],
                     max_columns=2),
        row("Device-posture integrations", posture),
    ]
