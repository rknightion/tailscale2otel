# report-drift composite action

Shared triage step for the Tailscale API drift lanes. On detection it:

1. **Upserts a deduplicated tracking issue** — finds the open issue carrying the
   lane's marker label and comments on it, or creates one. One issue per lane, not
   one per run.
2. **Enriches with Claude** (only when `ANTHROPIC_API_KEY` is set in the job env) —
   runs `anthropics/claude-code-action` to explain the break, comment the fix on the
   issue, and optionally open a **draft** PR when the fix is mechanical. SKIPs cleanly
   when the key is absent.
3. **Fails the job** (`exit 1`) so the scheduled run goes red and you get the email.

## Inputs

| input | required | description |
| --- | --- | --- |
| `lane-label` | yes | marker label: `api-drift`, `clientlib-drift`, or `live-contract` |
| `title` | yes | issue title (used only when creating a new issue) |
| `report-path` | yes | path to a markdown report file (issue/comment body) |

## One-time setup (required before any lane can file an issue)

The marker labels must exist — `gh issue create --label` fails on a missing label:

```sh
gh label create api-drift      -c FBCA04 -d "Tailscale OpenAPI spec drift on consumed operations"
gh label create clientlib-drift -c FBCA04 -d "tailscale-client-go/v2 main/latest breaks our build"
gh label create live-contract  -c FBCA04 -d "Live Tailscale API contract failing"
```

Calling workflows must grant `issues: write` (and `pull-requests: write` for the draft
PR). Set the optional `ANTHROPIC_API_KEY` repo secret to enable Claude enrichment.
