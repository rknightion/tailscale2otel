# Backlog board conventions

Read before writing to the board: creating or finalizing a task, running a fan-out wave, or
resolving a `#NNN` citation.

Open work is `backlog/`, driven only through the `backlog` CLI. GitHub Issues was retired here and
the filed issues were deleted from GitHub, so `gh issue view <N>` 404s. Historical `#NNN` citations
resolve through the *Closed GitHub issues* doc and `archive/github-issues-2026-08-14.json`
(redacted; `archive/README.md` maps the placeholders). New work is `TSO-NNNN`. Two ID spaces, no
overlap.

The GitHub tracker is still open deliberately: external contributors file there and Renovate's
dependency dashboard lives there. Anything arriving that way becomes a `TSO-NNNN` task, and the
board, not the issue, is where it is worked.

- **Never use `--notes` or `--plan` bare** - they *silently replace* the whole section, destroying
  another session's writes with no warning. Use `--append-notes` and `--append-plan`.
- Finalize in one call, so an interrupted run cannot leave finished work looking unfinished:
  `backlog task edit TSO-0007 --check-ac 1 --check-ac 2 -s Done`. Checking criteria at one step and
  setting status several steps later leaves the task inconsistent if anything interrupts between.
- Hand-editing task, draft, doc, decision or milestone markdown breaks the HTML-comment section
  markers, and a broken section is *silently dropped* at exit 0 - the data is still in the file but
  invisible, until the next write destroys it for real. There is no repair command; `backlog doctor`
  only fixes duplicate task IDs. `backlog/config.yml` is the one file edited by hand, because
  list-valued keys cannot be set through `backlog config set`.
- Never let two agents edit the same task. The concurrency fix covers the edit funnel but not
  reorder, draft saves, the TUI edit path, `doc update` or decision updates.
- `Parked` is a real status, not a synonym for To Do: attempted, blocked, and left with a concrete
  resume boundary. Flattening it loses the most valuable thing a long autonomous run produces.
- **Do not build on decisions, and do not use the MCP surface.** Decisions are half-built upstream -
  no edit, view or update, no supersede mechanism, no validation - so durable reference goes in
  **docs** and tasks stay the unit. MCP is frozen upstream and costs 10-50k tokens of permanent
  context against 1-2k for the CLI.
- Read the *Wave operating model* doc before designing a fan-out wave here; the canonical fan-out
  protocol is rendered onto this board too. Docs load on demand via `backlog doc view <id> --plain`.
