---
id: TSO-0053
title: Cardinality backstop for the posture attribute-namespace wildcard
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-30 10:07'
labels: []
milestone: m-1
dependencies: []
priority: medium
ordinal: 56000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
WithAttributeNamespaces("*") (internal/collector/devices/devices.go:540-562) promotes every posture namespace to metric labels with no cap, unlike every other cardinality lever in the repo (tag_rollup_limit, __other__ buckets). One MDM vendor emitting a per-scan-timestamp value under the wildcard blows up series with no built-in bound. Add a cap with overflow behaviour consistent with existing levers. Shares design with the posture compliance-gauge task TSO-0039 - do them together or sequence explicitly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Wildcard namespace promotion is bounded with a documented overflow behaviour
- [ ] #2 The cap interacts sanely with cardinality.metric_limit accounting
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

Two integer keys on `DevicesCollector`, both following `tag_rollup_limit`s conventions exactly (`0 or negative = unlimited`):

```
collectors:
  devices:
    attribute_key_limit: 200     # max distinct posture attribute KEYS promoted to tailscale.device.attribute{,.info}; over-cap keys are dropped fleet-wide and counted. 0 or negative = unlimited
    attribute_value_limit: 50    # max distinct VALUES per attribute key on tailscale.device.attribute.info; over-cap values collapse to "__other__". 0 or negative = unlimited
```

- Go: `AttributeKeyLimit int` and `AttributeValueLimit int` on `DevicesCollector` (internal/config/config.go:1125), defaults 200 and 50 in internal/config/defaults.go.
- Both are plain ints -> no env-loader change; `TS2OTEL_COLLECTORS__DEVICES__ATTRIBUTE_KEY_LIMIT` works automatically.
- Add numeric bounds to internal/config/validate.go with a remediation string, and a numeric range to the reflected schema.
- Four config seams apply (struct+defaults, config.example.yaml, values.yaml, then `just gen-config-schema gen-envref gen-helm`) — `TestExampleConfigCoversEveryKey` and `TestHelmValuesCoverEveryKey` enforce two of them.

## Overflow behaviour — DROP for keys, `__other__` for values

- **Keys.** Select deterministically: rank attribute keys by the number of devices carrying them this tick, break ties lexically (never map iteration order — that would make the cap non-deterministic and flap series between scrapes). Keys beyond the cap are DROPPED from `docAttribute`, `docAttributeInfo` AND `docAttributeExpiry` in the same tick, so the three signals never disagree about scope — which is the invariant the existing allow-list comment at devices.go:1700-1702 already states.
- **Values.** On `docAttributeInfo` only, per attribute key, rank values by device count and collapse the remainder to `value="__other__"` reusing the existing `tagOther` sentinel spelling. This is what actually defends against the per-scan-timestamp case, and here the fold IS coherent (constant-1 info gauge, so the count of devices is preserved).
- The cap applies to the WILDCARD and to an explicit namespace list alike. An operator naming one chatty namespace explicitly has the same problem; do not gate the cap on `attrNamespaceWildcard`.

## Observability of the cap (this is what makes it debuggable)

Add ONE new metric so a dropped attribute is visible rather than silent:

`tailscale.device.attributes.dropped` — gauge, unit `1` (so its Prometheus spelling gets the `_ratio` suffix, per the OTLP naming rule in AGENTS.md), value = count of distinct attribute keys suppressed by the cap this tick. Emitted every tick, 0 when nothing is dropped, so an alert can be written on it.

That means catalog work: add the descriptor to internal/collector/devices/catalog.go (near `docAttribute`/`docAttributeInfo` at catalog.go:303-311, and to the slice at catalog.go:540), then `just gen-metrics`. NOTE THE COVERAGE GATE: a new signal starts with an EMPTY disposition in internal/catalog/signal_dispositions.json and an empty disposition ALWAYS FAILS. Per AGENTS.md there is no value a human may assign — the ONLY way to settle it is to give the metric a real dashboard panel. So this lane must also add a panel to `deploy/grafana/gen/build.py` and then run `go test ./internal/catalog -run TestSignalDispositionsInSync -update` followed by `just gen-dashboards gen-coverage gen-counts`. Budget for that; it is not optional and it cannot be worked around.

`deploy/grafana/gen/build.py` is a doc-0002 single-owner file — serialize with any other lane touching it.

## Interaction with cardinality.metric_limit (AC#2)

`cardinality.metric_limit` is the SDK-level backstop: `internal/app/options.go:62` -> `telemetry.Options.CardinalityLimit` -> `sdkmetric.WithCardinalityLimit` (internal/telemetry/processors.go:93), which collapses excess series into `otel_metric_overflow`. The new caps sit UPSTREAM of it and are per-metric-family, so they reduce what reaches the SDK limit and never raise it. Document that ordering in the config comment: the attribute caps shape the series, `metric_limit` is the last-resort guard, and hitting `metric_limit` is still possible with the caps in place.

## Work

1. Test-first against `internal/telemetrytest.Recorder`: over-cap keys absent while under-cap keys present; deterministic selection across two identical ticks; ties broken lexically; over-cap values collapsed to `__other__` with the device count preserved; `0` = unlimited restores todays behaviour; the dropped gauge reports the right count and reports 0 when nothing is dropped; the three attribute signals agree on scope.
2. Config seams + validation + regeneration as above.
3. Catalog descriptor + dashboard panel + disposition regeneration, in the SAME commit.
4. Gate: `just check`.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9)

`WithAttributeNamespaces("*")` (internal/collector/devices/devices.go:548) sets `c.attrNamespaceWildcard = true`, which is checked in two places and bounds nothing in either:

- `emitAttributes` (devices.go:1665) — for EVERY posture attribute of EVERY device, `c.gsb.Add(docAttribute...)` for bool/float64 values, and `c.gsb.Add(docAttributeInfo...)` for strings with the raw string carried as the `value` label (devices.go:1685).
- `emitAttributeExpiries` (devices.go:1703) — same allow-list check, adds `docAttributeExpiry` per (device, attribute).

So the cardinality is per-(device x attribute-key), and on `docAttributeInfo` it is per-(device x attribute-key x VALUE). The tasks scenario — an MDM vendor writing a per-scan timestamp — lands on that third dimension, where one device can produce a new series on every scrape forever. That is the sharpest edge and it is a VALUE problem, not only a namespace problem. A cap on namespaces alone would not have caught it.

## Precedent for the shape of the fix

Two existing levers, and they are deliberately different from each other:

- `collectors.devices.tag_rollup_limit` — CONFIG key, default 50, `0 or negative = unlimited`, overflow folds into `tailscale.tag="__other__"` (`tagOther`, devices.go:234; config.go:1160).
- `distroRollupLimit = 50` — a CONSTANT, not a config key, with an explicit comment (devices.go:236-243) saying why: the distro vocabulary is a short upstream list, unlike operator-authored ACL tags. That comment is the repos stated test for constant-vs-key.

Posture attribute keys are VENDOR-authored and genuinely unbounded (Intune, Fleet, Huntress, custom:), which puts them on the `tag_rollup_limit` side of that test: a config key.

## Why `__other__` does not transfer verbatim

`by_tag` and `by_distro` are AGGREGATE counts, so folding the overflow into one bucket preserves the fleet total — that is what makes `__other__` correct there. `docAttribute` is a per-device gauge whose VALUE carries meaning (0/1 for bool, the number itself otherwise); there is no total to preserve and summing unrelated attribute values into one series is meaningless. `docAttributeInfo` is a constant-1 info gauge, where an `__other__` fold is coherent but of near-zero value.

So the overflow behaviour must be DROP-plus-observability, and that difference has to be written down or a future reader will "fix" it back to `__other__`.

## Sequencing with TSO-0039 (m-3, depends on this)

TSO-0039 adds posture compliance gauges and its AC#2 requires "any attribute-to-label promotion path has an enforced cardinality cap". Land the cap HERE as the shared mechanism; TSO-0039 then reuses it rather than inventing a second one.
<!-- SECTION:NOTES:END -->
