"""tab_devices_posture() — the "Posture & Security" sub-tab of the Devices leaf (#526).

Content is LIFTED UNCHANGED from tabs/fleet.py's three posture / credential-hygiene rows:
"Device security flags" (two fleet roll-ups plus five aggregates over the per-device flag
gauges, #401), "Key-expiry distribution" (the point-in-time expiry bands derived from the
per-device expiry GAUGE rather than the latching cumulative histogram, #109) and "Device
posture". tab_fleet() was one 59-panel leaf; #526 splits it into three sub-tabs, of which
this is the security-facing one. No panel was added, dropped or re-queried here.

Sentinel scoping (#526): has_device_flags, has_key_expiry_hist, has_posture and
pii_perdevice are declared HERE at TAB scope rather than at DASHBOARD scope — tabs/fleet.py
declared all four dashboard-wide and that module is gone. Two of them are referenced by rows
this module does NOT own: has_posture by tabs/events.py's "Posture logs" row and by
tabs/security.py's "Security posture" / "Device posture log" rows, and pii_perdevice by four
rows in tabs/security.py as well as by both sibling Devices sub-tabs. A row whose gate names
a sentinel that nothing declares in the same scope renders PERMANENTLY HIDDEN — on screen
indistinguishable from a correctly-gated empty row — so each of those modules must declare
the name at its OWN scope. The registry allows one name in two different TAB scopes; it
forbids only the dashboard+tab mix.
"""

from builder import (autogrid_row, barchart_opts, hq, lot, organize, panel, PII,
                     pii_sentinel, prom_t, row, sentinel, stat_opts, thr, ts_custom,
                     ts_opts, WIN_SLOW)
from tabs._devices_common import NO_PER_DEVICE, TP

# Tailnet/provider selectors (#392). Both are real per-series metric LABELS (not resource
# attributes), so a panel filters them straight in the selector — see the note in builder.py.
# allValue ".*" also matches series that carry no such label, so a single-tailnet deployment
# reads exactly as it did before.

# Tailscale SSH and key-expiry state are not concepts every control plane has: Headscale
# emits neither metric at all. Zero-filling them would render "0 problems" for a question
# that was never asked (#385, #392).
_NO_SSH_STATE = ("Not reported — needs a control plane that reports Tailscale SSH "
                 "state, and the devices collector.")
_NO_KEY_EXPIRY_STATE = ("Not reported — needs a control plane that reports node-key "
                        "expiry state, and the devices collector.")


def tab_devices_posture(scope):
    # Every sentinel this sub-tab's rows reference, declared at THIS tab's scope.
    sentinel("has_device_flags", "tailscale_device_blocks_incoming_connections_ratio", scope)
    sentinel("has_key_expiry_hist", "tailscale_devices_key_expiry_days_count", scope)
    sentinel("has_posture", "tailscale_device_posture_ratio", scope)
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

    # Shared infra label exclusion list for instant-vector tables
    _infra = ["Time", "__name__", "job", "instance", "host_id",
              "service_instance_id", "service_name", "service_namespace"]

    # B2. Device security flags (#401). Two fleet roll-ups plus five aggregates over
    # per-device gauges. Every panel names its own prerequisite instead of zero-filling:
    # presence alone cannot tell "cardinality.per_entity.device is off" from "this
    # control plane has no such concept", so neither claim is made.
    # Seven same-size (4x5) stats -> AutoGrid, so the row reflows rather than being
    # pinned to a hand-written 24-column split (#526 decision 6).
    secflags = [
        panel("Tailscale SSH enabled (fleet)", "stat",
              [prom_t("max(%s)" % lot("tailscale_devices_ssh_enabled_ratio", WIN_SLOW))],
              unit="short", options=stat_opts(color="value"), novalue=_NO_SSH_STATE,
              desc="Devices accepting Tailscale SSH sessions, from the fleet roll-up "
                   "(emitted regardless of cardinality.per_entity.device)."),
        panel("Key expiry disabled (fleet)", "stat",
              [prom_t("max(%s)" % lot("tailscale_devices_key_expiry_disabled_ratio", WIN_SLOW))],
              unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
              options=stat_opts(color="background"), novalue=_NO_KEY_EXPIRY_STATE,
              desc="Devices whose node key never expires. These are invisible to every "
                   "key-expiry panel on this tab, so this is the only place they show up."),
        panel("Tailscale SSH enabled (devices)", "stat",
              [prom_t("count(%s == 1)" % lot("tailscale_device_ssh_enabled_ratio" + df))],
              unit="short", options=stat_opts(color="value"), novalue=_NO_SSH_STATE,
              desc="Same count derived from the per-device gauge, so it honours the tab's "
                   "OS/host/user/tag filters. Diverging from the fleet roll-up means the filters bite."),
        panel("Key expiry disabled (devices)", "stat",
              [prom_t("count(%s == 1)" % lot("tailscale_device_key_expiry_disabled_ratio" + df))],
              unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
              options=stat_opts(color="background"), novalue=_NO_KEY_EXPIRY_STATE,
              desc="Per-device view of never-expiring node keys, filterable to a host or tag."),
        panel("Blocking incoming connections", "stat",
              [prom_t("count(%s == 1)" % lot("tailscale_device_blocks_incoming_connections_ratio" + df))],
              unit="short", options=stat_opts(color="value"), novalue=NO_PER_DEVICE,
              desc="Devices refusing inbound tailnet connections. Protective, but it also makes a "
                   "device unreachable for admin access — read it as configuration, not as a fault."),
        panel("Multiple simultaneous connections", "stat",
              [prom_t("count(%s == 1)" % lot("tailscale_device_multiple_connections_ratio" + df))],
              unit="short", thresholds=thr([(None, "green"), (1, "red")]),
              options=stat_opts(color="background"), novalue=NO_PER_DEVICE,
              desc="Devices whose identity is in use by more than one client at once — a duplicated "
                   "node key or a cloned state store."),
        panel("Posture identity disabled", "stat",
              [prom_t("count(%s == 1)" % lot("tailscale_device_posture_identity_disabled_ratio" + df))],
              unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
              options=stat_opts(color="background"), novalue=NO_PER_DEVICE,
              desc="Devices exempted from posture-identity collection, so posture-gated ACL rules "
                   "cannot evaluate them."),
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

    return [
        autogrid_row("Device security flags", secflags, present="has_device_flags"),
        # Deliberate asymmetry (a 6-wide stat beside an 18-wide timeseries, then a
        # full-width barchart) — AutoGrid would flatten it to equal cells, so this row
        # keeps its explicit widths.
        row("Key-expiry distribution", keylife, present="has_key_expiry_hist"),
        row("Device posture", posture, present="has_posture", hide_when=["pii_perdevice"]),
    ]
