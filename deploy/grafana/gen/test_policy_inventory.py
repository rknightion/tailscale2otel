#!/usr/bin/env python3
"""Coverage + safety tests for the audit/integration and inventory surfaces
(#393 "Events & Logs" audit semantics, #403 OAuth/webhook/log-stream inventory).

Everything here asserts against the BUILT dashboard document, never the
generator's source text. A panel can be present in policy.py and still never
reach the artifact — wrong row, wrong tab, an element that no GridLayoutItem
references — and a source-text grep says nothing about that.

Two of these tests exist because the failure they catch is SILENT:

* the URL/secret scan. `tailscale.oauth_app.redirect_uris` and the webhook
  endpoint metrics are deliberately COUNTS and CLASSES; the URIs, endpoint
  URLs, bucket destinations and secrets are never emitted. A panel that
  grouped by, renamed to, or merely promised one of those fields would render
  as an empty column with no error, while telling an operator that the value
  is available. The guards-the-guard test below proves the scan can fail.
* the signal inventory. A panel deleted in a later refactor takes its signal's
  coverage with it, and `signal_dispositions.json` would quietly flip back to
  `raw_only`. The explicit list makes that a test failure here first.
"""

import importlib.util
from pathlib import Path
import re
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


dashboard = load_module("tailscale2otel_dashboard_inventory", Path(__file__).with_name("build.py"))


# ---------------------------------------------------------------------------
# document helpers — resolve rows to their panels the way Grafana does
# ---------------------------------------------------------------------------

def find_rows(doc):
    """Every RowsLayoutRow in the document, as {title: row}."""
    out = {}

    def walk(node):
        if isinstance(node, dict):
            if node.get("kind") == "RowsLayoutRow":
                out[node["spec"].get("title", "")] = node
            for v in node.values():
                walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)

    walk(doc)
    return out


def element_names(node):
    """Panel element names a layout subtree references, in layout order."""
    found = []

    def walk(n):
        if isinstance(n, dict):
            if n.get("kind") == "ElementReference":
                found.append(n["name"])
            for v in n.values():
                walk(v)
        elif isinstance(n, list):
            for v in n:
                walk(v)

    walk(node)
    return found


def row_panels(doc, title):
    """The resolved Panel dicts a named row lays out."""
    rows = find_rows(doc)
    if title not in rows:
        raise AssertionError("no row titled %r in the built dashboard (rows: %s)"
                             % (title, sorted(rows)))
    elements = doc["spec"]["elements"]
    return [elements[n] for n in element_names(rows[title])]


def panel_queries(panel):
    """(datasource-variable-name, query-string) for each of a panel's queries.

    Prometheus and Loki both key the text as `expr`; Tempo uses `query`.
    """
    out = []
    for q in panel["spec"]["data"]["spec"]["queries"]:
        qs = q["spec"]["query"]
        text = qs["spec"].get("expr") or qs["spec"].get("query") or ""
        out.append((qs.get("datasource", {}).get("name", ""), text))
    return out


def all_panels(doc):
    return list(doc["spec"]["elements"].values())


def all_query_text(doc):
    """Every query string in the document, including template variables — a
    signal reached only by a variable's label_values() still counts as covered,
    which is how internal/catalog scores the dashboard."""
    texts = []
    for p in all_panels(doc):
        texts.extend(t for (_, t) in panel_queries(p))
    for v in doc["spec"]["variables"]:
        q = v["spec"].get("query")
        if isinstance(q, dict):
            texts.append(q.get("spec", {}).get("query", ""))
    return texts


# ---------------------------------------------------------------------------
# the assigned inventory (#393 + #403)
# ---------------------------------------------------------------------------
#
# Left column is the OTEL source name (what signal_dispositions.json keys on);
# right column is the exact token a query has to contain for it to be covered —
# the NORMALIZED Prometheus spelling for a metric, the literal event_name for a
# log event. Never regenerate this from the catalog: a list derived from the
# thing under test cannot fail.

ASSIGNED_METRICS = {
    # OAuth applications (#403)
    "tailscale.oauth_app.node_attributes": "tailscale_oauth_app_node_attributes_ratio",
    "tailscale.oauth_app.redirect_uris": "tailscale_oauth_app_redirect_uris_ratio",
    "tailscale.oauth_app.scope_class": "tailscale_oauth_app_scope_class_ratio",
    "tailscale.oauth_app.scopes": "tailscale_oauth_app_scopes_ratio",
    "tailscale.oauth_apps.age": "tailscale_oauth_apps_age_seconds_bucket",
    "tailscale.oauth_apps.count": "tailscale_oauth_apps_count_ratio",
    # Webhook endpoints (#403)
    "tailscale.webhook_endpoint.age": "tailscale_webhook_endpoint_age_seconds_bucket",
    "tailscale.webhook_endpoint.subscriptions": "tailscale_webhook_endpoint_subscriptions_ratio",
    "tailscale.webhook_endpoints.desired_unrecognized":
        "tailscale_webhook_endpoints_desired_unrecognized_ratio",
    "tailscale.webhook_endpoints.event_desired_covered":
        "tailscale_webhook_endpoints_event_desired_covered_ratio",
    "tailscale.webhook_endpoints.event_subscriptions":
        "tailscale_webhook_endpoints_event_subscriptions_ratio",
    # ACL policy validation (#393/#403)
    "tailscale.acl.validation.errors": "tailscale_acl_validation_errors_ratio",
    "tailscale.acl.validation.ok": "tailscale_acl_validation_ok_ratio",
    "tailscale.acl.validation.test_failures": "tailscale_acl_validation_test_failures_ratio",
    "tailscale.acl.validation.warnings": "tailscale_acl_validation_warnings_ratio",
    # Log streaming + audit pipeline (#393)
    "tailscale.logstream.requests": "tailscale_logstream_requests_total",
    "tailscale.config.audit.deferred.delay": "tailscale_config_audit_deferred_delay_seconds_bucket",
    "tailscale.config.audit.processing.delay":
        "tailscale_config_audit_processing_delay_seconds_bucket",
}

ASSIGNED_LOG_EVENTS = {
    "tailscale.oauth_app.info": "tailscale.oauth_app.info",
    "tailscale.webhook_endpoints.event_mismatch": "tailscale.webhook_endpoints.event_mismatch",
    "tailscale.acl.validation_issue": "tailscale.acl.validation_issue",
}

# Rows this work added. An inventory, so deleting one fails HERE — with the
# issue number attached — instead of silently un-covering six signals.
NEW_ROWS = [
    "OAuth applications",          # #403
    "ACL policy validation",       # #393/#403
    "Webhook endpoint inventory",  # #403
    "Audit pipeline state",        # #393 — the four-state distinction
    "Audit pipeline latency",      # #393
]

POLICY_ROWS = [
    "Access control (ACL)", "ACL policy validation", "DNS", "Settings & features",
    "Users", "Per-user detail", "API keys & credential scopes", "Key expiry detail",
    "OAuth applications", "Services / VIP", "VIP service detail",
    "Webhook endpoint inventory", "Device-posture integrations",
]

# The four states #393 requires an operator to be able to tell apart. Each
# string must appear in the "Audit pipeline state" row's rendered text.
FOUR_STATES = [
    "absent Loki data",
    "audit collection not enabled",
    "idle tailnet",
    "ingestion failure",
]


class AssignedSignalCoverageTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = dashboard.build_family()
        cls.queries = all_query_text(cls.doc)

    def test_every_assigned_metric_is_queried(self):
        blob = "\n".join(self.queries)
        missing = sorted(otel for (otel, prom) in ASSIGNED_METRICS.items() if prom not in blob)
        self.assertEqual(missing, [],
                         "assigned signal(s) %s are not queried by any panel — the dashboard "
                         "half of #393/#403 is incomplete for them" % missing)

    def test_every_assigned_log_event_is_queried(self):
        blob = "\n".join(self.queries)
        missing = sorted(otel for (otel, name) in ASSIGNED_LOG_EVENTS.items() if name not in blob)
        self.assertEqual(missing, [],
                         "assigned log event(s) %s are queried by no Loki panel. Log events are "
                         "reached by event_name, not as metrics." % missing)

    def test_the_extraction_is_not_vacuous(self):
        # Guards the guard: if panel_queries() ever stops finding query text the two
        # tests above pass by finding nothing, forever.
        self.assertGreater(len(self.queries), 200,
                           "only %d queries extracted from the built dashboard; the extraction "
                           "is broken and every coverage assertion above is vacuous"
                           % len(self.queries))
        self.assertNotIn("tailscale_this_metric_does_not_exist", "\n".join(self.queries))

    def test_new_rows_exist(self):
        rows = find_rows(self.doc)
        missing = [r for r in NEW_ROWS if r not in rows]
        self.assertEqual(missing, [],
                         "row(s) %s were removed. They carry the only coverage for the OAuth "
                         "app, webhook-endpoint, ACL-validation and audit-pipeline signals; "
                         "removing one silently flips those back to raw_only." % missing)

    def test_new_rows_are_not_empty(self):
        for title in NEW_ROWS:
            with self.subTest(row=title):
                self.assertTrue(row_panels(self.doc, title),
                                "row %r lays out no panels" % title)

    def test_policy_queries_honor_tailnet_and_provider_selectors(self):
        offenders = []
        for row_title in POLICY_ROWS:
            for p in row_panels(self.doc, row_title):
                title = p["spec"]["title"]
                for datasource, query in panel_queries(p):
                    if "prometheus" in datasource and "tailscale_" in query:
                        if ('tailscale_tailnet=~"$tailnet"' not in query or
                                'tailscale2otel_provider=~"$provider"' not in query):
                            offenders.append((row_title, title, datasource, query))
                    if "loki" in datasource and 'service_name="tailscale2otel"' in query:
                        if ('tailscale_tailnet=~"$tailnet"' not in query or
                                'tailscale2otel_provider=~"$provider"' not in query):
                            offenders.append((row_title, title, datasource, query))
        self.assertEqual(offenders, [],
                         "Policy & Config queries bypass dashboard selectors: %s" % offenders)


# ---------------------------------------------------------------------------
# PII / secret containment
# ---------------------------------------------------------------------------
#
# Whole-word tokens, so `tailscale_oauth_app_redirect_uris_ratio` (a legitimate
# COUNT) and `..._age_seconds_bucket` do not trip the scan — the following `_`
# is a word character, so \b does not match after `redirect_uris` or before
# `bucket`.

URL_BEARING_TOKENS = [
    "url", "urls", "uri", "endpoint_url", "webhook_url", "redirect_uri",
    "redirect_uris", "destination_url", "bucket", "bucket_name", "object_key",
    "secret", "client_secret", "sink_url", "hec_token",
]
_URL_RE = re.compile(r"\b(?:%s)\b" % "|".join(URL_BEARING_TOKENS), re.IGNORECASE)

# The subset that can only ever mean a destination or a credential. "bucket" and
# "uri" are ambiguous dashboard-wide — a histogram bucket and an expiry bucket
# are both legitimate — so the full list above is applied only to the rows this
# work owns, and this narrower one to every panel in the document.
_LEAK_RE = re.compile(
    r"\b(?:endpoint_url|webhook_url|redirect_uri|client_secret|object_key"
    r"|sink_url|destination_url|hec_token)\b", re.IGNORECASE)


def url_bearing_hits(panel, pattern=_URL_RE):
    """Every URL/secret-shaped token in a panel's operator-visible text.

    Scans the title, description, query text, and the transformation rename /
    exclude maps — a rename map is where a raw label reaches the operator as a
    column heading.
    """
    spec = panel["spec"]
    texts = [spec.get("title", ""), spec.get("description", "")]
    texts.extend(t for (_, t) in panel_queries(panel))
    for tr in spec["data"]["spec"].get("transformations") or []:
        opts = tr.get("spec", {}).get("options", {})
        texts.extend(str(k) for k in (opts.get("renameByName") or {}))
        texts.extend(str(v) for v in (opts.get("renameByName") or {}).values())
        texts.extend(str(k) for k in (opts.get("excludeByName") or {}))
    hits = []
    for t in texts:
        hits.extend(pattern.findall(t))
    return hits


class NoURLBearingFieldsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = dashboard.build_family()

    def test_no_panel_in_the_new_rows_exposes_a_url_bearing_field(self):
        offenders = {}
        for title in NEW_ROWS:
            for panel in row_panels(self.doc, title):
                hits = url_bearing_hits(panel)
                if hits:
                    offenders[panel["spec"]["title"]] = sorted(set(hits))
        self.assertEqual(offenders, {},
                         "panel(s) reference URL/secret-bearing fields: %s. Redirect URIs, "
                         "webhook endpoint URLs, bucket/object destinations and secrets are "
                         "deliberately never emitted — those metrics are counts and classes. A "
                         "panel naming one renders empty AND tells the operator a value exists."
                         % offenders)

    def test_no_panel_anywhere_names_a_destination_or_a_credential(self):
        offenders = {}
        for panel in all_panels(self.doc):
            hits = url_bearing_hits(panel, _LEAK_RE)
            if hits:
                offenders[panel["spec"]["title"]] = sorted(set(hits))
        self.assertEqual(offenders, {},
                         "panel(s) %s name a destination or credential field. None of these are "
                         "emitted by the exporter at all." % offenders)

    def test_the_url_scan_can_fail(self):
        # Guards the guard. Without this, a broken regex makes the test above
        # pass on a dashboard that leaks every endpoint URL.
        leaky = {"spec": {
            "title": "Webhook endpoints",
            "description": "Shows each endpoint's webhook_url.",
            "data": {"spec": {"queries": [], "transformations": [
                {"spec": {"options": {"renameByName": {"tailscale_webhook_endpoint_url": "URL"}}}}]}}}}
        self.assertEqual(sorted(set(t.lower() for t in url_bearing_hits(leaky))),
                         ["url", "webhook_url"])

    def test_the_scan_tolerates_the_legitimate_count_metrics(self):
        # The counterpart risk: a scan so blunt it forbids the very metrics #403
        # asks us to chart would be "fixed" by deleting the panels.
        benign = {"spec": {
            "title": "OAuth app scopes",
            "description": "Counts only.",
            "data": {"spec": {"queries": [
                {"spec": {"query": {"datasource": {"name": "${ds_prometheus}"},
                                    "spec": {"expr": "tailscale_oauth_app_redirect_uris_ratio "
                                                     "+ tailscale_oauth_apps_age_seconds_bucket"}}}}],
                          "transformations": []}}}}
        self.assertEqual(url_bearing_hits(benign), [])


# ---------------------------------------------------------------------------
# #393 — the four-state distinction and log/metric time alignment
# ---------------------------------------------------------------------------

class AuditPipelineStateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = dashboard.build_family()
        cls.panels = row_panels(cls.doc, "Audit pipeline state")

    def test_the_row_renders_all_four_states(self):
        blob = "\n".join(p["spec"]["title"] + "\n" + p["spec"]["description"]
                         for p in self.panels)
        missing = [s for s in FOUR_STATES if s not in blob]
        self.assertEqual(missing, [],
                         "the Audit pipeline state row never names %s. #393's core ask is that "
                         "an operator can tell these apart; a panel set that does not name them "
                         "leaves the reader to guess which one they are looking at." % missing)

    def test_the_row_states_what_it_cannot_separate(self):
        # #385: presence cannot distinguish disabled from unsupported from
        # never-deployed. The row must SAY so rather than imply a distinction
        # the signals do not support.
        blob = "\n".join(p["spec"]["description"] for p in self.panels)
        self.assertIn("cannot", blob.lower(),
                      "no panel in the Audit pipeline state row admits a limit. Presence alone "
                      "cannot separate disabled from unsupported from never-deployed, and text "
                      "that implies otherwise is a guess rendered as fact.")

    def test_the_row_pairs_a_metric_panel_with_a_loki_panel(self):
        kinds = {ds for p in self.panels for (ds, _) in panel_queries(p)}
        self.assertIn("${ds_prometheus}", kinds)
        self.assertIn("${ds_loki}", kinds,
                      "the row has no Loki panel, so 'metrics say events arrived but Loki has "
                      "none' — the absent-Loki-data state — cannot be read off it")

    def test_the_range_paired_panels_share_one_window(self):
        # A Loki panel and a metric panel disagreeing on window is the classic
        # false 'the data is missing' report. The two counting panels must both
        # be $__range and nothing else.
        paired = [p for p in self.panels if "(this range)" in p["spec"]["title"]]
        self.assertEqual(len(paired), 2,
                         "expected exactly one metric and one Loki '(this range)' counter, got "
                         "%d" % len(paired))
        self.assertEqual({ds for p in paired for (ds, _) in panel_queries(p)},
                         {"${ds_prometheus}", "${ds_loki}"})
        for p in paired:
            for (_, expr) in panel_queries(p):
                windows = set(re.findall(r"\[([^\]]+)\]", expr))
                self.assertEqual(windows, {"$__range"},
                                 "panel %r uses window(s) %s; both '(this range)' panels must "
                                 "read exactly $__range or they answer different questions"
                                 % (p["spec"]["title"], sorted(windows)))


# ---------------------------------------------------------------------------
# empty states (#385) and tailnet/provider filtering (#393)
# ---------------------------------------------------------------------------

CONFIG_KEY_RE = re.compile(r"\b(collectors|cardinality|logs|tailscale)\.[a-z_.]+")

# Panels whose noValue text must name the config key and prerequisite that has
# to be true for data to appear — never a cause, which presence cannot know.
EMPTY_STATE_PANELS = [
    "OAuth applications",
    "ACL policy validated",
    "Desired-event coverage",
    "Per-endpoint subscriptions",
    "Audit poll collector",
]


class EmptyStateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.by_title = {p["spec"]["title"]: p for p in all_panels(dashboard.build_family())}

    def test_empty_states_name_a_config_key(self):
        for title in EMPTY_STATE_PANELS:
            with self.subTest(panel=title):
                panel = self.by_title.get(title)
                self.assertIsNotNone(panel, "panel %r is gone" % title)
                nv = panel["spec"]["vizConfig"]["spec"]["fieldConfig"]["defaults"].get("noValue", "")
                self.assertTrue(nv, "panel %r has no noValue empty state" % title)
                self.assertRegex(nv, CONFIG_KEY_RE,
                                 "panel %r's empty state names no config key. An operator has to "
                                 "be told what to turn on; presence cannot tell them WHY it is "
                                 "off (#385)." % title)

    def test_empty_states_do_not_assert_a_cause(self):
        banned = re.compile(r"\b(because|due to|caused by|is disabled\b|has been disabled)\b",
                            re.IGNORECASE)
        for title in EMPTY_STATE_PANELS:
            with self.subTest(panel=title):
                nv = (self.by_title[title]["spec"]["vizConfig"]["spec"]["fieldConfig"]
                      ["defaults"].get("noValue", ""))
                self.assertNotRegex(nv, banned,
                                    "panel %r's empty state asserts a cause. Absence cannot "
                                    "distinguish disabled from unsupported from never-deployed."
                                    % title)


class TailnetFilterTest(unittest.TestCase):
    """#393 asks for tailnet/provider filters on the audit/integration surfaces."""

    @classmethod
    def setUpClass(cls):
        cls.doc = dashboard.build_family()

    def test_new_prometheus_panels_filter_by_tailnet_and_provider(self):
        unfiltered = []
        for title in NEW_ROWS:
            for panel in row_panels(self.doc, title):
                for (ds, expr) in panel_queries(panel):
                    if ds != "${ds_prometheus}" or not expr:
                        continue
                    if '$tailnet' not in expr or '$provider' not in expr:
                        unfiltered.append(panel["spec"]["title"])
        self.assertEqual(sorted(set(unfiltered)), [],
                         "panel(s) %s in the new rows ignore the Tailnet/Provider variables, so "
                         "on a multi-tailnet deployment they silently blend every tailnet's "
                         "series together" % sorted(set(unfiltered)))

    def test_new_loki_panels_filter_by_tailnet(self):
        unfiltered = []
        for title in NEW_ROWS:
            for panel in row_panels(self.doc, title):
                for (ds, expr) in panel_queries(panel):
                    if ds == "${ds_loki}" and "$tailnet" not in expr:
                        unfiltered.append(panel["spec"]["title"])
        self.assertEqual(sorted(set(unfiltered)), [])


if __name__ == "__main__":
    unittest.main()
