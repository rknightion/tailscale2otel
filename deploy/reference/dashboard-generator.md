# Dashboard and alert generators

Read before editing anything under `deploy/grafana/gen/` or `deploy/alerts/gen/`, renaming a panel,
or adding an alert `panel=` link. How the artifacts reach the live stack is `reference/grafana-delivery.md`
at the repo root.

`grafana/gen/` is modular: `dashboards.py` (what dashboards exist and what is on each), `builder.py`
(primitives plus the scoped sentinel registry), `variables.py`, `maps.py`, `tabs/*.py` (one module
per tab), `build.py` (orchestrator). `just gen-dashboards` regenerates the dashboards, the
Grafana-managed rules and `docs/alert-profiles.md`; `just gen-promrules` regenerates the shipped
Prometheus rules. `alerts/gen/build_rules.py` builds the rule manifests.

- **The dashboard family is built in ONE process on purpose.** The signal-coverage gate in
  `internal/catalog` takes the union across every artifact; a gate that could see only one file
  reports a metric as missing the moment its panel moves to the other, which makes splitting content
  structurally impossible. For the same reason `--flat`/`--tab` previews require `--dashboard`: a
  tab title such as "Overview" exists on both.
- **The dashboard `apiVersion` is required - do not "simplify" it to an alpha version.** The
  `variables` field on `RowsLayoutRowSpec`/`TabsLayoutTabSpec`, which is what lets a presence
  sentinel live on the tab that consumes it instead of on the dashboard, exists only in `v2beta1`
  and `v2`. `v2alpha1` has no such field, so a downgrade drops every scoped variable silently
  rather than erroring.
- **`AdhocVariableKind` puts `datasource` and a required `group` at the KIND level**, as siblings of
  `spec`, unlike `QueryVariable`. Getting it wrong produces a 422 whose CUE disjunction error names
  `layout.kind` and never mentions the variable - bisect from a known-valid fragment.
  `gen/variables.py:adhoc_var()` owns the correct shape.
- **A panel TITLE is the alert-link key, across the whole family.** `alerts/gen/build_rules.py`
  resolves each alert's `__dashboardUid__`/`__panelId__` by title and raises when a title matches
  zero panels or more than one, over every artifact. Renaming or merging a panel breaks any alert
  linking it (loudly, which is the point), and a title used on both dashboards fails the build
  outright. Where a product dashboard keeps a glance copy of a panel the health dashboard owns, the
  copy carries a **`(summary)`** suffix and the unsuffixed title stays with the canonical detailed
  panel. That is the convention, not decoration.
- **A `panel=` that resolves is not a panel that shows the condition.** Three shipped alerts once
  pointed at panels charting a different metric, including one aimed at the `last_sync` panel that
  is the signal the alert exists because it cannot detect. All three passed the link gate. When
  adding `panel=`, check it charts the metric the expression fires on.
