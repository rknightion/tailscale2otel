//go:build live

package live

import (
	"context"
	"os"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/tsapi"
	"github.com/rknightion/tailscale2otel/internal/tsapi/contract"
)

// TestLiveContract hits the real Tailscale API read-only using a token passed in via
// TS_API_ACCESS_TOKEN — minted per-run by the workflow from a read-only OAuth client,
// never stored — and asserts every consumed GET still decodes cleanly. Note the skip
// below exits 0: the workflow guarantees both vars are set and treats a skip as a
// failure, so it can't be mistaken for a clean run. Ops flagged LiveSkip carry a
// placeholder path param that would 404 against prod, so they are excluded here
// and covered instead by the fuzz + unit tests.
func TestLiveContract(t *testing.T) {
	token, tailnet := os.Getenv("TS_API_ACCESS_TOKEN"), os.Getenv("TS_TAILNET")
	if token == "" || tailnet == "" {
		t.Skip("TS_API_ACCESS_TOKEN/TS_TAILNET unset — live contract skipped")
	}
	c, err := tsapi.NewClient(tsapi.Options{Tailnet: tailnet, APIKey: token})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	for _, op := range contract.Manifest {
		if op.LiveSkip {
			continue
		}
		t.Run(op.ID, func(t *testing.T) {
			if err := op.Invoke(context.Background(), c); err != nil {
				t.Errorf("%s live decode failed: %v", op.ID, err)
			}
		})
	}
}
