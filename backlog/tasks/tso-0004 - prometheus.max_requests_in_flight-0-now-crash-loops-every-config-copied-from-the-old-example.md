---
id: TSO-0004
title: >-
  prometheus.max_requests_in_flight: 0 now crash-loops every config copied from
  the old example
status: Done
assignee: []
created_date: '2026-08-25 11:32'
updated_date: '2026-08-25 11:43'
labels: []
dependencies: []
ordinal: 8000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The security remediation commit made `prometheus.max_requests_in_flight: 0` a hard startup error and moved the default from 0 to 4. The shipped `config.example.yaml` had recommended `0` (documented as "0 = unlimited") right up to that commit, so every deployment whose config was copied from the example now fails `config.Load` in a restart loop with `prometheus.max_requests_in_flight must be positive (got 0)`. The live deployment host hit exactly this.

Two defects, one deployment fix:

1. The validation is ungated. Both `docs/configuration.md` and the struct comment say the value must be positive *while Prometheus is enabled*, and every comparable rule in validate.go (e.g. `events.max_events`) returns early when its feature is off. This one does not, so a config with `prometheus.enabled: false` and the old example's `max_requests_in_flight: 0` refuses to start over a key that controls nothing. That is the widest blast radius: the old example shipped both of those values together.
2. The error text does not say what changed. An operator upgrading sees a value they copied from the project's own example rejected with no hint that `0` used to mean unlimited, and no statement of what to set instead.

Separately, the same commit left the "Mapping examples" table in `docs/configuration.md` broken: it re-headed a two-column config-key/env-var table as three columns and the prometheus/profiling reference rows for `max_requests_in_flight`, `timeout`, `coalesce_gather`, the `tls.*` keys and the two `*_file` keys sit in it instead of in the `prometheus` / `profiling` reference sections, where a reader looking up this key would actually go.

`config.example.yaml`, the Helm chart values, the chart README and `config.schema.json` already carry the corrected `4` / `8s` / `true` values and need no change - verified, not assumed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Validate() accepts max_requests_in_flight <= 0 when prometheus.enabled is false, and still rejects it when enabled, each pinned by a test
- [x] #2 The rejection message states that 0 previously meant unlimited, is no longer accepted, and names the value to set
- [x] #3 docs/configuration.md: the Mapping examples table is two columns again, and every prometheus/profiling reference row moved into its own section's key table
- [x] #4 The live deployment host starts cleanly with no config-load error in its logs
- [x] #5 config.example.yaml, Helm values, chart README and config.schema.json confirmed to teach a valid default
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root cause: 45e489c moved the default from 0 to 4 and made 0 a hard error, but the rule was written ungated while both the struct comment and docs/configuration.md described it as conditional on prometheus.enabled. config.example.yaml had shipped 'enabled: false' next to 'max_requests_in_flight: 0' up to that commit, so the ungated rule refused to start every config copied from the example - whether or not the endpoint was ever turned on.

Fix: validate.go returns early when prometheus.enabled is false (matching events.max_events), and the rejection now says 0 used to mean unlimited, why unlimited is the state the cap exists to prevent, and to set 4. Tests: a new TestValidate_PrometheusMaxRequestsGatedOnEnabled pins both directions and asserts the message names 'unlimited' and '4'; the three existing TestValidate_PrometheusHandlerLimits subtests now set Enabled=true, since they exercise the enabled-path rule and previously passed only by accident of the rule being ungated.

Deliberately NOT changed: config.schema.json keeps 'prometheus.max_requests_in_flight' minimum 1. The suffix-rule schema generator cannot express 'positive only while enabled' without conditional schema, and an editor-time nudge on a value that has no valid meaning either way is the better failure - it flags the stale 0 before someone enables the endpoint and hits the startup error.

Docs: the security commit had re-headed the two-column 'Mapping examples' config-key/env-var table in docs/configuration.md as three columns, leaving eight prometheus rows and one profiling row rendering as env-var-less mapping examples. Those rows moved into the 'prometheus' and 'profiling' reference tables where a reader looking up the key would go, and the mapping table is two columns again. Added an 'Upgrading to v4.0.0' section covering all three changed prometheus defaults (the module path is already /v4 while the release manifest is still 3.0.0, so 4.0.0 is the committed next version).

Verified: go build, go vet, go test -race ./... all green; golangci-lint 0 issues; scripts/regen-generated.sh leaves no drift; coderabbit review returned one minor finding, in renovate.json, which belongs to another session's uncommitted TSO-0003 work and not this diff.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Gated prometheus.max_requests_in_flight validation on prometheus.enabled and rewrote its rejection message to explain that 0 used to mean unlimited and to name 4 as the value to set.

45e489c moved the default from 0 to 4 and made 0 invalid, but wrote the rule ungated even though the struct comment and docs both scoped it to an enabled endpoint. Because config.example.yaml had paired 'enabled: false' with 'max_requests_in_flight: 0' until that same commit, every config copied from the project's own example refused to start - the live deployment host restart-looped on it. The value bounds gathers on a listener that is not running, so it is now unchecked while the endpoint is off, matching events.max_events.

Verified: new TestValidate_PrometheusMaxRequestsGatedOnEnabled fails before the change on both directions and passes after; the three pre-existing handler-limit subtests now set Enabled=true because they exercise the enabled-path rule. go build, go vet, go test -race ./... green; golangci-lint 0 issues; scripts/regen-generated.sh produces no drift. The live host was backed up, corrected to 4/8s/true, restarted, and is running with restarts=0, zero config-load errors since start, and /metrics answering 200.

Docs: the same commit had re-headed the two-column Mapping examples table in docs/configuration.md as three columns, stranding eight prometheus rows and one profiling row in it; those moved into their own reference tables and the mapping table is two columns again. Added an 'Upgrading to v4.0.0' section covering all three changed prometheus defaults. config.example.yaml, Helm values, chart README and config.schema.json already taught the corrected values - confirmed by reading them and by a clean regen.
<!-- SECTION:FINAL_SUMMARY:END -->
