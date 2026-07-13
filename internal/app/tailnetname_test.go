package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
)

type fakeResolver struct {
	resp audit.ConfigurationResponse
	err  error
}

func (f fakeResolver) ConfigAuditLogs(_ context.Context, _, _ time.Time) (audit.ConfigurationResponse, error) {
	return f.resp, f.err
}

func TestResolveTailnetName(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	// Resolves from the envelope tailnetId.
	if got := resolveTailnetName(context.Background(), fakeResolver{resp: audit.ConfigurationResponse{TailnetID: "m7kni.io"}}, now, nil); got != "m7kni.io" {
		t.Errorf("got %q, want m7kni.io", got)
	}
	// API error -> empty (caller keeps "-").
	if got := resolveTailnetName(context.Background(), fakeResolver{err: errors.New("403")}, now, nil); got != "" {
		t.Errorf("error case got %q, want empty", got)
	}
	// Empty envelope -> empty.
	if got := resolveTailnetName(context.Background(), fakeResolver{}, now, nil); got != "" {
		t.Errorf("empty envelope got %q, want empty", got)
	}
}
