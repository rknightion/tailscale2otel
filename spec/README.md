# Vendored Tailscale OpenAPI spec

`tailscale-api.json` is a vendored copy of the **published Tailscale OpenAPI schema**, committed as
the **baseline** the API-drift CI diffs the live spec against, and as the input the schema-driven
decode-fuzz lane reads. It is also a convenient, LLM-readable reference for the API surface.

- **Source:** `https://api.tailscale.com/api/v2?outputOpenapiSchema=true` (YAML).
- **Vendored format:** JSON only. The drift parser (`internal/oas`) is stdlib-only and parses JSON;
  convert the live endpoint's YAML with `yq` before vendoring. Committing YAML too would either pull a
  third-party YAML parser into that leaf package or risk a second out-of-sync copy.
- **Do not hand-edit.** Refresh by re-fetching upstream:

  ```sh
  (
    set -eu
    upstream=
    converted=
    trap 'rm -f "$upstream" "$converted"' 0
    trap 'exit 1' HUP INT TERM
    upstream=$(mktemp)
    converted=$(mktemp spec/.tailscale-api.json.XXXXXX)
    curl --max-time 90 -fsSL "https://api.tailscale.com/api/v2?outputOpenapiSchema=true" -o "$upstream"
    yq -o=json '.' "$upstream" > "$converted"
    mv -f "$converted" spec/tailscale-api.json
  )
  ```

  Run from the repository root with Mike Farah's `yq`. The subshell stops on download or
  conversion failure and cleans up both temporary files, leaving the baseline untouched. The
  converted JSON is created beside the baseline so the final rename replaces it atomically.

  Refreshing is how you acknowledge a detected drift — re-vendor, then update any decoders/manifest
  the change affects.

Consumed by: `internal/oas` + `internal/tsapi/contract` tests (read it directly), and
`.github/workflows/api-drift.yml` (the drift tool's `-old` baseline).
