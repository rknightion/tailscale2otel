# GitHub Issues archive

`github-issues-2026-08-14.json` is the complete contents of this repository's GitHub Issues tracker
as of **2026-08-14**, captured immediately before the issues themselves were deleted. Work was then
tracked on a Backlog.md board and is tracked in GitHub issues again; the board's *Closed GitHub
issues* document is the browsable index to this file, and this file holds the bodies and replies
behind it.

**This is the record, not a convenience copy.** The issues it describes no longer exist on GitHub, so
`gh issue view <N>` will 404. Anything in the repository that cites `#NNN` resolves here, with two
exceptions: a number below 795 may instead be a pull request or a bot-filed issue, which GitHub still
has, and a board id such as `TSO-0001` is not an issue number at all: `just issue TSO-0001` prints the
issue it became.

## What it contains

402 issues and all 481 comments, verified against the REST API's own per-issue comment counts before
capture. Per issue: number, title, body, state, state reason, author, labels, milestone, assignees,
created/updated/closed timestamps, URL, and every comment with its author and timestamp.

```sh
jq '.[] | select(.number == 526)' archive/github-issues-2026-08-14.json          # one issue
jq -r '.[] | select(.number == 526) | .comments[].body' archive/…                # its replies
jq -r '.[] | select(.title | test("dashboard"; "i")) | "#\(.number) \(.title)"' archive/…
```

## It is redacted, and the placeholders are stable

The tracker was a live-lab project, and issue bodies quoted host names, tailnet addresses and account
identifiers that this repository's own rules keep out of tracked files. Those were replaced before
the file was committed — 93 substitutions over 15 distinct values.

| Placeholder | Was |
| --- | --- |
| `<host-N>`, `<device-N>` | machine and device names |
| `<tailnet-N>` | a tailnet name (`example.ts.net` is left intact — it is a documentation example) |
| `<tailnet-ip-N>`, `<lan-ip-N>` | tailnet CGNAT and RFC1918 addresses |
| `<email-N>` | email addresses |
| `<token-N>` | credential-shaped strings (all four were placeholders in docs, not live secrets) |
| `<lab-domain>` | a private domain |

**One distinct real value maps to one placeholder throughout**, so a reader can still tell that two
issues discuss the same host without learning which host. Unnumbered `<tailnet>` and `<token>`
strings that appear in the text are original documentation placeholders written by the issue author,
not redactions.

Verification swept the decoded string fields, **not** the serialized JSON. That distinction is not
pedantic: in `json.dumps` output an escape such as `\n` leaves a literal `n` immediately before the
following word, which breaks a `\b` word boundary and silently undercounts. Scanning the blob found
51 occurrences of one host name where scanning the fields found 57. A sweep run the convenient way
would have reported this file clean while it still leaked.
