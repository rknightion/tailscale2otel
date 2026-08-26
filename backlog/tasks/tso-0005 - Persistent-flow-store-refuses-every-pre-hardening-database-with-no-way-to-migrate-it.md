---
id: TSO-0005
title: >-
  Persistent flow store refuses every pre-hardening database with no way to
  migrate it
status: Done
assignee: []
created_date: '2026-08-25 11:44'
updated_date: '2026-08-26 11:01'
labels: []
dependencies: []
ordinal: 9000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The security remediation commit gave each SQLite flow database a tailnet-identity stamp and a filename that embeds a hash of the tailnet name (`flows-<slug>-<sha256[:16]>.db`). `prepareDBPath` in internal/flowstore/sqlitestore/schema.go refuses to open a database under the old `flows-<slug>.db` name outright, because it cannot prove which tailnet the rows belong to.

The refusal is correct - the old filename is attacker-influenceable and the rows carry user identities - but it is a dead end for every existing deployment. The error says to 'archive it or explicitly migrate it after independently verifying ownership' and there is no migration path anywhere: no script, and no mention of the legacy filename in docs/ or scripts/. Renaming the file by hand is not enough either, since `ensureTailnetIdentity` refuses a database that has no identity row when it is not being created fresh, so the operator also has to INSERT the metadata row by hand with no documentation telling them the table, key, or value.

The failure is quiet in the worst way: the process starts normally and logs one ERROR line, then runs with the flow view disabled and flow history silently unavailable. The live deployment host is in exactly this state now, holding a 30-day flow database it can no longer open.

Found while fixing TSO-0004; the two share a cause (the same security batch) but nothing else.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An operator with a pre-hardening flow database has a documented, supported way to keep its rows - a script, a flag, or a documented manual procedure covering both the rename and the identity row
- [x] #2 The refusal message points at that procedure instead of telling the operator to verify ownership with no instructions
- [x] #3 Losing the flow view is louder than one ERROR line at startup - surfaced on the admin status page or as a startup advisory that persists
- [x] #4 The chosen path is exercised by a test against a database written in the legacy layout
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 go build ./... && go vet ./... && go test -race ./...
- [x] #2 golangci-lint run
- [x] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add an explicit, operator-driven adoption path rather than any automatic or config-standing one, since the whole point is supplying the ownership proof the filename cannot. 2. Point the refusal at that path. 3. Make the resulting loss of the flow view visible on the status surface instead of one log line. 4. Cover the legacy layout with tests, verify end to end with the real binary, and correct the documentation that still described the pre-hardening filename.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Adoption is a one-shot CLI mode, -adopt-flow-db <tailnet>, alongside the existing -preflight/-once/-healthcheck modes. A config key was rejected deliberately: a standing setting would keep adopting on every start, which turns a one-time operator assertion into a permanent policy. The tailnet is named on the command line and must match a configured one, so a typo cannot silently adopt nothing and look like success.

Ordering is load-bearing. The identity row is stamped BEFORE the file moves, so an interrupted run leaves a legacy file already carrying the right identity and a re-run finishes the job. The reverse order would leave a digest-named file with no identity, which Open refuses outright and which nothing could then repair.

The move is all-or-nothing. It refuses a database whose write-ahead log still holds frames, which means something else has it open, and it renames the database back if a sidecar cannot follow it. Both were raised by review and both are covered by tests that force the failure.

The status surfacing deliberately does not use the componentHealth tracker, because that gates /readyz. A failed admin flow view must not pull the pod out of rotation while OTLP export is unaffected. It follows the delivery-failure precedent instead: it feeds deriveHealth so the page reads degraded, and adds FlowStoreInfo.Failures so the page names the tailnet and the cause. A new field on a nested object is additive under docs/api/compatibility.md, so no schema_version bump; docs/api/schemas/status.schema.json regenerated.

Review caught that the first version of the empty-flag test passed for the wrong reason: -adopt-flow-db= exited non-zero because the server failed to start, not because the flag was rejected. The flag now checks presence via fs.Visit rather than emptiness, and the test asserts exit 2 and the message.

End-to-end verified with the built binary against a real 1234-row legacy database: adoption reported the row count and moved the file, the rows and the identity row survived, a re-run was a clean no-op, a mistyped tailnet was rejected naming the configured one, and an empty flag value exited 2.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Pre-hardening flow databases now have a supported way to keep their rows, the refusal names it, and losing the flow view is no longer silent.

Adoption is a one-shot CLI mode, -adopt-flow-db <tailnet>, which stamps the tailnet identity row, moves the file to the digest-qualified name, reports the row count and exits. Naming the tailnet is the ownership assertion the legacy filename cannot make, and it must match a configured tailnet so a typo cannot silently adopt nothing and look like success. The identity is written before the move, so an interrupted run is finished by a re-run rather than left unrepairable. The move is all-or-nothing: it refuses a database whose write-ahead log still holds frames, and renames the database back if a sidecar cannot follow it. It refuses, untouched, a database naming a different tailnet, and a directory holding both layouts.

The Open refusal now quotes that command instead of telling the operator to verify ownership with no instructions.

For the silence: newFlowStore returns its error, each runtime keeps it, and the status page grows a Flow view disabled block naming the tailnet and the cause, with overall health reading degraded. The page was previously byte-identical to one where flows were simply switched off. This follows the delivery-failure precedent and deliberately does not gate /readyz, since OTLP export is unaffected and pulling every pod out of rotation over an admin view would turn a partial fault into an outage.

Documentation corrected as well: flow-view.md and configuration.md still described the pre-hardening filename, and both now carry the digest form plus an adoption section, with a matching troubleshooting entry for the 404 an operator actually hits.

Verified: eleven new tests covering the legacy layout end to end, including row and identity survival, idempotence, resumption after an interrupted run, foreign-owner and both-layouts refusals, the live write-ahead-log refusal and the sidecar rollback; go build, go vet and go test -race clean across the repository; golangci-lint 0 issues; no generated-artifact drift after regenerating; CodeRabbit clean after fixing four of five findings. Confirmed end to end with the built binary against a real 1234-row legacy database. Landed as 0741792 with every gating CI lane green on the default branch.
<!-- SECTION:FINAL_SUMMARY:END -->
