# Directory-sharded CodeRabbit review

For a wave-sized diff, run the grouped recipe with the wave base and the
changed directories:

```sh
just review-sharded main "internal/app internal/collector scripts"
```

The default shards are `cmd internal deploy scripts tools`; pass the directory
list as the second positional argument, quoted as one string, when the wave
uses a different set. The recipe parameters are positional, so `base=main` or
`dirs=...` is not accepted: `just` passes it through literally. The recipe runs
`coderabbit review --agent --base <base> --dir <directory>` once per shard and
aggregates each command's raw NDJSON stdout in a securely created temporary
file whose path is printed at completion. Direct script callers can pass
`--output <path>` when they need a stable destination. Each shard has a
15-minute timeout by default; direct callers can adjust it with
`--timeout-seconds`.

Each shard's status line reports both the transport result and its findings.
A shard whose command exited zero and emitted a structured `complete` event is
either `CLEAN (complete, 0 findings)`, meaning its NDJSON holds no
`{"type":"finding"}` events, or `COMPLETE: 2 major, 1 minor`, the finding counts
by severity (`critical`, `major`, `minor`, `trivial`, `info`; a finding with no
severity counts as `unknown`). `FAILED` means a command failed or did not emit
the `complete` event. The final line totals the run, for example
`1 clean, 1 with findings (2 major, 1 minor), 0 failed`.

Findings are not transport failures, so a completed shard with findings still
exits zero. Only a failed shard, including one with no `complete` line or one
that timed out, makes the recipe exit nonzero. Read the summary for finding
counts, and triage the findings in the aggregate before treating a zero exit as
a pass.

## Scope warning

`--dir` hides the rest of the repository. A finding that says a symbol or
wiring is missing can be a false positive because its definition or call site
is outside the shard. Before acting on any missing-symbol or missing-wiring
finding, search the whole tree and verify the complete call graph. Sharding
reduces review payload size; it does not make each shard a complete view of the
repository.
