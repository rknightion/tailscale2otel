// Package live holds the build-tagged live Tailscale API contract test. Without
// the "live" build tag it is empty, so normal `go test ./...` neither builds nor
// runs it. The live lane (see .github/workflows/live-contract.yml) runs it with
// `-tags live` against the real API using a short-lived OIDC-minted read-only
// token.
package live
