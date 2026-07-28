# Admin API contract & compatibility policy

tailscale2otel's admin server exposes a small, **read-only** JSON API (#323).
Nothing here is a remote-control surface — every endpoint listed below only
serves state the process already holds; the admin server's one mutating
endpoint (`POST /api/rdns/purge`) is out of scope for this contract and
carries no schema version.

| Endpoint | Response type | Published schema | `schema_version` field |
| --- | --- | --- | --- |
| `GET /api/status.json` | `statusdata.Status` | [`schemas/status.schema.json`](schemas/status.schema.json) | `.schema_version` |
| `GET /api/config.json` | `statusdata.ConfigSummary` | [`schemas/config-summary.schema.json`](schemas/config-summary.schema.json) | `.schema_version` |
| `GET /api/cardinality.json` | `statusdata.CardinalityInfo` | [`schemas/cardinality.schema.json`](schemas/cardinality.schema.json) | `.schema_version` |
| `GET /api/flows.json` | `flowsdata.Response` | [`schemas/flows.schema.json`](schemas/flows.schema.json) | `.schema_version` |
| `GET /api/flows/export.json` | (internal envelope) | [`schemas/flows-export.schema.json`](schemas/flows-export.schema.json) | `.schema_version` |
| `GET /api/flows/export.csv` | CSV | — (no JSON schema; see below) | `# schema_version=N` comment line |
| `GET /api/events.json` | `eventsdata.Response` | [`schemas/events.schema.json`](schemas/events.schema.json) | `.schema_version` |

`Status.config` and `Status.cardinality` carry `ConfigSummary`/`CardinalityInfo`
verbatim, so those two objects have their **own** `schema_version` wherever
they appear — nested inside `/api/status.json` or served standalone — because
each is also independently exposed and can change on its own schedule.

## Reading `schema_version`

Every JSON response body in the table above carries a top-level integer field
`schema_version`, starting at `1`. It exists so external automation can tell
"the shape I already understand" from "something changed I need to look at"
without guessing from field presence. A consumer should:

1. Read `schema_version`.
2. If it matches the version the integration was written against, decode
   normally — new, unfamiliar fields may appear (see Additive below) and
   should be ignored, not treated as an error.
3. If it is higher than expected, either fall back to a defensive/partial
   decode or refuse and alert — the response may no longer have the shape the
   integration assumes.

## Additive vs. breaking

**Additive (schema_version does NOT change):**

- A new top-level field, or a new field on any nested object.
- A new element appearing in an existing array (e.g. a new row kind, a new
  collector name in `collectors[]`).
- A previously-always-present field becoming conditionally omitted **only**
  when the underlying feature it describes is disabled/unavailable (this
  repo's existing `omitempty`/`omitzero` convention — e.g. `tls_certificates`
  is omitted entirely when no listener runs TLS). This is additive because a
  consumer that already handles "feature off" gracefully sees no change; one
  that does not was already relying on undocumented behavior.
- A documentation-only change: a clearer description, an added `docs/api/`
  page, a doc comment.

**Breaking (requires a `schema_version` bump on the AFFECTED response, in the
same change that makes it):**

- Removing a field.
- Renaming a field's JSON key (equivalent to removing the old key).
- Changing a field's JSON type (e.g. a duration string becoming a raw
  integer, an object becoming an array).
- Changing what an EXISTING field's value means without renaming it (units,
  scale, or semantics) — this is the one class of break the automated gate
  below **cannot** detect by shape alone, because the JSON type is unchanged.
  It must be called out explicitly in the PR/issue and treated as breaking by
  policy even though the tooling stays silent.
- A field that was always present becoming conditionally omitted for a reason
  OTHER than the feature-disabled case above (e.g. omitting it under normal
  operation to save bytes) — this removes a guarantee a consumer may depend
  on, unlike the additive case.

When in doubt, treat it as breaking: bumping a `schema_version` is cheap, and
a downstream integration silently misinterpreting a field is not.

## How CI enforces it

Enforcement is two independent, deliberately different mechanisms, both
living beside the response types they describe
(`internal/app/apicontract`, plus one file in `internal/app` for the one
unexported response type). Both run inside the normal `go test -race ./...`
gate — no separate workflow step.

1. **Drift gate** (`TestSchemasInSync` / `TestFlowsExportSchemaInSync`) — the
   schema published under `docs/api/schemas/*.schema.json` must equal what
   reflecting over the LIVE Go response type produces, right now. ANY shape
   change — additive or breaking — fails this test until the schema is
   regenerated and the diff committed:

   ```sh
   go test ./internal/app/apicontract -run TestSchemasInSync -update
   go test ./internal/app -run TestFlowsExportSchemaInSync -update
   ```

   This proves the published docs are current. It does **not** prove a
   change was safe — an accidental field removal regenerates just as cleanly
   as an addition. That's gate 2.

2. **Compatibility gate** (`TestSchemasAreBackwardCompatible` /
   `TestFlowsExportSchemaIsBackwardCompatible`) — a committed
   `docs/api/schemas/*.baseline.json` file records every field path and type
   the CURRENT `schema_version` promises. This test flattens the live schema
   and checks every baseline-listed path still resolves to the same type. It
   is **never** satisfied by the routine `-update` flag above — regenerating
   it requires the separate, explicit `-update-baseline` flag:

   ```sh
   go test ./internal/app/apicontract -run TestSchemasAreBackwardCompatible -update-baseline
   go test ./internal/app -run TestFlowsExportSchemaIsBackwardCompatible -update-baseline
   ```

   Running `-update-baseline` is the deliberate, auditable act of
   acknowledging a breaking change — it should be done in the same commit
   that bumps the response's `SchemaVersion` constant
   (`statusdata.StatusSchemaVersion`, `statusdata.ConfigSummarySchemaVersion`,
   `statusdata.CardinalitySchemaVersion`, `flowsdata.SchemaVersion`,
   `eventsdata.SchemaVersion`, or `flowsExportSchemaVersion` in
   `internal/app/admin_flows_export.go`). The baseline itself also records the
   `schema_version` it was generated against, and the test fails loudly if
   that number and the compiled-in constant disagree — so the two files
   cannot silently drift apart.

   A removed field, a renamed field, or a changed field type all fail this
   test by name (`internal/app/apicontract/apicontract_test.go`'s
   `TestCompareBaseline_*` tests prove the detector catches each of the three
   directly; the live Go types were also mutated by hand during development
   of #323 and observed to fail this exact test for the exact reason).

3. **End-to-end wiring** (`TestAdminServer_SchemaVersionsAreWiredOnTheWire`,
   `internal/app/apicontract_test.go`) drives the real admin server
   (`buildAdminServer`) over every endpoint in the table above and asserts
   `schema_version` is actually present with the expected value on the wire
   — not merely correct as a Go struct field nobody assigns.

## What this contract does NOT cover

- **CSV export** (`/api/flows/export.csv`) has no JSON schema; its column
  order (`csvExportHeader` in `admin_flows_export.go`) is its de facto
  contract, and it carries the same `schema_version` as a `#`-comment line
  for consistency, not as an enforced shape.
- **Cross-field relationships** — a field's value being consistent with
  another field's — are outside a shape-only contract, the same limitation
  `internal/config/schema.go`'s `config.schema.json` documents for the
  application config.
- **Semantic drift with no shape change** (see the "changing what an existing
  field means" bullet above) is a documentation and code-review
  responsibility, not something JSON-Schema-shape comparison can catch.
