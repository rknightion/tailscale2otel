---
id: TSO-0066
title: Per-tailnet cardinality limit overrides
status: Done
assignee: []
created_date: '2026-08-30 09:31'
updated_date: '2026-08-30 12:58'
labels: []
milestone: m-1
dependencies: []
priority: high
ordinal: 69000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
cardinality.metric_limit and the warning/critical thresholds are process-global; in MSP mode one noisy tailnet forces raising the ceiling for all. Design decided (owner, 2026-08-30): optional per-entry limit in the tailnets: list, falling back to the global value - fits the per-tailnet Provider structure (internal/telemetry/providerset.go). Overflow/limits accounting and self-obs must stay per-tailnet attributable.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A tailnets: entry can carry its own metric limit and thresholds, defaulting to global
- [x] #2 Overflow behaviour and self-obs metrics are attributable per tailnet
- [x] #3 Config schema/docs regenerated
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

```
tailnets:
  - name: alpha
    cardinality:                 # OPTIONAL per-tailnet overrides; each key falls back to the global cardinality: block. FILE-ONLY, like the rest of the list.
      metric_limit: 0            # 0 = inherit cardinality.metric_limit
      warning_threshold: 0       # 0 = inherit cardinality.warning_threshold
      critical_threshold: 0      # 0 = inherit cardinality.critical_threshold
```

- Go: new `TailnetCardinality` struct with `MetricLimit`, `WarningThreshold`, `CriticalThreshold` ints; `Cardinality TailnetCardinality `yaml:"cardinality"`` on `TailnetConfig` (internal/config/config.go:503).
- **0 means INHERIT, and negative is NOT an override.** The global `metric_limit` already defines `0 or negative = unlimited`; if the per-entry field reused that, an operator could not distinguish "inherit" from "unlimited". So per-entry: `0` = inherit the global (whatever it means, including unlimited); a NEGATIVE value = explicitly unlimited for this tailnet only; positive = that limit. Write that three-way rule into the field comment — it is the one thing a reader will get wrong.
- Resolution belongs beside `ResolvedTailnets()` (internal/config/config.go:549) and NOT scattered at the use sites. Add the three effective values to `ResolvedTailnet` so the app layer reads one place, exactly as `MaxResponseBytes`/`MaxLogResponseBytes` are copied onto it today (config.go:534-537). Follow that precedent literally.
- `PerTailnetOptions` (internal/telemetry/providerset.go:22) gains `CardinalityLimit int`; `NewProviderSet` applies it over `base` (providerset.go:50-53) when non-zero.

## AC#2 is the hard half — do not stop at the provider

- The two thresholds must reach `internal/app/status.go:426-427` per tailnet, not from `a.cfg`. Establish where the status page already distinguishes runtimes and carry the effective values there; if it does not distinguish them yet, that is the work.
- Self-obs and overflow accounting must stay per-tailnet attributable. Each tailnet already has its own `Provider` stamping `tailscale.tailnet` as a signal-scoped const attribute (providerset.go:27-31), so an overflow emitted through the tailnet provider is attributable for free — VERIFY that rather than assume it, and pin it with a test that runs two runtimes with different limits and asserts the overflow/limit self-obs series carry distinct `tailscale.tailnet` values.
- Acceptance evidence for AC#2 is a two-tailnet test where the noisy tailnet overflows at its own limit while the quiet one does not, with the self-obs attributable to each. "It compiles and the field is set" is not evidence.

## Config-shape seams

This is a NEW STRUCTURED SHAPE inside a file-only list — the doc-0002 four-seam case:
1. `internal/config/config.go` + `defaults.go`;
2. `config.example.yaml` (the `tailnets:` example entry) — `TestExampleConfigCoversEveryKey`;
3. `config.schema.json` via `just gen-config-schema`;
4. `deploy/helm/tailscale2otel/values.yaml` + `just gen-helm` — `TestHelmValuesCoverEveryKey`;
5. env loader: NO CHANGE, and confirm `TestStructSliceEnvKeysMatchesStructSliceFields` still passes (it keeps `structSliceEnvKeys` in step with the real []struct fields).
Plus `just gen-envref`.

## Work

1. Test-first: per-entry values override; 0 inherits each field independently; negative = unlimited for that tailnet only; the effective-value validation rejects `critical < warning` and `warning > metric_limit` AFTER fallback; a `TS2OTEL_TAILNETS__0__CARDINALITY__METRIC_LIMIT` variable is still a hard Load error.
2. Then the provider wiring, then the status-page/self-obs half.
3. Regenerate and gate with `just check`.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9)

Traced all three global values to their consumers. They are NOT wired the same way, and that asymmetry is the whole difficulty of this task.

**metric_limit** flows through the provider: `internal/app/options.go:62` sets `telemetry.Options.CardinalityLimit` from `cfg.Cardinality.MetricLimit`; `internal/telemetry/provider.go:271` passes it to `metricProviderOptions` -> `sdkmetric.WithCardinalityLimit` (internal/telemetry/processors.go:93); provider.go:329 also passes it to `NewCardinalityTrackerWithLimits`. `NewProviderSet` (internal/telemetry/providerset.go:41) builds each tailnet provider from ONE `base Options` template, overriding only `TailnetName` and `InstanceID` from `PerTailnetOptions` (providerset.go:22-25, 50-53). So a per-tailnet limit is a natural extension of `PerTailnetOptions` — the fan-out point already exists.

**warning_threshold / critical_threshold** do NOT flow through the provider at all. They are read straight off the global config at the render site: `internal/app/status.go:426-427` (`a.cfg.Cardinality.WarningThreshold` / `.CriticalThreshold`). Nothing per-tailnet exists on that path. This is the trap: a lane that adds fields to `PerTailnetOptions` and stops will deliver a per-tailnet metric_limit and silently leave the two thresholds global, and the admin status page will keep showing the global numbers against per-tailnet series counts — a wrong reading that looks right.

**Env is already handled.** `tailnets` is in `structSliceEnvKeys` (internal/config/env.go:52), so any `TS2OTEL_TAILNETS__0__...` variable is already a hard Load error (#79). Nesting a new struct under a tailnets entry needs NO env-loader change and MUST NOT get one — the list is file-only by design.

**Existing global validation to mirror per entry.** `CardinalityConfig` (internal/config/config.go:956) documents: `CriticalThreshold >= WarningThreshold`, and when `MetricLimit > 0` both must be `<= MetricLimit`. Defaults 2000 / 8000 / 10000. The per-entry rule must be checked on the EFFECTIVE (post-fallback) values, not the raw ones — an entry setting only `metric_limit: 1000` while inheriting the global 2000/8000 thresholds is invalid and would otherwise pass.

Wave 1 Lane C2 started by root at 1de673f after W1; goal §6.1 negative metric_limit means per-tailnet unlimited and overrides the contradictory sentence in the task plan.

Validation: two-tailnet tests prove independent limit resolution, overflow attribution, status thresholds, and a GaugeSnapshot that cannot mix tailnets. just check passed at cd3bfa0; exact-head CI run 33312668201 concluded success.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed 175e3ce: per-tailnet metric limits and warning and critical thresholds resolve independently with attributable overflow and status. Verified by race tests, full gate, CodeRabbit correction, and CI 33312668201.
<!-- SECTION:FINAL_SUMMARY:END -->
