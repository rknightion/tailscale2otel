# Directory-sharded CodeRabbit review

For a wave-sized diff, run the grouped recipe with the wave base and the
changed directories:

```sh
just review-sharded base=main dirs="internal/app internal/collector scripts"
```

The default shards are `cmd internal deploy scripts tools`; pass `dirs=` when
the wave uses a different set. The recipe runs
`coderabbit review --agent --base <base> --dir <directory>` once per shard and
aggregates each command's raw NDJSON stdout in a securely created temporary
file whose path is printed at completion. Direct script callers can pass
`--output <path>` when they need a stable destination. Each shard has a
15-minute timeout by default; direct callers can adjust it with
`--timeout-seconds`.

The status printed for each shard is a transport result, not a finding verdict:
`CLEAN` means the command exited zero and emitted a structured `complete` event;
`FAILED` means a command failed or did not emit that event. Any failed shard,
including one with no `complete` line, makes the recipe exit nonzero. A shard
can therefore be `CLEAN` while its aggregate still contains findings that need
triage.

## Scope warning

`--dir` hides the rest of the repository. A finding that says a symbol or
wiring is missing can be a false positive because its definition or call site
is outside the shard. Before acting on any missing-symbol or missing-wiring
finding, search the whole tree and verify the complete call graph. Sharding
reduces review payload size; it does not make each shard a complete view of the
repository.
