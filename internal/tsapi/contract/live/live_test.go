//go:build live

package live

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi/contract"
)

// TestLiveContract hits the real Tailscale API read-only using a token passed in via
// TS_API_ACCESS_TOKEN — minted per-run by the workflow from a read-only OAuth client,
// never stored — and asserts every consumed GET still decodes cleanly.
//
// Two different "did not run" outcomes, deliberately reported differently:
//
//   - MISCONFIGURATION → t.Skip. The preflight skip below exits 0, so the workflow
//     treats ANY "--- SKIP" in the log as a failure; that is what stops a lane which
//     checked nothing from reporting clean.
//   - NO SUITABLE RESOURCE → a NOT-APPLICABLE log line, never t.Skip. Ops addressed
//     by a path parameter (#424) can only run if the tailnet actually contains a
//     device / a Tailscale Service. That is a legitimate, expected outcome and must
//     not trip the misconfiguration guard — so it deliberately emits no "--- SKIP"
//     marker at all. The workflow parses these lines and reports them in the step
//     summary as "no suitable resource", so they are surfaced rather than silent.
//
// An op that is genuinely un-runnable live (contract.Op.LiveSkip) is excluded outright.
func TestLiveContract(t *testing.T) {
	token, tailnet := os.Getenv("TS_API_ACCESS_TOKEN"), os.Getenv("TS_TAILNET")
	if token == "" || tailnet == "" {
		t.Skip("TS_API_ACCESS_TOKEN/TS_TAILNET unset — live contract skipped")
	}
	c, err := tsapi.NewClient(tsapi.Options{Tailnet: tailnet, APIKey: token})
	if err != nil {
		t.Fatalf("NewClient: %v", contract.SanitizeEvidence(err.Error(), tailnet))
	}
	ctx := context.Background()

	// Resolve real path parameters from list endpoints. Strictly read-only: this
	// never creates or mutates a resource to manufacture a fixture.
	args, unavailable := contract.ResolveLiveArgs(ctx, c)
	for _, res := range sortedResources(unavailable) {
		t.Logf("NOT-APPLICABLE-RESOURCE: %s :: %s", res,
			contract.SanitizeEvidence(unavailable[res], tailnet))
	}

	for _, op := range contract.Manifest {
		if op.LiveSkip {
			continue
		}
		if missing := args.Missing(op.LiveRequires); len(missing) > 0 {
			// Not t.Skip — see the header comment. Everything reported here is
			// already explained by a NOT-APPLICABLE-RESOURCE line above.
			t.Logf("NOT-APPLICABLE: %s :: unresolved %v in this tailnet", op.ID, missing)
			continue
		}
		t.Run(op.ID, func(t *testing.T) {
			if err := op.LiveRun(ctx, c, args); err != nil {
				// The report lands in a PUBLIC issue: sanitize before it leaves.
				t.Errorf("%s live decode failed: %s", op.ID,
					contract.SanitizeEvidence(err.Error(), tailnet))
			}
		})
	}
}

// sortedResources gives the NOT-APPLICABLE-RESOURCE lines a stable order, so two
// runs of the same tailnet produce a diffable log.
func sortedResources(m map[contract.LiveResource]string) []contract.LiveResource {
	out := make([]contract.LiveResource, 0, len(m))
	for r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
