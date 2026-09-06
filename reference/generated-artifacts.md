# Generated artifacts and signal coverage

Read before regenerating a committed artifact, changing a `gen-*` recipe, bumping a Helm generator
pin, or touching `internal/catalog/signal_dispositions.json`.

## Regeneration

Every committed generated artifact has a `gen-<family>` recipe and a CI fail-on-diff gate. `just gen`
reproduces the set; the `gen` group in `just --list` is the only place the set is written down
(`scripts/regen-generated.sh` holds a `regen_<target>` function per family, never a target list).
`just gen <family>...` and `just gen-<family>` are the same thing - test failure messages and CI job
names use the spaced spelling.

- The two Helm generators are version-pinned in the justfile and installed by `just gen-tools`. A
  different version produces different bytes, landing as unrelated churn or a red fail-on-diff. CI
  pins the *actions*, and an action version is not its tool version, so when Renovate bumps
  `losisin/helm-docs-github-action` or `losisin/helm-values-schema-json-action` in
  `.github/workflows/helm.yml`, re-derive the justfile pins. The generation script verifies the
  installed version against the pin and SKIPs loudly rather than writing a wrong file.
- A plain `go install .../helm-docs@<ver>` yields a binary that reports **no version**, because
  helm-docs reads its version from a build-time ldflag rather than Go build info. The chart README
  template guards its version footer with `{{ if .HelmDocsVersion }}`, so that binary silently drops
  the footer - a plausible-but-wrong README. `just gen-tools` passes the ldflag; never hand-install
  either of these two.
- Each `tools/*` is a separate Go module. `go run ./tools/metricscatalog` from the repo root fails
  ("main module does not contain package") despite the tool's own help text. Use
  `go run -C tools/metricscatalog .` with an absolute `-file`, or build first - the default
  `docs/metrics.md` path is CWD-relative.
- Never hand-edit between `<!-- BEGIN GENERATED -->` and `<!-- END GENERATED -->` in
  `docs/metrics.md`. Prose outside the markers is safe.
- `config.schema.json` at the repo root is the schema for `config.yaml`. The chart's
  `values.schema.json` is a different artifact with a different generator; do not conflate them.
- `.githooks/pre-commit` regenerates only the artifacts your *staged* changes invalidate and
  re-stages them; it is a silent no-op otherwise. It shells out to `just gen-<family>` recipes and
  never to a script, because a hook calling a generator directly is a second, divergent definition
  of how an artifact is built. A missing tool is a loud SKIP, never a block. Bypass one run with
  `git commit --no-verify`.

## Signal dispositions

`internal/catalog/signal_dispositions.json` is the one generated-adjacent file you do NOT blindly
regenerate. `just gen-coverage` rebuilds only the *page* from the manifest. The manifest itself is
updated by hand-running `go test ./internal/catalog -run TestSignalDispositionsInSync -update`,
which is deliberately non-silencing: it derives `visualized`/`alertable`/`recorded`/
`drives_a_variable` from the actual dashboard and rule artifacts and prunes dead rows, but leaves a
new signal's disposition EMPTY, and an empty disposition always fails the gate. There is no value a
human may assign. A signal on no surface is settled by giving it a panel, not by editing the
manifest. Regenerating after changing dashboards or rules is correct; regenerating to turn a red
gate green will not work.

- `raw_only`, `omitted` and `pending_panel` were removed. Do not reintroduce any of them. The only
  exemptions are the structural classes in `catalog.StructuralExemptions()`, each individually
  justified.
- `visualized` means a PANEL, and `drives_a_variable` is a separate value on purpose: a presence
  sentinel's `label_values()` call is as real a reference as a panel query and shows nobody
  anything. While both fed one value, a signal could clear the coverage bar while being invisible.
  Do not fold the two back together.
- `internal/catalog/dashboardrefs_test.go` checks every metric name the dashboards and rule
  manifests QUERY against the catalog's normalized Prometheus spellings. Nothing else connects those
  artifacts to the catalog, so a renamed metric leaves a panel silently empty - it still loads, it
  just shows "No data". It has to subtract the catalog's LABEL and log-attribute names too: labels
  share the `tailscale_` prefix, so a text scan cannot tell a metric from a label by shape.
