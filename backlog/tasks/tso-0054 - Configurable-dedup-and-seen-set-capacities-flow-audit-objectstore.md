---
id: TSO-0054
title: 'Configurable dedup and seen-set capacities (flow, audit, objectstore)'
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-30 10:07'
labels: []
milestone: m-1
dependencies: []
priority: medium
ordinal: 57000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Three boundary-protection capacities are compile-time constants: flow dedup 16384 (internal/collector/flowlogs/flowlogs.go:42), audit dedup 4096 (internal/collector/auditlogs/auditlogs.go:29, shared default internal/dedup/dedup.go:15), objectstore maxSeenKeys 5000 (internal/collector/objectstore/objectstore.go:100-103). A chatty tailnet can evict entries younger than the overlap horizon the dedup exists to protect, silently double-counting; an objectstore provider writing many small objects inside the lookback re-ingests evicted objects as new. Make all three configurable with the current values as defaults. Pairs with the eviction-age observability task (youngest-eviction gauge).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 All three capacities are config keys with validated bounds and current defaults
- [ ] #2 Docs/schema/env reference regenerated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
## Frozen seam (do not renegotiate)

Three keys, all plain ints, all defaulting to todays constants so behaviour is unchanged:

```
collectors:
  flowlogs:
    dedup_capacity: 16384       # keys remembered for window-boundary de-dup (poll path) AND cross-source de-dup. Raise on a tailnet whose per-window connection count approaches it: a FIFO eviction younger than the window overlap silently double-counts.
  auditlogs:
    dedup_capacity: 4096        # same, for audit events; also bounds the webhook<->audit de-dup set
    objectstore:
      max_seen_keys: 5000       # durable seen-object keys retained per destination, independent of time-based pruning; too low re-ingests evicted objects inside the lookback as new
```

- Go: `DedupCapacity int `yaml:"dedup_capacity"`` on `FlowlogsCollector` (internal/config/config.go:1167) and on `AuditlogsCollector`; `MaxSeenKeys int `yaml:"max_seen_keys"`` on `ObjectStoreConfig` next to `MaxObjects` (config.go:1249).
- Defaults in internal/config/defaults.go: 16384, 4096, 5000.
- Validate: reject <= 0 with a remediation string (unlike `tag_rollup_limit`, "unlimited" is NOT a sane option here — an unbounded dedup set is a memory leak; say that in the comment so nobody adds a 0-means-unlimited escape later). Add numeric minimums to the reflected schema.
- Plain ints -> no env-loader change. `TS2OTEL_COLLECTORS__FLOWLOGS__DEDUP_CAPACITY` works automatically. The per-tailnet objectstore copies stay file-only, already covered by `structSliceEnvKeys["tailnets"]` (internal/config/env.go:52).

## ONE value reaches ALL its sites

- `collectors.flowlogs.dedup_capacity` replaces flowlogs.go:42 AND app.go:45.
- `collectors.auditlogs.dedup_capacity` replaces auditlogs.go:29 AND app.go:46, and therefore the webhook set at app.go:588 and app.go:662 too.
- Delete the constants; do not leave a constant as a "default" beside the config default, or the two drift.
- The collectors take the capacity through their existing `With...` option pattern, set from the composition root. `internal/app/app.go`, `internal/app/tailnetruntime.go` and `internal/app/collectors.go` are doc-0002 single-owner wiring files — this lane owns them for the run or the root does the wiring pass.

## Work

1. Test-first: a `Set` at capacity N evicts the oldest and re-admits it as new (pin the documented FIFO contract if not already pinned); each collector honours a configured capacity; a config of 0 or negative is a Validate error with the remediation text; the objectstore seen-set trims to the configured value at objectstore.go:1264.
2. Four config seams — and remember `max_seen_keys` needs THREE blocks in config.example.yaml and THREE in values.yaml.
3. Regenerate `just gen-config-schema gen-envref gen-helm` in the same commit.
4. Gate: `just check`.

## Explicitly OUT of scope

The paired "youngest-eviction age" observability gauge the description mentions. It is a new signal, and a new signal drags in the catalog descriptor, an empty `signal_dispositions.json` entry that always fails the gate, and therefore a dashboard panel (AGENTS.md). File it as its own task rather than smuggling it in here. If `Set.evictions`/`Set.hits` are already surfaced as self-obs, say so in the notes; if they are not, that is the same new-signal cost and the same answer.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — there are SIX constant sites, not three

The task names three. A grep for the actual construction sites finds the capacities duplicated across the poll path AND the composition root:

```
internal/collector/flowlogs/flowlogs.go:42    dedupCapacity      = 16384   -> dedup.New at flowlogs.go:167
internal/collector/auditlogs/auditlogs.go:29  dedupCapacity      = 4096    -> dedup.New at auditlogs.go:99
internal/dedup/dedup.go:15                    defaultCapacity    = 4096    (fallback for New(<=0))
internal/app/app.go:45                        flowDedupCapacity  = 16384   -> dedup.New at tailnetruntime.go:156
internal/app/app.go:46                        auditDedupCapacity = 4096    -> dedup.New at tailnetruntime.go:192 AND app.go:588, app.go:662 (webhook dedup)
internal/collector/objectstore/objectstore.go:103  maxSeenKeys   = 5000    -> objectstore.go:1264-1266
```

The collector constants bound the POLL boundary-overlap set; the `internal/app` pair bounds the CROSS-SOURCE dedup on each tailnet runtime plus the webhook set. They are the same numbers today by coincidence of intent, not by any shared definition — a config key that changed only one pair would produce two different capacities for what an operator reads as one setting. Any lane that stops at the three the task names will ship exactly that.

Note the webhook dedup at app.go:588/662 uses `auditDedupCapacity`: it deduplicates webhook events against audit-log events, so it is audit-shaped and belongs under the audit key.

`internal/dedup/dedup.go:15 defaultCapacity` is the library fallback for `New(0)`. Leave it as-is; every call site will pass an explicit, validated value.

## Seam warning: max_seen_keys lands on a SHARED struct

`maxSeenKeys` naturally belongs on `ObjectStoreConfig` (internal/config/config.go, alongside the existing `MaxObjects int `yaml:"max_objects"`` at config.go:1249). That struct is embedded SIX times: `collectors.flowlogs.objectstore`, `collectors.auditlogs.objectstore`, `collectors.k8s_audit.objectstore`, and `tailnets[].objectstore.{flow,audit,k8s_audit}`. In Go that is one field; in the FLAT artifacts it is not:

- `config.example.yaml` carries `max_objects` 3 times (lines 363, 394, 420) — the three top-level blocks.
- `deploy/helm/tailscale2otel/values.yaml` carries it 3 times.
- `config.schema.json` and the chart `values.schema.json` expand every occurrence.

`TestExampleConfigCoversEveryKey` and `TestHelmValuesCoverEveryKey` (internal/config/completeness_test.go:74 and below) flatten `Default()` and diff the key SET, so a `max_seen_keys` added to the struct but written into only one of the three example blocks fails the root test suite. Expect three edits per file, not one.

## Why the capacities matter (the failure this prevents)

`internal/dedup.Set` is a FIFO: at capacity the OLDEST key is evicted, and an evicted key that reappears "counts as new" (dedup.go:5-6). A tailnet chatty enough to push more than `capacity` distinct keys through in less than the window overlap therefore evicts entries YOUNGER than the horizon the set exists to protect, and silently double-counts at every window boundary. `Set` already tracks `evictions` and `hits` counters (dedup.go:26-27) — check whether those are already exported as self-obs before adding anything new.
<!-- SECTION:NOTES:END -->
