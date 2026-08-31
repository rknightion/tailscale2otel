#!/usr/bin/env python3
"""Structural gates over the two-dashboard seam (#526).

These assert the CONTRACT the parallel tab lanes code against — the shape of a
DashboardSpec, of an AutoGrid row, of a grouping-level variable list, of the
adhoc-filter and cross-link helpers — not the content of any one tab. A lane
that adds panels can fail the budget gates here; a lane that renames a seam
field must fail the shape gates here.

Kept separate from test_nested_domains.py on purpose: that file asserts the
domain/leaf ARRANGEMENT of the product dashboard, this one asserts the seams
both dashboards are built out of.
"""

import importlib.util
from pathlib import Path
import sys
import unittest


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


GEN = Path(__file__).parent
build = load_module("ts2_build_specs", GEN / "build.py")
builder = build.builder
# build.py does `from variables import build_variables`, so the module object is
# in sys.modules by now but is not an attribute of build.
variables = sys.modules["variables"]


class AutoGridSeam(unittest.TestCase):
    """builder.autogrid_row() — decision 6 of #526: rows that are N same-size
    panels reflow responsively; rows with deliberate asymmetry keep row()."""

    def test_autogrid_row_states_no_panel_sizes(self):
        # The whole point: sizes are NOT stated, so the layout reflows. A helper
        # that still took (name, w, h) triples would be row() with extra steps.
        r = builder.autogrid_row("Uniform", ["panel-1", "panel-2", "panel-3"])
        lay = r["spec"]["layout"]
        self.assertEqual(lay["kind"], "AutoGridLayout")
        self.assertEqual(len(lay["spec"]["items"]), 3)
        for item in lay["spec"]["items"]:
            self.assertEqual(item["kind"], "AutoGridLayoutItem")
            self.assertNotIn("width", item["spec"])
            self.assertNotIn("height", item["spec"])

    def test_autogrid_row_references_its_panels_by_element_name(self):
        r = builder.autogrid_row("Uniform", ["panel-7"])
        el = r["spec"]["layout"]["spec"]["items"][0]["spec"]["element"]
        self.assertEqual(el, {"kind": "ElementReference", "name": "panel-7"})

    def test_autogrid_row_still_carries_conditional_rendering(self):
        # A feature-gated uniform row must gate identically to a fixed-grid one,
        # or converting a row to AutoGrid would silently un-gate it.
        r = builder.autogrid_row("Uniform", ["panel-1"], present="has_x",
                                 hide_when=["pii_node"])
        items = r["spec"]["conditionalRendering"]["spec"]["items"]
        self.assertEqual(items[0]["spec"]["variable"], "has_x")
        self.assertEqual(items[0]["spec"]["operator"], "matches")
        self.assertEqual(items[1]["spec"]["variable"], "pii_node")
        self.assertEqual(items[1]["spec"]["operator"], "notMatches")

    def test_max_columns_is_absent_unless_asked_for(self):
        self.assertNotIn("maxColumnCount",
                         builder.autogrid_row("U", ["panel-1"])["spec"]["layout"]["spec"])
        self.assertEqual(
            builder.autogrid_row("U", ["panel-1"], max_columns=2)["spec"]["layout"]["spec"]["maxColumnCount"],
            2)


class GroupingLevelVariables(unittest.TestCase):
    """The `variables` field on RowsLayoutRowSpec/TabsLayoutTabSpec is what makes
    #526 possible at all. It exists ONLY on v2beta1 and v2 — v2alpha1 has no such
    field, so a silent apiVersion downgrade would drop every scoped variable
    without erroring."""

    VAR = [{"kind": "QueryVariable", "spec": {"name": "os_type"}}]

    def test_row_tab_and_domain_all_carry_variables(self):
        for node in (builder.row("R", [], variables=self.VAR),
                     builder.tab("T", [], variables=self.VAR),
                     builder.tab_group("D", [], variables=self.VAR)):
            self.assertEqual(node["spec"]["variables"], self.VAR, node["kind"])

    def test_variables_key_is_absent_when_none_are_scoped_there(self):
        # Absent, not []. Emitting an empty list on every row would make an
        # accidental downgrade to an apiVersion without the field indetectable.
        for node in (builder.row("R", []), builder.tab("T", []),
                     builder.tab_group("D", [])):
            self.assertNotIn("variables", node["spec"], node["kind"])


class AdhocAndLinkSeams(unittest.TestCase):

    def test_adhoc_puts_datasource_and_group_at_the_kind_level(self):
        # AdhocVariableKind is NOT shaped like QueryVariable: `datasource` and a
        # required `group` are siblings of `spec`, not nested inside it. Getting
        # this wrong produces a 422 whose CUE disjunction error names layout.kind
        # and never mentions the variable — the real cause is masked (#526).
        v = variables.adhoc_var("device_filters", "Device filters",
                                [("__name__", "=~", "tailscale_device_.*")])
        self.assertEqual(v["kind"], "AdhocVariable")
        self.assertIn("datasource", v)
        self.assertIn("group", v)
        self.assertNotIn("datasource", v["spec"])
        self.assertNotIn("group", v["spec"])

    def test_adhoc_base_filters_are_expanded_to_key_operator_value(self):
        v = variables.adhoc_var("device_filters", "Device filters",
                                [("__name__", "=~", "tailscale_device_.*")])
        self.assertEqual(v["spec"]["baseFilters"],
                         [{"key": "__name__", "operator": "=~",
                           "value": "tailscale_device_.*"}])
        self.assertEqual(v["spec"]["filters"], [])

    def test_cross_link_propagates_time_and_the_named_variables(self):
        link = builder.dashboard_link("Exporter health", "tailscale2otel-health",
                                      ("tailnet", "ds_prometheus"))
        self.assertEqual(link["placement"], "inControlsMenu")
        self.assertTrue(link["keepTime"])
        self.assertIn("${__url_time_range}", link["url"])
        self.assertIn("${tailnet:queryparam}", link["url"])
        self.assertIn("${ds_prometheus:queryparam}", link["url"])
        self.assertTrue(link["url"].startswith("/d/tailscale2otel-health?"), link["url"])


class VariableBudgets(unittest.TestCase):
    """#526's variable budgets, and the reason the scoped-variable seam exists.

    82 dashboard-level variables — 16 visible controls and 66 hidden presence
    sentinels — is what the single dashboard shipped. Every hidden sentinel is a
    Prometheus query on every dashboard load, and 16 controls is a control bar an
    operator reads as noise. The fix is not deleting them but MOVING them to the
    tab that consumes them, which is why these gates count dashboard-level
    variables specifically rather than variables in total.
    """

    MAX_DASHBOARD_VARS = 15
    # TSO-0086 deliberately adds one fleet-wide Instance / environment ad-hoc
    # filter. It earns one control on both dashboards because it scopes every
    # Prometheus panel when several exporter installs share a stack.
    MAX_VISIBLE = 7

    @classmethod
    def setUpClass(cls):
        cls.docs = {spec.uid: build.build(spec) for spec in build.dashboards.ALL}

    @staticmethod
    def visible(doc):
        return [v["spec"]["name"] for v in doc["spec"]["variables"]
                if v["spec"].get("hide", "dontHide") == "dontHide"]

    def test_dashboard_level_variable_count(self):
        for uid, doc in self.docs.items():
            with self.subTest(dashboard=uid):
                names = [v["spec"]["name"] for v in doc["spec"]["variables"]]
                self.assertLessEqual(len(names), self.MAX_DASHBOARD_VARS, names)

    def test_visible_control_count(self):
        for uid, doc in self.docs.items():
            with self.subTest(dashboard=uid):
                self.assertLessEqual(len(self.visible(doc)), self.MAX_VISIBLE,
                                     self.visible(doc))

    def test_multi_install_filter_is_dashboard_wide(self):
        for uid, doc in self.docs.items():
            with self.subTest(dashboard=uid):
                variables = {v["spec"]["name"]: v for v in doc["spec"]["variables"]}
                variable = variables["deployment_filters"]
                self.assertEqual(variable["kind"], "AdhocVariable")
                self.assertEqual(variable["spec"]["label"], "Instance / environment")

    def test_a_tab_scoped_variable_is_not_also_declared_on_the_dashboard(self):
        # Both would resolve, and the tab's copy would win on that tab only — so
        # the same name would mean two different things depending on where you
        # stood. Cheap to assert, very hard to notice on screen.
        for uid, doc in self.docs.items():
            at_dashboard = {v["spec"]["name"] for v in doc["spec"]["variables"]}

            def walk(tabs_layout, seen):
                for t in tabs_layout["spec"]["tabs"]:
                    for v in t["spec"].get("variables", []):
                        seen.append((t["spec"]["title"], v["spec"]["name"]))
                    inner = t["spec"]["layout"]
                    if inner.get("kind") == "TabsLayout":
                        walk(inner, seen)
                    else:
                        for r in inner.get("spec", {}).get("rows", []):
                            for v in r["spec"].get("variables", []):
                                seen.append((r["spec"]["title"], v["spec"]["name"]))
                return seen

            for where, name in walk(doc["spec"]["layout"], []):
                with self.subTest(dashboard=uid, where=where, variable=name):
                    self.assertNotIn(name, at_dashboard)


if __name__ == "__main__":
    unittest.main()
