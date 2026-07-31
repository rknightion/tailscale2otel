"""tab_k8saudit() — Kubernetes-audit signals from tsrecorder (issue #462, commit b61fe7a).

Read docs/kubernetes-audit.md before touching this file. The one fact that shapes every
panel here: the source records carry NO response status, NO latency and NO byte count —
tsrecorder logs a request as the proxy forwards it, nothing on the way back. Every counter
below is an ATTEMPT count. No panel title or description may imply an outcome (allowed,
denied, failed, succeeded, error rate, latency) — that would be a claim the data cannot
support.

Cardinality is bounded by construction on every metric attribute (closed admit-set, unknown
values folded to `other`); the exceptions carried only on the Loki log records
(`tailscale.k8s.api_request` / `tailscale.k8s.session`) are object names, paths, selectors,
pod/container names and the raw exec command line. `tailscale_k8s_command_class` is the
bounded, non-PII summary of the exec command; the raw `tailscale_k8s_command` text is the
one attribute with its own redaction switch (`pii_filter.command_text`).
"""

from builder import (barchart_opts, bargauge_opts, logs_opts, loki_t, organize, panel, PII,
                     pii_sentinel, prom_t, RI, row, sentinel, thr, ts_custom, ts_opts, stat_opts)
from builder import DASHBOARD  # #526: wave 1 leaves every sentinel dashboard-level

# Metric names — frozen strings from issue #462 / commit b61fe7a, never re-derived.
K8S_REQUESTS = "tailscale_k8s_api_requests_total"
K8S_SENSITIVE_READS = "tailscale_k8s_api_sensitive_reads_total"
K8S_EXEC_SESSIONS = "tailscale_k8s_api_exec_sessions_total"
K8S_MUTATIONS = "tailscale_k8s_api_mutations_total"
K8S_RBAC_PROBES = "tailscale_k8s_api_rbac_probes_total"
K8S_SESSION_STARTED = "tailscale_k8s_session_started_total"
K8S_SCHEMA_DRIFT = "tailscale_k8s_schema_drift_total"

# Loki event streams. Both carry `tailscale_k8s_user` (identity) and, when the request is
# an exec/attach/portforward, the raw `tailscale_k8s_command` — so every panel querying
# either stream verbatim needs BOTH the emails and command-text redaction gates.
K8S_REQ_LOG = "{service_name=\"tailscale2otel\"} | event_name=`tailscale.k8s.api_request`"
K8S_SESSION_LOG = "{service_name=\"tailscale2otel\"} | event_name=`tailscale.k8s.session`"


def tab_k8saudit(scope):
    # Presence sentinels this tab declares. has_k8s_audit also gates the whole tab itself
    # (see build.py's tab_defs) — declared here because this is the tab it names.
    sentinel("has_k8s_audit", K8S_REQUESTS, DASHBOARD)
    # Only has_k8s_audit stays dashboard-level, because it gates this whole TAB from
    # the outside and a tab cannot gate itself on a variable that exists only once it
    # renders. Everything below gates a row inside the tab, so #526 scopes it here.
    sentinel("has_k8s_sensitive_reads", K8S_SENSITIVE_READS, scope)
    sentinel("has_k8s_exec_sessions", K8S_EXEC_SESSIONS, scope)
    sentinel("has_k8s_mutations", K8S_MUTATIONS, scope)
    sentinel("has_k8s_rbac_probes", K8S_RBAC_PROBES, scope)
    sentinel("has_k8s_sessions", K8S_SESSION_STARTED, scope)
    sentinel("has_k8s_schema_drift", K8S_SCHEMA_DRIFT, scope)
    # tailscale_k8s_user is a Kubernetes identity (typically an email/login via OIDC), so any
    # row that groups by it — metric or raw log record — hides under the SAME emails-redaction
    # gate the rest of the dashboard uses.
    #
    # Declared HERE since #526 wave 3. It used to be registered at DASHBOARD scope by
    # tabs/policy.py, where it gated nothing at all and this tab rode on it; that module
    # is gone, and this is now its only consumer on either dashboard. Left undeclared,
    # eight rows here would have gated on a name nothing defines and rendered
    # PERMANENTLY HIDDEN — which on screen is indistinguishable from a correctly
    # redacted deployment, so the Kubernetes audit tab would simply have looked empty.
    pii_sentinel("pii_emails", PII + '{category="emails"} == 0', scope)
    pii_sentinel("pii_command_text", PII + '{category="command_text"} == 0', scope)

    # -------------------------------------------------------------------------------------
    # Row 1: overall request volume. Ungated beyond the tab's own has_k8s_audit — it queries
    # the exact metric that sentinel checks, same as nodemetrics.py's "Traffic (tailscaled)"
    # row under has_nodemetrics.
    # -------------------------------------------------------------------------------------
    volume = [
        (panel("Kubernetes API requests/s", "timeseries",
               [prom_t("sum(rate(%s[%s]))" % (K8S_REQUESTS, RI))],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Kubernetes API requests proxied through Tailscale, per second. Counts "
                    "attempts only — the source record carries no response status, so this "
                    "is request volume, not success or failure rate."), 12, 7),
        (panel("Requests by verb", "timeseries",
               [prom_t("sum by (tailscale_k8s_verb) (rate(%s[%s]))" % (K8S_REQUESTS, RI),
                       legend="{{tailscale_k8s_verb}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Proxied Kubernetes API requests over time, by HTTP verb (get/list/watch/"
                    "create/update/patch/delete/...). A verb mix shift is worth correlating "
                    "with a client or access change."), 12, 7),
        (panel("Requests by namespace", "bargauge",
               [prom_t("sum by (tailscale_k8s_namespace) (rate(%s[%s]))" % (K8S_REQUESTS, RI),
                       legend="{{tailscale_k8s_namespace}}")],
               unit="cps", options=bargauge_opts(),
               desc="Request rate by target namespace. A cluster-scoped request (no "
                    "namespace) reports an empty namespace label."), 12, 6),
        (panel("Requests by resource (top $topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_resource) (increase(%s[$__range])))" % K8S_REQUESTS,
                       legend="{{tailscale_k8s_resource}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N Kubernetes resource types by request count over the selected "
                    "range. `other` means the resource kind fell outside the bounded "
                    "admit-set."), 12, 6),
        (panel("Requests by user agent (top $topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user_agent) (increase(%s[$__range])))" % K8S_REQUESTS,
                       legend="{{tailscale_k8s_user_agent}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N requesting clients by user agent string over the selected "
                    "range. Not identity — the same person can appear under several "
                    "agents (kubectl, a controller, a CI job)."), 24, 6),
    ]

    # -------------------------------------------------------------------------------------
    # Row 2/2b: sensitive-resource reads (secrets, service accounts, RBAC objects,
    # token/CSR reviews). A read being attempted, not that it succeeded or returned data.
    # -------------------------------------------------------------------------------------
    sensitive = [
        (panel("Sensitive reads/s by resource", "timeseries",
               [prom_t("sum by (tailscale_k8s_resource) (rate(%s[%s]))" % (K8S_SENSITIVE_READS, RI),
                       legend="{{tailscale_k8s_resource}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Read (get/list/watch) attempts against sensitive resource kinds — "
                    "secrets, service accounts, RBAC objects, token/CSR reviews. Attempts "
                    "only; RBAC may have refused any of these."), 12, 7),
        (panel("Sensitive reads by namespace", "bargauge",
               [prom_t("sum by (tailscale_k8s_namespace) (rate(%s[%s]))" % (K8S_SENSITIVE_READS, RI),
                       legend="{{tailscale_k8s_namespace}}")],
               unit="cps", options=bargauge_opts(),
               desc="Sensitive-resource read attempts by target namespace."), 12, 7),
        (panel("Sensitive reads by user agent (top $topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user_agent) (increase(%s[$__range])))" % K8S_SENSITIVE_READS,
                       legend="{{tailscale_k8s_user_agent}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N clients, by user agent, attempting sensitive-resource reads "
                    "over the selected range."), 24, 6),
    ]
    sensitive_users = [
        (panel("Users reading sensitive resources ($topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user) (increase(%s[$__range])))" % K8S_SENSITIVE_READS,
                       legend="{{tailscale_k8s_user}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N identities attempting sensitive-resource reads over the "
                    "selected range. Hidden when email/identity redaction is active."), 24, 7),
    ]

    # -------------------------------------------------------------------------------------
    # Row 3/3b: exec/attach/portforward sessions. command_class is the bounded, non-PII
    # summary and MUST stay visible even when the raw command text is redacted.
    # -------------------------------------------------------------------------------------
    exec_ = [
        (panel("Exec sessions/s by command class", "timeseries",
               [prom_t("sum by (tailscale_k8s_command_class) (rate(%s[%s]))" % (K8S_EXEC_SESSIONS, RI),
                       legend="{{tailscale_k8s_command_class}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Exec/attach/portforward requests against a pod, per second, by the "
                    "bounded command classification (interactive_shell, recon, "
                    "credential_read, package_mgmt, net_tool, file_transfer, none, other). "
                    "Stays visible even when raw command text is redacted."), 12, 7),
        (panel("Exec sessions by namespace", "bargauge",
               [prom_t("sum by (tailscale_k8s_namespace) (rate(%s[%s]))" % (K8S_EXEC_SESSIONS, RI),
                       legend="{{tailscale_k8s_namespace}}")],
               unit="cps", options=bargauge_opts(),
               desc="Exec/attach/portforward request rate by target namespace."), 12, 7),
        (panel("Exec sessions by session type", "barchart",
               [prom_t("sum by (tailscale_k8s_session_type) (increase(%s[$__range]))" % K8S_EXEC_SESSIONS,
                       legend="{{tailscale_k8s_session_type}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Exec/attach/portforward request count over the selected range, by "
                    "session type."), 24, 6),
    ]
    exec_users = [
        (panel("Users by exec/attach/portforward sessions ($topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user) (increase(%s[$__range])))" % K8S_EXEC_SESSIONS,
                       legend="{{tailscale_k8s_user}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N identities requesting exec/attach/portforward sessions over "
                    "the selected range. Hidden when email/identity redaction is active."), 24, 7),
    ]

    # -------------------------------------------------------------------------------------
    # Row 4/4b: mutating requests. Counts the request being made, not that it was
    # admitted or persisted.
    # -------------------------------------------------------------------------------------
    mutations = [
        (panel("Mutations/s by verb", "timeseries",
               [prom_t("sum by (tailscale_k8s_verb) (rate(%s[%s]))" % (K8S_MUTATIONS, RI),
                       legend="{{tailscale_k8s_verb}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Mutating (create/update/patch/delete/deletecollection) request "
                    "attempts, per second, by verb. Counts the request being made, not "
                    "that the change was admitted or persisted."), 12, 7),
        (panel("Mutations by resource (top $topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_resource) (increase(%s[$__range])))" % K8S_MUTATIONS,
                       legend="{{tailscale_k8s_resource}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N resource kinds targeted by mutating requests over the selected "
                    "range."), 12, 7),
        (panel("Mutations by namespace", "bargauge",
               [prom_t("sum by (tailscale_k8s_namespace) (rate(%s[%s]))" % (K8S_MUTATIONS, RI),
                       legend="{{tailscale_k8s_namespace}}")],
               unit="cps", options=bargauge_opts(),
               desc="Mutating request attempt rate by target namespace."), 24, 6),
    ]
    mutation_users = [
        (panel("Users making mutating requests ($topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user) (increase(%s[$__range])))" % K8S_MUTATIONS,
                       legend="{{tailscale_k8s_user}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N identities attempting mutating requests over the selected "
                    "range. Hidden when email/identity redaction is active."), 24, 7),
    ]

    # -------------------------------------------------------------------------------------
    # Row 5/5b: permission-enumeration probes (SelfSubjectAccessReview and friends). A
    # probe being made, not its answer — there is no status on the wire to read.
    # -------------------------------------------------------------------------------------
    rbac = [
        (panel("RBAC probes/s by resource", "timeseries",
               [prom_t("sum by (tailscale_k8s_resource) (rate(%s[%s]))" % (K8S_RBAC_PROBES, RI),
                       legend="{{tailscale_k8s_resource}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="SelfSubjectAccessReview/SelfSubjectRulesReview/SubjectAccessReview/"
                    "TokenReview requests — a client asking what it or another identity is "
                    "allowed to do — per second. A burst from an unexpected client is the "
                    "signal; normal for UI clients, which is why no alert ships by default."), 12, 7),
        (panel("RBAC probes by namespace", "bargauge",
               [prom_t("sum by (tailscale_k8s_namespace) (rate(%s[%s]))" % (K8S_RBAC_PROBES, RI),
                       legend="{{tailscale_k8s_namespace}}")],
               unit="cps", options=bargauge_opts(),
               desc="Permission-enumeration probe rate by namespace."), 12, 7),
    ]
    rbac_users = [
        (panel("Users making RBAC/permission probes ($topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user) (increase(%s[$__range])))" % K8S_RBAC_PROBES,
                       legend="{{tailscale_k8s_user}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N identities making permission-enumeration probes over the "
                    "selected range. Hidden when email/identity redaction is active."), 24, 7),
    ]

    # -------------------------------------------------------------------------------------
    # Row 6/6b: recorded terminal sessions. Completeness is not observable — there is no
    # documented end-of-recording signal — so this counts session STARTS only.
    # -------------------------------------------------------------------------------------
    sessions = [
        (panel("Sessions started/s by type", "timeseries",
               [prom_t("sum by (tailscale_k8s_session_type) (rate(%s[%s]))" % (K8S_SESSION_STARTED, RI),
                       legend="{{tailscale_k8s_session_type}}")],
               unit="cps", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Recorded terminal sessions started against a Kubernetes pod, per "
                    "second, by session type. Session completeness cannot be observed, so "
                    "this counts starts only."), 12, 7),
        (panel("Sessions by namespace", "bargauge",
               [prom_t("sum by (tailscale_k8s_namespace) (rate(%s[%s]))" % (K8S_SESSION_STARTED, RI),
                       legend="{{tailscale_k8s_namespace}}")],
               unit="cps", options=bargauge_opts(),
               desc="Recorded terminal-session start rate by target namespace."), 12, 7),
        (panel("Sessions by command class", "barchart",
               [prom_t("sum by (tailscale_k8s_command_class) (increase(%s[$__range]))" % K8S_SESSION_STARTED,
                       legend="{{tailscale_k8s_command_class}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Recorded terminal-session starts over the selected range, by the "
                    "bounded launch-command classification. Stays visible even when raw "
                    "command text is redacted."), 24, 6),
    ]
    session_users = [
        (panel("Users starting terminal sessions ($topn)", "barchart",
               [prom_t("topk($topn, sum by (tailscale_k8s_user) (increase(%s[$__range])))" % K8S_SESSION_STARTED,
                       legend="{{tailscale_k8s_user}}", instant=True, fmt="table")],
               unit="short", options=barchart_opts(),
               transformations=[organize(exclude=["Time"])],
               desc="Top-N identities starting recorded terminal sessions over the "
                    "selected range. Hidden when email/identity redaction is active."), 24, 7),
    ]

    # -------------------------------------------------------------------------------------
    # Row 7: schema drift — the feed's own health signal. Upstream's event schema is
    # unversioned and beta; a healthy feed reports nothing here at all.
    # -------------------------------------------------------------------------------------
    drift = [
        (panel("Schema drift events/s by field", "timeseries",
               [prom_t("sum by (field, status) (rate(%s[%s]))" % (K8S_SCHEMA_DRIFT, RI),
                       legend="{{field}}/{{status}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Kubernetes-audit schema vocabulary observations, per second, by field "
                    "and whether the value is known to this collector version. A healthy "
                    "feed reports nothing here — any nonzero rate means an unexpected "
                    "event type or enum value, worth checking after an operator/recorder "
                    "upgrade."), 12, 6),
        (panel("Schema drift events (range)", "stat",
               [prom_t("sum(increase(%s[$__range])) or vector(0)" % K8S_SCHEMA_DRIFT, instant=True)],
               unit="short", novalue="0", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               desc="Total schema-drift observations over the selected range. A healthy "
                    "feed reports 0; any nonzero count means upstream's unversioned event "
                    "schema moved."), 12, 6),
    ]

    # -------------------------------------------------------------------------------------
    # Row 8: raw log detail. The full record — verbatim tailscale.k8s.api_request /
    # tailscale.k8s.session — carries the requesting identity and, for exec-shaped
    # requests, the raw command line, so both redaction gates apply here.
    # -------------------------------------------------------------------------------------
    logdetail = [
        (panel("Recent Kubernetes API requests", "logs",
               [loki_t("%s |~ `$log_filter`" % K8S_REQ_LOG, maxlines=200)],
               options=logs_opts(),
               desc="Live stream of proxied Kubernetes API requests; filter with the Log "
                    "filter variable. Hidden when email or exec-command redaction is "
                    "active — the record carries requesting identity and, for exec-shaped "
                    "requests, the raw command."), 12, 10),
        (panel("Recent terminal sessions", "logs",
               [loki_t("%s |~ `$log_filter`" % K8S_SESSION_LOG, maxlines=200)],
               options=logs_opts(),
               desc="Live stream of recorded terminal-session starts; filter with the Log "
                    "filter variable. Hidden when email or exec-command redaction is "
                    "active — the record carries requesting identity and the raw launch "
                    "command."), 12, 10),
    ]

    # -------------------------------------------------------------------------------------
    # Row 9: curated investigative queries from docs/kubernetes-audit.md's "Investigating"
    # section, including the anti-forensics query (reads of the recorder's own logs).
    # -------------------------------------------------------------------------------------
    # Only the exec panel below can surface raw command text, so it is the ONLY one
    # gated on pii_command_text. Gating the whole row on it (as this tab first did)
    # meant an operator who redacted command text also lost the anti-forensics and
    # enumeration panels, which carry no command at all — the most valuable panels
    # here, hidden for a reason that does not apply to them.
    investigations = [
        (panel("Secret & sensitive resource reads", "logs",
               [loki_t("%s | tailscale_k8s_resource=`secrets`" % K8S_REQ_LOG, maxlines=100)],
               options=logs_opts(),
               desc="Who attempted to read secrets, and from where. Attempts only — RBAC "
                    "may have refused any of these. Hidden under email redaction."), 12, 8),
        (panel("Permission enumeration probes", "logs",
               [loki_t("%s | tailscale_k8s_resource=~`selfsubject.*`" % K8S_REQ_LOG, maxlines=100)],
               options=logs_opts(),
               desc="Detail behind the RBAC-probe metrics. A burst from an unexpected "
                    "user agent is the signal; normal for UI clients. Hidden under email "
                    "redaction."), 12, 8),
        (panel("Reads of the recorder's own logs (anti-forensics)", "logs",
               [loki_t("%s | tailscale_k8s_subresource=`log` | tailscale_k8s_namespace=`tailscale`"
                       % K8S_REQ_LOG, maxlines=100)],
               options=logs_opts(),
               desc="Requests to read the tsrecorder's own log objects — a client "
                    "reaching for the audit trail itself is an anti-forensics indicator. "
                    "Hidden under email redaction."), 12, 8),
    ]

    exec_detail = [
        (panel("Exec / attach / port-forward requests", "logs",
               [loki_t("%s | tailscale_k8s_subresource=~`exec|attach|portforward`" % K8S_REQ_LOG,
                       maxlines=100)],
               options=logs_opts(),
               desc="Every exec/attach/port-forward request, with the bounded command "
                    "classification alongside the raw command line when it is not redacted. "
                    "This is the one investigation panel that can surface command text, so "
                    "it is the one hidden under exec-command redaction."), 12, 8),
    ]

    return [
        row("Kubernetes API request volume", volume),
        row("Sensitive resource reads", sensitive, present="has_k8s_sensitive_reads"),
        row("Sensitive resource reads — top readers", sensitive_users,
            present="has_k8s_sensitive_reads", hide_when=["pii_emails"]),
        row("Exec / attach / portforward sessions", exec_, present="has_k8s_exec_sessions"),
        row("Exec / attach / portforward sessions — top users", exec_users,
            present="has_k8s_exec_sessions", hide_when=["pii_emails"]),
        row("Mutating requests", mutations, present="has_k8s_mutations"),
        row("Mutating requests — top users", mutation_users,
            present="has_k8s_mutations", hide_when=["pii_emails"]),
        row("RBAC / permission probes", rbac, present="has_k8s_rbac_probes"),
        row("RBAC / permission probes — top users", rbac_users,
            present="has_k8s_rbac_probes", hide_when=["pii_emails"]),
        row("Terminal sessions started", sessions, present="has_k8s_sessions"),
        row("Terminal sessions — top users", session_users,
            present="has_k8s_sessions", hide_when=["pii_emails"]),
        row("Schema drift (feed health)", drift, present="has_k8s_schema_drift"),
        row("Kubernetes audit log detail", logdetail, hide_when=["pii_emails", "pii_command_text"]),
        row("Kubernetes audit investigations", investigations, hide_when=["pii_emails"]),
        row("Kubernetes audit investigations — exec detail", exec_detail,
            present="has_k8s_exec_sessions", hide_when=["pii_emails", "pii_command_text"]),
    ]
