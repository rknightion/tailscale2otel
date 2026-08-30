---
id: TSO-0044
title: Policy file snapshots to Loki (full ACL/grants body as log records)
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-2
dependencies: []
priority: medium
ordinal: 47000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Emit the full HuJSON policy file as a log record so a Grafana dashboard panel can display the current ACL and its history from Loki. Design settled (owner, 2026-08-30): RAW body in the log record (not base64 - grep-able in Loki), emitted on ETag/revision change plus a daily heartbeat snapshot, OFF by default because the policy contains user emails and group members (pii_filter-style opt-in). The acl collector already polls getPolicyFile with ETag revision tracking - hook there. Attribute-mark the record (snapshot marker, etag, size) so dashboards can query latest-snapshot. Add a Policy tab panel rendering the latest snapshot.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 On policy revision change and on a daily heartbeat, an opt-in log record carries the full raw policy body with etag/size attributes
- [x] #2 Off by default; enabling it is an explicit config opt-in documented with the PII implications
- [x] #3 A generated dashboard panel displays the latest policy snapshot
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
## Frozen seam - build on the shared snapshot emitter, do not hand-roll

This task is the FIRST consumer of the shared snapshot emitter that TSO-0046 generalizes. The root freezes that seam before this lane starts (see the Wave 2 goal file); code against it rather than writing a policy-specific emitter that TSO-0046 then has to unpick.

The emitter owns: change detection, heartbeat cadence, chunking with seq/total, and the uniform attribute marking every snapshot record carries.

## This lane

1. Hook the ACL collector where it already tracks the ETag: `internal/collector/acl/acl.go:156-165` fetches `PolicyFileRaw` and compares `raw.ETag` against `c.lastETag`, so the change edge already exists and is already restart-stable via `revisionCheckpointPrefix` (acl.go:27, 204-216). Emit on that edge, plus the heartbeat.
2. Config key under the acl collector, opt-in, default false, with the PII wording above verbatim in the comment. Four config seams plus `just gen-config-schema gen-envref gen-helm` - `TestExampleConfigCoversEveryKey` and `TestHelmValuesCoverEveryKey` make config.example.yaml and the chart values.yaml mandatory, not optional.
3. Chunking: size each chunk against the CONFIGURED `otlp.limits.log_body_bytes`, leaving room for `logTruncationMarker`. Assert in a test that no emitted chunk is ever truncated by the emitter at the default cap AND at the 64-byte minimum.
4. Catalogue the new log event and give it a PANEL on `deploy/grafana/gen/tabs/policy_access.py`. The signal-coverage gate has no escape: a new signal with no panel comes back with an empty disposition and always fails. Budget for the panel; it is not optional. The panel reassembles chunks by sorting on seq within one etag.
5. Tests, test-first: emits on ETag change; emits on heartbeat with no change; does NOT emit per poll when neither fired; chunk boundaries are UTF-8 safe; a policy that fits emits exactly one record with seq=1 total=1; the opt-in defaults off and no record is emitted when off.
6. Gate: `just check`.

Wave 2 root freeze plan: add the shared snapshot emitter plus ACL snapshot enabled/heartbeat config defaults off; regenerate all config artifacts and prove the full gate before any lane dispatch.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Owner decisions and the measured size limits, 2026-08-30 (Wave 2 planning)

### The real ceiling is Loki 256 KB, and OUR default is the tighter bound

Checked against Grafana Loki upstream configuration docs rather than assumed:

- `limits_config.max_line_size` **default 256KB**.
- `limits_config.max_line_size_truncate` **default false** - so an over-limit line is **DISCARDED, not truncated**, and surfaces as the `line_too_long` validation error. Grafana own troubleshooting page states the 256 KB default and strongly discourages raising it.
- Our own `otlp.limits.log_body_bytes` defaults to **32768** (32 KiB) - `config.example.yaml:170`, `docs/configuration.md:477`. That is **8x tighter than Loki**, so WE are the binding constraint, not the backend, and a policy between 32 KiB and 256 KB is one we truncate for no backend reason.

**FALSE-PASS TRAP, name it in the tests:** an oversized line is dropped at the Loki DISTRIBUTOR, after the OTLP gateway has already returned success. A 2xx on the OTLP push is therefore NOT evidence the snapshot landed. Do not write an acceptance check that reads the export result.

### Decision: CHUNK, with seq/total as log attributes (owner, 2026-08-30)

Emit whole when it fits; chunk when it does not. Do not raise `otlp.limits.log_body_bytes` globally - it governs every log record in the process (flow logs, audit, k8s audit) and changing it to suit one snapshot is a cross-cutting behaviour change nobody asked for.

Chunk at the CONFIGURED `otlp.limits.log_body_bytes` minus headroom for the truncation marker, so the emitter never truncates a chunk. The emitter applies its cap AFTER the collector hands the body over (`internal/telemetry/emitter.go:327-331`), so a chunker that ignores the configured value silently produces truncated chunks that still look well formed.

### Decision: the opt-in overrides pii_filter, documented (owner, 2026-08-30)

`pii_filter` is NOT consulted for the snapshot body. Turning the key on IS the consent, and the config comment must say so in those terms: enabling ships the raw policy including every user email and group member it contains, and your logs retention then holds tailnet identity data. Do not build a HuJSON-aware redactor - a partially redacted ACL is not a usable ACL, and a redactor that misses a field shape leaks silently with nothing to catch it.

The body stays RAW (not base64) so it is greppable in Loki, per the original design note.

Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.

Freeze review follow-up: CodeRabbit found that effective chunk budgets of 1-3 bytes could not preserve a four-byte UTF-8 rune while honoring the configured limit. Changed snapshot.New to return an error when the safe body budget is below utf8.UTFMax. Added boundary coverage for 1, 2, and 3 bytes; negative-tested by weakening the constructor guard and observing all three cases fail, then restored it.

CodeRabbit also found that runtime validation rejected non-positive snapshot heartbeats but the generated JSON Schema still admitted them. Added a positive-duration schema rule for collectors.acl.snapshot_heartbeat and regenerated config.schema.json. Negative-tested by reverting the rule to the generic signed duration pattern and observing TestConfigSchemaFieldShapes fail, then restored it.

Second CodeRabbit follow-up: the review reported generated config artefacts missing, but rerunning just gen-config-schema, just gen-envref, and just gen-helm produced zero unstaged diff; the staged change already contains config.example.yaml, docs/configuration.md, docs/env-vars.md, Helm values/README/schema, and the root schema, and the completeness/drift gates pass. Disposition: factually false finding, with generator readback evidence.

The review correctly identified that a textually non-zero sub-nanosecond duration could match the positive schema pattern while time.ParseDuration yields zero. Tightened the field-specific pattern to require a representable positive component, preserved representative positive and compound forms, regenerated config.schema.json, and added TestPositiveDurationPatternAgreesWithPositiveParsedValues. Negative-tested by restoring the permissive pattern and observing the 0.0000000001s case fail, then restored the fix.

Third CodeRabbit follow-up: accepted the reassembly-collision finding. The goal freezes a minimum attribute set but explicitly permits the set to grow. Added tailscale.snapshot.emission_id, generated from a per-emitter random seed plus a monotonic emission sequence, so all chunks from one logical emission group together while a later heartbeat with the same revision cannot collide. Tests assert same-id chunk grouping and distinct change/heartbeat ids. Negative-tested by emitting an empty id and observing both guards fail for the intended reason, then restored the implementation.

Fourth CodeRabbit disposition: the remaining major finding is intentionally downstream of Freeze. The A1 snapshot dashboard panel does not exist yet and A1 may not begin before the root freeze commit. A1 must group/reassemble chunks by tailscale.snapshot.emission_id, validate that each group has one matching revision/ETag, and sort by tailscale.snapshot.seq. This is a mandatory lane acceptance requirement, not deferred cleanup.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added opt-in raw policy snapshots on change and heartbeat using UTF-8-safe chunks bounded below the configured OTLP log-body limit, explicit PII consent, and a generated latest-snapshot panel. Verified by boundary and telemetry tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
