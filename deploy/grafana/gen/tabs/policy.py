"""tab_policy() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, loki_t, lot, merge, organize, panel,
                     PII, prom_t, row, stat_opts, thr, WIN_SLOW)
from maps import BOOL_HEALTHY_ON, BOOL_NEUTRAL


def tab_policy():
    _infra_tbl = ["Time", "__name__", "job", "instance",
                  "service_instance_id", "service_name", "service_namespace"]
    acl = [
        (panel("ACL last changed", "stat", [prom_t("time() - max(%s)" % lot("tailscale_acl_last_changed_seconds", WIN_SLOW))],
               unit="s", options=stat_opts(graph="none")), 6, 5),
        (panel("ACL size", "stat", [prom_t("max(%s)" % lot("tailscale_acl_size_bytes", WIN_SLOW))],
               unit="bytes", options=stat_opts()), 6, 5),
        (panel("ACL rules by section", "bargauge",
               [prom_t("max by (tailscale_acl_section) (%s)" % lot("tailscale_acl_rules_ratio", WIN_SLOW), legend="{{tailscale_acl_section}}")],
               unit="short", options=bargauge_opts()), 12, 5),
        # Task 1H.9 — ACL inventory counts (risk stats live on Security/WU7)
        (panel("Auto-approvers (inventory)", "bargauge",
               [prom_t("sum by (tailscale_acl_autoapprover_kind) (%s)" % lot("tailscale_acl_autoapprovers_ratio", WIN_SLOW),
                       legend="{{tailscale_acl_autoapprover_kind}}")],
               unit="short", options=bargauge_opts()), 12, 5),
        (panel("Posture-gated rules (inventory)", "bargauge",
               [prom_t("sum by (tailscale_acl_section) (%s)" % lot("tailscale_acl_posture_gated_rules_ratio", WIN_SLOW))],
               unit="short", options=bargauge_opts()), 12, 5),
    ]
    dns = [
        (panel("MagicDNS", "stat", [prom_t("max(%s)" % lot("tailscale_dns_magic_dns_ratio", WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 6, 5),
        (panel("Nameservers", "stat", [prom_t("max(%s)" % lot("tailscale_dns_nameservers_count_ratio", WIN_SLOW))], unit="short", options=stat_opts()), 6, 5),
        (panel("Search paths", "stat", [prom_t("max(%s)" % lot("tailscale_dns_search_paths_count_ratio", WIN_SLOW))], unit="short", options=stat_opts()), 6, 5),
        (panel("Split-DNS zones", "stat", [prom_t("max(%s)" % lot("tailscale_dns_split_zones_count_ratio", WIN_SLOW))], unit="short", options=stat_opts()), 6, 5),
        # Task 1.6 Step 1 — A3 DNS additions (stats; ungated)
        (panel("Override local DNS", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_override_local_ratio", WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background")), 6, 5),
        (panel("Exit-node resolvers", "stat",
               [prom_t("max(%s)" % lot("tailscale_dns_resolvers_use_with_exit_node_ratio", WIN_SLOW))],
               unit="short", options=stat_opts()), 6, 5),
        # Task 1.6 Step 1 — Search domains barchart (no resolver-presence gate needed)
        (panel("Search domains", "barchart",
               [prom_t("count by (tailscale_dns_search_path_domain) (%s)" % lot("tailscale_dns_search_path_ratio", WIN_SLOW),
                       legend="{{tailscale_dns_search_path_domain}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 12, 6),
    ]
    # Task 1.6 Step 1 — Resolvers table gated by has_dns_resolver
    dns_resolvers = [
        (panel("Resolvers", "table",
               [prom_t("%s" % lot("tailscale_dns_resolver_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra_tbl + ["Value"],
                   rename={"tailscale_dns_resolver_address": "Address",
                           "tailscale_dns_resolver_kind": "Kind",
                           "tailscale_dns_resolver_use_with_exit_node": "ExitNode"})],
               desc="DNS resolver configuration. FIX-3: no domain label on live wire."), 24, 6),
    ]
    settings = [
        # Neutral map: a tailnet setting being off is a fact, not a verdict — the risky
        # ones have their own colour-coded panels.
        (panel("Tailnet settings", "table", [prom_t(lot("tailscale_setting_enabled_ratio", WIN_SLOW), instant=True, fmt="table")],
               mappings=BOOL_NEUTRAL,
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_setting_name": "Setting", "Value": "Enabled"})],
               desc="Per-setting enabled (1) / disabled (0)."), 8, 7),
        (panel("Device key duration", "stat", [prom_t("max(%s)" % lot("tailscale_setting_devices_key_duration_days", WIN_SLOW))],
               unit="d", options=stat_opts()), 4, 7),
        (panel("Tailnet features", "table", [prom_t(lot("tailscale_feature_enabled_ratio", WIN_SLOW), instant=True, fmt="table")],
               mappings=BOOL_NEUTRAL,
               transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
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
                       "scoped to read webhooks."), 6, 5),
    ]
    users = [
        # No zero-fill: per-user series are gated by cardinality.per_entity.user, and this
        # panel used to render a reassuring 0 with the gate closed (#385).
        (panel("Stale users (>30d)", "stat",
               [prom_t("count((time() - %s) > 30*86400)"
                       % lot("tailscale_user_last_seen_seconds", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No per-user data — needs the users collector and "
                       "cardinality.per_entity.user.",
               desc="Users not seen in over 30 days (last-seen staleness). Empty, not zero, when "
                    "per-user metrics are disabled; see the Per-user detail row."), 6, 5),
        (panel("Users by role", "barchart",
               [prom_t("sum by (tailscale_user_role) (max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s))" % lot("tailscale_users_count_ratio", WIN_SLOW), legend="{{tailscale_user_role}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 6),
        (panel("Users by status", "barchart",
               [prom_t("sum by (tailscale_user_status) (max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s))" % lot("tailscale_users_count_ratio", WIN_SLOW), legend="{{tailscale_user_status}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 6),
        (panel("Users by type", "barchart",
               [prom_t("sum by (tailscale_user_type) (max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s))" % lot("tailscale_users_count_ratio", WIN_SLOW), legend="{{tailscale_user_type}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 6),
    ]
    users_pe = [
        (panel("Per-user detail", "table",
               [prom_t(lot("tailscale_user_connected_ratio", WIN_SLOW), instant=True, fmt="table", refid="A"),
                prom_t(lot("tailscale_user_devices_ratio", WIN_SLOW), instant=True, fmt="table", refid="B"),
                prom_t("time() - %s" % lot("tailscale_user_last_seen_seconds", WIN_SLOW), instant=True, fmt="table", refid="C")],
               transformations=[merge(),
                                organize(exclude=["Time", "__name__", "job", "instance", "user_id",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"user_name": "User", "Value #A": "Connected",
                                                 "Value #B": "Devices", "Value #C": "Last seen"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last seen"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per-user connected / device count / time since last seen."), 24, 8),
    ]
    invites = [
        (panel("User invites", "bargauge",
               # Group by the labels the code ACTUALLY emits: role + delivery
               # (internal/collector/users emits tailscale.user_invite.role and
               # .delivery). This used to group by tailscale_user_invite_accepted,
               # which is emitted nowhere — PromQL silently collapses an unknown
               # grouping label, so the panel rendered with a blank "accepted="
               # legend and no error. Caught by
               # TestFlagshipDashboardQueriesOnlyCatalogMetrics (#438).
               [prom_t("max by (tailscale_user_invite_role, tailscale_user_invite_delivery) (%s)" % lot("tailscale_user_invites_count_ratio", WIN_SLOW),
                       legend="{{tailscale_user_invite_role}} via {{tailscale_user_invite_delivery}}")],
               unit="short", options=bargauge_opts()), 24, 5),
    ]
    keys = [
        # Task 1.6 Step 2 — updated Keys by type (aggregate to type+auth_kind)
        (panel("Keys by type", "bargauge",
               [prom_t("sum by (tailscale_key_type, tailscale_key_auth_kind) (%s)" % lot("tailscale_keys_count_ratio", WIN_SLOW),
                       legend="{{tailscale_key_type}} / {{tailscale_key_auth_kind}}")],
               unit="short", options=bargauge_opts()), 10, 7),
        (panel("Key expiry (time until)", "table",
               [prom_t("%s - time()" % lot("tailscale_key_expiry_seconds", WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"tailscale_key_id": "Key ID", "tailscale_key_type": "Type",
                                                           "tailscale_key_description": "Description", "Value": "Expires in"})],
               desc="Time until each API/auth key expires."), 14, 7),
        # Task 1.6 Step 2 — Preauthorized auth keys
        (panel("Preauthorized auth keys", "stat",
               [prom_t("sum(%s == 1)" % lot("tailscale_key_preauthorized_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               novalue="No per-key data — needs the keys collector and "
                       "cardinality.per_entity.key."), 10, 7),
    ]
    # Task 1.6 Step 2 — Credential scopes top-N (gated on the key-scopes metric)
    credscopes = [
        (panel("Credential scopes (top-N)", "table",
               [prom_t("topk($topn, %s)" % lot("tailscale_key_scopes_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra_tbl + ["tailscale_key_id"],
                   rename={"tailscale_key_description": "Description",
                           "tailscale_key_type": "Type",
                           "Value": "Scopes"})],
               desc="Top-N keys by scope count. Excludes raw key ID."), 24, 7),
    ]
    # Task 1H.3 — Key scope inventory (Loki)
    keyscopes = [
        (panel("Key scope inventory (logs)", "table",
               [loki_t(
                   'sum by (tailscale_key_scope_values) (count_over_time({service_name="tailscale2otel"} | event_name=`tailscale.key.scopes`[$__range]))',
                   instant=True)],
               transformations=[organize(
                   exclude=["Time"],
                   rename={"tailscale_key_scope_values": "Scopes", "Value": "Keys"})],
               novalue="0",
               desc="Credential scope values observed in key.scopes log events."), 24, 7),
    ]
    services_vip = [
        (panel("Services (VIP)", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_services_count_ratio", WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Tailscale Services (VIP services) advertised in the tailnet."), 6, 6),
        (panel("Port rules per service", "bargauge",
               [prom_t("max by (tailscale_service_name) (%s)" % lot("tailscale_service_ports", WIN_SLOW), legend="{{tailscale_service_name}}")],
               unit="short", options=bargauge_opts(),
               desc="Port rules exposed by each Service. Gated by cardinality.per_entity.service."), 18, 6),
        (panel("Backing hosts by service", "table",
               [prom_t(lot("tailscale_service_hosts_ratio", WIN_SLOW), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"tailscale_service_name": "Service",
                                                               "tailscale_service_approval": "Approval",
                                                               "tailscale_service_configured": "Configured",
                                                               "Value": "Hosts"})],
               desc="Backing-host count per Service, bucketed by approval + configured state. "
                    "Gated by collect_hosts + cardinality.per_entity.service."), 24, 7),
        # Task 1H.8 — VIP service health (merged hosts + port-rules)
        (panel("VIP service health", "table",
               [prom_t(lot("tailscale_service_hosts_ratio", WIN_SLOW), instant=True, fmt="table", refid="A"),
                prom_t("max by (tailscale_service_name) (%s)" % lot("tailscale_service_ports", WIN_SLOW),
                       instant=True, fmt="table", refid="B")],
               transformations=[merge(),
                                organize(
                                    exclude=_infra_tbl,
                                    rename={"tailscale_service_name": "Service",
                                            "tailscale_service_approval": "Approval",
                                            "tailscale_service_configured": "Configured",
                                            "Value #A": "Hosts",
                                            "Value #B": "Port rules"})],
               desc="Merged view: hosts + port-rule count per VIP service. "
                    "Services with 1 host have no HA. Requires collect_hosts + per_entity.service."), 24, 7),
    ]
    return [row("Access control (ACL)", acl),
            row("DNS", dns),
            row("DNS resolvers", dns_resolvers, present="has_dns_resolver"),
            row("Settings & features", settings),
            row("Services / VIP", services_vip, present="has_services"),
            row("Users", users),
            # Task 1H.4 — PII gate: users_pe shows user_name
            row("Per-user detail", users_pe, present="has_users_pe", hide_when=["pii_usernames"]),
            row("User invites", invites, present="has_invites"),
            row("API keys", keys),
            row("Credential scopes", credscopes, present="has_key_scopes"),
            # Task 1H.3 — key scope inventory (Loki); no personal PII, no present gate
            row("Key scope inventory", keyscopes),
            ]
