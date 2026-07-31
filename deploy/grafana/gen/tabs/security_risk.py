"""tab_security_risk() — the "Risk & ACL" leaf of Security & Audit (#526).

Wave 2 split of the old 49-panel tabs/security.py leaf into four sub-tabs. This one owns
the policy-risk view: which access rules are unrestricted or wildcarded, and whether
tailnet lock is signing everything it should.

Content is COPIED from tabs/security.py (rows "ACL risk indicators" and "Tailnet lock"),
plus ONE new panel for a catalog signal that reached no panel anywhere before #526:

  * `tailscale.acl.risky_rule` (log event, WARN) — emitted once per unrestricted ACL/grant
    rule (wildcard `src` AND wildcard `dst` in a non-deny rule). It is the per-rule detail
    behind the "Unrestricted rules (acls)" / "Unrestricted rules (grants)" counts, so it
    sits in the same row rather than in one of its own.

Scope boundary (#526 decision 10): Policy & Config owns the CONFIGURATION view of the ACL
(what the policy says); this tab owns the RISK view (which of it is dangerous). A signal
appearing on both is intended, not duplication to reconcile.

Sentinel scoping (#526): all three are declared HERE at TAB scope. has_acl_risk and
has_tailnet_lock came from tabs/security.py and pii_perdevice from tabs/fleet.py, both at
DASHBOARD scope; a row gating on a sentinel nothing declares renders PERMANENTLY hidden and
is indistinguishable on screen from a correctly-gated empty row.
"""

from builder import (barchart_opts, bargauge_opts, logs_opts, loki_t, lot, organize, panel,
                     PII, pii_sentinel, prom_t, row, sentinel, stat_opts, thr, WIN_FAST,
                     WIN_SLOW)
from maps import BOOL_HEALTHY_OFF


def tab_security_risk(scope):
    sentinel("has_acl_risk", "tailscale_acl_unrestricted_rules_ratio", scope)
    sentinel("has_tailnet_lock", "tailscale_tailnet_lock_errors_ratio", scope)
    # Same expression as tabs/fleet.py's DASHBOARD-scoped declaration — kept
    # byte-identical on purpose so the two never diverge into two meanings.
    pii_sentinel("pii_perdevice",
                 '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)'
                 % (PII, PII), scope)

    # -----------------------------------------------------------------------
    # ACL risk indicators (from tabs/security.py, unchanged) + the new
    # per-rule log detail.
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
        # #526: tailscale.acl.risky_rule — the per-rule detail behind the two
        # "Unrestricted rules" counts above. No zero-fill: a log panel with no lines
        # is the correct healthy reading, and the description names the prerequisite.
        (panel("Unrestricted ACL/grant rules (log detail)", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.acl.risky_rule` "
                       "|~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="One WARN record per unrestricted rule — wildcard `src` AND wildcard `dst` in "
                    "a non-deny rule — naming the policy section (tailscale_acl_section) and the "
                    "offending src/dst entries (tailscale_acl_rule). This is the detail behind the "
                    "'Unrestricted rules' counts above: the counts say how many, this says which. "
                    "Needs the acl collector (collectors.acl); the rule text is free-text and is "
                    "dropped when pii_filter.free_text_details is on, leaving the section only."), 24, 9),
    ]

    # -----------------------------------------------------------------------
    # Tailnet lock (from tabs/security.py, unchanged). hide_when=["pii_perdevice"]
    # — tailnet-lock log records carry per-device host identity.
    # -----------------------------------------------------------------------
    tlock = [
        (panel("Tailnet-lock errors", "stat",
               [prom_t("max(%s) or vector(0)" % lot("tailscale_tailnet_lock_errors_ratio", WIN_FAST),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Devices with a non-empty tailnet-lock error (e.g. an unsigned node). >0 means a "
                    "signing node must sign the affected keys."), 6, 6),
        (panel("Nodes with tailnet-lock errors", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.tailnet_lock_error` "
                       "|~ `$log_filter`", maxlines=100)],
               options=logs_opts(),
               desc="Per-device tailnet-lock error events; the error text is the log body."), 18, 6),
    ]

    return [
        row("ACL risk indicators", aclrisk, present="has_acl_risk"),
        row("Tailnet lock", tlock, present="has_tailnet_lock", hide_when=["pii_perdevice"]),
    ]
