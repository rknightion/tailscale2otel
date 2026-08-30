---
id: TSO-0051
title: 'Quiet key-expiry warnings: on-change + daily mode as default'
status: To Do
assignee: []
created_date: '2026-08-30 09:30'
updated_date: '2026-08-30 10:06'
labels: []
milestone: m-1
dependencies: []
priority: high
ordinal: 54000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The key-expiry WARN fires on every scrape per expiring device/key (internal/collector/devices/devices.go:1083-1094, internal/collector/keys/keys.go:252-266) - roughly 20k near-identical lines per expiring device key over a 14-day window at the 60s default interval. Design decided (owner, 2026-08-30): add an on-change + once-daily reminder mode mirroring the existing posture_log_mode: changes pattern (devices.go:207-217) and make the quiet mode the DEFAULT. Coordinate with the lifecycle-timeline task TSO-0050 so expiring-soon events do not double up.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Expiring keys/devices produce a warning on state change plus at most one daily reminder by default
- [ ] #2 The legacy every-scrape behaviour remains selectable via config
- [ ] #3 Docs/env reference regenerated for the new mode key
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

Two keys, same closed enum, same default:

```
collectors:
  devices:
    expiry_log_mode: daily      # daily (log on change + at most one reminder per 24h; DEFAULT) | always (every scrape, legacy) | off
  keys:
    expiry_log_mode: daily      # same three values
```

- Go: `ExpiryLogMode string `yaml:"expiry_log_mode"`` on `DevicesCollector` (internal/config/config.go:1125) and on `KeysCollector` (config.go:1299).
- Default: `"daily"` in internal/config/defaults.go for both.
- Value name is `daily`, NOT `changes`, deliberately: postures `changes` means on-change ONLY, and reusing the word for on-change-plus-reminder would make two keys with one spelling and two meanings.
- Reminder period is a CONSTANT, not a fourth config key: `expiryReminderInterval = 24 * time.Hour`, declared once per collector package next to the existing window constants.
- `collectors.devices.expiry_log_mode` governs BOTH device sites (node key at devices.go:1083 and attribute expiry at devices.go:1721). One key, two sites — say so in the comment.
- `collectors.keys.expiry_warn` is untouched. It sets the WINDOW; the new key sets the CADENCE. Do not merge them.

## Behaviour of `daily`

Emit the WARN when either is true:
- the entity was not in the warn window on the previous scrape (first-seen inside the window counts as a change), or its expiry timestamp changed (a rotated key must not be silenced by its predecessors reminder);
- `now - lastEmitted >= 24h`.

State maps, one per site, keyed by the stable identity (device ID; device ID + attribute key; key ID), holding `{expiresAt time.Time, lastEmitted time.Time}`. Prune each map to the current ticks entity set exactly as `lastPosture` is pruned at devices.go:1098-1113. Leaving the warn window drops the entry, so re-entering it warns again.

`always` reproduces todays behaviour byte-for-byte. `off` suppresses the log; the `docDevicesKeyExpiry` histogram, the `docKeyExpiry` gauge and `docAttributeExpiry` gauge are unaffected in every mode — mirror `posture_log_mode`s metric-always-emitted contract and say so in the comment.

## Coordination with TSO-0050 (dependent, m-2)

TSO-0050 will emit a normalized `expiring-soon` lifecycle event for keys. Do NOT build it here. Leave a comment at the keys.go emit site naming TSO-0050 and stating that the lifecycle event must consume the same state map rather than adding a second cadence — that is the whole content of TSO-0050 AC#3.

## Work

1. Test-first per site, driving the collector against `internal/telemetrytest.Recorder` (repo rule: assert emitted telemetry, not internals). Cases per site: first scan inside window emits once; second scan 60s later emits nothing; a scan 24h+ later emits once; an expiry-value change emits immediately; `always` emits every scan; `off` emits never while the metric still emits; the state map is pruned when the entity leaves the fleet.
2. Add the enum to the schema map at internal/config/schema.go:103 and a `oneOfRemediation` validator alongside internal/config/validate.go:1792, for both keys.
3. Four config seams: struct + defaults, config.example.yaml, deploy/helm/tailscale2otel/values.yaml, then regenerate `just gen-config-schema gen-envref gen-helm`.
4. Gate: `just check`.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9)

There are THREE expiry-warn sites, not two. The task names two; the third is the same shape and would otherwise be left noisy:

1. `internal/collector/devices/devices.go:1083` — device node key. `days > 0 && days <= keyExpiryWarnDays` (a hardcoded `const keyExpiryWarnDays = 14`, devices.go:315) -> `docDeviceKeyExpiryLog` WARN.
2. `internal/collector/devices/devices.go:1721` — posture ATTRIBUTE expiry, in `emitAttributeExpiries`. Same fixed 14-day window, same shape -> `docDeviceAttributeExpiringLog` WARN.
3. `internal/collector/keys/keys.go:252-266` — auth/API keys. Window is already configurable: `collectors.keys.expiry_warn`, default `168h` (config.example.yaml:433, `KeysCollector.ExpiryWarn`, internal/config/config.go:1304) -> `docKeyExpiring` WARN.

Volume check: at the 60s default devices interval a single device inside the 14-day window emits 14*24*60 = 20,160 near-identical WARNs.

## The pattern to mirror

`posture_log_mode` (`DevicesCollector.PostureLogMode`, config.go:1141; enum `changes|always|off` in the schema map at internal/config/schema.go:103; validated at internal/config/validate.go:1792; normalized by `normalizePostureLogMode`, devices.go:530). Its change-detection state is `c.lastPosture map[string]string` (devices.go:352), populated at devices.go:1629 and PRUNED to the current ticks fleet at devices.go:1098-1113 so it cannot grow under device churn (#61). Copy that pruning — it is the part that is easy to omit and expensive to omit.

## Config-shape seams

Both new keys are plain enum strings, so the env loader needs no change (`TS2OTEL_COLLECTORS__DEVICES__EXPIRY_LOG_MODE` works automatically; NOT a `listEnvKeys` or `structSliceEnvKeys` case). But `TestExampleConfigCoversEveryKey` AND `TestHelmValuesCoverEveryKey` (internal/config/completeness_test.go:74 and the block after it) mean config.example.yaml and deploy/helm/tailscale2otel/values.yaml are both MANDATORY — the charts `config:` block carries `# @schema additionalProperties:false`, so a key missing from values.yaml is not merely undocumented, it is unusable by chart operators.
<!-- SECTION:NOTES:END -->
