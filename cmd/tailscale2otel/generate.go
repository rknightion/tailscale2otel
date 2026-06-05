package main

// Dev bootstrap. `go generate ./...` installs the repo's git hooks — the
// pre-commit hook (.githooks/pre-commit) that keeps generated artifacts
// (chart README.md, values.schema.json, docs/metrics.md) in sync so they don't
// red CI's fail-on-diff gates. Git can't run anything on clone (by design), so
// this — or running scripts/setup.sh directly — wires up core.hooksPath once per
// clone. CI never runs `go generate`, so it has no effect there. See CLAUDE.md.
//
//go:generate sh ../../scripts/setup.sh
