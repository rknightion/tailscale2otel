"""tab_fleet() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, hq, lot, merge, organize, panel, PII,
                     pii_sentinel, prom_t, row, sentinel, stat_opts, thr, ts_custom, ts_opts,
                     WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_OFF, BOOL_HEALTHY_ON, BOOL_NEUTRAL
from builder import DASHBOARD  # #526: wave 1 leaves every sentinel dashboard-level

# Tailnet/provider selectors (#392). Both are real per-series metric LABELS (not
# resource attributes), so a panel filters them straight in the selector — see the
# note in builder.py. allValue ".*" also matches series that carry no such label,
# so a single-tailnet deployment reads exactly as it did before.
TP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'

# The three device populations #392 is about. They are different populations, not
# slices of one: an unauthorized device is a pending admin decision, a shared-in
# device belongs to someone else's tailnet entirely, and the old fleet totals
# summed all three into one number.
#
# The matcher sets PARTITION {authorized, external}^2 — every emitted series of
# tailscale_devices_count_ratio is selected by exactly one of them (both labels
# are always present; devices.go sets each from a bool). So the three panels can
# neither double-count a device nor hide one, which is asserted directly in
# test_fleet_signals.AuthorizationSplitTest.
_POPULATIONS = {
    "authorized": 'tailscale_authorized="true", tailscale_external="false"',
    "unauthorized": 'tailscale_authorized="false", tailscale_external="false"',
    "external": 'tailscale_external="true"',
}

# Every population panel reads the same gauge; absence means the devices collector
# never ran, so none of them zero-fill.
_NO_INVENTORY = ("No device inventory — needs the devices collector "
                 "(collectors.devices) reporting tailscale.devices.count.")

# Tailscale SSH and key-expiry state are not concepts every control plane has:
# Headscale emits neither metric at all. Zero-filling them would render "0
# problems" for a question that was never asked (#385, #392).
_NO_SSH_STATE = ("Not reported — needs a control plane that reports Tailscale SSH "
                 "state, and the devices collector.")
_NO_KEY_EXPIRY_STATE = ("Not reported — needs a control plane that reports node-key "
                        "expiry state, and the devices collector.")
_NO_PER_DEVICE = ("No per-device series — needs cardinality.per_entity.device, and a "
                  "control plane that reports this field.")
_NO_CONNECTIVITY = ("No per-device connectivity series — needs collect_connectivity "
                    "and cardinality.per_entity.device.")


def _pop(which, win=WIN_SLOW):
    """last_over_time over one device population's slice of the fleet count."""
    return lot("tailscale_devices_count_ratio{%s, %s}" % (_POPULATIONS[which], TP), win)


def host_drilldown(field="Host"):
    """Field override putting a drill-down link on a host column (#392).

    The URL is a bare query string on purpose: it resolves against whichever
    dashboard is open, so it survives the `--tab` preview builds and any `--uid`
    the artifact is generated under. A `d/<uid>` path would pin it to one.
    """
    return {"matcher": {"id": "byName", "options": field},
            "properties": [{"id": "links", "value": [
                {"title": "Scope this dashboard to ${__value.text}",
                 "url": "?var-host_name=${__value.text}&${__url_time_range}",
                 "targetBlank": False}]}]}


def flag_map(field, mapping):
    """Field override applying a semantic 0/1 value map to one table column.

    Table columns cannot use the panel-level `mappings` slot (each column needs
    its own polarity), so they go through overrides — which also keeps them out
    of test_semantic_maps.py's panel-level inventory. test_fleet_signals owns
    their polarity instead.
    """
    return {"matcher": {"id": "byName", "options": field},
            "properties": [{"id": "mappings", "value": mapping}]}


def tab_fleet(scope):
    # Presence sentinels this tab declares (moved from variables.py, #495).
    sentinel("has_posture", "tailscale_device_posture_ratio", DASHBOARD)  # also consumed by events.py, security.py
    sentinel("has_routes", "tailscale_device_routes_advertised", DASHBOARD)
    sentinel("has_derp", "tailscale_device_derp_latency_seconds", DASHBOARD)
    sentinel("has_connectivity", "tailscale_device_connectivity_hard_nat_ratio", DASHBOARD)
    # Gates the "Device security flags" row. Those seven panels all read per-device
    # flag gauges that cardinality.per_entity.device switches off wholesale, so
    # without a gate the row renders seven empty-state panels on every deployment
    # that has the toggle off — which reads as "nothing to report" rather than
    # "not collected". blocks_incoming_connections is emitted for every device
    # whenever the per-entity series exist, so its presence is the right proxy.
    sentinel("has_device_flags", "tailscale_device_blocks_incoming_connections_ratio", DASHBOARD)
    sentinel("has_exit", "tailscale_device_exit_node_ratio", DASHBOARD)
    sentinel("has_subnet", "tailscale_subnet_routes_advertised", DASHBOARD)
    sentinel("has_version_skew", "tailscale_device_version_skew_ratio", DASHBOARD)
    sentinel("has_key_expiry_hist", "tailscale_devices_key_expiry_days_count", DASHBOARD)
    pii_sentinel("pii_host", PII + '{category="hostnames"} == 0', DASHBOARD)  # dead: no row gates on it
    pii_sentinel("pii_perdevice",
                 '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)'
                 % (PII, PII), DASHBOARD)  # also consumed by security.py

    # tailscale_tags=~"$device_tag" (allValue ".*") matches series that lack the
    # label too, so untagged devices still appear under "All". $tailnet/$provider
    # were added by #392 — without them a multi-tailnet target sums every tailnet
    # into one fleet no matter what the dropdown says.
    df = ("{os_type=~\"$os_type\", host_name=~\"$host_name\", tailscale_user=~\"$device_user\", "
          "tailscale_tags=~\"$device_tag\", %s}" % TP)
    on = lot("tailscale_device_online_ratio" + df)

    # Shared infra label exclusion list for instant-vector tables
    _infra = ["Time", "__name__", "job", "instance", "host_id",
              "service_instance_id", "service_name", "service_namespace"]

    inv = [
        (panel("Online", "stat", [prom_t("count(%s == 1) or vector(0)" % on)],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 3, 5),
        (panel("Total", "stat", [prom_t("count(%s) or vector(0)" % on)], unit="short", options=stat_opts()), 3, 5),
        (panel("Offline", "stat", [prom_t("count(%s == 0) or vector(0)" % on)], unit="short", options=stat_opts(color="value")), 3, 5),
        # No zero-fill: absent update-availability means the control plane never reported
        # it (Headscale) or per-device metrics are off — not "everything is current" (#385).
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_update_available_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="value"),
               novalue="No device update data — needs the devices collector and a control plane "
                       "that reports update availability."), 3, 5),
        # A. Fix: count actually-connected users (tailscale_user_connected_ratio == 1) not a device-derived proxy
        (panel("Distinct users", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_user_connected_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(),
               novalue="No per-user data — needs the users collector and "
                       "cardinality.per_entity.user."), 3, 5),
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
        (panel("Devices by tag", "bargauge",
               [prom_t("count by (tailscale_tags) (%s)" % lot("tailscale_device_online_ratio" + df), legend="{{tailscale_tags}}")],
               unit="short", options=bargauge_opts(),
               desc="Device count per ACL tag combination (untagged devices group under an empty bar). "
                    "Requires the tailscale.tags label (exporter >= this release)."), 9, 5),
    ]
    overtime = [
        (panel("Online vs total", "timeseries",
               [prom_t("count(tailscale_device_online_ratio%s == 1)" % df, legend="online"),
                prom_t("count(tailscale_device_online_ratio%s)" % df, legend="total")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts()), 12, 7),
        (panel("Devices by OS over time", "timeseries",
               [prom_t("sum by (os_type, tailscale_authorized, tailscale_external) "
                       "(tailscale_devices_count_ratio{%s})" % TP,
                       legend="{{os_type}} · authorized={{tailscale_authorized}} · external={{tailscale_external}}")],
               unit="short", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right"),
               desc="Stacked OS mix over time with authorization and shared-in state preserved, so a "
                    "growing band of unauthorized or external devices is visible rather than absorbed."), 12, 7),
    ]

    # A. Authorization & sharing (#392). Three populations, one disjoint selector
    # each — see _POPULATIONS. Ungated: tailscale.devices.count is emitted by the
    # devices collector unconditionally, exactly like the Inventory row above.
    authsplit = [
        (panel("Authorized (internal)", "stat",
               [prom_t("sum(%s)" % _pop("authorized"))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_INVENTORY,
               desc="Devices belonging to this tailnet that an admin has authorized."), 4, 5),
        (panel("Unauthorized (internal)", "stat",
               [prom_t("sum(%s)" % _pop("unauthorized"))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"), novalue=_NO_INVENTORY,
               desc="Devices registered to this tailnet and waiting on an admin authorization "
                    "decision. They hold a node key but carry no ACL grants until authorized."), 4, 5),
        (panel("External (shared-in)", "stat",
               [prom_t("sum(%s)" % _pop("external"))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_INVENTORY,
               desc="Devices shared in from another tailnet. This tailnet cannot tag them, "
                    "authorize them or read their version, so fleet-hygiene numbers exclude them."), 4, 5),
        (panel("Population split over time", "timeseries",
               [prom_t("sum(%s)" % _pop("authorized", WIN_FAST), legend="authorized (internal)"),
                prom_t("sum(%s)" % _pop("unauthorized", WIN_FAST), legend="unauthorized (internal)"),
                prom_t("sum(%s)" % _pop("external", WIN_FAST), legend="external (shared-in)")],
               unit="short", custom=ts_custom(stack="normal", fill=25), options=ts_opts(),
               desc="The three populations over time. They partition the fleet, so the stack total "
                    "is the device count and no device is counted in two bands."), 12, 5),
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
               desc="Device count per ACL tag across the fleet."), 12, 7),
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
    ]

    # B2. Device security flags (#401). Two fleet roll-ups plus five aggregates over
    # per-device gauges. Every panel names its own prerequisite instead of zero-filling:
    # presence alone cannot tell "cardinality.per_entity.device is off" from "this
    # control plane has no such concept", so neither claim is made.
    secflags = [
        (panel("Tailscale SSH enabled (fleet)", "stat",
               [prom_t("max(%s)" % lot("tailscale_devices_ssh_enabled_ratio", WIN_SLOW))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_SSH_STATE,
               desc="Devices accepting Tailscale SSH sessions, from the fleet roll-up "
                    "(emitted regardless of cardinality.per_entity.device)."), 4, 5),
        (panel("Key expiry disabled (fleet)", "stat",
               [prom_t("max(%s)" % lot("tailscale_devices_key_expiry_disabled_ratio", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"), novalue=_NO_KEY_EXPIRY_STATE,
               desc="Devices whose node key never expires. These are invisible to every "
                    "key-expiry panel on this tab, so this is the only place they show up."), 4, 5),
        (panel("Tailscale SSH enabled (devices)", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_ssh_enabled_ratio" + df))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_SSH_STATE,
               desc="Same count derived from the per-device gauge, so it honours the tab's "
                    "OS/host/user/tag filters. Diverging from the fleet roll-up means the filters bite."), 4, 5),
        (panel("Key expiry disabled (devices)", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_key_expiry_disabled_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"), novalue=_NO_KEY_EXPIRY_STATE,
               desc="Per-device view of never-expiring node keys, filterable to a host or tag."), 4, 5),
        (panel("Blocking incoming connections", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_blocks_incoming_connections_ratio" + df))],
               unit="short", options=stat_opts(color="value"), novalue=_NO_PER_DEVICE,
               desc="Devices refusing inbound tailnet connections. Protective, but it also makes a "
                    "device unreachable for admin access — read it as configuration, not as a fault."), 4, 5),
        (panel("Multiple simultaneous connections", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_multiple_connections_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"), novalue=_NO_PER_DEVICE,
               desc="Devices whose identity is in use by more than one client at once — a duplicated "
                    "node key or a cloned state store."), 4, 5),
        (panel("Posture identity disabled", "stat",
               [prom_t("count(%s == 1)" % lot("tailscale_device_posture_identity_disabled_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"), novalue=_NO_PER_DEVICE,
               desc="Devices exempted from posture-identity collection, so posture-gated ACL rules "
                    "cannot evaluate them."), 4, 5),
    ]

    # C. Key-expiry distribution row (gate present="has_key_expiry_hist")
    # NB: the cumulative key-expiry histogram buckets (tailscale_devices_key_expiry_days_bucket) are
    # lifetime observation totals that only ever grow — using them as current device counts latches
    # and shows garbage after a key is rotated (issue #109). The expired stat and the distribution
    # barchart below are derived from the per-device expiry GAUGE instead, so they are point-in-time
    # and clear after re-auth. (The median panel keeps histogram_quantile(rate(_bucket)), which is a
    # correct use of the histogram.)
    _kexp = "max by (host_id) (%s)" % lot("tailscale_device_key_expiry_seconds", WIN_SLOW)
    _kdte = "(%s - time())" % _kexp  # seconds-to-expiry per device
    _kbands = [
        ("expired", "%s <= 0" % _kdte),
        ("<=7d", "%s > 0 and %s < 7*86400" % (_kdte, _kdte)),
        ("7-30d", "%s >= 7*86400 and %s < 30*86400" % (_kdte, _kdte)),
        ("30-90d", "%s >= 30*86400 and %s < 90*86400" % (_kdte, _kdte)),
        ("90-180d", "%s >= 90*86400 and %s < 180*86400" % (_kdte, _kdte)),
        ("180-365d", "%s >= 180*86400 and %s < 365*86400" % (_kdte, _kdte)),
        (">365d", "%s >= 365*86400" % _kdte),
    ]
    _kdist = " or ".join('label_replace(count(%s), "band", "%s", "", "")' % (cond, name)
                         for (name, cond) in _kbands)
    keylife = [
        (panel("Keys already expired", "stat",
               [prom_t('count((%s - time()) <= 0) or vector(0)'
                       % lot("tailscale_device_key_expiry_seconds", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Devices whose node key has already expired (per-device expiry gauge; clears after re-auth)."), 6, 5),
        (panel("Median days-to-expiry", "timeseries",
               [prom_t(hq("0.5", "tailscale_devices_key_expiry_days"), legend="p50")],
               unit="d", custom=ts_custom(fill=10), options=ts_opts(),
               desc="Median days until device key expiry across the fleet."), 18, 5),
        (panel("Devices by days-to-expiry bucket", "barchart",
               [prom_t(_kdist, instant=True, fmt="table")],
               options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Current device count per days-to-key-expiry band, from the per-device expiry gauge "
                    "(point-in-time, non-cumulative; clears after re-auth)."), 24, 7),
    ]

    # D. Connectivity aggregate row (gate present="has_connectivity", no PII)
    connectivity = [
        # FIX-1: ratio numerator has extra labels so must use / on() group_left() to join
        (panel("Direct-capable %", "stat",
               [prom_t("%s / on() group_left() sum(%s)"
                       % (lot("tailscale_devices_direct_capable_ratio"), lot("tailscale_devices_count_ratio")))],
               unit="percentunit",
               thresholds=thr([(None, "red"), (0.5, "yellow"), (0.8, "green")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices capable of direct (non-relay) connections."), 6, 5),
        (panel("Hard-NAT %", "stat",
               [prom_t("%s / on() group_left() sum(%s)"
                       % (lot("tailscale_devices_hard_nat_ratio"), lot("tailscale_devices_count_ratio")))],
               unit="percentunit",
               thresholds=thr([(None, "green"), (0.2, "yellow"), (0.5, "red")]),
               options=stat_opts(color="background"),
               desc="Fraction of devices behind hard NAT (require relay for inbound connections)."), 6, 5),
        (panel("Client capability support", "barchart",
               [prom_t("sum by (tailscale_connectivity_capability) (max by (tailscale_connectivity_capability) (%s))"
                       % lot("tailscale_devices_client_supports_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Number of devices supporting each connectivity capability."), 12, 5),
        # #401. These four read the per-device connectivity gauges, which share this
        # row's gate exactly (collect_connectivity + cardinality.per_entity.device is
        # what emits has_connectivity's metric too), and aggregate them fleet-wide.
        (panel("Direct-capable coverage (per-device)", "stat",
               [prom_t("count(%s == 1) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_connectivity_direct_capable_ratio"),
                          lot("tailscale_device_connectivity_direct_capable_ratio")))],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.5, "yellow"), (0.8, "green")]),
               options=stat_opts(color="background"), novalue=_NO_CONNECTIVITY,
               desc="Share of reporting devices that look able to hold a direct connection "
                    "(UDP usable and not behind a hard NAT). An eligibility heuristic, not the "
                    "path actually in use — cross-check against 'Direct-capable %' beside it."), 6, 5),
        (panel("IPv6 support coverage", "stat",
               [prom_t("count(%s == 1) / clamp_min(count(%s), 1)"
                       % (lot("tailscale_device_connectivity_ipv6_ratio"),
                          lot("tailscale_device_connectivity_ipv6_ratio")))],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.5, "yellow"), (0.8, "green")]),
               options=stat_opts(color="background"), novalue=_NO_CONNECTIVITY,
               desc="Share of reporting devices whose OS supports IPv6. Says nothing about whether "
                    "IPv6 connectivity is actually available on their network."), 6, 5),
        (panel("UDP blocked (DERP-forced)", "stat",
               [prom_t("count(%s == 0)" % lot("tailscale_device_connectivity_udp_ratio"))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (5, "red")]),
               options=stat_opts(color="background"), novalue=_NO_CONNECTIVITY,
               desc="Devices whose current network blocks UDP. Every one of their peer connections "
                    "is relayed through DERP, which costs latency and relay capacity."), 6, 5),
        (panel("Endpoint candidates per device", "timeseries",
               [prom_t("quantile(0.5, %s)" % lot("tailscale_device_connectivity_endpoints_ratio"),
                       legend="p50"),
                prom_t("max(%s)" % lot("tailscale_device_connectivity_endpoints_ratio"),
                       legend="max")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts(), novalue=_NO_CONNECTIVITY,
               desc="Magicsock UDP endpoint candidates advertised per device. Few candidates means "
                    "fewer chances of a direct path; the addresses themselves are never emitted."), 6, 5),
        (panel("NAT → relay pressure", "timeseries",
               [prom_t("sum(%(lot_hnat)s) / on() group_left() sum(%(lot_cnt)s)"
                       % {"lot_hnat": lot("tailscale_devices_hard_nat_ratio", WIN_FAST),
                          "lot_cnt": lot("tailscale_devices_count_ratio", WIN_FAST)},
                       legend="hard-NAT %"),
                prom_t("sum(tailscaled_peer_relay_endpoints)", legend="relay endpoints")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts(),
               desc="Correlation between hard-NAT fraction and relay endpoint count over time."), 24, 7),
    ]

    # D (per-device part). Hard-NAT device table — separate row so PII gate only hides this table
    needsrelay = [
        (panel("Needs relay (hard-NAT)", "table",
               [prom_t("%s == 1" % lot('tailscale_device_connectivity_hard_nat_ratio{host_name=~"$host_name"}'),
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra + ["Value"],
                   rename={"host_name": "Host"})],
               desc="Devices behind hard NAT that require relay for inbound peer connections."), 24, 8),
    ]

    # D (per-device drill-down, #401). One row per device across the four connectivity
    # gauges, bounded by the $host_name selector the way every other per-device table
    # here is. Booleans are mapped per column: 1 is the healthy state for all three
    # capability flags, so they take BOOL_HEALTHY_ON.
    _cxdf = '{host_name=~"$host_name"}'
    connectivity_detail = [
        (panel("Connectivity by device", "table",
               [prom_t(lot("tailscale_device_connectivity_direct_capable_ratio" + _cxdf),
                       instant=True, fmt="table", refid="A"),
                prom_t(lot("tailscale_device_connectivity_udp_ratio" + _cxdf),
                       instant=True, fmt="table", refid="B"),
                prom_t(lot("tailscale_device_connectivity_ipv6_ratio" + _cxdf),
                       instant=True, fmt="table", refid="C"),
                prom_t(lot("tailscale_device_connectivity_endpoints_ratio" + _cxdf),
                       instant=True, fmt="table", refid="D")],
               transformations=[merge(),
                                organize(exclude=_infra,
                                         rename={"host_name": "Host",
                                                 "Value #A": "Direct capable", "Value #B": "UDP",
                                                 "Value #C": "IPv6", "Value #D": "Endpoints"})],
               overrides=[host_drilldown(),
                          flag_map("Direct capable", BOOL_HEALTHY_ON),
                          flag_map("UDP", BOOL_HEALTHY_ON),
                          flag_map("IPv6", BOOL_HEALTHY_ON)],
               novalue=_NO_CONNECTIVITY,
               desc="Per-device transport situation: direct-path eligibility, UDP usability, IPv6 "
                    "support and endpoint-candidate count. Click a host to scope the whole "
                    "dashboard to it."), 24, 8),
    ]

    # E. Exit/subnet aggregate stats (present="has_exit", no PII)
    exitsubnet = [
        (panel("Exit nodes advertised", "stat",
               [prom_t('sum(%s) or vector(0)'
                       % lot('tailscale_exit_nodes_count_ratio{tailscale_exit_node_state="advertised"}', WIN_SLOW))],
               unit="short", options=stat_opts(color="value"),
               desc="Exit nodes currently in the 'advertised' state."), 6, 5),
        (panel("Exit nodes enabled", "stat",
               [prom_t('sum(%s) or vector(0)'
                       % lot('tailscale_exit_nodes_count_ratio{tailscale_exit_node_state="enabled"}', WIN_SLOW))],
               unit="short", options=stat_opts(color="value"),
               desc="Exit nodes currently in the 'enabled' state."), 6, 5),
        (panel("Unapproved subnet routes", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_subnet_routes_unapproved", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Subnet routes advertised but not yet approved by an admin."), 6, 5),
        # #392: the approved counterpart of the stat beside it. Absent means the exit/
        # subnet collection never ran, which is not the same as "no routes approved".
        (panel("Enabled subnet routes", "stat",
               [prom_t("max(%s)" % lot("tailscale_subnet_routes_enabled", WIN_SLOW))],
               unit="short", options=stat_opts(color="value"),
               novalue="No subnet-route data — needs the devices collector "
                       "(collectors.devices) and a control plane that reports approved routes.",
               desc="Distinct subnet CIDRs approved on at least one device (exit-node default "
                    "routes excluded). Read against 'Unapproved subnet routes' beside it."), 6, 5),
    ]

    # E (per-device exit table). Separate row for PII gate
    exitinv = [
        (panel("Exit-node inventory", "table",
               [prom_t("%s == 1" % lot('tailscale_device_exit_node_ratio{host_name=~"$host_name"}'),
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra + ["Value"],
                   rename={"host_name": "Host",
                           "tailscale_exit_node_enabled": "Enabled"})],
               desc="Devices currently advertising or acting as exit nodes."), 24, 8),
    ]

    # E (subnet redundancy). Separate row for topology PII gate
    subnetredund = [
        (panel("Subnet-route redundancy by CIDR", "barchart",
               [prom_t("max by (tailscale_route_cidr) (%s)"
                       % lot("tailscale_subnet_routes_routers_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Number of routers advertising each subnet CIDR (redundancy indicator)."), 24, 7),
    ]

    # F. Version staleness — per-device table (hide on pii_perdevice)
    versiontable = [
        (panel("Most-behind devices (top-N)", "table",
               [prom_t("topk($topn, %s)" % lot('tailscale_device_version_skew_ratio{host_name=~"$host_name"}'),
                       instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_infra,
                   rename={"host_name": "Host", "Value": "Minors behind"})],
               desc="Devices furthest behind the fleet's latest version (top-N by minor version gap)."), 24, 8),
    ]

    # F (exporter update stat) — non-PII, sits in hygiene-adjacent position; present="has_version_skew"
    exporterver = [
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
               desc="Whether a newer version of the tailscale2otel exporter is available."), 6, 5),
    ]

    # G. Existing per-device tables (add hide_when=["pii_perdevice"]).
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
                    "never appear here — see the 'Device security flags' row."), 8, 8),
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
               novalue=_NO_PER_DEVICE,
               desc="Per-device security flags. A column is blank when this control plane does not "
                    "report that field at all, which is not the same as reporting it off."), 24, 8),
    ]
    derp = [
        (panel("DERP latency by host / region", "table",
               [prom_t(lot("tailscale_device_derp_latency_seconds{host_name=~\"$host_name\"}"), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=_infra,
                                                   rename={"host_name": "Host", "tailscale_derp_region": "Region",
                                                           "tailscale_derp_preferred": "Preferred", "Value": "Latency"})],
               overrides=[host_drilldown()],
               desc="Per-device DERP relay latency by region; one row per device/region pair "
                    "reporting a relay path."), 14, 8),
        (panel("Preferred DERP regions", "bargauge",
               [prom_t("count by (tailscale_derp_region) (max by (tailscale_derp_region, host_name) (%s))"
                       % lot("tailscale_device_derp_latency_seconds{tailscale_derp_preferred=\"true\"}"),
                       legend="{{tailscale_derp_region}}")], unit="short", options=bargauge_opts(),
               desc="Device count per preferred DERP relay region."), 10, 8),
    ]
    routes = [
        (panel("Subnet routes — advertised vs enabled", "table",
               [prom_t(lot("tailscale_device_routes_advertised{host_name=~\"$host_name\"}"), instant=True, fmt="table", refid="A"),
                prom_t(lot("tailscale_device_routes_enabled{host_name=~\"$host_name\"}"), instant=True, fmt="table", refid="B")],
               unit="short", transformations=[merge(),
                                              organize(exclude=_infra,
                                                       rename={"host_name": "Host", "Value #A": "Advertised", "Value #B": "Enabled"})],
               overrides=[host_drilldown()],
               desc="Per-device advertised vs enabled subnet-route counts — a gap between the two "
                    "means an advertised route is still awaiting approval."), 24, 8),
    ]
    posture = [
        (panel("Posture overview", "table",
               [prom_t(lot("tailscale_device_posture_ratio{host_name=~\"$host_name\"}", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(exclude=_infra + ["Value"])],
               desc="Per-device posture: OS, client version, auto-update, encryption, track."), 16, 8),
        (panel("Clients by version", "barchart",
               [prom_t("count by (ts_version) (max by (ts_version, host_name) (%s))" % lot("tailscale_device_posture_ratio", WIN_SLOW), legend="{{ts_version}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Device count by reported Tailscale client version, from posture data."), 8, 8),
    ]

    # Wire all rows: aggregates first, then per-device (PII-gated)
    return [
        row("Inventory", inv),
        row("Authorization & sharing", authsplit),
        row("Trends", overtime),
        row("Fleet hygiene", hygiene),
        # Ungated on purpose: there is no sentinel for the per-device flag family, and
        # each panel already names its own prerequisite rather than showing a zero. A
        # `has_device_flags` sentinel on tailscale_device_blocks_incoming_connections_ratio
        # would let this row hide entirely — it needs a _SENTINEL_ORDER entry in builder.py.
        row("Device security flags", secflags, present="has_device_flags"),
        row("Key-expiry distribution", keylife, present="has_key_expiry_hist"),
        row("Connectivity", connectivity, present="has_connectivity"),
        row("Connectivity detail (per device)", connectivity_detail,
            present="has_connectivity", hide_when=["pii_perdevice"]),
        row("Needs relay (hard-NAT devices)", needsrelay,
            present="has_connectivity", hide_when=["pii_perdevice"]),
        row("Exit nodes", exitsubnet, present="has_exit"),
        row("Exit-node inventory", exitinv,
            present="has_exit", hide_when=["pii_perdevice"]),
        row("Subnet redundancy", subnetredund,
            present="has_subnet", hide_when=["pii_topology"]),
        row("Version staleness (top-N)", versiontable,
            present="has_version_skew", hide_when=["pii_perdevice"]),
        row("Exporter version", exporterver, present="has_version_skew"),
        row("Device health", tables, hide_when=["pii_perdevice"]),
        row("Connectivity / DERP", derp,
            present="has_derp", hide_when=["pii_perdevice"]),
        row("Subnet routes", routes,
            present="has_routes", hide_when=["pii_perdevice"]),
        row("Device posture", posture,
            present="has_posture", hide_when=["pii_perdevice"]),
    ]
