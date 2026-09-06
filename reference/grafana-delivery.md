# Grafana delivery

Read before pushing, deleting or debugging anything on the live Grafana stack, or before editing an
artifact under `deploy/grafana` or `deploy/alerts`.

Nothing under `deploy/grafana` or `deploy/alerts` is hand-maintained. The project is Grafana v2 /
Grafana 13+ only and will never ship a Classic export: v1 cannot express `conditionalRendering`, so
every feature-gated tab would render permanently empty rather than hiding. Alert rules are
Grafana-managed `rules.alerting.grafana.app/v0alpha1` manifests, one JSON per rule.

## Rules go via gcx

- **Pushing alert rules to Rob's Grafana stack is pre-authorized - do not ask.** That covers
  `gcx resources push -p deploy/alerts/grafana-managed` and deleting a rule the repo no longer
  ships. It does NOT extend to mutating the tailnet itself.
- `gcx resources push` is ADDITIVE: it creates and updates but never deletes, so a rule removed from
  the repo keeps evaluating forever until deleted by hand. `just verify-deploy` is read-only and
  finds orphans (exit 0 in sync, 1 drift, 2 unreachable).
- Two silent format traps in the manifests: `noDataState` and `execErrState` both spell the OK state
  `"Ok"` (`"OK"` is rejected outright), and durations are Go-style strings (`"30m0s"`, not `"5m"`).
- **`gcx resources validate` does not validate the spec** - it says so - and neither
  `validate_manifests.py` nor promtool exercises the real schema. Only a real `gcx resources push`
  proves a rule is deployable, and pushing is pre-authorized, so push and read the result rather
  than trusting a green offline run. A validator written from the same assumption as the generator
  cannot catch that assumption being wrong.
- `promtool test rules` executes the shipped Prometheus rules against the fixtures in
  `deploy/alerts/tests/`. Parsing proves an expression is well-formed; only execution proves it
  fires when it should.

## Dashboards go via GitSync, never gcx

- **Do NOT push DASHBOARDS with `gcx`.** `.github/workflows/grafana-sync.yml` commits
  `deploy/grafana/*.json` into `m7kni/gc-gitsync-m7kni`, a Grafana GitSync source that Grafana also
  writes UI saves back into. An API push is an out-of-band edit and leaves the repo and the stack
  disagreeing with no way to tell which is right.
- Deleting a dashboard through the API is undone by the next sync, which re-creates it from whatever
  file is still in the GitSync repo. Retire one by deleting it from `deploy/grafana/`; the workflow
  prunes the far side.
- A green `grafana-sync` run is not proof it published anything. Verify the far side by listing
  `repos/m7kni/gc-gitsync-m7kni/git/trees/main`, not by the workflow's conclusion.
