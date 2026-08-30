"""tab_devices_inventory() — the "Inventory & Hygiene" sub-tab of Devices (#526).

Content is LIFTED from tabs/fleet.py's inventory/hygiene rows: "Inventory", "Authorization
& sharing" (the three-population split, #392), "Trends", "Fleet hygiene" and the per-device
"Device health" tables. tab_fleet() was one 59-panel leaf; #526 splits it into three
sub-tabs, of which this is the "what is in the fleet, and is it looked after" one.

Three catalog signals that reached NO panel anywhere before #526 land here, per the issue's
shrink-only pending-panel ledger:

  * tailscale.device.distro (`tailscale_device_distro_ratio`) — the PER-DEVICE distro gauge,
    charted as a fleet breakdown by distro name, NOT one series per device. It sits beside
    the pre-existing "Devices by distribution", which reads the unconditional fleet ROLLUP
    (tailscale_devices_by_distro_ratio): the roll-up is always emitted, this one needs
    cardinality.per_entity.device and honours the tab's host filter. Same fleet-roll-up vs
    per-device-derived pairing the "Tailscale SSH enabled (fleet)/(devices)" stats already use.
  * tailscale.devices.by_country (`tailscale_devices_by_country_ratio`) — devices per country.
  * tailscale.device_invite — a LOG event, not a metric, so it is a Loki logs panel on the
    "Authorization & sharing" row, which is the row that covers device sharing.
  * tailscale.device.change — a LOG event, not a metric, so it is a Loki logs panel on its own
    "Device change log" row. The row is hidden when per-device identity is redacted.

Row consolidation (#526 decision 7) — two of fleet.py's single-panel rows are folded in:

  * "Exporter version" -> "Fleet hygiene". It loses present="has_version_skew", which was a
    mismatched gate anyway (the panel reads tailscale2otel_update_available_ratio, the
    exporter's OWN update check, gated on per-DEVICE version skew) and it already carries a
    noValue naming version_checks.self.
  * "Version staleness (top-N)" -> "Device health", NOT "Fleet hygiene" as the brief asked.
    That table renders a host_name column, so it must keep hide_when=["pii_perdevice"];
    "Fleet hygiene" is ungated and merging there would have dropped the PII gate (which
    test_fleet_signals.test_every_row_exposing_a_hostname_is_pii_gated catches), while
    gating "Fleet hygiene" instead would hide nine non-PII aggregates. "Device health" is
    already the pii_perdevice-gated per-device TABLE row, so the panel keeps its PII gate
    and gains the row's neighbours. It loses present="has_version_skew", so it now carries a
    noValue naming version_checks.devices instead of rendering blank.

  has_version_skew consequently gates nothing and is deliberately NOT declared — a declared
  sentinel that gates nothing fails the build.

One panel is DROPPED as a near-duplicate: "Devices by tag" (Inventory row,
`count by (tailscale_tags)` over the online gauge) versus "Fleet hygiene"'s "Fleet tags
(rollup)" (`tailscale_devices_by_tag_ratio` by `tailscale_tag`). Both answer "how many
devices per tag"; the roll-up is the per-tag-rather-than-per-tag-COMBINATION one and is the
signal a catalog metric of its own is emitted for, and the untagged count the dropped panel
also showed already has its own "Untagged" stat. `tailscale_device_online_ratio` keeps four
other panels, so no signal loses its last panel.

Sentinel scoping (#526): pii_perdevice is declared HERE at TAB scope. tabs/fleet.py declared
it at DASHBOARD scope and that module is gone; it is also consumed by four rows in
tabs/security.py and by both sibling Devices sub-tabs, each of which must declare it at its
own scope — the registry allows one name in two different TAB scopes and forbids only the
dashboard+tab mix. A row whose gate names a sentinel nothing declares in the same scope
renders permanently hidden, which on screen is indistinguishable from a correctly-gated
empty row.
"""

from builder import (autogrid_row, barchart_opts, bargauge_opts, hq, logs_opts, loki_t, lot,
                     merge, organize, panel, PII, pii_sentinel, prom_t, row,
                     stat_opts, thr, ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_OFF, BOOL_HEALTHY_ON, BOOL_NEUTRAL
from tabs._devices_common import NO_PER_DEVICE, TP, flag_map, host_drilldown

# Tailnet/provider selectors (#392). Both are real per-series metric LABELS (not resource
# attributes), so a panel filters them straight in the selector — see the note in builder.py.
# allValue ".*" also matches series that carry no such label, so a single-tailnet deployment
# reads exactly as it did before.

# The three device populations #392 is about. They are different populations, not slices of
# one: an unauthorized device is a pending admin decision, a shared-in device belongs to
# someone else's tailnet entirely, and the old fleet totals summed all three into one number.
#
# The matcher sets PARTITION {authorized, external}^2 — every emitted series of
# tailscale_devices_count_ratio is selected by exactly one of them (both labels are always
# present; devices.go sets each from a bool). So the three panels can neither double-count a
# device nor hide one, which is asserted directly in test_fleet_signals.AuthorizationSplitTest.
_POPULATIONS = {
    "authorized": 'tailscale_authorized="true", tailscale_external="false"',
    "unauthorized": 'tailscale_authorized="false", tailscale_external="false"',
    "external": 'tailscale_external="true"',
}

# Every population panel reads the same gauge; absence means the devices collector never ran,
# so none of them zero-fill.
_NO_INVENTORY = ("No device inventory — needs the devices collector "
                 "(collectors.devices) reporting tailscale.devices.count.")


# The two #526 additions that need cardinality/enrichment prerequisites named rather than
# zero-filled: absence is "never collected", not "none found".
_NO_DEVICE_DISTRO = ("No per-device distribution series — needs cardinality.per_entity.device "
                     "and the devices collector (collectors.devices). Only clients that report "
                     "a distribution are counted at all.")
_NO_GEO = ("No device geolocation series — needs enrichment.geoip.enabled and "
           "collect_connectivity. Devices with no globally-routable endpoint are absent "
           "entirely; there is no 'unknown' bucket to read as zero.")


def _pop(which, win=WIN_SLOW):
    """last_over_time over one device population's slice of the fleet count."""
    return lot("tailscale_devices_count_ratio{%s, %s}" % (_POPULATIONS[which], TP), win)


def _pop_count(which, win=WIN_SLOW):
    """Population count, zero only while the unfiltered inventory family is present."""
    return "sum(%s) or (0 * sum(%s))" % (
        _pop(which, win), lot("tailscale_devices_count_ratio{%s}" % TP, win))


def tab_devices_inventory(scope):
    # The one sentinel this sub-tab's rows reference, declared at THIS tab's scope.
    pii_sentinel("pii_perdevice",
                 '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)'
                 % (PII, PII), scope)

    # os_type and host_name stay explicit dropdowns (#526 decision 3): an operator picks
    # from them constantly and a short known option list beats free-form typing. The
    # user/tag/posture-attribute filters they used to sit beside are gone from the query
    # entirely — they folded into the `device_filters` adhoc variable, which injects its
    # own matchers into every query against this datasource, so naming them here as well
    # would filter twice and pin the panel to whatever the deleted variables defaulted to.
    df = "{os_type=~\"$os_type\", host_name=~\"$host_name\", %s}" % TP
    on = lot("tailscale_device_online_ratio" + df)

    # Shared infra label exclusion list for instant-vector tables
    _infra = ["Time", "__name__", "job", "instance", "host_id",
              "service_instance_id", "service_name", "service_namespace"]

    inv = [
        (panel("Online", "stat", [prom_t("count(%s == 1) or vector(0)" % on)],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background"),
               desc="Devices currently reporting as online, within the tab's OS/host/user/tag "
                    "filters."), 3, 5),
        (panel("Total", "stat", [prom_t("count(%s) or vector(0)" % on)], unit="short", options=stat_opts(),
               desc="Devices the control plane knows about, online or not, within the tab's "
                    "filters."), 3, 5),
        (panel("Offline", "stat", [prom_t("count(%s == 0) or vector(0)" % on)], unit="short", options=stat_opts(color="value"),
               desc="Devices registered but not currently connected. Read alongside 'Stale "
                    "devices (>30d)' below, which separates briefly-offline from abandoned."), 3, 5),
        # No zero-fill: absent update-availability means the control plane never reported
        # it (Headscale) or per-device metrics are off — not "everything is current" (#385).
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_update_available_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="value"),
               novalue="No device update data — needs the devices collector and a control plane "
                       "that reports update availability.",
               desc="Devices whose control plane reports a newer Tailscale client available."), 3, 5),
        # A. Fix: count actually-connected users (tailscale_user_connected_ratio == 1) not a device-derived proxy
        (panel("Distinct users", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_user_connected_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               novalue="No per-user data — needs the users collector and "
                       "cardinality.per_entity.user.",
               desc="Users with at least one connected device, counted from the per-user "
                    "connected gauge rather than inferred from device ownership."), 3, 5),
        # #392: the old expression grouped by os/authorized/external and then summed
        # the state labels straight back out, so an unauthorized macOS laptop was
        # indistinguishable from an authorized one. Keep the state in the legend.
        (panel("Devices by OS", "bargauge",
               [prom_t("sum by (os_type, tailscale_authorized, tailscale_external) (%s)"
                       % lot("tailscale_devices_count_ratio{%s}" % TP, WIN_SLOW),
                       legend="{{os_type}} · authorized={{tailscale_authorized}} · external={{tailscale_external}}")],
               unit="short", options=bargauge_opts(),
               desc="Device count per OS, kept split by authorization and shared-in state — "
                    "an OS total that sums those away hides the devices an admin still has to act on."), 9, 5),
    ]

    overtime = [
        panel("Online vs total", "timeseries",
              [prom_t("count(tailscale_device_online_ratio%s == 1)" % df, legend="online"),
               prom_t("count(tailscale_device_online_ratio%s)" % df, legend="total")],
              unit="short", custom=ts_custom(fill=10), options=ts_opts(),
              desc="Online device count against the registered total over time; a widening gap "
                   "is devices dropping off rather than leaving the tailnet."),
        panel("Devices by OS over time", "timeseries",
              [prom_t("sum by (os_type, tailscale_authorized, tailscale_external) "
                      "(tailscale_devices_count_ratio{%s})" % TP,
                      legend="{{os_type}} · authorized={{tailscale_authorized}} · external={{tailscale_external}}")],
              unit="short", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right"),
              desc="Stacked OS mix over time with authorization and shared-in state preserved, so a "
                   "growing band of unauthorized or external devices is visible rather than absorbed."),
    ]

    # A. Authorization & sharing (#392). Three populations, one disjoint selector
    # each — see _POPULATIONS. Ungated: tailscale.devices.count is emitted by the
    # devices collector unconditionally, exactly like the Inventory row above.
    authsplit = [
        (panel("Authorized (internal)", "stat",
               [prom_t(_pop_count("authorized"))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_INVENTORY,
               desc="Devices belonging to this tailnet that an admin has authorized."), 4, 5),
        (panel("Unauthorized (internal)", "stat",
               [prom_t(_pop_count("unauthorized"))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"), novalue=_NO_INVENTORY,
               desc="Devices registered to this tailnet and waiting on an admin authorization "
                    "decision. They hold a node key but carry no ACL grants until authorized."), 4, 5),
        (panel("External (shared-in)", "stat",
               [prom_t(_pop_count("external"))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_INVENTORY,
               desc="Devices shared in from another tailnet. This tailnet cannot tag them, "
                    "authorize them or read their version, so fleet-hygiene numbers exclude them."), 4, 5),
        (panel("Population split over time", "timeseries",
               [prom_t(_pop_count("authorized", WIN_FAST), legend="authorized (internal)"),
                prom_t(_pop_count("unauthorized", WIN_FAST), legend="unauthorized (internal)"),
                prom_t(_pop_count("external", WIN_FAST), legend="external (shared-in)")],
               unit="short", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="The three populations over time. They partition the fleet, so the stack total "
                    "is the device count and no device is counted in two bands."), 12, 5),
        # #526 pending-panel ledger: tailscale.device_invite reached no panel anywhere. It is
        # a LOG EVENT, not a metric, so it is read through Loki with the same event_name
        # idiom the per-signal streams on the Events tab use.
        (panel("Device share invite log", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device_invite` "
                       "|~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="One INFO line per device-share invite seen during collection — the sharing "
                    "device, the invitee, the accepting user when accepted, and the delivery "
                    "state (emailed / manual_link / unknown). Needs collect_device_invites, which "
                    "costs one API call per device and is off by default; filter with the Log "
                    "filter variable. The invite URL is a bearer token and is never emitted."), 24, 10),
    ]

    # TSO-0047: one structured record for each device addition/removal and each
    # material field change. It carries host/user identity, so keep it behind
    # the same per-device redaction sentinel as the health tables.
    change_log = [
        (panel("Device inventory changes", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.change` "
                       "|~ `$log_filter`", maxlines=300)],
               options=logs_opts(),
               desc="Device additions, removals and material field changes observed by the "
                    "devices collector. The first successful poll after restart establishes "
                    "a silent baseline; later records identify the device, transition and "
                    "changed field. Requires collectors.devices.change_log_enabled. Hidden "
                    "when per-device identity redaction is active."), 24, 10),
    ]

    # B. Fleet hygiene row (no PII gate — counts/enums only)
    hygiene = [
        (panel("Stale devices (>30d)", "stat",
               [prom_t("count((time() - %s) > 30*86400) or vector(0)"
                       % lot("tailscale_device_last_seen_seconds", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Devices not seen in over 30 days (last-seen staleness) — candidates for "
                    "decommissioning. Companion to the per-device 'Last seen' table below."), 4, 5),
        (panel("Untagged", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_devices_untagged_ratio", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"),
               desc="Devices not associated with any ACL tag."), 4, 5),
        (panel("Ephemeral", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_devices_ephemeral_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(color="value"),
               desc="Ephemeral devices currently registered."), 4, 5),
        # No zero-fill: emitted only when version_checks.devices is on AND the upstream
        # latest version is known, so absence means the comparison never ran.
        (panel("Outdated (≥N behind)", "stat",
               [prom_t("max(%s)" % lot("tailscale_devices_outdated_ratio", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No fleet version comparison — enable version_checks.devices "
                       "(and the upstream latest version must be reachable).",
               desc="Devices running a client version that is at least one minor release behind the fleet latest."), 4, 5),
        # Merged in from fleet.py's single-panel "Exporter version" row (#526 decision 7).
        # Inverse risk with its own vocabulary, and no zero-fill: the gauge is emitted only
        # when version_checks.self is on and both versions parse (dev builds never emit),
        # so a zero would claim "up to date" for a check that never ran (#385).
        (panel("Exporter update available", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_update_available_ratio", WIN_SLOW))],
               mappings=bool_map("up to date", "update available", "green", "yellow"),
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               novalue="No self update-check data — enable version_checks.self "
                       "(dev builds never report).",
               desc="Whether a newer version of the tailscale2otel exporter is available."), 8, 5),
        (panel("Latest stable", "table",
               [prom_t(lot("tailscale_fleet_latest_version_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra + ["Value"],
                   rename={"tailscale_client_version": "Version"})],
               desc="The latest stable Tailscale client version seen in this fleet."), 12, 5),
        (panel("Clients by version", "barchart",
               [prom_t("sum by (tailscale_client_version) (max by (tailscale_client_version) (%s))"
                       % lot("tailscale_devices_by_version_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Fleet distribution by Tailscale client version."), 12, 7),
        (panel("Fleet tags (rollup)", "barchart",
               [prom_t("sum by (tailscale_tag) (max by (tailscale_tag) (%s))"
                       % lot("tailscale_devices_by_tag_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Device count per ACL tag across the fleet. Counts individual tags, so a "
                    "device carrying two tags is counted under each."), 12, 7),
        # #392: the fleet age distribution. Devices whose `created` the control plane
        # omits (every shared-in device) are skipped by the exporter, so this describes
        # the internal population only.
        (panel("Device age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_devices_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_devices_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               desc="Time since devices joined the tailnet. External/shared-in devices report no "
                    "creation time and are excluded rather than counted as brand new."), 12, 7),
        (panel("Devices by distribution", "barchart",
               [prom_t("sum by (tailscale_distro_name, tailscale_distro_codename) (%s)"
                       % lot("tailscale_devices_by_distro_ratio", WIN_SLOW),
                       legend="{{tailscale_distro_name}} {{tailscale_distro_codename}}",
                       instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="OS distribution mix. Only clients that report a distribution appear (most "
                    "non-Linux clients do not); beyond 50 pairs the tail folds into __other__."), 12, 7),
        # #526 pending-panel ledger: tailscale.device.distro reached no panel anywhere. It is
        # a constant-1 gauge carrying host identity, so it is COUNTED by distro name into a
        # fleet breakdown rather than plotted one series per device.
        (panel("Devices by distribution (per-device)", "barchart",
               [prom_t('count by (tailscale_distro_name) (%s)'
                       % lot('tailscale_device_distro_ratio{host_name=~"$host_name", %s}' % TP, WIN_SLOW),
                       instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               novalue=_NO_DEVICE_DISTRO,
               desc="Device count per distribution derived from the PER-DEVICE distro gauge, so it "
                    "honours the tab's host filter — the roll-up beside it does not and is emitted "
                    "unconditionally. The two disagreeing means either the host filter is biting or "
                    "the per-device series are switched off. Codename is carried on the gauge but "
                    "deliberately not grouped on here; the roll-up splits by it."), 12, 7),
        # #526 pending-panel ledger: tailscale.devices.by_country reached no panel anywhere.
        # A count despite the `_ratio` suffix (unit "1" gauge -> _ratio on the Prometheus side).
        (panel("Devices by country", "barchart",
               [prom_t("sum by (geo_country_iso_code) (max by (geo_country_iso_code) (%s))"
                       % lot("tailscale_devices_by_country_ratio{%s}" % TP, WIN_SLOW),
                       instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               novalue=_NO_GEO,
               desc="Devices per country, geolocated from the first globally-routable magicsock "
                    "endpoint each device advertises. Devices with no globally-routable endpoint, "
                    "or that the local GeoIP database does not cover, are simply ABSENT — there is "
                    "no 'unknown' bucket, so the bars will not sum to the fleet total."), 24, 7),
    ]

    # G. Per-device tables, hide_when=["pii_perdevice"].
    # The exclude lists were three hand-copied duplicates of _infra; they are the
    # same list, so they use the constant now (#392 — scrape-transport columns must
    # not reach an operator-facing table, and one list is one place to get it right).
    tables = [
        (panel("Updates available", "table",
               [prom_t("%s == 1" % lot("tailscale_device_update_available_ratio" + df), instant=True, fmt="table")],
               transformations=[organize(exclude=_infra + ["Value"],
                                         rename={"host_name": "Host", "os_type": "OS", "os_version": "OS version",
                                                 "tailscale_user": "User"})],
               overrides=[host_drilldown()],
               desc="Devices with a client update available."), 8, 8),
        (panel("Device key expiry (time until)", "table",
               [prom_t("%s - time()" % lot("tailscale_device_key_expiry_seconds" + df, WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=_infra,
                                                   rename={"host_name": "Host", "tailscale_user": "User",
                                                           "Value": "Expires in"})],
               desc="Time until each device node key expires. Devices with key expiry disabled "
                    "never appear here — see the 'Device security flags' row on the Posture & "
                    "Security sub-tab."), 8, 8),
        (panel("Last seen (time since)", "table",
               [prom_t("time() - %s" % lot("tailscale_device_last_seen_seconds" + df, WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=_infra,
                                                   rename={"host_name": "Host", "tailscale_user": "User",
                                                           "Value": "Last seen"})],
               desc="Time since each device was last seen."), 8, 8),
        # #401: the five per-device security flags in one row-per-device view. Each
        # column carries its own polarity — blocks-incoming is protective, SSH is a
        # deliberate configuration either way, the other three are inverse risk.
        (panel("Device security flags (per device)", "table",
               [prom_t(lot("tailscale_device_blocks_incoming_connections_ratio" + df), instant=True, fmt="table", refid="A"),
                prom_t(lot("tailscale_device_ssh_enabled_ratio" + df), instant=True, fmt="table", refid="B"),
                prom_t(lot("tailscale_device_key_expiry_disabled_ratio" + df), instant=True, fmt="table", refid="C"),
                prom_t(lot("tailscale_device_multiple_connections_ratio" + df), instant=True, fmt="table", refid="D"),
                prom_t(lot("tailscale_device_posture_identity_disabled_ratio" + df), instant=True, fmt="table", refid="E")],
               transformations=[merge(),
                                organize(exclude=_infra,
                                         rename={"host_name": "Host", "os_type": "OS",
                                                 "tailscale_user": "User",
                                                 "Value #A": "Blocks incoming", "Value #B": "SSH enabled",
                                                 "Value #C": "Key expiry disabled",
                                                 "Value #D": "Multiple connections",
                                                 "Value #E": "Posture identity disabled"})],
               overrides=[host_drilldown(),
                          flag_map("Blocks incoming", BOOL_HEALTHY_ON),
                          flag_map("SSH enabled", BOOL_NEUTRAL),
                          flag_map("Key expiry disabled", BOOL_HEALTHY_OFF),
                          flag_map("Multiple connections", BOOL_HEALTHY_OFF),
                          flag_map("Posture identity disabled", BOOL_HEALTHY_OFF)],
               novalue=NO_PER_DEVICE,
               desc="Per-device security flags. A column is blank when this control plane does not "
                    "report that field at all, which is not the same as reporting it off."), 24, 8),
        # Merged in from fleet.py's single-panel "Version staleness (top-N)" row (#526
        # decision 7). It lands here rather than in "Fleet hygiene" because it renders a
        # host_name column and so must keep this row's pii_perdevice gate; the noValue
        # replaces the present="has_version_skew" gate it gives up.
        (panel("Most-behind devices (top-N)", "table",
               [prom_t("topk($topn, %s)" % lot('tailscale_device_version_skew_ratio{host_name=~"$host_name"}'),
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra,
                   rename={"host_name": "Host", "Value": "Minors behind"})],
               novalue="No per-device version-skew series — needs version_checks.devices and "
                       "cardinality.per_entity.device, and the upstream latest version must be "
                       "reachable.",
               desc="Devices furthest behind the fleet's latest version (top-N by minor version "
                    "gap; set the count with the Top N variable)."), 24, 8),
    ]

    return [
        row("Inventory", inv),
        row("Authorization & sharing", authsplit),
        row("Device change log", change_log, hide_when=["pii_perdevice"]),
        # Two same-size (12x7) timeseries -> AutoGrid, so the row reflows (#526 decision 6).
        autogrid_row("Trends", overtime),
        row("Fleet hygiene", hygiene),
        row("Device health", tables, hide_when=["pii_perdevice"]),
    ]
