---
id: TSO-0078
title: Document restart-required vs hot-reloadable config keys
status: Done
assignee: []
created_date: '2026-08-30 09:35'
updated_date: '2026-08-30 12:58'
labels: []
milestone: m-1
dependencies: []
priority: medium
ordinal: 81000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Substantial engineering avoids restarts (credential/TLS reload, checkpoint durability) yet no doc states which keys hot-reload. Generate a table in docs/configuration.md from struct tags (or an equivalent single source of truth) marking each key restart-required vs hot-reloadable, gated against drift like the other generated docs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every config key carries a reload classification in generated docs
- [x] #2 A drift gate fails when a new key lacks a classification
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
## Frozen seam (do not renegotiate)

Struct tag `reload:"..."` on every leaf field of `Config` and its nested structs. Closed enum, TWO values today:

- `reload:"restart"` — changing this value takes effect only after a process restart. The default for the overwhelming majority.
- `reload:"file_content"` — the value is a filesystem PATH whose CONTENT is re-read while the process runs. Changing the PATH still needs a restart; replacing the FILE does not.

Do NOT add a `hot` value speculatively. Nothing in the codebase re-reads a config value live, so a `hot` member would have zero instances and its gate branch would never execute — an unexercised classification that the next reader would trust. Add it when a feature earns it.

## New file: internal/config/reload.go

- `type ReloadClass string` with the two constants.
- `ReloadClassifications() []ReloadRow` — reflection walk over `Config`, emitting `{Key string, Class ReloadClass}` using the SAME dotted lower_snake_case key convention as `pathFields` (internal/config/paths.go:38-42) and `envReferenceRows`. Read the convention from those two before writing a third one.

## Gates (both must be negative-tested — doc-0002 recurring defect)

1. `TestReloadClassificationCoversEveryKey` — every leaf key produced by the walk carries a valid `reload` tag. A field with no tag, or an unknown value, fails and NAMES the field. Negative-test: delete one tag, watch it go red naming that field, restore.
2. `TestFileContentReloadKeysArePathFields` — every key tagged `file_content` also appears in `Config.pathFields()`. This is the cross-check that makes the classification evidence-based rather than asserted: a field cannot claim its content is watched unless the loader already treats it as a path. Negative-test: tag a non-path field `file_content`, watch it fail, restore.

## Assigning the values — derive, do not guess

Default EVERY field to `restart`, then promote to `file_content` ONLY the paths a real reloader reads. The complete candidate set, derived from the call sites:

- `certreload.New` (4 sites): `admin.tls.{cert_file,key_file}`, `prometheus.tls.{cert_file,key_file}`, `streaming.tls.{cert_file,key_file}`, `webhook.tls.{cert_file,key_file}`.
- `credreload.Sources` (internal/app/credreload.go:88 `newCredReloaders`): the OTLP bearer-token / header-value / CA / client-keypair files it is actually constructed with, INCLUDING the per-signal `otlp.{metrics,logs,traces}.tls.*` overrides if those are wired. Read `newCredReloaders` and enumerate what it passes; do not infer from the config tree.
- geoip: the `.mmdb` paths hot-swapped on `enrichment.geoip.reload_interval`.

Anything not reachable from one of those three stays `restart`. If a path LOOKS watched but the wiring does not pass it to a reloader, it is `restart` and that is a finding worth a line in the notes.

## Output

- `envRow` (internal/config/envref.go:22) gains a `Reload` field, populated by joining on `Key`; the rendered table gains a `Reload` column; `just gen-envref` regenerates `docs/env-vars.md`; `TestEnvReferenceDocInSync` is the existing drift gate and needs no new lane.
- `docs/configuration.md` gains a short hand-written section defining the two values, stating the path-vs-content distinction explicitly, and linking the generated table.

## Work

1. Tests first (both gates, both negative-tested) — they fail immediately because no field is tagged.
2. Tag every field; promote the derived `file_content` set.
3. Wire the column, regenerate, write the prose.
4. Gate: `just check`.

## Ownership note

This touches EVERY struct in `internal/config/config.go` — the widest possible diff on doc-0002s single-owner file. It cannot run concurrently with any other config-key lane and should be scheduled LAST among them, after TSO-0031/0051/0053/0054/0066 have landed their fields, so it tags a settled struct instead of racing new ones.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9)

## The binary in the task title is wrong, and that is the most valuable finding

"restart-required vs hot-reloadable" does not describe this codebase. Nothing re-reads a config VALUE while running. What is live is the CONTENT OF FILES AT CONFIGURED PATHS:

- `internal/certreload` (package doc, certreload.go:1-14) serves TLS keypairs that rotate under a running listener, for FOUR listeners: admin (internal/app/admin.go:329), prometheus (internal/app/metrics.go:85), the streaming receiver (internal/stream/stream.go:620) and the webhook receiver (internal/webhook/webhook.go:626).
- `internal/credreload` (Sources, credreload.go:41-65) polls outbound credential and TLS files: a bearer-token file, arbitrary header-value files, a CA bundle, and a client keypair. Started at internal/app/app.go:746 for the life of the process.
- `enrichment.geoip.reload_interval` (internal/config/config.go:886) re-stats the .mmdb paths and hot-swaps a changed database; `geoip.Updater` runs at internal/app/app.go:751-755.

In every one of those the KEY is still restart-required — pointing `admin.tls.cert_file` at a different path needs a restart — while the FILE it names is re-read live. A two-value table would have to call those keys "hot", which is false and is precisely the misreading that would send an operator to rewrite a path expecting it to take effect.

## Recommendation: STRUCT TAGS, not a sidecar manifest — freeze this

The task left the fork open. Take tags, for three reasons grounded in this repo:

1. **A sidecar can be edited to silence the gate; a tag cannot.** `internal/catalog/signal_dispositions.json` is the cautionary precedent — #526 had to DELETE three escape hatches (`raw_only`, `omitted`, `pending_panel`) that had accumulated 35 rows of exactly that behaviour. A new keys missing classification must be fixable only by classifying the field.
2. **The reflection walk already exists.** `internal/config/schema.go` reflects over `Config` field by field to generate config.schema.json, and `TestPathFieldsCoversEveryPathBearingField` (internal/config/paths_test.go:169) already derives a key set from the struct by reflection and diffs it against a hand-maintained registry. There is no new machinery to invent.
3. **A tag survives a rename or a move between structs.** A manifest keyed by dotted path silently goes stale on both.

## Do NOT add a twelfth generated-artifact family

`docs/env-vars.md` is already generated with exactly one row per leaf key, from `config.example.yaml`, by `envReferenceRows` (internal/config/envref.go:39) under `TestEnvReferenceDocInSync` and `just gen-envref`. Add a COLUMN there instead of a new table with a new generator, a new `gen-*` recipe, a new `fail-on-diff` lane and a new row in the AGENTS.md artifact table. The join is safe because `TestExampleConfigCoversEveryKey` (internal/config/completeness_test.go:74) already proves the example-file key set and the `Default()` key set are identical, so every envref row has exactly one struct field to read the tag from.

`docs/configuration.md` then gets a short HAND-WRITTEN section explaining the three values and pointing at the generated table — no generator, no drift gate needed for prose that names no counts.

(Housekeeping the lane should flag rather than fix: AGENTS.md says eleven generated families and doc-0002 says eight. Both are stale against `just --list`. Not this tasks job, but worth a needs-triage note.)

Wave 1 Lane D1 started by root after C2 and C3 completed. Harness Codex; Appendix A route EXECUTION to Luna/max. Lane owns all config leaf reload classification and env-reference/prose artifacts; root retains integrated gate, review, commit, push, discovered-task creation, and tracker finalization.

Negative guard evidence captured verbatim: removing log_level reload tag made TestReloadClassificationCoversEveryKey fail naming log_level; changing it to file_content made TestFileContentReloadKeysArePathFields fail naming log_level. Both were restored and passed together. Generated env reference, just check, and CI 33312668201 passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed cd3bfa0: every config leaf has restart or file_content classification, with generated env-reference output and two negative-tested drift guards. Verified by race tests, full gate, and exact-head CI 33312668201.
<!-- SECTION:FINAL_SUMMARY:END -->
