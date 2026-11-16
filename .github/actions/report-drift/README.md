# report-drift composite action

Shared triage step for the Tailscale API drift lanes. On detection it:

1. **Upserts a deduplicated tracking issue** — finds the open issue carrying the
   lane's marker label and comments on it, or creates one. One issue per lane, not
   one per run.
2. **Enriches with Claude** (only when `ANTHROPIC_API_KEY` is set in the job env) —
   runs `anthropics/claude-code-action` to explain the break and comment the concrete
   fix on the issue. SKIPs cleanly when the key is absent. It does NOT open a pull
   request: the calling workflows grant only `contents: read`, which is enough for the
   mandatory issue/comment outputs but not for pushing a fix branch — the maintainer
   applies the commented fix by hand.
3. **Fails the job** (`exit 1`) so the scheduled run goes red and you get the email.

## Inputs

| input | required | description |
| --- | --- | --- |
| `lane-label` | yes | marker label: `api-drift`, `api-operation-drift`, `clientlib-drift`, `live-contract`, `iana-drift`, `changelog-review`, or `fuzz-finding` |
| `title` | yes | issue title (used only when creating a new issue) |
| `report-path` | yes | path to a markdown report file (issue/comment body) |

## One-time setup (required before any lane can file an issue)

The marker labels must exist — `gh issue create --label` fails on a missing label:

```sh
gh label create api-drift      -c FBCA04 -d "Tailscale OpenAPI spec drift on consumed operations"
gh label create clientlib-drift -c FBCA04 -d "tailscale-client-go/v2 main/latest breaks our build"
gh label create live-contract  -c FBCA04 -d "Live Tailscale API contract failing"
gh label create api-operation-drift -c FBCA04 -d "Tailscale published an unclassified API operation"
gh label create iana-drift -c FBCA04 -d "Committed IANA service registry is out of date"
gh label create changelog-review -c FBCA04 -d "Tailscale changelog entries awaiting a verdict"
gh label create fuzz-finding -c FBCA04 -d "Scheduled fuzzing found a crasher"
```

Calling workflows must grant `issues: write`. `pull-requests: write` is NOT required —
this action never opens a pull request (see above); the calling workflows use only
`contents: read` + `issues: write`. Set the optional `ANTHROPIC_API_KEY` repo secret to
enable Claude enrichment.
