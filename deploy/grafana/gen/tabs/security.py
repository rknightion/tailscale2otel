"""tab_security() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, hq, logs_opts, loki_t, lot, merge, organize,
                     panel, PII, pii_sentinel, prom_t, RI, row, sentinel, stat_opts, thr,
                     ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import BOOL_HEALTHY_OFF

# Scrape-transport columns, excluded from every operator-facing table (#392).
_INFRA = ["Time", "__name__", "job", "instance",
          "service_instance_id", "service_name", "service_namespace"]


def tab_security():
    # Presence sentinels this tab declares (moved from variables.py, #495).
    sentinel("has_audit", "tailscale_config_audit_events_total")  # also consumed by events.py
    sentinel("has_posture_integration", "tailscale_posture_integrations_count_ratio")
    sentinel("has_tailnet_lock", "tailscale_tailnet_lock_errors_ratio")
    sentinel("has_acl_risk", "tailscale_acl_unrestricted_rules_ratio")  # also consumed by overview.py
    sentinel("has_audit_changes", "tailscale_config_audit_changes_total")
    sentinel("has_invites_dev", "tailscale_device_invites_count_ratio")
    sentinel("has_device_attr", "tailscale_device_attribute_ratio")
    sentinel("has_posture_int", "tailscale_posture_integration_matched_ratio")  # dead: no row gates on it
    pii_sentinel("pii_actor",
                 '(%s{category="emails"} == 0) and ignoring(category) (%s{category="user_display_names"} == 0)'
                 % (PII, PII))

    AUD = "{service_name=\"tailscale2otel\"} | event_name=`tailscale.config.audit`"
    POS = lot("tailscale_device_posture_ratio", WIN_FAST)  # posture is emitted every scrape

    # -----------------------------------------------------------------------
    # Task 1.5 Step 4: PII-split audit row into aggregate (no gate) + actor row (pii_actor)
    # -----------------------------------------------------------------------
    audit = [
        (panel("Audit actions over time", "timeseries",
               [loki_t("sum by (tailscale_audit_action) (count_over_time(%s [$__auto]))" % AUD,
                       legend="{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right")), 12, 7),
        (panel("Audit events (range)", "stat",
               [loki_t("sum(count_over_time(%s [$__range]))" % AUD, instant=True)],
               unit="short", novalue="0", options=stat_opts(color="value")), 6, 7),
        (panel("Failed changes — WARN (range)", "stat",
               # severity field is severity_text (value "INFO"/"WARN"), verified live — NOT `severity`.
               # novalue="0": LogQL count_over_time over an empty match yields no series (not 0),
               # so show 0 rather than "No data" on a healthy tailnet with no WARN audits.
               [loki_t("sum(count_over_time(%s | severity_text=`WARN` [$__range]))" % AUD, instant=True)],
               unit="short", novalue="0", thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
               desc="Audit events emitted at WARN (the event carried an error)."), 6, 7),
        (panel("Audit events by target type", "timeseries",
               [loki_t("sum by (tailscale_target_type) "
                       "(count_over_time(%s | tailscale_target_type != `` [$__auto]))" % AUD,
                       legend="{{tailscale_target_type}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts()), 24, 7),
    ]
    # Actor/identity panels moved here — hidden when pii_actor redaction is active
    # (actor login and actor emails in log bodies are PII).
    audit_actors = [
        # Rendered as timeseries, not barchart — this dashboard has no Loki barchart
        # precedent (all barcharts are Prometheus instant+table); the proven Loki
        # aggregation pattern here is the range timeseries (see "Log volume by event").
        (panel("Top $topn actors over time", "timeseries",
               [loki_t("topk($topn, sum by (user_name) "
                       "(count_over_time(%s | user_name != `` [$__auto])))" % AUD,
                       legend="{{user_name}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 8),
        (panel("Top $topn targets over time", "timeseries",
               [loki_t("topk($topn, sum by (tailscale_target_name) "
                       "(count_over_time(%s | tailscale_target_name != `` [$__auto])))" % AUD,
                       legend="{{tailscale_target_name}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right")), 12, 8),
        (panel("Recent configuration changes", "logs",
               [loki_t("%s |~ `$log_filter`" % AUD, maxlines=200)],
               options=logs_opts(), desc="Live audit stream; filter with the Log filter variable."), 24, 10),
    ]

    # -----------------------------------------------------------------------
    # Task 1.5 Step 1: NEW aclrisk row — present="has_acl_risk" (no PII)
    # -----------------------------------------------------------------------
    aclrisk = [
        (panel("Wildcard rules", "bargauge",
               [prom_t("sum by (tailscale_acl_section, tailscale_acl_position) (%s)"
                       % lot("tailscale_acl_wildcard_rules_ratio", WIN_SLOW),
                       legend="{{tailscale_acl_section}}/{{tailscale_acl_position}}")],
               unit="short", options=bargauge_opts(),
               desc="Number of ACL rules containing wildcards, by section and position."), 8, 6),
        (panel("Unrestricted rules (grants)", "stat",
               [prom_t("sum(%s) or vector(0)"
                       % lot('tailscale_acl_unrestricted_rules_ratio{tailscale_acl_section="grants"}', WIN_SLOW),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Grant rules with no destination restriction."), 4, 6),
        (panel("Unrestricted rules (acls)", "stat",
               [prom_t("sum(%s) or vector(0)"
                       % lot('tailscale_acl_unrestricted_rules_ratio{tailscale_acl_section="acls"}', WIN_SLOW),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="ACL rules with no destination restriction."), 4, 6),
        (panel("SSH wildcard", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_acl_ssh_wildcard_ratio", WIN_SLOW),
                       instant=True)],
               unit="short", mappings=BOOL_HEALTHY_OFF, thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Whether any SSH rule uses a wildcard source or destination."), 4, 6),
        (panel("Auto-approvers by kind", "barchart",
               [prom_t("sum by (tailscale_acl_autoapprover_kind) (%s)"
                       % lot("tailscale_acl_autoapprovers_ratio", WIN_SLOW),
                       legend="{{tailscale_acl_autoapprover_kind}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Count of auto-approver entries by kind (routes/exit-nodes)."), 12, 6),
        (panel("Posture-gated rules", "bargauge",
               [prom_t("sum by (tailscale_acl_section) (%s)"
                       % lot("tailscale_acl_posture_gated_rules_ratio", WIN_SLOW),
                       legend="{{tailscale_acl_section}}")],
               unit="short", options=bargauge_opts(),
               desc="Rules that require a passing device-posture check, by section."), 12, 6),
    ]

    # -----------------------------------------------------------------------
    # Task 1.5 Step 2: NEW changes row — present="has_audit_changes"
    # -----------------------------------------------------------------------
    changes = [
        (panel("Security/lifecycle changes/s", "timeseries",
               [prom_t("sum by (tailscale_audit_change, tailscale_audit_action) "
                       "(rate(tailscale_config_audit_changes_total[%s]))" % RI,
                       legend="{{tailscale_audit_change}}/{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Rate of audit change events by change kind and action."), 12, 7),
        (panel("Device churn", "timeseries",
               [prom_t('sum by (tailscale_audit_action) '
                       '(rate(tailscale_config_audit_changes_total{tailscale_audit_change="device"}[%s]))' % RI,
                       legend="{{tailscale_audit_action}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Device add/remove/update rate over time."), 12, 7),
        (panel("Changes by actor type", "barchart",
               [prom_t("sum by (tailscale_actor_type) "
                       "(increase(tailscale_config_audit_changes_total[$__range]))",
                       legend="{{tailscale_actor_type}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Total change events in the selected range, broken out by actor type (user/api-key/etc)."), 8, 7),
    ]

    # -----------------------------------------------------------------------
    # Task 1.5 Step 3: NEW devinvites row — present="has_invites_dev" (enum labels; no PII)
    # -----------------------------------------------------------------------
    devinvites = [
        (panel("Device shares: pending vs accepted", "timeseries",
               [prom_t("sum by (tailscale_device_invite_accepted) (%s)"
                       % lot("tailscale_device_invites_count_ratio"),
                       legend="accepted={{tailscale_device_invite_accepted}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Count of device share invites grouped by accepted status."), 12, 6),
        (panel("Exit-node-granting shares", "stat",
               [prom_t("sum(%s) or vector(0)"
                       % lot('tailscale_device_invites_count_ratio{tailscale_device_invite_allow_exit_node="true"}'),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Device share invites that also grant exit-node access."), 6, 6),
        (panel("Multi-use shares", "stat",
               [prom_t("sum(%s) or vector(0)"
                       % lot('tailscale_device_invites_count_ratio{tailscale_device_invite_multi_use="true"}'),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Device share invites that can be reused more than once."), 6, 6),
    ]

    # -----------------------------------------------------------------------
    # Task 1H.2: NEW mdmposture row — present="has_device_attr" (aggregate panels, no PII)
    # Per-device table goes in separate mdmfail row with hide_when=["pii_perdevice"].
    # -----------------------------------------------------------------------
    mdmposture = [
        (panel("Encryption coverage", "stat",
               [prom_t("avg(%s)"
                       % lot('tailscale_device_attribute_ratio{attribute="intune:isEncrypted"}', WIN_FAST),
                       instant=True)],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Average encryption coverage across devices (Intune isEncrypted attribute)."), 6, 6),
        (panel("Compliance distribution", "barchart",
               [prom_t("count by (value) (%s)"
                       % lot('tailscale_device_attribute_info_ratio{attribute=~"$posture_attr"}', WIN_FAST),
                       legend="{{value}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Distribution of attribute values for the selected posture attribute."), 18, 6),
    ]
    # Per-device table: hidden when pii_perdevice redaction is active (host_name is PII).
    mdmfail = [
        (panel("Devices failing posture attr", "table",
               [prom_t("%s == 0"
                       % lot('tailscale_device_attribute_ratio{attribute=~"$posture_attr", host_name=~"$host_name"}', WIN_FAST),
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=["Time", "__name__", "job", "instance",
                             "service_instance_id", "service_name", "service_namespace", "Value"],
                   rename={"host_name": "Host", "attribute": "Attribute"})],
               desc="Devices with a failing (0) posture attribute. Hidden when host-name redaction is active."), 24, 8),
    ]

    # -----------------------------------------------------------------------
    # Task 1H.6: NEW auditcorr row — present="has_audit_changes", hide_when=["pii_actor"]
    # -----------------------------------------------------------------------
    auditcorr = [
        (panel("Audit: metric vs log", "timeseries",
               [prom_t("sum by (tailscale_audit_change, tailscale_audit_action) "
                       "(rate(tailscale_config_audit_changes_total[%s]))" % RI,
                       legend="metric {{tailscale_audit_change}}/{{tailscale_audit_action}}"),
                loki_t("sum(rate(%s [%s]))" % (AUD, RI),
                       legend="log events")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               novalue="0",
               desc="Metric change counters vs Loki log event rate — divergence indicates missing ingestion path."), 24, 8),
    ]
    # Action breakdown has no actor context — can be ungated; placed in separate row.
    auditbreakdown = [
        (panel("Audit action breakdown (logs)", "timeseries",
               [loki_t("sum by (tailscale_audit_action, tailscale_target_type) "
                       "(count_over_time(%s [$__auto]))" % AUD,
                       legend="{{tailscale_audit_action}}/{{tailscale_target_type}}")],
               unit="cps", novalue="0", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Audit log action/target-type breakdown — no actor identity, safe to show when PII redaction is active."), 24, 7),
    ]
    # Per-device posture snapshot log — hide when pii_perdevice (host_name in log body).
    posturelog = [
        (panel("Device posture snapshot", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.posture` |~ `$log_filter`",
                       maxlines=200)],
               options=logs_opts(),
               desc="Per-device posture log stream (host identity in body — hidden when host-name redaction is active)."), 24, 10),
    ]

    # -----------------------------------------------------------------------
    # Existing posture panels (unchanged)
    # -----------------------------------------------------------------------
    posture = [
        # The {label=...} selector MUST be INSIDE last_over_time(...) — appending a
        # matcher to a function result (lot(x){...}) is a PromQL parse error.
        (panel("Auto-update coverage", "stat",
               [prom_t("count(%s) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_posture_ratio{auto_update=\"true\"}", WIN_FAST),
                          lot("tailscale_device_posture_ratio", WIN_FAST)), instant=True)],
               unit="percentunit", min_=0, max_=1, thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices with Tailscale client auto-update enabled."), 6, 6),
        (panel("State-encryption coverage", "stat",
               [prom_t("count(%s) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_posture_ratio{encrypted=\"true\"}", WIN_FAST),
                          lot("tailscale_device_posture_ratio", WIN_FAST)), instant=True)],
               unit="percentunit", min_=0, max_=1, thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices reporting an encrypted local state store."), 6, 6),
        (panel("Devices needing update", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_update_available_ratio"), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="background"),
               novalue="No device update data — needs the devices collector and a control plane "
                       "that reports update availability."), 6, 6),
        (panel("Release track split", "barchart",
               [prom_t("count by (track) (max by (track, host_id) (%s))" % POS,
                       legend="{{track}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 6, 6),
        (panel("Client version distribution", "barchart",
               [prom_t("count by (ts_version) (max by (ts_version, host_id) (%s))" % POS,
                       legend="{{ts_version}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 24, 7),
    ]
    expiry = [
        (panel("Device keys ≤7d", "stat",
               [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_device_key_expiry_seconds", WIN_SLOW), lot("tailscale_device_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]), options=stat_opts(color="background")), 6, 5),
        (panel("Device keys ≤30d", "stat",
               [prom_t("count((%s - time() < 30*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_device_key_expiry_seconds", WIN_SLOW), lot("tailscale_device_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", options=stat_opts()), 6, 5),
        (panel("API/auth keys ≤7d", "stat",
               [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_key_expiry_seconds", WIN_SLOW), lot("tailscale_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="background")), 6, 5),
        (panel("API/auth keys ≤30d", "stat",
               [prom_t("count((%s - time() < 30*86400) and (%s - time() > 0)) or vector(0)"
                       % (lot("tailscale_key_expiry_seconds", WIN_SLOW), lot("tailscale_key_expiry_seconds", WIN_SLOW)), instant=True)],
               unit="short", options=stat_opts()), 6, 5),
    ]

    # -----------------------------------------------------------------------
    # Task 1H.7/1H.8: posture_integ with added "Posture match rate" stat
    # -----------------------------------------------------------------------
    posture_integ = [
        (panel("Integrations configured", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_posture_integrations_count_ratio", WIN_SLOW), instant=True)],
               unit="short", options=stat_opts(color="value"),
               desc="Configured device-posture (MDM/EDR) integrations, e.g. Intune."), 6, 5),
        (panel("Devices matched by integration", "bargauge",
               [prom_t("max by (tailscale_posture_provider, tailscale_posture_integration) (%s)"
                       % lot("tailscale_posture_integration_matched_ratio", WIN_SLOW),
                       legend="{{tailscale_posture_provider}} / {{tailscale_posture_integration}}")],
               unit="short", options=bargauge_opts(),
               desc="Devices matched to a provider host by each posture integration."), 12, 5),
        # No zero-fill: last_sync is emitted only once a sync has occurred, and "0s ago"
        # is the healthiest possible reading of "no integration has ever synced".
        (panel("Oldest sync age", "stat",
               [prom_t("max(time() - %s)" % lot("tailscale_posture_integration_last_sync_seconds", WIN_SLOW), instant=True)],
               unit="s", thresholds=thr([(None, "green"), (3600, "yellow"), (86400, "red")]),
               options=stat_opts(color="background"),
               novalue="No posture integration has reported a sync yet.",
               desc="Time since the least-recently-synced integration last synced (alert on staleness)."), 6, 5),
        # Task 1H.8: match rate = matched / possible-match (clamped to avoid div-by-zero)
        (panel("Posture match rate", "stat",
               [prom_t("%s / clamp_min(%s, 1)"
                       % (lot("tailscale_posture_integration_matched_ratio", WIN_SLOW),
                          lot("tailscale_posture_integration_possible_matched_ratio", WIN_SLOW)),
                       instant=True)],
               unit="percentunit",
               thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of possible-match devices that were actually matched by the integration."), 6, 5),
        (panel("Integration sync detail", "table",
               [prom_t(lot("tailscale_posture_integration_matched_ratio", WIN_SLOW), instant=True, fmt="table", refid="A"),
                prom_t(lot("tailscale_posture_integration_possible_matched_ratio", WIN_SLOW), instant=True, fmt="table", refid="B"),
                prom_t(lot("tailscale_posture_integration_provider_hosts_ratio", WIN_SLOW), instant=True, fmt="table", refid="C"),
                prom_t("time() - %s" % lot("tailscale_posture_integration_last_sync_seconds", WIN_SLOW), instant=True, fmt="table", refid="D")],
               transformations=[merge(),
                                organize(exclude=["Time", "__name__", "job", "instance",
                                                  "service_instance_id", "service_name", "service_namespace"],
                                         rename={"tailscale_posture_provider": "Provider",
                                                 "tailscale_posture_integration": "Integration",
                                                 "Value #A": "Matched", "Value #B": "Possible",
                                                 "Value #C": "Provider hosts", "Value #D": "Last sync age"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last sync age"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per integration: matched / possible-match / provider-host counts and sync age."), 24, 7),
    ]

    # -----------------------------------------------------------------------
    # tlock: add hide_when=["pii_perdevice"] — tailnet-lock logs expose per-device host identity
    # -----------------------------------------------------------------------
    tlock = [
        (panel("Tailnet-lock errors", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_tailnet_lock_errors_ratio", WIN_FAST), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]), options=stat_opts(color="background"),
               desc="Devices with a non-empty tailnet-lock error (e.g. an unsigned node). >0 means a "
                    "signing node must sign the affected keys."), 6, 6),
        (panel("Nodes with tailnet-lock errors", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.tailnet_lock_error` |~ `$log_filter`", maxlines=100)],
               options=logs_opts(), desc="Per-device tailnet-lock error events; the error text is the log body."), 18, 6),
    ]

    # -----------------------------------------------------------------------
    # Task 1H.8: contact stat — "Contact needs verification" (single global stat, no gate)
    # -----------------------------------------------------------------------
    contact = [
        # No zero-fill: this row is ungated, so with the contacts collector off the
        # zero read as a green "all contacts verified" for a tailnet nobody had checked.
        (panel("Contact needs verification", "stat",
               [prom_t("max(%s)" % lot("tailscale_contact_needs_verification_ratio", WIN_SLOW),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               novalue="No contact data — enable the contacts collector (collectors.contacts).",
               desc="Whether any tailnet contact address requires re-verification (admin/security/billing)."), 6, 5),
    ]

    # -----------------------------------------------------------------------
    # #392/#401: credential inventory. Both signals are fleet aggregates that stay
    # available when cardinality.per_entity.key is off, so the row is ungated and
    # each panel names the collector it needs instead.
    # -----------------------------------------------------------------------
    keyinv = [
        (panel("Key age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_keys_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_keys_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No key age data — needs the keys collector (collectors.keys). "
                       "Keys whose creation time the API omits are skipped.",
               desc="How old the tailnet's credentials are. A rising p90 means long-lived keys "
                    "nobody is rotating, which expiry panels alone cannot show."), 12, 6),
        (panel("Keys by owner and type", "bargauge",
               [prom_t("sum by (tailscale_key_owner, tailscale_key_type) (%s)"
                       % lot("tailscale_keys_by_owner_ratio", WIN_SLOW),
                       legend="{{tailscale_key_owner}} / {{tailscale_key_type}}")],
               unit="short", options=bargauge_opts(),
               novalue="No key ownership data — needs the keys collector (collectors.keys). "
                       "Keys with no owning user are excluded.",
               desc="Who holds the tailnet's credentials, by owning user id and key type. Concentration "
                    "on one owner is a departure risk; the owner is an opaque user id, not an email."), 12, 6),
    ]

    # #401 key-owner half: how tightly each OAuth client is scoped. 0 allowed tags
    # means unrestricted, which is the security-relevant reading — the exporter
    # emits it at 0 rather than omitting it for exactly that reason.
    #
    # Gated on has_key_scopes: tailscale.key.scopes and tailscale.key.allowed_tags
    # are both emitted only under cardinality.per_entity.key, so the scopes gauge is
    # the closest available presence proxy for "per-key series exist".
    # PII: tailscale.key.description is operator free text and routinely names a
    # person, so the row hides under the same actor-redaction gate as the audit rows.
    oauthscope = [
        (panel("Unrestricted OAuth clients", "stat",
               [prom_t("count(%s == 0)" % lot("tailscale_key_allowed_tags_ratio", WIN_SLOW),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               novalue="No OAuth client data — needs the keys collector (collectors.keys) and "
                       "cardinality.per_entity.key.",
               desc="OAuth clients whose device-create capability is restricted to no tags at all, "
                    "so any device they register can claim any tag."), 6, 6),
        (panel("OAuth client tag restrictions", "table",
               [prom_t(lot("tailscale_key_allowed_tags_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_INFRA,
                   rename={"tailscale_key_id": "Key", "tailscale_key_owner": "Owner",
                           "tailscale_key_description": "Description",
                           "tailscale_key_type": "Type", "Value": "Allowed tags"})],
               novalue="No OAuth client data — needs the keys collector (collectors.keys) and "
                       "cardinality.per_entity.key.",
               desc="Allowed-tag count per OAuth client credential. 0 is unrestricted."), 18, 6),
    ]

    # -----------------------------------------------------------------------
    # #392/#401: how long identities and outstanding invites have been sitting
    # around. All three are bounded fleet histograms with no per-entity labels, so
    # the row is ungated and each panel names its own prerequisite.
    # -----------------------------------------------------------------------
    identityage = [
        (panel("User account age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_users_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_users_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No user age data — needs the users collector (collectors.users). "
                       "Users with no reported creation time are skipped.",
               desc="Age of the tailnet's user accounts. Users with no creation time are omitted "
                    "rather than reported as age zero."), 8, 6),
        (panel("Pending user-invite age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_user_invites_pending_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_user_invites_pending_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No pending user-invite age data — needs the users collector "
                       "(collectors.users) and at least one emailed invite.",
               desc="Time since Tailscale last emailed each unaccepted user invite. Manual-link "
                    "invites carry no delivery timestamp and are omitted."), 8, 6),
        (panel("Pending device-invite age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_device_invites_pending_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_device_invites_pending_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No pending device-invite age data — needs collect_device_invites. "
                       "Accepted invites are excluded.",
               desc="How long unaccepted device shares have been outstanding. A long tail is a "
                    "standing offer of access nobody took up and nobody revoked."), 8, 6),
    ]

    return [
        row("ACL risk indicators", aclrisk, present="has_acl_risk"),
        row("Audit changes", changes, present="has_audit_changes"),
        row("Device share invites", devinvites, present="has_invites_dev"),
        row("Configuration audit", audit, present="has_audit"),
        row("Configuration audit — actors", audit_actors, present="has_audit", hide_when=["pii_actor"]),
        row("Audit correlation", auditcorr, present="has_audit_changes", hide_when=["pii_actor"]),
        row("Audit action breakdown", auditbreakdown, present="has_audit"),
        row("Posture integrations (MDM/EDR sync)", posture_integ, present="has_posture_integration"),
        row("MDM device posture", mdmposture, present="has_device_attr"),
        row("Devices failing posture", mdmfail, present="has_device_attr", hide_when=["pii_perdevice"]),
        row("Security posture", posture, present="has_posture"),
        row("Device posture log", posturelog, present="has_posture", hide_when=["pii_perdevice"]),
        row("Tailnet lock", tlock, present="has_tailnet_lock", hide_when=["pii_perdevice"]),
        row("Contact verification", contact),
        row("Key & access expiry risk", expiry),
        row("Key inventory & age", keyinv),
        row("OAuth client tag scope", oauthscope,
            present="has_key_scopes", hide_when=["pii_actor"]),
        row("Identity & invite hygiene", identityage),
    ]
