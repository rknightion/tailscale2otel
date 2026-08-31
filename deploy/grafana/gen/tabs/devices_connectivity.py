"""tab_devices_connectivity() — the "Connectivity & Routing" sub-tab of Devices (#526).

Content is LIFTED UNCHANGED from tabs/fleet.py's transport/routing rows: "Connectivity"
(the eight NAT/UDP/IPv6/endpoint aggregates), the two per-device connectivity tables,
"Exit nodes" + the exit-node inventory, the subnet-route pair, and "Connectivity / DERP".
tab_fleet() was one 59-panel leaf; #526 splits it into three sub-tabs, of which this is the
transport-facing one. No panel was added, dropped or re-queried.

Row consolidation (#526 decision 7) — three of fleet.py's single-panel rows are folded into
their topical neighbour, but ONLY where the gates match, because fleet.py split those rows
FOR the gate (its own comments say so: "separate row so the PII gate only hides this table"):

  * "Connectivity detail (per device)" + "Needs relay (hard-NAT devices)" -> one row. Both
    carried present="has_connectivity" + hide_when=["pii_perdevice"], so the merged row is
    byte-identical in gating.
  * "Subnet redundancy" -> "Subnet routes". Its present= moves from has_subnet
    (tailscale_subnet_routes_advertised) to has_routes (tailscale_device_routes_advertised)
    and the merged row hides on EITHER pii_perdevice or pii_topology. Both metrics come from
    the devices collector and are emitted together, and hiding on either redaction is
    strictly stricter than before — no panel becomes visible in a state where it was hidden.
    has_subnet consequently gates nothing any more and is deliberately NOT declared.
  * "Exit-node inventory" is NOT merged into "Exit nodes", which the brief asked for. That
    merge would have to put hide_when=["pii_perdevice"] on the row, and the four exit/subnet
    aggregates there are plain counts carrying no device identity — a redacting deployment
    would lose its only view of exit-node and unapproved-route counts. Keeping the split is
    exactly why fleet.py had it.

Sentinel scoping (#526): has_connectivity, has_exit, has_routes, has_derp, pii_perdevice and
pii_topology are declared HERE at TAB scope. tabs/fleet.py declared the first four and
pii_perdevice at DASHBOARD scope and is gone; pii_topology is declared by tabs/network.py,
still at DASHBOARD scope at the time of writing — and the registry REFUSES one name at both
dashboard and tab scope, so network.py has to move its declaration to its own tab scope for
the family to build. A row whose gate names a sentinel nothing declares in the same scope
renders permanently hidden, which on screen is indistinguishable from a correctly-gated
empty row, so referencing without declaring is not an option.
"""

from builder import (autogrid_row, bargauge_opts, barchart_opts, lot, merge, organize,
                     panel, PII, pii_sentinel, prom_t, row, sentinel, stat_opts, thr,
                     ts_custom, ts_opts, WIN_FAST, WIN_SLOW)
from maps import BOOL_HEALTHY_ON
from tabs._devices_common import flag_map, host_drilldown

_NO_CONNECTIVITY = ("No per-device connectivity series — needs collect_connectivity "
                    "and cardinality.per_entity.device.")

TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'


def sel(metric, extra=""):
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


def tab_devices_connectivity(scope):
    # Every sentinel this sub-tab's rows reference, declared at THIS tab's scope.
    sentinel("has_connectivity", "tailscale_device_connectivity_hard_nat_ratio", scope)
    sentinel("has_exit", "tailscale_device_exit_node_ratio", scope)
    sentinel("has_routes", "tailscale_device_routes_advertised", scope)
    sentinel("has_derp", "tailscale_device_derp_latency_seconds", scope)
    pii_sentinel("pii_perdevice",
                 '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)'
                 % (PII, PII), scope)
    pii_sentinel("pii_topology", PII + '{category="network_topology"} == 0', scope)

    # Shared infra label exclusion list for instant-vector tables
    _infra = ["Time", "__name__", "job", "instance", "host_id",
              "service_instance_id", "service_name", "service_namespace"]

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
               [prom_t("count(%s == 0) or (0 * count(%s))"
                       % (lot("tailscale_device_connectivity_udp_ratio"),
                          lot("tailscale_device_connectivity_udp_ratio")))],
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
        (panel("Hard-NAT fraction", "timeseries",
               [prom_t("sum(%(lot_hnat)s) / on() group_left() sum(%(lot_cnt)s)"
                       % {"lot_hnat": lot(sel("tailscale_devices_hard_nat_ratio"), WIN_FAST),
                          "lot_cnt": lot(sel("tailscale_devices_count_ratio"), WIN_FAST)},
                       legend="hard-NAT fraction")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(fill=10),
               options=ts_opts(),
               desc="Fraction of devices behind hard NAT. This is a connectivity property, not "
                    "a measurement of DERP load."), 12, 7),
        (panel("Configured peer-relay endpoints", "timeseries",
               [prom_t("sum(%s)" % sel("tailscaled_peer_relay_endpoints"),
                       legend="configured endpoints")],
               unit="short", custom=ts_custom(fill=10), options=ts_opts(),
               desc="Configured peer-relay endpoint count reported by node metrics. This is "
                    "inventory, not measured relay traffic or DERP load."), 12, 7),
    ]

    # D (per-device part), #401 + #526 decision 7. fleet.py's "Connectivity detail (per
    # device)" and "Needs relay (hard-NAT devices)" were two single-panel rows carrying
    # IDENTICAL gates; merged here into one. Both tables are 24-wide and the same height,
    # so the row is an AutoGrid.
    _cxdf = '{host_name=~"$host_name"}'
    connectivity_detail = [
        panel("Connectivity by device", "table",
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
                   "dashboard to it."),
        panel("Needs relay (hard-NAT)", "table",
              [prom_t("%s == 1" % lot('tailscale_device_connectivity_hard_nat_ratio{host_name=~"$host_name"}'),
                      instant=True, fmt="table")],
              transformations=[organize(
                  exclude=_infra + ["Value"],
                  rename={"host_name": "Host"})],
              desc="Devices behind hard NAT that require relay for inbound peer connections."),
    ]

    # E. Exit/subnet aggregate stats (present="has_exit", no PII). Four same-size 6x5
    # stats -> AutoGrid.
    exitsubnet = [
        panel("Exit nodes advertised", "stat",
              [prom_t('sum(%s) or vector(0)'
                      % lot('tailscale_exit_nodes_count_ratio{tailscale_exit_node_state="advertised"}', WIN_SLOW))],
              unit="short", options=stat_opts(color="value"),
              desc="Exit nodes currently in the 'advertised' state."),
        panel("Exit nodes enabled", "stat",
              [prom_t('sum(%s) or vector(0)'
                      % lot('tailscale_exit_nodes_count_ratio{tailscale_exit_node_state="enabled"}', WIN_SLOW))],
              unit="short", options=stat_opts(color="value"),
              desc="Exit nodes currently in the 'enabled' state."),
        panel("Unapproved subnet routes", "stat",
              [prom_t("max(%s) or vector(0)" % lot("tailscale_subnet_routes_unapproved", WIN_SLOW))],
              unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
              options=stat_opts(color="background"),
              desc="Subnet routes advertised but not yet approved by an admin."),
        # #392: the approved counterpart of the stat beside it. Absent means the exit/
        # subnet collection never ran, which is not the same as "no routes approved".
        panel("Enabled subnet routes", "stat",
              [prom_t("max(%s)" % lot("tailscale_subnet_routes_enabled", WIN_SLOW))],
              unit="short", options=stat_opts(color="value"),
              novalue="No subnet-route data — needs the devices collector "
                      "(collectors.devices) and a control plane that reports approved routes.",
              desc="Distinct subnet CIDRs approved on at least one device (exit-node default "
                   "routes excluded). Read against 'Unapproved subnet routes' beside it."),
        # #526: the third member of the set, and until now the only one with no panel.
        # It reached the coverage gate solely through the `has_subnet` sentinel's
        # label_values() query, which wave 3 deleted once the row it gated was merged
        # away — so the signal was never actually on screen, it just looked covered.
        # advertised >= enabled + unapproved is the invariant worth being able to see:
        # a gap that closes to neither means routes are being withdrawn rather than
        # approved.
        panel("Advertised subnet routes", "stat",
              [prom_t("max(%s)" % lot("tailscale_subnet_routes_advertised", WIN_SLOW))],
              unit="short", options=stat_opts(color="value"),
              novalue="No subnet-route data — needs the devices collector "
                      "(collectors.devices) and a control plane that reports advertised routes.",
              desc="Distinct subnet CIDRs advertised by at least one device (exit-node "
                   "default routes excluded). The denominator for the two stats beside "
                   "it: advertised minus enabled is what is still awaiting approval."),
    ]

    # E (per-device exit table). Kept as its own row on purpose — see the module docstring:
    # folding it into "Exit nodes" would drag hide_when=["pii_perdevice"] onto four plain
    # counts that carry no device identity.
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

    # E (subnet routing), #526 decision 7: fleet.py's "Subnet routes" and the single-panel
    # "Subnet redundancy" row are one row here. Deliberate asymmetry in height, so explicit
    # widths rather than AutoGrid.
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
        (panel("Subnet-route redundancy by CIDR", "barchart",
               [prom_t("max by (tailscale_route_cidr) (%s)"
                       % lot("tailscale_subnet_routes_routers_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Number of routers advertising each subnet CIDR (redundancy indicator)."), 24, 7),
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

    return [
        row("Connectivity", connectivity, present="has_connectivity"),
        # Collapsed: per-device tables are drill-down detail after the fleet summary.
        autogrid_row("Connectivity detail (per device)", connectivity_detail,
                     present="has_connectivity", hide_when=["pii_perdevice"], collapse=True),
        autogrid_row("Exit nodes", exitsubnet, present="has_exit"),
        # Collapsed: inventory and route details follow the exit-node summary.
        row("Exit-node inventory", exitinv,
            present="has_exit", hide_when=["pii_perdevice"], collapse=True),
        row("Subnet routes", routes,
            present="has_routes", hide_when=["pii_perdevice", "pii_topology"], collapse=True),
        # Collapsed: relay-region investigation follows a connectivity symptom.
        row("Connectivity / DERP", derp,
            present="has_derp", hide_when=["pii_perdevice"], collapse=True),
    ]
