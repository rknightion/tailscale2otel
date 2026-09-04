"""Border0-only Tailscale PAM inventory, configuration shape and sessions."""

from builder import (DASHBOARD, RI, WIN_SLOW, bargauge_opts, logs_opts, loki_t,
                     lot, merge, organize, panel, prom_t, row, sentinel,
                     stat_opts, ts_custom, ts_opts)


TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'
LOKI_TN = ('{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet" '
           '| tailscale2otel_provider=~"$provider"')
_INFRA = ["Time", "__name__", "job", "instance", "service_instance_id",
          "service_name", "service_namespace", "deployment_environment_name",
          "otel_scope_name", "otel_scope_version"]


def sel(metric, extra=""):
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


def latest(metric):
    return lot(sel(metric), WIN_SLOW)


def tab_policy_pam(scope):
    # This is the only whole-tab sentinel. It is dashboard-scoped because a tab
    # cannot be gated by a variable that exists only after that tab renders.
    sentinel("has_pam", "tailscale_pam_connectors_ratio", DASHBOARD)

    inventory = [
        (panel("Connectors", "stat",
               [prom_t("max(%s) or vector(0)" % latest("tailscale_pam_connectors_ratio"), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Configured Border0 PAM connectors."), 6, 5),
        (panel("Services by type", "bargauge",
               [prom_t("max by (tailscale_pam_service_type) (%s)" % latest("tailscale_pam_services_ratio"),
                       legend="{{tailscale_pam_service_type}}")],
               unit="short", options=bargauge_opts(),
               desc="Border0 PAM services grouped by socket type. This does not restate Tailscale Service VIP or port inventory."), 6, 5),
        (panel("Policies", "stat",
               [prom_t("max(%s) or vector(0)" % latest("tailscale_pam_policies_ratio"), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Configured Border0 PAM policies."), 6, 5),
        (panel("Identities by kind and role", "bargauge",
               [prom_t("max by (tailscale_pam_identity_kind, tailscale_pam_identity_role) (%s)"
                       % latest("tailscale_pam_identities_ratio"),
                       legend="{{tailscale_pam_identity_kind}} / {{tailscale_pam_identity_role}}")],
               unit="short", options=bargauge_opts(),
               desc="PAM identities split by kind and role. The role split keeps tag-mirrored client accounts distinct from real service accounts."), 6, 5),
    ]

    connector_health = [
        (panel("Connector health", "table",
               [prom_t(latest("tailscale_pam_connector_connected_ratio"), instant=True, fmt="table", refid="A"),
                prom_t(latest("tailscale_pam_connector_last_seen_age_seconds"), instant=True, fmt="table", refid="B"),
                prom_t(latest("tailscale_pam_connector_sockets_ratio"), instant=True, fmt="table", refid="C"),
                prom_t(latest("tailscale_pam_connector_tokens_ratio"), instant=True, fmt="table", refid="D"),
                prom_t(latest("tailscale_pam_connector_plugins_ratio"), instant=True, fmt="table", refid="E")],
               transformations=[merge(), organize(exclude=_INFRA,
                                         rename={"tailscale_pam_connector_name": "Connector",
                                                 "Value #A": "Connected",
                                                 "Value #B": "Last seen age",
                                                 "Value #C": "Services",
                                                 "Value #D": "Tokens",
                                                 "Value #E": "Plugins"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last seen age"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Border0 connector connectivity, freshness and attached-object counts. Connector names are operator supplied and bounded by the per-tailnet metric limit."), 16, 7),
        (panel("Connector builds", "table",
               [prom_t(latest("tailscale_pam_connector_info_ratio"), instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA,
                                         rename={"tailscale_pam_connector_name": "Connector",
                                                 "tailscale_pam_version": "Version",
                                                 "tailscale_pam_built_date": "Build date",
                                                 "Value": "Info"})],
               desc="Connector version and build metadata."), 8, 7),
    ]

    configuration = [
        (panel("Service health", "table",
               [prom_t(latest("tailscale_pam_service_alive_ratio"), instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA,
                                         rename={"tailscale_pam_service_name": "Service",
                                                 "tailscale_pam_service_type": "Type",
                                                 "Value": "Alive"})],
               desc="Border0 service liveness. This is PAM-specific state, not duplicated Tailscale Service inventory."), 8, 7),
        (panel("Service settings", "table",
               [prom_t(latest("tailscale_pam_service_setting_enabled_ratio"), instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA,
                                         rename={"tailscale_pam_service_name": "Service",
                                                 "tailscale_pam_service_type": "Type",
                                                 "tailscale_pam_setting_name": "Setting",
                                                 "Value": "Enabled"})],
               desc="One 0/1 series per stable PAM service setting."), 8, 7),
        (panel("Policy settings", "table",
               [prom_t(latest("tailscale_pam_policy_setting_enabled_ratio"), instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA,
                                         rename={"tailscale_pam_policy_name": "Policy",
                                                 "tailscale_pam_version": "Version",
                                                 "tailscale_pam_setting_name": "Setting",
                                                 "Value": "Enabled"})],
               desc="One 0/1 series per stable PAM policy setting."), 8, 7),
    ]

    organization = [
        (panel("Organization settings", "table",
               [prom_t(latest("tailscale_pam_org_setting_enabled_ratio"), instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA,
                                         rename={"tailscale_pam_setting_name": "Setting", "Value": "Enabled"})],
               desc="Boolean Border0 organization configuration."), 8, 6),
        (panel("Plan", "table",
               [prom_t(latest("tailscale_pam_org_plan_info_ratio"), instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA,
                                         rename={"tailscale_pam_plan": "Plan", "Value": "Info"})],
               desc="Current bounded Border0 plan slug."), 6, 6),
        (panel("Subscription limits", "bargauge",
               [prom_t("max by (tailscale_pam_limit_name) (%s)"
                       % latest("tailscale_pam_subscription_limit_ratio"),
                       legend="{{tailscale_pam_limit_name}}")],
               unit="short", options=bargauge_opts(),
               desc="Configured subscription ceilings by stable limit name."), 10, 6),
    ]

    sessions = [
        (panel("Authorized sessions", "timeseries",
               [prom_t("sum by (tailscale_pam_service_name, tailscale_pam_session_type, tailscale_pam_session_authorization_result) "
                       "(rate(%s[%s]))" % (sel("tailscale_pam_sessions_total"), RI),
                       legend="{{tailscale_pam_service_name}} / {{tailscale_pam_session_type}} / {{tailscale_pam_session_authorization_result}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Sessions that reached the connector and were authorized. This is not an access-attempt or connection-health signal."), 12, 7),
        (panel("Active sessions", "bargauge",
               [prom_t("sum by (tailscale_pam_session_type) (%s)" % latest("tailscale_pam_sessions_active"),
                       legend="{{tailscale_pam_session_type}}")],
               unit="short", options=bargauge_opts(),
               desc="Active PAM sessions visible in the bounded newest-first polling prefix, derived from an absent end time. The API offers no active-session filter."), 6, 7),
        (panel("Killed sessions", "timeseries",
               [prom_t("sum by (tailscale_pam_session_type) (rate(%s[%s]))"
                       % (sel("tailscale_pam_sessions_killed_total"), RI),
                       legend="{{tailscale_pam_session_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Sessions marked killed by Border0."), 6, 7),
        (panel("Session duration (p50 / p90 / p99)", "timeseries",
               [prom_t("histogram_quantile(0.50, sum by (le, tailscale_pam_session_type) (rate(%s[%s])))"
                       % (sel("tailscale_pam_session_duration_seconds_bucket"), RI), legend="p50 / {{tailscale_pam_session_type}}"),
                prom_t("histogram_quantile(0.90, sum by (le, tailscale_pam_session_type) (rate(%s[%s])))"
                       % (sel("tailscale_pam_session_duration_seconds_bucket"), RI), legend="p90 / {{tailscale_pam_session_type}}"),
                prom_t("histogram_quantile(0.99, sum by (le, tailscale_pam_session_type) (rate(%s[%s])))"
                       % (sel("tailscale_pam_session_duration_seconds_bucket"), RI), legend="p99 / {{tailscale_pam_session_type}}")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Duration of completed PAM sessions by session type."), 12, 7),
        (panel("Session events", "timeseries",
               [prom_t("sum by (tailscale_pam_session_event_type, tailscale_pam_session_event_status) "
                       "(rate(%s[%s]))" % (sel("tailscale_pam_session_events_total"), RI),
                       legend="{{tailscale_pam_session_event_type}} / {{tailscale_pam_session_event_status}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Bounded PAM session-event types and statuses. Command text and other event metadata never become metric labels."), 12, 7),
    ]

    snapshots = [
        (panel("PAM configuration snapshots", "logs",
               [loki_t("%s | tailscale_snapshot_kind=`pam` | "
                       "event_name=`tailscale.pam.snapshot` |~ `$log_filter`" % LOKI_TN,
                       maxlines=500)],
               options=logs_opts(),
               desc="Opt-in PAM inventory and configuration-shape snapshots. Group chunks "
                    "by tailscale_snapshot_emission_id, require one matching "
                    "tailscale_snapshot_revision, then sort tailscale_snapshot_seq before "
                    "reassembly. Authentication objects, credentials and identity details "
                    "are removed before serialization. Enable with "
                    "collectors.pam.snapshot_enabled."), 24, 10),
    ]

    return [
        row("PAM inventory", inventory),
        row("Connector health", connector_health),
        row("Configuration shape", configuration),
        row("Organization and limits", organization),
        row("Session telemetry", sessions),
        row("Configuration history (explicit snapshot opt-in)", snapshots),
    ]
