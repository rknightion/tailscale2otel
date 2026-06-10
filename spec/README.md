# Vendored Tailscale OpenAPI spec

`tailscale-api.json` is a vendored copy of the **published Tailscale OpenAPI schema**, committed as
the **baseline** the API-drift CI diffs the live spec against, and as the input the schema-driven
decode-fuzz lane reads. It is also a convenient, LLM-readable reference for the API surface.

- **Source:** `https://api.tailscale.com/api/v2?outputOpenapiSchema=true` (JSON).
- **Format:** JSON only. The drift parser (`internal/oas`) is stdlib-only and parses JSON; the live
  endpoint emits JSON; JSON is fully LLM-consumable. Committing YAML too would either pull a
  third-party YAML parser into that leaf package or risk a second out-of-sync copy.
- **Do not hand-edit.** Refresh by re-fetching upstream:

  ```sh
  curl -fsSL "https://api.tailscale.com/api/v2?outputOpenapiSchema=true" -o spec/tailscale-api.json
  ```

  Refreshing is how you acknowledge a detected drift — re-vendor, then update any decoders/manifest
  the change affects.

Consumed by: `internal/oas` + `internal/tsapi/contract` tests (read it directly), and
`.github/workflows/api-drift.yml` (the drift tool's `-old` baseline).
