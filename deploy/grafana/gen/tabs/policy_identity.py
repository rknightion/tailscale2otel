"""tab_policy_identity() — the "Identity & Credentials" leaf of the Policy & Config
sub-tab group (#526).

Split out of tabs/policy.py. Who exists on the tailnet (users, outstanding invites) and
what credentials exist for it (API/auth keys, their scopes, registered OAuth applications).

Scope boundary (#526 decision 10): this is the CONFIGURATION view — which keys and OAuth
apps exist and what scopes they carry. The RISK view — what is expiring, what is
over-scoped, which actor did what — is Security & Audit's, and a signal appearing on both
tabs is intended rather than a duplicate to remove.

Consolidation (#526 decision 7):
  * the single-panel "User invites" row is merged into "Users";
  * the single-panel "API keys", "Credential scopes" and "Key scope inventory" rows are
    merged into one "API keys & credential scopes" row.
Both merges drop a row-level presence gate (`has_invites`, `has_key_scopes`) rather than
applying it to unrelated neighbours; each affected panel now names its own prerequisites in
noValue prose, which is strictly more informative than a row that silently is not there.
`has_key_scopes` in particular is left undeclared here on purpose: tabs/security.py gates a
row of its own on that name, and a name registered at both dashboard and tab scope raises.

"Per-user detail" is deliberately NOT merged despite being a single panel: it is the only
panel on this leaf that renders a user NAME, so it carries a `hide_when=["pii_usernames"]`
redaction gate. Folding it into "Users" would extend that gate over four panels that show
no PII at all, and dropping it would leak names the operator asked to have redacted.

Sentinels declared at TAB scope: has_users_pe, pii_usernames (both consumed by the
"Per-user detail" row) and has_keys (the "Key expiry detail" row). All three were
DASHBOARD-scoped in policy.py and are consumed nowhere else in this dashboard.
"""

from builder import (barchart_opts, bargauge_opts, loki_t, lot, merge, organize, panel, PII,
                     pii_sentinel, prom_t, RI, row, sentinel, stat_opts, thr, ts_custom,
                     ts_opts, WIN_SLOW)

DOCS = "https://m7kni.io/tailscale2otel"
CFG_DOC = DOCS + "/configuration/"

TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'
LOKI_TN = ('{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet" '
           '| tailscale2otel_provider=~"$provider"')

_INFRA_TBL = ["Time", "__name__", "job", "instance",
              "service_instance_id", "service_name", "service_namespace",
              "deployment_environment_name", "otel_scope_name", "otel_scope_version"]

def sel(metric, extra=""):
    """`<metric>{<tailnet/provider filter>[, <extra>]}` — the filtered selector."""
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


_USERS_AGG = ("max by (tailscale_user_role, tailscale_user_status, tailscale_user_type) (%s)"
              % lot(sel("tailscale_users_count_ratio"), WIN_SLOW))


def q_hist(quantile, metric):
    """histogram_quantile over a tailnet-filtered `<metric>_bucket`."""
    return ("histogram_quantile(%s, sum by (le) (rate(%s[%s])))"
            % (quantile, sel(metric + "_bucket"), RI))


def tab_policy_identity(scope):
    sentinel("has_users_pe", "tailscale_user_connected_ratio", scope)
    sentinel("has_keys", "tailscale_key_expiry_seconds", scope)
    pii_sentinel("pii_usernames", PII + '{category="user_display_names"} == 0', scope)

    users = [
        # No zero-fill: per-user series are gated by cardinality.per_entity.user, and this
        # panel used to render a reassuring 0 with the gate closed (#385).
        (panel("Stale users (>30d)", "stat",
               [prom_t("count((time() - %s) > 30*86400)"
                       % lot(sel("tailscale_user_last_seen_seconds"), WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No per-user data — needs the users collector and "
                       "cardinality.per_entity.user.",
               desc="Users not seen in over 30 days (last-seen staleness). Empty, not zero, when "
                    "per-user metrics are unavailable; see the Per-user detail row."), 6, 5),
        # Merged in from the old single-panel "User invites" row (#526 decision 7).
        (panel("User invites", "bargauge",
               # Group by the labels the code ACTUALLY emits: role + delivery
               # (internal/collector/users emits tailscale.user_invite.role and
               # .delivery). This used to group by tailscale_user_invite_accepted,
               # which is emitted nowhere — PromQL silently collapses an unknown
               # grouping label, so the panel rendered with a blank "accepted="
               # legend and no error. Caught by
               # TestFlagshipDashboardQueriesOnlyCatalogMetrics (#438).
               [prom_t("max by (tailscale_user_invite_role, tailscale_user_invite_delivery) (%s)" % lot(sel("tailscale_user_invites_count_ratio"), WIN_SLOW),
                       legend="{{tailscale_user_invite_role}} via {{tailscale_user_invite_delivery}}")],
               unit="short", options=bargauge_opts(),
               novalue="No user-invite series. Prerequisites: collectors.users.enabled and a "
                       "credential that can read tailnet users. See " + CFG_DOC,
               desc="Outstanding open user invites, by role and delivery method."), 18, 5),
        (panel("Users by role", "barchart",
               [prom_t("sum by (tailscale_user_role) (%s)" % _USERS_AGG,
                       legend="{{tailscale_user_role}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Tailnet users by assigned role (owner / admin / member / ...). An "
                    "aggregate of the bounded users.count gauge, so it stays populated with "
                    "per-user series switched off."), 8, 6),
        (panel("Users by status", "barchart",
               [prom_t("sum by (tailscale_user_status) (%s)" % _USERS_AGG,
                       legend="{{tailscale_user_status}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Tailnet users by account status (active / idle / suspended / needs-approval). "
                    "Suspended and needs-approval accounts still hold a tailnet identity."), 8, 6),
        (panel("Users by type", "barchart",
               [prom_t("sum by (tailscale_user_type) (%s)" % _USERS_AGG,
                       legend="{{tailscale_user_type}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Tailnet users by type — a member of the tailnet versus a shared-in user "
                    "from another tailnet."), 8, 6),
    ]

    users_pe = [
        (panel("Per-user detail", "table",
               [prom_t(lot(sel("tailscale_user_connected_ratio"), WIN_SLOW), instant=True, fmt="table", refid="A"),
                prom_t(lot(sel("tailscale_user_devices_ratio"), WIN_SLOW), instant=True, fmt="table", refid="B"),
                prom_t("time() - %s" % lot(sel("tailscale_user_last_seen_seconds"), WIN_SLOW), instant=True, fmt="table", refid="C")],
               transformations=[merge(),
                                organize(exclude=_INFRA_TBL + ["user_id"],
                                         rename={"user_name": "User", "Value #A": "Connected",
                                                 "Value #B": "Devices", "Value #C": "Last seen"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last seen"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per-user connected / device count / time since last seen."), 24, 8),
    ]

    # Merged row (#526 decision 7): the aggregate key inventory, the per-key scope
    # top-N, and the scope values observed on the key.scopes log event.
    keys = [
        # Task 1.6 Step 2 — Keys by type (aggregate to type+auth_kind)
        (panel("Keys by type", "bargauge",
               [prom_t("sum by (tailscale_key_type, tailscale_key_auth_kind) (%s)" % lot(sel("tailscale_keys_count_ratio"), WIN_SLOW),
                       legend="{{tailscale_key_type}} / {{tailscale_key_auth_kind}}")],
               unit="short", options=bargauge_opts(),
               desc="API and auth keys the tailnet holds, split by key type and by auth kind "
                    "(user-owned versus OAuth-minted). An aggregate, so it survives "
                    "cardinality.per_entity.key being switched off."), 24, 7),
        # Task 1.6 Step 2 — Credential scopes top-N (previously its own has_key_scopes row)
        (panel("Credential scopes (top-N)", "table",
               [prom_t("topk($topn, %s)" % lot(sel("tailscale_key_scopes_ratio"), WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_INFRA_TBL + ["tailscale_key_id"],
                   rename={"tailscale_key_description": "Description",
                           "tailscale_key_type": "Type",
                           "Value": "Scopes"})],
               novalue="No per-key scope series. Prerequisites: collectors.keys.enabled and "
                       "cardinality.per_entity.key. See " + CFG_DOC,
               desc="Top-N keys by how many scopes they carry — the configuration view of scope "
                    "sprawl. Excludes the raw key ID; whether a given breadth is too much for "
                    "its purpose is a Security & Audit question."), 24, 7),
        # Task 1H.3 — Key scope inventory (Loki)
        (panel("Key scope inventory (logs)", "table",
               [loki_t(
                   'sum by (tailscale_key_scope_values) (count_over_time('
                   '%s | event_name=`tailscale.key.scopes`[$__range]))' % LOKI_TN,
                   instant=True)],
               transformations=[organize(
                   exclude=["Time"],
                   rename={"tailscale_key_scope_values": "Scopes", "Value": "Keys"})],
               novalue="0",
               desc="Credential scope values observed in key.scopes log events over the "
                    "dashboard time range. Counts observations, not keys: one key re-logged "
                    "each collection tick contributes many."), 24, 7),
    ]

    # has_keys wired (#495): both panels below read a PER-KEY gauge, which
    # cardinality.per_entity.key switches off wholesale. Gating them — rather
    # than the whole keys row — keeps the aggregate Keys by type visible
    # when per-key series are off, which is the state the toggle is FOR.
    keys_detail = [
        (panel("Key expiry (time until)", "table",
               [prom_t("%s - time()" % lot(sel("tailscale_key_expiry_seconds"), WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=_INFRA_TBL,
                                                   rename={"tailscale_key_id": "Key ID", "tailscale_key_type": "Type",
                                                           "tailscale_key_description": "Description", "Value": "Expires in"})],
               desc="Time until each API/auth key expires."), 14, 7),
        # Task 1.6 Step 2 — Preauthorized auth keys
        (panel("Preauthorized auth keys", "stat",
               [prom_t("sum(%s == 1)" % lot(sel("tailscale_key_preauthorized_ratio"), WIN_SLOW))],
               unit="short", options=stat_opts(),
               novalue="No per-key data — needs the keys collector and "
                       "cardinality.per_entity.key.",
               desc="Count of auth keys marked preauthorized (auto-approved, no manual login)."), 10, 7),
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
                                organize(exclude=_INFRA_TBL + ["tailscale_oauth_app_id"],
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

    return [
        row("Users", users),
        # Task 1H.4 — PII gate: this is the only panel here that renders a user name
        row("Per-user detail", users_pe, present="has_users_pe", hide_when=["pii_usernames"]),
        row("API keys & credential scopes", keys),
        # has_keys wired (#495): per-key gauges only
        row("Key expiry detail", keys_detail, present="has_keys"),
        row("OAuth applications", oauthapps),
    ]
