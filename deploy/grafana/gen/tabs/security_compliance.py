"""tab_security_compliance() — the "Posture & Compliance" leaf of Security & Audit (#526).

Wave 2 split of the old 49-panel tabs/security.py leaf into four sub-tabs. This one owns
device posture: what the MDM/EDR integrations report, which devices fail an attribute, and
the client-hygiene coverage numbers.

Content is COPIED from tabs/security.py (rows "Posture integrations (MDM/EDR sync)",
"MDM device posture", "Devices failing posture", "Security posture", "Device posture log"),
plus TWO new panels for catalog signals that reached no panel anywhere before #526:

  * `tailscale.device.attribute.expiring` (log event, WARN) — per device+attribute, when a
    posture attribute's expiry falls inside the fixed 14-day warn window.
  * `tailscale_device_attribute_expiry_seconds` (gauge, ALERTABLE-ONLY before this panel) —
    the Unix epoch at which a posture attribute expires, charted as time remaining.

Both carry host identity, so both land in the per-device row behind hide_when=["pii_perdevice"]
alongside "Devices failing posture" — which is also the #526 decision-7 merge that removes that
single-panel row.

DE-DUPLICATED: tabs/events.py's "Posture log stream" panel and tabs/security.py's "Device
posture snapshot" were the same query (`event_name=`tailscale.device.posture``, maxLines 200)
under two titles on two tabs. Only the security.py one is carried forward — it is the one that
sits behind the host-name redaction gate and says so in its description.

NOT merged, deliberately: "Device posture log" stays its own row. Its gate is
present="has_posture" + hide_when=["pii_perdevice"] and the only topically adjacent row,
"Security posture", is present="has_posture" with NO redaction gate. Merging would put five
aggregate coverage panels — which contain no host identity at all — behind host-name
redaction. Decision 7 removes single-panel rows; it does not license broadening a PII gate.

Sentinel scoping (#526): all four are declared HERE at TAB scope. has_posture_integration and
has_device_attr came from tabs/security.py, has_posture and pii_perdevice from tabs/fleet.py,
all at DASHBOARD scope; a row gating on a sentinel nothing declares renders PERMANENTLY hidden
and is indistinguishable on screen from a correctly-gated empty row.
"""

from builder import (barchart_opts, bargauge_opts, logs_opts, loki_t, lot, merge, organize,
                     panel, PII, pii_sentinel, prom_t, row, sentinel, stat_opts, thr,
                     WIN_FAST, WIN_SLOW)

# Scrape-transport columns, excluded from every operator-facing table (#392).
_INFRA = ["Time", "__name__", "job", "instance",
          "service_instance_id", "service_name", "service_namespace"]


def tab_security_compliance(scope):
    sentinel("has_posture_integration", "tailscale_posture_integrations_count_ratio", scope)
    sentinel("has_device_attr", "tailscale_device_attribute_ratio", scope)
    sentinel("has_posture", "tailscale_device_posture_ratio", scope)
    # Same expression as tabs/fleet.py's DASHBOARD-scoped declaration — kept
    # byte-identical on purpose so the two never diverge into two meanings.
    pii_sentinel("pii_perdevice",
                 '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)'
                 % (PII, PII), scope)

    POS = lot("tailscale_device_posture_ratio", WIN_FAST)  # posture is emitted every scrape

    # -----------------------------------------------------------------------
    # Posture integrations (MDM/EDR sync) — from tabs/security.py, unchanged.
    # -----------------------------------------------------------------------
    posture_integ = [
        (panel("Integrations configured", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_posture_integrations_count_ratio", WIN_SLOW),
                       instant=True)],
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
               [prom_t("max(time() - %s)" % lot("tailscale_posture_integration_last_sync_seconds", WIN_SLOW),
                       instant=True)],
               unit="s", thresholds=thr([(None, "green"), (3600, "yellow"), (86400, "red")]),
               options=stat_opts(color="background"),
               novalue="No posture integration has reported a sync yet.",
               desc="Time since the least-recently-synced integration last synced (alert on staleness)."), 6, 5),
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
                                organize(exclude=_INFRA,
                                         rename={"tailscale_posture_provider": "Provider",
                                                 "tailscale_posture_integration": "Integration",
                                                 "Value #A": "Matched", "Value #B": "Possible",
                                                 "Value #C": "Provider hosts", "Value #D": "Last sync age"})],
               overrides=[{"matcher": {"id": "byName", "options": "Last sync age"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="Per integration: matched / possible-match / provider-host counts and sync age."), 24, 7),
    ]

    # -----------------------------------------------------------------------
    # MDM device posture (aggregate, no per-device identity) — unchanged.
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
                       % lot('tailscale_device_attribute_info_ratio', WIN_FAST),
                       legend="{{value}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Distribution of attribute values for the selected posture attribute."), 18, 6),
    ]

    # -----------------------------------------------------------------------
    # Per-device attribute risk. Hidden when pii_perdevice redaction is active
    # (host_name is PII, in the table columns and in the log bodies alike).
    # This row is the #526 decision-7 merge of the former single-panel
    # "Devices failing posture" row plus the two new attribute-expiry panels.
    # -----------------------------------------------------------------------
    _ATTR_EMPTY = ("No posture-attribute expiry series. Only attributes explicitly set WITH an "
                   "expiry appear here — most posture attributes never carry one, so an empty "
                   "panel is the normal reading, not a fault. Needs the devices collector with "
                   "collect_posture, and the attribute inside the attribute_namespaces allow-list.")
    attrisk = [
        (panel("Devices failing posture attr", "table",
               [prom_t("%s == 0"
                       % lot('tailscale_device_attribute_ratio', WIN_FAST),
                       instant=True, fmt="table")],
               transformations=[organize(exclude=_INFRA + ["Value"],
                                         rename={"host_name": "Host", "attribute": "Attribute"})],
               desc="Devices with a failing (0) posture attribute. Hidden when host-name redaction is active."), 24, 8),
        # #526: tailscale_device_attribute_expiry_seconds — ALERTABLE-ONLY before this
        # panel existed, i.e. an operator could be paged by it and find nothing on any
        # dashboard. Charted as time-until-expiry (`... - time()`), the same idiom the
        # device/API key expiry panels use. A NEGATIVE value is an attribute that has
        # already lapsed.
        (panel("Posture attribute expiry — time remaining", "table",
               [prom_t("%s - time()"
                       % lot("tailscale_device_attribute_expiry_seconds", WIN_SLOW),
                       instant=True, fmt="table")],
               unit="s", novalue=_ATTR_EMPTY,
               thresholds=thr([(None, "red"), (0, "yellow"), (1209600, "green")]),
               transformations=[organize(exclude=_INFRA,
                                         rename={"host_name": "Host", "host_id": "Device ID",
                                                 "attribute": "Attribute", "Value": "Expires in"})],
               desc="Time left on each posture attribute that carries an explicit expiry, per device "
                    "and attribute. A negative value has already lapsed. Only attributes explicitly "
                    "set WITH an expiry appear at all — most never carry one, so an empty panel is "
                    "normal rather than a fault. Gated by collect_posture and the "
                    "attribute_namespaces allow-list."), 24, 8),
        # #526: tailscale.device.attribute.expiring — the WARN log behind the table above.
        (panel("Expiring posture attributes (log detail)", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.attribute.expiring` "
                       "|~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="One WARN record per device+attribute whose posture-attribute expiry falls inside "
                    "the fixed 14-day warn window and has not yet lapsed. Carries host_name, host_id, "
                    "the attribute key and tailscale_device_attribute_expires_in_days. Needs the "
                    "devices collector with collect_posture; hidden when host-name redaction is "
                    "active."), 24, 9),
    ]

    # -----------------------------------------------------------------------
    # Security posture (client hygiene coverage) — from tabs/security.py, unchanged.
    # -----------------------------------------------------------------------
    posture = [
        # The {label=...} selector MUST be INSIDE last_over_time(...) — appending a
        # matcher to a function result (lot(x){...}) is a PromQL parse error.
        (panel("Auto-update coverage", "stat",
               [prom_t("count(%s) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_posture_ratio{auto_update=\"true\"}", WIN_FAST),
                          lot("tailscale_device_posture_ratio", WIN_FAST)), instant=True)],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices with Tailscale client auto-update enabled."), 6, 6),
        (panel("State-encryption coverage", "stat",
               [prom_t("count(%s) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_posture_ratio{encrypted=\"true\"}", WIN_FAST),
                          lot("tailscale_device_posture_ratio", WIN_FAST)), instant=True)],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.8, "yellow"), (0.95, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices reporting an encrypted local state store."), 6, 6),
        (panel("Devices needing update", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_update_available_ratio"), instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="background"),
               novalue="No device update data — needs the devices collector and a control plane "
                       "that reports update availability.",
               desc="Count of devices with a client update available."), 6, 6),
        (panel("Release track split", "barchart",
               [prom_t("count by (track) (max by (track, host_id) (%s))" % POS,
                       legend="{{track}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Device count by Tailscale release track (stable/unstable), from posture data."), 6, 6),
        (panel("Client version distribution", "barchart",
               [prom_t("count by (ts_version) (max by (ts_version, host_id) (%s))" % POS,
                       legend="{{ts_version}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Device count by reported Tailscale client version, from posture data."), 24, 7),
    ]

    # Per-device posture snapshot log — the surviving copy of the two identical
    # posture log panels (see module docstring). Own row on purpose: see docstring.
    posturelog = [
        (panel("Device posture snapshot", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.posture` "
                       "|~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="Per-device posture log stream (host identity in body — hidden when host-name "
                    "redaction is active). Filter with the Log filter variable."), 24, 10),
    ]

    return [
        row("Posture integrations (MDM/EDR sync)", posture_integ, present="has_posture_integration"),
        row("MDM device posture", mdmposture, present="has_device_attr"),
        row("Posture attribute risk — per device", attrisk,
            present="has_device_attr", hide_when=["pii_perdevice"]),
        row("Security posture", posture, present="has_posture"),
        row("Device posture log", posturelog, present="has_posture", hide_when=["pii_perdevice"]),
    ]
