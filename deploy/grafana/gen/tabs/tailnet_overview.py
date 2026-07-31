"""tab_tailnet_overview() — the tailscale2otel-tailnet dashboard's "Overview" tab (#526).

The first tab every operator opens. It reads top-down as one question at a time:

    is the tailnet healthy  ->  is the exporter feeding it healthy  ->  what is
    happening  ->  what is it capable of  ->  what is risky

and every conditional row sits BELOW the always-on ones, so a single-tailnet
deployment with a healthy exporter sees an uninterrupted always-on stack.

Assembled from the pre-split `tabs/overview.py` (7 rows / 34 panels) plus two tabs
that #526 dissolved into it — `tabs/tailnets.py` (decision 5) and the event-rate
summary from `tabs/events.py` (decision 9) — under a hard <=30-panel budget. What
changed, and why:

* **"Service health (golden signals)" is GONE** (5 panels). Those were the
  EXPORTER's golden signals on a tab that answers a question about the TAILNET,
  and the companion tailscale2otel-health dashboard's own Overview already opens
  with a "Golden signals" row carrying the same reading. Every metric it took with
  it (api/export duration histograms, scrape budget, series active/limit, export
  datapoints + log records) is panelled on that dashboard's
  Collection / Delivery / Cost & Cardinality tabs.
* **"Exporter health" is cut to a permanent 4-panel strip** (decision 2). Without
  *some* exporter reading here a broken collector reads as a quiet tailnet, so the
  strip stays unconditionally; but it answers only "is the feed alive?" — up,
  collectors OK, scrape errors, export failures. The two cardinality/enrichment
  internals it used to carry (series active, enrich-cache size) are health-dashboard
  content and moved there.
* **A gated "Exporter degradation detail" row** pairs with that strip (decision 2):
  it renders only when `is_degraded` fires, and its whole job is to say WHICH
  collector or stage is degraded and SINCE WHEN — enough to decide whether to follow
  the "Exporter health ->" cross-link in the controls menu. It deliberately does not
  reproduce the diagnosis; that is what the link is for.
* **The `Tailnets` tab folded in** (decision 5) and merged with the existing
  "MSP / multi-tailnet summary" row rather than being concatenated onto it — the two
  overlapped on three of seven panels.
* **The event-rate summary folded in** (decision 9) as its own always-on row; the log
  explorer and per-signal streams stay with their owning domain tabs.

Sentinel scoping (#526): all three are declared HERE, at this tab's own scope.
`is_degraded` gates the degradation row and nothing else. `has_multitailnet` and
`has_acl_risk` were DASHBOARD-scoped, declared by `tabs/tailnets.py` and
`tabs/security.py` — both of which this wave dissolved, so the declarations had to
travel with the consumer rather than be assumed to survive. `has_acl_risk` is now
declared at two tab scopes (here and `tabs/security_risk.py`), which is the ≤2-tab
duplication case and is safe: Grafana resolves a tab-scoped variable per tab.

A row gating on a sentinel nothing declares in ITS OWN scope renders permanently
hidden, and on screen that is indistinguishable from a correctly-gated row that
simply has no data — which is why `test_sentinel_registry` checks resolution per
scope rather than merely that the name exists somewhere.
"""

from builder import (autogrid_row, lot, merge, organize, panel, prom_t, raw_sentinel, RI,
                     row, sentinel, stat_opts, thr, ts_custom, ts_opts, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON, UP_MAP


# The controls-menu cross-link the degradation row points at (see builder.dashboard_link
# and dashboards.py's TAILNET.sibling_title). builder.panel() hardcodes `links: []`, so a
# panel description is the only markdown surface available to name it.
HEALTH_LINK = ('Follow "Exporter health →" in the controls menu for the full '
               'per-stage diagnosis.')

# tailscale_tailnet is a real per-series metric label (roadmap item L) — filter it
# directly, no target_info join. Excludes "" and the "-" single-tailnet placeholder.
_TN = 'tailscale_tailnet!="", tailscale_tailnet!="-"'

# Resource/infra columns stripped from the per-tailnet tables. Deliberately NOT
# builder.TBL_NOISE: these tables key on `tailscale_tailnet` / `tailscale2otel_provider`
# and must keep the Value column, which TBL_NOISE drops.
_TBL_INFRA = ["Time", "__name__", "job", "instance", "service_instance_id",
              "service_name", "service_namespace"]


# Four tiles on this tab duplicate a panel the health dashboard owns, so they carry a
# "(summary)" suffix. That is not cosmetic: an alert rule links to its panel BY TITLE,
# across the whole family, and build_rules.py raises on a title matching more than one
# panel. Without the suffix, "Component errors/s" matched two panels on two dashboards
# and the alert catalogue would not build at all. The unsuffixed title always belongs to
# the canonical, detailed panel on tailscale2otel-health; the copy here is the glance
# version that tells a reader whether to cross over.


def tab_tailnet_overview(scope):
    # Gates the degradation-detail row below, and nothing else on this dashboard —
    # hence this tab's scope rather than DASHBOARD.
    # NOT sentinel("...scrape_errors_total"): that is a PRESENCE check, and the
    # counter exists from the first scrape whether or not it has ever been
    # incremented — so the row rendered on a perfectly healthy exporter, showing
    # two "No data" panels. Verified on a live render before this was fixed.
    # Decision 2 asks for a row that appears when the exporter IS degraded, which
    # is a threshold question, so it needs a raw query rather than presence.
    #
    # Fires when a collector's LAST scrape failed (a live condition) or an export
    # failed within the hour (a counter, so recent history). Each side zero-fills
    # independently, or a deployment missing one family entirely would drag the sum
    # to no-result and hide the row exactly when the other side was firing.
    raw_sentinel(
        "is_degraded",
        "query_result("
        "(count(tailscale2otel_scrape_success_ratio == 0) or vector(0)) + "
        "(sum(increase(tailscale2otel_export_failures_total[1h])) or vector(0)) > 0)",
        scope)
    # Re-homed here in #526 wave 3. Both names used to be declared at DASHBOARD scope by
    # modules that no longer exist: has_multitailnet by tabs/tailnets.py, which decision 5
    # folds into this tab, and has_acl_risk by tabs/security.py, which decision 5 splits
    # into four sub-tabs. A row gating on a sentinel nothing declares renders PERMANENTLY
    # HIDDEN and is indistinguishable on screen from a correctly-gated row with no data,
    # so the declaration has to travel with the consumer, not be assumed to survive.
    #
    # has_acl_risk is also declared, at its own scope, by tabs/security_risk.py. That is
    # the ≤2-tab duplication case, not a collision: Grafana resolves a tab-scoped variable
    # per tab (verified live by rendering two tabs that declared the same name over
    # opposite-truth queries and confirming each row gated on its own).
    sentinel("has_acl_risk", "tailscale_acl_unrestricted_rules_ratio", scope)
    # Not a plain metric-presence check: it gates on ">1 distinct tailnet observed",
    # excluding "" and the "-" single-tailnet placeholder so an unnamed-tailnet series
    # cannot false-positive a single-tailnet deployment into the MSP row.
    raw_sentinel(
        "has_multitailnet",
        'query_result(count(count by (tailscale_tailnet) '
        '({__name__=~"tailscale_.+", tailscale_tailnet!="", tailscale_tailnet!="-"})) > 1)',
        scope)

    # --- 1. is the tailnet healthy? -----------------------------------------
    #
    # "Devices online" / "Total devices" / "Offline" were three separate stat panels
    # over one gauge, which is the cheapest merge available anywhere on this tab: the
    # three readings are three reductions of `tailscale_device_online_ratio`, so
    # folding them into one three-value stat costs no signal at all. The per-panel
    # red/green background on the old "Devices online" tile is the only casualty —
    # "1 device online" is not a threshold anyone can set fleet-independently, and the
    # trend panel in Activity is the honest version of that question.
    health = [
        (panel("Devices online / total / offline", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_device_online_ratio"),
                       legend="online"),
                prom_t("count(%s) or vector(0)" % lot("tailscale_device_online_ratio"),
                       legend="total"),
                prom_t("count(%s == 0) or vector(0)" % lot("tailscale_device_online_ratio"),
                       legend="offline")],
               unit="short", options=stat_opts(color="value"),
               desc="Devices currently reporting online, the total the exporter can see, and "
                    "the remainder offline (normal for laptops and phones)."), 6, 5),
        # No zero-fill: update availability is suppressed entirely when the control plane
        # does not report it (Headscale) or per-device metrics are off, and "0 updates
        # needed" is a different statement from "we were not told" (#385).
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_update_available_ratio"))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="value"),
               novalue="No device update data — needs the devices collector and a control plane "
                       "that reports update availability.",
               desc="Devices with a Tailscale client update waiting."), 4, 5),
        (panel("Users", "stat",
               [prom_t("sum(max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s)) or vector(0)"
                       % lot("tailscale_users_count_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               desc="Users known to the tailnet, across every role, status and type."), 4, 5),
        (panel("Device keys ≤7d", "stat",
               [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_device_key_expiry_seconds", WIN_SLOW),
                          lot("tailscale_device_key_expiry_seconds", WIN_SLOW)))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]),
               options=stat_opts(color="background"),
               desc="Device node keys expiring within 7 days."), 4, 5),
        (panel("ACL changed", "stat",
               [prom_t("time() - max(%s)" % lot("tailscale_acl_last_changed_seconds", WIN_SLOW))],
               unit="s", options=stat_opts(graph="none"),
               desc="Time since the ACL policy last changed."), 3, 5),
        (panel("Flow logging", "stat",
               [prom_t("max(%s)" % lot('tailscale_feature_enabled_ratio{tailscale_feature="network_flow_logging"}',
                                       WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Tailnet network-flow-logging feature state."), 3, 5),
    ]

    # --- 2. is the exporter feeding it healthy? ------------------------------
    #
    # Permanent strip, #526 decision 2 — "without it a broken collector reads as a
    # quiet tailnet". Four panels, one question: is the feed alive? Four same-size
    # stats, so autogrid rather than a fixed 24-column split (decision 6).
    exporter = [
        panel("Exporter up (summary)", "stat", [prom_t("max(%s)" % lot("tailscale2otel_up_ratio"))],
              mappings=UP_MAP, thresholds=thr([(None, "red"), (1, "green")]),
              options=stat_opts(color="background"),
              desc="Whether the exporter process feeding this dashboard is reporting alive. "
                   "Down means every panel here is stale, not that the tailnet is quiet."),
        panel("Collectors OK (summary)", "stat",
              [prom_t("count(%s == 1) or vector(0)" % lot("tailscale2otel_scrape_success_ratio"))],
              unit="short", thresholds=thr([(None, "green")]), options=stat_opts(color="value"),
              desc="Collectors whose last scrape succeeded. A drop here empties whichever "
                   "panels that collector feeds; the degradation row below names it."),
        panel("Scrape errors/s (summary)", "stat",
              [prom_t("sum(rate(tailscale2otel_scrape_errors_total[%s])) or vector(0)" % RI)],
              unit="cps", thresholds=thr([(None, "green"), (0.001, "red")]),
              options=stat_opts(color="background"),
              desc="Collector scrape attempts failing per second, fleet-wide. Non-zero opens "
                   "the degradation-detail row below."),
        panel("Export failures/s", "stat",
              [prom_t("sum(rate(tailscale2otel_export_failures_total[%s])) or vector(0)" % RI)],
              unit="cps", thresholds=thr([(None, "green"), (0.001, "red")]),
              options=stat_opts(color="background"),
              desc="OTLP exports failing per second. Data is being collected but not "
                   "delivered, so panels here go stale while the tailnet is fine."),
    ]

    # --- 3. what is happening? ----------------------------------------------
    #
    # Deliberate asymmetry is absent here (three equal-width timeseries), but the
    # panels differ in what they plot rather than in size, so autogrid is correct.
    activity = [
        panel("Network throughput", "timeseries",
              [prom_t("sum(rate(tailscale_network_io_rollup_bytes_total[%s])) or "
                      "sum(rate(tailscale_network_io_bytes_total[%s]))" % (RI, RI),
                      legend="throughput")],
              unit="Bps", custom=ts_custom(fill=20), options=ts_opts(),
              desc="Total flow throughput (rollup if present, else raw)."),
        panel("Audit & flow events/s", "timeseries",
              [prom_t("sum(rate(tailscale_config_audit_events_total[%s]))" % RI, legend="audit/s"),
               prom_t("sum(rate(tailscale_network_flows_total[%s]))" % RI, legend="flows/s")],
              unit="cps", custom=ts_custom(), options=ts_opts(),
              desc="Combined tailnet event rate — configuration-audit events and network "
                   "flows. The audit line is broken down by action and origin in the row "
                   "below."),
        panel("Devices online over time", "timeseries",
              [prom_t("count(tailscale_device_online_ratio == 1)", legend="online"),
               prom_t("count(tailscale_device_online_ratio)", legend="total")],
              unit="short", custom=ts_custom(fill=10), options=ts_opts(),
              desc="Online device count against the total the exporter can see — the trend "
                   "behind the Tailnet health tiles."),
    ]

    # Event-rate summary, folded in from tabs/events.py's "Audit & event rates" row
    # (#526 decision 9). That row shipped three panels; the third, an "Audit events
    # (range)" stat, was `sum(increase(tailscale_config_audit_events_total[$__range]))`
    # — the same metric these two already plot, reduced to a single number that the
    # combined-rate panel above already conveys. Dropped rather than carried, so the
    # fold costs one panel of the budget instead of three and no signal at all.
    rates = [
        panel("Audit events/s by action", "timeseries",
              [prom_t("sum by (tailscale_audit_action) (rate(tailscale_config_audit_events_total[%s]))" % RI,
                      legend="{{tailscale_audit_action}}")],
              unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
              desc="Configuration-audit events per second, split by the action performed."),
        panel("Audit events/s by origin", "timeseries",
              [prom_t("sum by (tailscale_audit_origin) (rate(tailscale_config_audit_events_total[%s]))" % RI,
                      legend="{{tailscale_audit_origin}}")],
              unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
              desc="The same audit events split by where they originated (admin console, API, "
                   "client, ...)."),
    ]

    # --- 4. what is it capable of? ------------------------------------------
    capabilities = [
        panel("Tailnet features", "timeseries",
              [prom_t("max by (tailscale_feature) (tailscale_feature_enabled_ratio)",
                      legend="{{tailscale_feature}}")],
              unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=0, points="always"),
              options=ts_opts(placement="right"),
              desc="Per-feature enabled (1) / disabled (0)."),
        panel("Tailnet settings", "timeseries",
              [prom_t("max by (tailscale_setting_name) (tailscale_setting_enabled_ratio)",
                      legend="{{tailscale_setting_name}}")],
              unit="short", min_=0, max_=1, custom=ts_custom(style="line", fill=0, points="always"),
              options=ts_opts(placement="right"),
              desc="Per-setting enabled (1) / disabled (0) for the tailnet's configuration "
                   "switches."),
    ]

    # --- gated: exporter degradation detail (is_degraded) --------------------
    #
    # The strip above says "something is wrong"; this says WHICH collector or stage and
    # SINCE WHEN, which is exactly the amount needed to decide whether to cross over to
    # tailscale2otel-health. No diagnosis here — that duplicates a whole dashboard.
    degraded = [
        panel("Scrape errors/s by collector", "timeseries",
              [prom_t("sum by (tailscale_collector) (rate(tailscale2otel_scrape_errors_total[%s]))" % RI,
                      legend="{{tailscale_collector}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
              desc="Which collector is failing, and how hard. Whatever panels that collector "
                   "feeds are the ones reading empty for the wrong reason. " + HEALTH_LINK),
        panel("Last scrape age by collector", "table",
              [prom_t("time() - %s" % lot("tailscale2otel_scrape_last_timestamp_seconds"),
                      instant=True, fmt="table")],
              unit="s",
              transformations=[organize(exclude=_TBL_INFRA,
                                        rename={"tailscale_collector": "Collector",
                                                "Value": "Age"})],
              desc="Since when: seconds elapsed since each collector last completed a scrape. "
                   "A row aging past its configured interval is a collector that stopped "
                   "running rather than one that is erroring. " + HEALTH_LINK),
        panel("Component errors/s (summary)", "timeseries",
              [prom_t("sum by (component) (rate(tailscale2otel_component_errors_total[%s]))" % RI,
                      legend="{{component}}")],
              unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
              desc="Which internal stage is erroring when no single collector is — processors, "
                   "receivers, the checkpoint store. " + HEALTH_LINK),
    ]

    # --- gated: MSP / multi-tailnet (has_multitailnet) -----------------------
    #
    # #526 decision 5: the `Tailnets` tab (3 panels) folds in here rather than surviving
    # as a tab, and the two overlapped heavily — both opened with a "Tailnets observed"
    # stat, and the old Overview's "Devices per tailnet" bargauge, its "Tailnets" table
    # (a raw per-tailnet series count) and the folded-in per-tailnet trend were three
    # readings of the same thing. Consolidated to four:
    #   * one "Tailnets observed" stat (the Tailnets-tab query, which excludes the "-"
    #     placeholder — the Overview's did not, and its `or vector(1)` fallback was dead
    #     code inside a row that only renders when >1 tailnet is present);
    #   * the Tailnets tab's richer per-tailnet scorecard table, which SUPERSEDES the
    #     Overview's series-count table (that one counted `{__name__=~"tailscale_.+"}`,
    #     an infra reading, not a tailnet one);
    #   * Providers, kept as-is — the only surface for tailscale2otel_provider here;
    #   * the per-tailnet trend, kept over the "Devices per tailnet" bargauge: both read
    #     tailscale_device_online_ratio, the scorecard already gives the current count,
    #     so the trend is the reading the table cannot provide.
    # has_multitailnet is DASHBOARD-scoped and declared elsewhere — referenced only.
    msp = [
        (panel("Tailnets observed", "stat",
               [prom_t('count(count by (tailscale_tailnet) '
                       '({__name__=~"tailscale_.+", %s}))' % _TN)],
               unit="short", options=stat_opts(color="value"),
               desc="Number of distinct tailnets observed by this exporter instance "
                    "(excluding the placeholder '-')."), 6, 5),
        (panel("Providers", "table",
               [prom_t('count by (tailscale2otel_provider) '
                       '({__name__=~"tailscale.+", tailscale2otel_provider=~"$provider", '
                       'tailscale2otel_provider!=""})', instant=True, fmt="table")],
               transformations=[organize(exclude=_TBL_INFRA,
                                         rename={"tailscale2otel_provider": "Provider",
                                                 "Value": "Series"})],
               desc="Control-plane provider (tailscale, headscale) and its active series "
                    "count."), 18, 5),
        (panel("Tailnet scorecard", "table",
               [prom_t('sum by (tailscale_tailnet) (tailscale_device_online_ratio{%s} == 1)' % _TN,
                       instant=True, fmt="table"),
                prom_t('max by (tailscale_tailnet) (tailscale2otel_scrape_staleness_seconds{%s})' % _TN,
                       instant=True, fmt="table"),
                prom_t('sum by (tailscale_tailnet) (rate(tailscale2otel_api_requests_total'
                       '{http_response_status_code=~"4..|5..", %s}[%s]))' % (_TN, RI),
                       instant=True, fmt="table")],
               transformations=[
                   merge(),
                   organize(exclude=_TBL_INFRA,
                            rename={"tailscale_tailnet": "Tailnet",
                                    "Value #A": "Online devices",
                                    "Value #B": "Max staleness (s)",
                                    "Value #C": "API errors/s"})],
               overrides=[{"matcher": {"id": "byName", "options": "Max staleness (s)"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per-tailnet health scorecard: online device count, worst scrape "
                    "staleness, and API error rate."), 24, 8),
        (panel("Per-tailnet online devices over time", "timeseries",
               [prom_t('sum by (tailscale_tailnet) (tailscale_device_online_ratio{%s} == 1)' % _TN,
                       legend="{{tailscale_tailnet}}")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts(placement="right"),
               desc="Count of online devices per tailnet over time."), 24, 7),
    ]

    # --- gated: what is risky (has_acl_risk) ---------------------------------
    #
    # has_acl_risk is DASHBOARD-scoped and declared by tabs/security.py — referenced
    # only. Carried over verbatim from the pre-split Overview, #395 comments included:
    # three of these panels are fed by the DEVICES collector inside a row gated on the
    # ACL collector's sentinel, so they must not zero-fill.
    scorecard = [
        (panel("Unrestricted ACL rules", "stat",
               [prom_t("sum(%(e)s) or vector(0)" % {"e": lot("tailscale_acl_unrestricted_rules_ratio", WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Total ACL rules that grant unrestricted access (wildcard src/dst). Any non-zero value warrants review."), 4, 5),
        (panel("Auto-approved exit nodes", "stat",
               [prom_t('sum(%(e)s) or vector(0)' % {
                   "e": lot('tailscale_acl_autoapprovers_ratio{tailscale_acl_autoapprover_kind="exit_node"}', WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]),
               options=stat_opts(color="background"),
               desc="ACL auto-approver entries for exit-node routes. Review whether automatic exit-node approval is intended."), 4, 5),
        # #395: this row is gated on has_acl_risk (acl collector), but this panel's
        # metric comes from the devices collector. Zero-filling here would render a
        # healthy-looking "0" when devices is absent rather than never checked.
        (panel("Unapproved subnet routes", "stat",
               [prom_t("max(%(e)s)" % {"e": lot("tailscale_subnet_routes_unapproved", WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No subnet-route data — needs the devices collector "
                       "(collectors.devices) and a control plane that reports approved routes.",
               desc="Subnet routes advertised but not yet approved by an admin."), 4, 5),
        # #395: same gating mismatch as above — devices collector, acl-gated row.
        (panel("Untagged devices", "stat",
               [prom_t("max(%(e)s)" % {"e": lot("tailscale_devices_untagged_ratio")},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"),
               novalue="No device-tag data — needs the devices collector (collectors.devices) "
                       "and a control plane that reports device tags.",
               desc="Devices not associated with any ACL tag — harder to audit and apply granular policies to."), 4, 5),
        # #395: same gating mismatch — device-invite data comes from the devices
        # collector, not acl.
        (panel("Pending exit-node shares", "stat",
               [prom_t('sum(%(e)s)' % {
                   "e": lot('tailscale_device_invites_count_ratio{tailscale_device_invite_accepted="false",tailscale_device_invite_allow_exit_node="true"}', WIN_SLOW)},
                       instant=True)],
               unit="short",
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No device-invite data — needs the devices collector "
                       "(collectors.devices.collect_device_invites).",
               desc="Pending device share invitations that grant exit-node access."), 4, 5),
        (panel("SSH wildcard enabled", "stat",
               [prom_t("max(%(e)s) or vector(0)" % {"e": lot("tailscale_acl_ssh_wildcard_ratio", WIN_SLOW)},
                       instant=True)],
               unit="short",
               # Inverse risk: a wildcard SSH rule (1) is the state to act on. The
               # scorecard rates it yellow, so the map is yellow too, not red.
               mappings=bool_map(off_color="green", on_color="yellow"),
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Whether the tailnet ACL contains a wildcard SSH rule."), 4, 5),
    ]

    return [
        # always-on, in reading order
        row("Tailnet health", health),
        autogrid_row("Exporter health", exporter),
        autogrid_row("Activity", activity),
        autogrid_row("Audit & event rates", rates),
        autogrid_row("Capabilities", capabilities),
        # gated, below the always-on stack
        autogrid_row("Exporter degradation detail", degraded, present="is_degraded"),
        row("MSP / multi-tailnet summary", msp, present="has_multitailnet"),
        row("Security scorecard", scorecard, present="has_acl_risk"),
    ]
