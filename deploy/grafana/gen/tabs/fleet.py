"""tab_fleet() — moved out of build.py in the module split."""

from builder import (barchart_opts, bargauge_opts, hq, lot, merge, organize, panel, PII,
                     prom_t, row, stat_opts, thr, ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import BOOL_MAP


def tab_fleet():
    # tailscale_tags=~"$device_tag" (allValue ".*") matches series that lack the
    # label too, so untagged devices still appear under "All".
    df = "{os_type=~\"$os_type\", host_name=~\"$host_name\", tailscale_user=~\"$device_user\", tailscale_tags=~\"$device_tag\"}"
    on = lot("tailscale_device_online_ratio" + df)

    # Shared infra label exclusion list for instant-vector tables
    _infra = ["Time", "__name__", "job", "instance", "host_id",
              "service_instance_id", "service_name", "service_namespace"]

    inv = [
        (panel("Online", "stat", [prom_t("count(%s == 1) or vector(0)" % on)],
               unit="short", thresholds=thr([(None, "red"), (1, "green")]), options=stat_opts(color="background")), 3, 5),
        (panel("Total", "stat", [prom_t("count(%s) or vector(0)" % on)], unit="short", options=stat_opts()), 3, 5),
        (panel("Offline", "stat", [prom_t("count(%s == 0) or vector(0)" % on)], unit="short", options=stat_opts(color="value")), 3, 5),
        (panel("Updates available", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_device_update_available_ratio" + df))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]), options=stat_opts(color="value")), 3, 5),
        # A. Fix: count actually-connected users (tailscale_user_connected_ratio == 1) not a device-derived proxy
        (panel("Distinct users", "stat",
               [prom_t("count(%s == 1) or vector(0)" % lot("tailscale_user_connected_ratio", WIN_SLOW))],
               unit="short", options=stat_opts()), 3, 5),
        (panel("Devices by OS", "bargauge",
               [prom_t("sum by (os_type) (max by (os_type, tailscale_authorized, tailscale_external) (%s))" % lot("tailscale_devices_count_ratio", WIN_SLOW), legend="{{os_type}}")],
               unit="short", options=bargauge_opts()), 9, 5),
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
               [prom_t("sum by (os_type) (tailscale_devices_count_ratio)", legend="{{os_type}}")],
               unit="short", custom=ts_custom(stack="normal", fill=30), options=ts_opts(placement="right")), 12, 7),
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
        (panel("Outdated (≥N behind)", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_devices_outdated_ratio", WIN_SLOW))],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
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
        (panel("Exporter update available", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale2otel_update_available_ratio", WIN_SLOW))],
               mappings=BOOL_MAP,
               thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Whether a newer version of the tailscale2otel exporter is available."), 6, 5),
    ]

    # G. Existing per-device tables (add hide_when=["pii_perdevice"])
    tables = [
        (panel("Updates available", "table",
               [prom_t("%s == 1" % lot("tailscale_device_update_available_ratio" + df), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                   "service_instance_id", "service_name", "service_namespace", "Value"],
                                          rename={"host_name": "Host", "os_type": "OS", "os_version": "OS version",
                                                  "tailscale_user": "User"})],
               desc="Devices with a client update available."), 8, 8),
        (panel("Device key expiry (time until)", "table",
               [prom_t("%s - time()" % lot("tailscale_device_key_expiry_seconds" + df, WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                             "service_instance_id", "service_name", "service_namespace"],
                                                    rename={"host_name": "Host", "tailscale_user": "User",
                                                            "Value": "Expires in"})],
               desc="Time until each device node key expires."), 8, 8),
        (panel("Last seen (time since)", "table",
               [prom_t("time() - %s" % lot("tailscale_device_last_seen_seconds" + df, WIN_SLOW), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"host_name": "Host", "tailscale_user": "User",
                                                           "Value": "Last seen"})],
               desc="Time since each device was last seen."), 8, 8),
    ]
    derp = [
        (panel("DERP latency by host / region", "table",
               [prom_t(lot("tailscale_device_derp_latency_seconds{host_name=~\"$host_name\"}"), instant=True, fmt="table")],
               unit="s", transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                            "service_instance_id", "service_name", "service_namespace"],
                                                   rename={"host_name": "Host", "tailscale_derp_region": "Region",
                                                           "tailscale_derp_preferred": "Preferred", "Value": "Latency"})]), 14, 8),
        (panel("Preferred DERP regions", "bargauge",
               [prom_t("count by (tailscale_derp_region) (max by (tailscale_derp_region, host_name) (%s))"
                       % lot("tailscale_device_derp_latency_seconds{tailscale_derp_preferred=\"true\"}"),
                       legend="{{tailscale_derp_region}}")], unit="short", options=bargauge_opts()), 10, 8),
    ]
    routes = [
        (panel("Subnet routes — advertised vs enabled", "table",
               [prom_t(lot("tailscale_device_routes_advertised{host_name=~\"$host_name\"}"), instant=True, fmt="table", refid="A"),
                prom_t(lot("tailscale_device_routes_enabled{host_name=~\"$host_name\"}"), instant=True, fmt="table", refid="B")],
               unit="short", transformations=[merge(),
                                              organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                                "service_instance_id", "service_name", "service_namespace"],
                                                       rename={"host_name": "Host", "Value #A": "Advertised", "Value #B": "Enabled"})]), 24, 8),
    ]
    posture = [
        (panel("Posture overview", "table",
               [prom_t(lot("tailscale_device_posture_ratio{host_name=~\"$host_name\"}", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(exclude=["Time", "__name__", "job", "instance", "host_id",
                                                  "service_instance_id", "service_name", "service_namespace", "Value"])],
               desc="Per-device posture: OS, client version, auto-update, encryption, track."), 16, 8),
        (panel("Clients by version", "barchart",
               [prom_t("count by (ts_version) (max by (ts_version, host_name) (%s))" % lot("tailscale_device_posture_ratio", WIN_SLOW), legend="{{ts_version}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])]), 8, 8),
    ]

    # Wire all rows: aggregates first, then per-device (PII-gated)
    return [
        row("Inventory", inv),
        row("Trends", overtime),
        row("Fleet hygiene", hygiene),
        row("Key-expiry distribution", keylife, present="has_key_expiry_hist"),
        row("Connectivity", connectivity, present="has_connectivity"),
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
