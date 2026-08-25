---
id: TSO-0005
title: >-
  Persistent flow store refuses every pre-hardening database with no way to
  migrate it
status: To Do
assignee: []
created_date: '2026-08-25 11:44'
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
- [ ] #1 An operator with a pre-hardening flow database has a documented, supported way to keep its rows - a script, a flag, or a documented manual procedure covering both the rename and the identity row
- [ ] #2 The refusal message points at that procedure instead of telling the operator to verify ownership with no instructions
- [ ] #3 Losing the flow view is louder than one ERROR line at startup - surfaced on the admin status page or as a startup advisory that persists
- [ ] #4 The chosen path is exercised by a test against a database written in the legacy layout
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 go build ./... && go vet ./... && go test -race ./...
- [ ] #2 golangci-lint run
- [ ] #3 scripts/regen-generated.sh (only if a generated artifact's inputs changed)
<!-- DOD:END -->
