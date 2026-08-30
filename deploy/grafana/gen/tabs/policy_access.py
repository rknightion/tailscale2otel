"""tab_policy_access() — the "Access & ACL" leaf of the Policy & Config sub-tab group (#526).

Split out of tabs/policy.py, which was one 47-panel leaf. Content is the ACL snapshot
(what the policy currently contains) and the validation health of that same policy —
the two rows an operator reads together when asking "what does my access policy say,
and does it still parse".

Scope boundary (#526 decision 10): this is the CONFIGURATION view. Which rules and
auto-approvers exist, how big the policy is, whether it validated. The RISK view —
wildcard SSH rules, over-broad grants — lives on Security & Audit, and a signal
appearing on both is intended.

Sentinels: none. Neither row is feature-gated; both surfaces are always emitted by the
acl collector, and the validation row relies on per-panel noValue prose that names its
prerequisites instead (a hidden row is indistinguishable from a correctly-gated empty
one, which is the wrong answer for "why is there no validation result").
"""

from builder import (acl_change_evidence, bargauge_opts, loki_t, lot, organize, panel, prom_t, row,
                     stat_opts, thr, WIN_SLOW)
from maps import BOOL_HEALTHY_ON

DOCS = "https://m7kni.io/tailscale2otel"
CFG_DOC = DOCS + "/configuration/"

# #393 — tailnet / provider filters. Both are real per-series metric labels
# (roadmap item L), so filtering is a plain selector with no target_info join.
TNP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'

# The same filter for Loki: the tailnet/provider const attrs ride every log record as
# structured metadata.
LOKI_TN = ('{service_name="tailscale2otel"} | tailscale_tailnet=~"$tailnet" '
           '| tailscale2otel_provider=~"$provider"')


def sel(metric, extra=""):
    """`<metric>{<tailnet/provider filter>[, <extra>]}` — the filtered selector."""
    return "%s{%s%s}" % (metric, TNP, (", " + extra) if extra else "")


def tab_policy_access(scope):
    del scope  # no tab-scoped sentinels on this leaf; see module docstring
    acl = [
        (panel("ACL change evidence age", "stat",
               [prom_t(acl_change_evidence(sel("tailscale_acl_last_audit_change_seconds"),
                                           sel("tailscale_acl_last_changed_seconds")),
                       legend="{{provenance}}")],
               unit="s", options=stat_opts(graph="none"),
               desc="Prefers the configuration-audit event's source timestamp. Until one has "
                    "been observed, shows when this exporter first observed the current ETag; "
                    "that approximation survives restart only with a durable checkpoint store. "
                    "This dates evidence for an investigation, not policy health."), 6, 5),
        (panel("ACL size", "stat",
               [prom_t("max(%s)" % lot(sel("tailscale_acl_size_bytes"), WIN_SLOW))],
               unit="bytes", options=stat_opts(),
               desc="Serialized size of the active policy file. Useful as a change detector "
                    "alongside ACL change evidence age — a large jump is a rewrite, not an edit."), 6, 5),
        (panel("ACL rules by section", "bargauge",
               [prom_t("max by (tailscale_acl_section) (%s)" % lot(sel("tailscale_acl_rules_ratio"), WIN_SLOW),
                       legend="{{tailscale_acl_section}}")],
               unit="short", options=bargauge_opts(),
               desc="Rule count per top-level policy section (acls, grants, ssh, nodeAttrs, "
                    "tagOwners, ...). Structure of the policy, not a verdict on it."), 12, 5),
        # Task 1H.9 — ACL inventory counts (risk stats live on Security & Audit)
        (panel("Auto-approvers (inventory)", "bargauge",
               [prom_t("sum by (tailscale_acl_autoapprover_kind) (%s) or (0 * count(%s))"
                       % (lot(sel("tailscale_acl_autoapprovers_ratio"), WIN_SLOW),
                          lot(sel("tailscale_acl_size_bytes"), WIN_SLOW)),
                       legend="{{tailscale_acl_autoapprover_kind}}")],
               unit="short", options=bargauge_opts(),
               desc="Auto-approver entries by kind (routes / exit node). Inventory only — "
                    "whether an auto-approver is too permissive is a Security & Audit question."), 12, 5),
        (panel("Posture-gated rules (inventory)", "bargauge",
               [prom_t("sum by (tailscale_acl_section) (%s)" % lot(sel("tailscale_acl_posture_gated_rules_ratio"), WIN_SLOW),
                       legend="{{tailscale_acl_section}}")],
               unit="short", options=bargauge_opts(),
               desc="Rules carrying a device-posture condition, by section. Counts how much of "
                    "the policy depends on posture data actually arriving."), 12, 5),
    ]

    # ------------------------------------------------------------------
    # #393/#403 — ACL policy validation
    # ------------------------------------------------------------------
    aclvalidation = [
        (panel("ACL policy validated", "stat",
               [prom_t("min(%s)" % lot(sel("tailscale_acl_validation_ok_ratio"), WIN_SLOW))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               # Absent, not 0, when the validate call itself is unavailable — so no
               # zero-fill, and the empty state names what has to be true instead.
               novalue="No validation result. Prerequisites: collectors.acl.enabled with "
                       "collectors.acl.validate, and a credential carrying the policy_file:read "
                       "scope. See " + CFG_DOC,
               desc="1 when the active policy — including the tests embedded in its own `tests` "
                    "section — validated cleanly on the last check. Empty rather than 0 when the "
                    "validate call is unavailable; tailscale2otel.api.availability carries that "
                    "state separately."), 6, 6),
        (panel("Validation issues (last check)", "bargauge",
               [prom_t("max(%s)" % lot(sel("tailscale_acl_validation_errors_ratio"), WIN_SLOW),
                       legend="errors"),
                prom_t("max(%s)" % lot(sel("tailscale_acl_validation_warnings_ratio"), WIN_SLOW),
                       legend="warnings"),
                prom_t("max(%s)" % lot(sel("tailscale_acl_validation_test_failures_ratio"), WIN_SLOW),
                       legend="test failures")],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=bargauge_opts(),
               desc="Counts from the last policy validation. Errors and test failures are "
                    "reported separately by the API, so errors stays 0 in the common case while "
                    "an embedded test fails. Warnings include advisory findings such as a group "
                    "that is not syncing from SCIM."), 8, 6),
        (panel("Validation issues by kind (logs)", "table",
               [loki_t("sum by (tailscale_acl_validation_kind) (count_over_time("
                       "%s | event_name=`tailscale.acl.validation_issue` [$__range]))" % LOKI_TN,
                       instant=True)],
               transformations=[organize(exclude=["Time"],
                                         rename={"tailscale_acl_validation_kind": "Kind",
                                                 "Value": "Observations"})],
               novalue="0",
               desc="One WARN per non-zero issue kind (error / warning / test_failure) per check. "
                    "The validator's free-text messages carry rule text, user names and addresses "
                    "and are deliberately never emitted, so the kind is all there is here — read "
                    "the policy itself for the detail."), 10, 6),
    ]

    # Snapshot chunks carry full raw policy text only after the explicit
    # snapshot opt-in. The table is intentionally keyed by emission ID and
    # ordered by sequence: never join same-ETag heartbeats together. Each
    # selected emission must have exactly one revision before its chunks are
    # concatenated in seq order by the reader/export workflow.
    snapshots = [
        (panel("Policy snapshots (chunked)", "logs",
               [loki_t("%s | event_name=`tailscale.acl.policy_snapshot` |~ `$log_filter`" % LOKI_TN,
                       maxlines=500)],
               options={"showTime": True, "wrapLogMessage": False,
                        "sortOrder": "Ascending", "enableLogDetails": True},
               desc="Raw policy chunks, grouped by tailscale_snapshot_emission_id. Reassemble only "
                    "one emission-id group whose tailscale_snapshot_revision is identical on every "
                    "chunk, sorting tailscale_snapshot_seq ascending; do not join two same-ETag "
                    "heartbeat groups. Disabled unless collectors.acl.snapshot_enabled explicitly "
                    "consents to exporting user emails and group membership into log retention."), 24, 12),
        (panel("Policy changes (unified diffs)", "logs",
               [loki_t("%s | event_name=`tailscale.acl.policy_diff` |~ `$log_filter`" % LOKI_TN,
                       maxlines=200)],
               options={"showTime": True, "wrapLogMessage": True,
                        "sortOrder": "Descending", "enableLogDetails": True},
               desc="Unified diff from the previous raw policy body to this revision. It is emitted "
                    "only when the snapshot opt-in is enabled and an ETag changes; the baseline is "
                    "persisted beside checkpoint evidence so a post-restart change remains useful."), 24, 9),
    ]

    return [
        row("Access control (ACL)", acl),
        # policy validation health, next to the ACL snapshot it validates
        row("ACL policy validation", aclvalidation),
        row("Policy history (explicit raw-body opt-in)", snapshots),
    ]
