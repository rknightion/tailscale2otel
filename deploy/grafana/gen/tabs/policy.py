"""tab_policy() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, loki_t, lot, merge, organize, panel,
                     PII, pii_sentinel, prom_t, RI, row, sentinel, stat_opts, thr,
                     ts_custom, ts_opts, WIN_SLOW)
from maps import BOOL_HEALTHY_ON, BOOL_NEUTRAL

# Cross-links to the operator-facing configuration reference. The panel
# `description` is the only text surface a v2 panel has that renders markdown —
# builder.panel() hardcodes `links: []` — so a "what do I turn on" pointer goes
# here rather than in a panel link (#403).
DOCS = "https://m7kni.io/tailscale2otel"
CFG_DOC = DOCS + "/configuration/"

# #393 — tailnet / provider filters. Both are real per-series metric labels
# (roadmap item L), so filtering is a plain selector with no target_info join.
# Every panel added for #393/#403 carries them; without them a multi-tailnet
# deployment blends every tailnet's inventory into one number and an operator
# reads a fleet-wide total as a per-tailnet one.
TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'

# The same filter for Loki. The tailnet/provider const attrs ride every log
# record too, so they arrive as structured metadata. A record without the
# attribute still matches when the variable is All ($tailnet -> ".*", which
# matches the empty string), so this is safe on a single-tailnet deployment.
LOKI_TN = '{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet"'


def sel(metric, extra=""):
    """`<metric>{<tailnet/provider filter>[, <extra>]}` — the filtered selector."""
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


def q_hist(quantile, metric):
    """histogram_quantile over a tailnet-filtered `<metric>_bucket`."""
    return ("histogram_quantile(%s, sum by (le) (rate(%s[%s])))"
            % (quantile, sel(metric + "_bucket"), RI))


def tab_policy():
    # Presence sentinels this tab declares (moved from variables.py, #495).
    sentinel("has_users_pe", "tailscale_user_connected_ratio")
    sentinel("has_invites", "tailscale_user_invites_count_ratio")
    # has_keys / has_svc were registered-but-dead (#495): they queried Prometheus on
    # every dashboard load and gated nothing. Both are now wired to the row that
    # needs the per-entity series they check for — see "Key expiry detail" and
    # "VIP service detail" below.
    sentinel("has_keys", "tailscale_key_expiry_seconds")
    sentinel("has_services", "tailscale_services_count_ratio")
    sentinel("has_key_scopes", "tailscale_key_scopes_ratio")
    sentinel("has_dns_resolver", "tailscale_dns_resolver_ratio")
    sentinel("has_svc", "tailscale_service_ports")
    pii_sentinel("pii_usernames", PII + '{category="user_display_names"} == 0')
    # pii_emails stays dead deliberately. No panel on any tab renders an email
    # address — contacts report a per-type verification flag and the address is
    # never emitted, and the OAuth/webhook inventory added for #403 carries app
    # names and endpoint ids only. Gating a row on it would suppress a row that
    # exposes nothing, so it waits for a panel that actually shows an address.
    pii_sentinel("pii_emails", PII + '{category="emails"} == 0')  # dead: nothing renders an email

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
               unit="short", options=bargauge_opts()), 24, 7),
    ]
    # has_keys wired (#495): both panels below read a PER-KEY gauge, which
    # cardinality.per_entity.key switches off wholesale. Gating them — rather
    # than the whole API keys row — keeps the aggregate Keys by type visible
    # when per-key series are off, which is the state the toggle is FOR.
    keys_detail = [
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
        (panel("Backing hosts by service", "table",
               [prom_t(lot("tailscale_service_hosts_ratio", WIN_SLOW), instant=True, fmt="table")],
               unit="short", transformations=[organize(exclude=["Time", "__name__", "job", "instance",
                                                                "service_instance_id", "service_name", "service_namespace"],
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
               [prom_t("max by (tailscale_service_name) (%s)" % lot("tailscale_service_ports", WIN_SLOW), legend="{{tailscale_service_name}}")],
               unit="short", options=bargauge_opts(),
               desc="Port rules exposed by each Service. Gated by cardinality.per_entity.service."), 24, 6),
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
    # ------------------------------------------------------------------
    # #403 — OAuth application inventory
    # ------------------------------------------------------------------
    # Everything here is a COUNT or a bounded CLASS. Redirect URIs, client
    # secrets and endpoint URLs are never emitted by the exporter, so no panel
    # can show one and no panel text may imply that one is available.
    oauthapps = [
        (panel("OAuth applications", "stat",
               [prom_t("max(%s)" % lot(sel("tailscale_oauth_apps_count_ratio"), WIN_SLOW))],
               unit="short", options=stat_opts(),
               # No zero-fill: an under-scoped or rejected credential emits nothing,
               # and a manufactured 0 would read as "this tailnet has no OAuth apps"
               # (#385). The text names prerequisites, never a cause — presence
               # cannot tell disabled from unsupported from never-deployed.
               novalue="No OAuth-application data. Prerequisites: collectors.oauth_apps.enabled, "
                       "a credential that can read OAuth applications, and a tailnet where the "
                       "alpha OAuth-apps API is available. See " + CFG_DOC,
               desc="Registered OAuth applications on the tailnet. Configuration reference: "
                    "[collectors.oauth_apps](%s)." % CFG_DOC), 5, 6),
        (panel("OAuth apps by privilege class", "bargauge",
               [prom_t("count by (tailscale_oauth_app_scope_class) (%s == 1)"
                       % lot(sel("tailscale_oauth_app_scope_class_ratio"), WIN_SLOW),
                       legend="{{tailscale_oauth_app_scope_class}}")],
               unit="short", options=bargauge_opts(),
               desc="Applications per privilege class, ranked by internal/tsscope: "
                    "none < read < all_read < write < all. The gauge is zero-seeded across "
                    "every class, so this counts only the class each app currently holds."), 9, 6),
        (panel("OAuth app age (p50 / p90)", "timeseries",
               [prom_t(q_hist("0.5", "tailscale_oauth_apps_age_seconds"), legend="p50"),
                prom_t(q_hist("0.9", "tailscale_oauth_apps_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Fleet age distribution of OAuth applications since their created "
                    "timestamp. A single bounded histogram, not a per-app series — apps with no "
                    "created timestamp are omitted rather than recorded as age 0."), 10, 6),
        (panel("OAuth app scope & attribute counts", "table",
               [prom_t(lot(sel("tailscale_oauth_app_scopes_ratio"), WIN_SLOW),
                       instant=True, fmt="table", refid="A"),
                prom_t(lot(sel("tailscale_oauth_app_node_attributes_ratio"), WIN_SLOW),
                       instant=True, fmt="table", refid="B"),
                prom_t(lot(sel("tailscale_oauth_app_redirect_uris_ratio"), WIN_SLOW),
                       instant=True, fmt="table", refid="C")],
               transformations=[merge(),
                                organize(exclude=_infra_tbl + ["tailscale_oauth_app_id"],
                                         rename={"tailscale_oauth_app_name": "Application",
                                                 "Value #A": "Scopes",
                                                 "Value #B": "Node attributes",
                                                 "Value #C": "Redirect targets"})],
               desc="Scope sprawl per application: granted scopes, custom node attributes it may "
                    "set, and how many redirect targets it has configured. All three are counts — "
                    "the exporter emits no redirect target values and no client credential, so none "
                    "can be shown here."), 14, 6),
        (panel("OAuth application inventory (logs)", "table",
               [loki_t("sum by (tailscale_oauth_app_scope_values) (count_over_time("
                       "%s | event_name=`tailscale.oauth_app.info` [$__range]))" % LOKI_TN,
                       instant=True)],
               transformations=[organize(exclude=["Time"],
                                         rename={"tailscale_oauth_app_scope_values": "Granted scopes",
                                                 "Value": "Observations"})],
               novalue="0",
               desc="Granted scope sets observed on the oauth_app.info inventory log, over the "
                    "dashboard time range. Counts observations, not applications: one app "
                    "re-logged each collection tick contributes many."), 24, 7),
    ]

    # ------------------------------------------------------------------
    # #393/#403 — ACL policy validation
    # ------------------------------------------------------------------
    aclvalidation = [
        (panel("ACL policy validated", "stat",
               [prom_t("min(%s)" % lot(sel("tailscale_acl_validation_ok_ratio"), WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               # Absent, not 0, when the validate call itself is unavailable — so no
               # zero-fill, and the empty state names what has to be true instead.
               novalue="No validation result. Prerequisites: collectors.acl.enabled with "
                       "collectors.acl.validate, and a credential carrying the policy_file:read "
                       "scope. See " + CFG_DOC,
               desc="1 when the active policy — including the tests embedded in its own `tests` "
                    "section — validated cleanly on the last check. Empty rather than 0 when the "
                    "validate call is unavailable; tailscale2otel.api.availability carries that "
                    "state separately."), 6, 6),
        (panel("Validation issues (last check)", "bargauge",
               [prom_t("max(%s)" % lot(sel("tailscale_acl_validation_errors_ratio"), WIN_SLOW),
                       legend="errors"),
                prom_t("max(%s)" % lot(sel("tailscale_acl_validation_warnings_ratio"), WIN_SLOW),
                       legend="warnings"),
                prom_t("max(%s)" % lot(sel("tailscale_acl_validation_test_failures_ratio"), WIN_SLOW),
                       legend="test failures")],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=bargauge_opts(),
               desc="Counts from the last policy validation. Errors and test failures are "
                    "reported separately by the API, so errors stays 0 in the common case while "
                    "an embedded test fails. Warnings include advisory findings such as a group "
                    "that is not syncing from SCIM."), 8, 6),
        (panel("Validation issues by kind (logs)", "table",
               [loki_t("sum by (tailscale_acl_validation_kind) (count_over_time("
                       "%s | event_name=`tailscale.acl.validation_issue` [$__range]))" % LOKI_TN,
                       instant=True)],
               transformations=[organize(exclude=["Time"],
                                         rename={"tailscale_acl_validation_kind": "Kind",
                                                 "Value": "Observations"})],
               novalue="0",
               desc="One WARN per non-zero issue kind (error / warning / test_failure) per check. "
                    "The validator's free-text messages carry rule text, user names and addresses "
                    "and are deliberately never emitted, so the kind is all there is here — read "
                    "the policy itself for the detail."), 10, 6),
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
               transformations=[organize(exclude=_infra_tbl + ["Time"],
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
               transformations=[organize(exclude=_infra_tbl + ["Time"],
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

    return [row("Access control (ACL)", acl),
            # #393/#403 — policy validation health, next to the ACL snapshot it validates
            row("ACL policy validation", aclvalidation),
            row("DNS", dns),
            row("DNS resolvers", dns_resolvers, present="has_dns_resolver"),
            row("Settings & features", settings),
            row("Services / VIP", services_vip, present="has_services"),
            # has_svc wired (#495): per-service port gauges only
            row("VIP service detail", services_detail, present="has_svc"),
            row("Users", users),
            # Task 1H.4 — PII gate: users_pe shows user_name
            row("Per-user detail", users_pe, present="has_users_pe", hide_when=["pii_usernames"]),
            row("User invites", invites, present="has_invites"),
            row("API keys", keys),
            # has_keys wired (#495): per-key gauges only
            row("Key expiry detail", keys_detail, present="has_keys"),
            row("Credential scopes", credscopes, present="has_key_scopes"),
            # Task 1H.3 — key scope inventory (Loki); no personal PII, no present gate
            row("Key scope inventory", keyscopes),
            # #403 — OAuth application and webhook-endpoint inventory. Ungated:
            # both surfaces need an empty state that names the prerequisite, and
            # a presence gate would hide the row instead of showing it (no new
            # sentinel is available anyway — _SENTINEL_ORDER is frozen).
            row("OAuth applications", oauthapps),
            row("Webhook endpoint inventory", webhookinv),
            ]
