"""tab_policy_dns() — the "DNS & Settings" leaf of the Policy & Config sub-tab group (#526).

Split out of tabs/policy.py. Two rows: the tailnet's DNS configuration (MagicDNS, name
servers, search paths, split-DNS zones, resolvers) and the tailnet-wide feature/setting
switches that sit alongside it.

Consolidation (#526 decision 7): the old single-panel "DNS resolvers" row is merged into
"DNS". It was gated `present="has_dns_resolver"`, and that gate is deliberately NOT carried
over — a hidden row is indistinguishable on screen from a correctly-gated empty one, so the
Resolvers table now states its own prerequisites in noValue prose instead. Nothing on this
leaf declares a sentinel as a result; there is no gated row left to consume one, and a
declared sentinel that gates nothing fails the build.
"""

from builder import (barchart_opts, lot, organize, panel, prom_t, row, stat_opts, thr,
                     WIN_SLOW)
from maps import BOOL_HEALTHY_ON, BOOL_NEUTRAL

DOCS = "https://m7kni.io/tailscale2otel"
CFG_DOC = DOCS + "/configuration/"

_INFRA_TBL = ["Time", "__name__", "job", "instance",
              "service_instance_id", "service_name", "service_namespace"]


def tab_policy_dns(scope):
    del scope  # no tab-scoped sentinels on this leaf; see module docstring

    dns = [
        (panel("MagicDNS", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_magic_dns_ratio", WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Whether MagicDNS is turned on for the tailnet. With it off, devices "
                    "resolve each other by address only and every search-path and split-DNS "
                    "setting below has no effect."), 6, 5),
        (panel("Nameservers", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_nameservers_count_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               desc="Global nameservers configured for the tailnet. Zero means devices keep "
                    "their local resolvers."), 6, 5),
        (panel("Search paths", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_search_paths_count_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               desc="Search domains appended to unqualified names on every device. Each one "
                    "adds a resolution attempt, so a long list slows first-lookup latency."), 6, 5),
        (panel("Split-DNS zones", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_split_zones_count_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               desc="Zones routed to a specific resolver rather than the global nameservers. "
                    "The per-zone resolver addresses are in the Resolvers table below."), 6, 5),
        # Task 1.6 Step 1 — A3 DNS additions (stats; ungated)
        (panel("Override local DNS", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_override_local_ratio", WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               desc="Whether the tailnet's nameservers replace each device's local resolvers "
                    "outright, rather than being added alongside them."), 6, 5),
        (panel("Exit-node resolvers", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_resolvers_use_with_exit_node_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               desc="Configured resolvers marked usable while a device is routing through an "
                    "exit node."), 6, 5),
        # Task 1.6 Step 1 — Search domains barchart (no resolver-presence gate needed)
        (panel("Search domains", "barchart",
               [prom_t("count by (tailscale_dns_search_path_domain) (%s)" % lot("tailscale_dns_search_path_ratio", WIN_SLOW),
                       legend="{{tailscale_dns_search_path_domain}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="The individual search domains behind the Search paths count, one bar "
                    "each — the fastest way to spot a stale or duplicated entry."), 12, 6),
        # Merged in from the old single-panel "DNS resolvers" row (#526 decision 7).
        (panel("Resolvers", "table",
               [prom_t("%s" % lot("tailscale_dns_resolver_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_INFRA_TBL + ["Value"],
                   rename={"tailscale_dns_resolver_address": "Address",
                           "tailscale_dns_resolver_kind": "Kind",
                           "tailscale_dns_resolver_use_with_exit_node": "ExitNode"})],
               novalue="No resolver series. Prerequisites: collectors.dns.enabled and a tailnet "
                       "with at least one global or split-DNS resolver configured. See " + CFG_DOC,
               desc="DNS resolver configuration: each resolver's address, kind (global or "
                    "split-DNS) and whether it stays in use behind an exit node. FIX-3: no "
                    "domain label on the live wire, so a split-DNS resolver is identified by "
                    "address rather than by the zone it serves."), 24, 6),
    ]

    settings = [
        # Neutral map: a tailnet setting being off is a fact, not a verdict — the risky
        # ones have their own colour-coded panels.
        (panel("Tailnet settings", "table",
               [prom_t(lot("tailscale_setting_enabled_ratio", WIN_SLOW), instant=True, fmt="table")],
               mappings=BOOL_NEUTRAL,
               transformations=[organize(exclude=_INFRA_TBL,
                                         rename={"tailscale_setting_name": "Setting", "Value": "Enabled"})],
               desc="Per-setting enabled (1) / disabled (0)."), 8, 7),
        (panel("Device key duration", "stat",
               [prom_t("max(%s)" % lot("tailscale_setting_devices_key_duration_days", WIN_SLOW))],
               unit="d", options=stat_opts(),
               desc="How long a device key stays valid before the device must re-authenticate. "
                    "The tailnet-wide default; individual devices may have key expiry turned "
                    "off, which the Fleet view reports."), 4, 7),
        (panel("Tailnet features", "table",
               [prom_t(lot("tailscale_feature_enabled_ratio", WIN_SLOW), instant=True, fmt="table")],
               mappings=BOOL_NEUTRAL,
               transformations=[organize(exclude=_INFRA_TBL,
                                         rename={"tailscale_feature": "Feature", "Value": "Enabled"})],
               desc="Per-feature enabled (1) / disabled (0)."), 12, 7),
        # Task 1H.8 — External-tailnets role
        (panel("External-tailnets role", "stat",
               [prom_t("max by (tailscale_setting_role) (%s)" % lot("tailscale_setting_users_external_tailnets_role_ratio", WIN_SLOW),
                       legend="{{tailscale_setting_role}}")],
               unit="short", options=stat_opts(),
               desc="Role granted to users joining from external tailnets. "
                    "Values: none / member / admin. Live: role=none."), 6, 5),
        # Task 1H.8 — Webhook endpoints
        # No zero-fill: the collector emits an explicit 0 when the tailnet exposes no
        # webhook surface, but emits NOTHING when the credential is rejected or
        # under-scoped — so a manufactured 0 hides a permissions problem.
        (panel("Webhook endpoints", "stat",
               [prom_t("max(%s)" % lot("tailscale_webhook_endpoints_count_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               novalue="No webhook data — needs the webhooks collector and a credential "
                       "scoped to read webhooks.",
               desc="Configured webhook endpoints on the tailnet. The Integrations sub-tab "
                    "breaks the same population down by subscribed event category and by "
                    "endpoint."), 6, 5),
    ]

    return [
        row("DNS", dns),
        row("Settings & features", settings),
    ]
