package acl_test

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestRiskFindingsCoverACLSSHAndAutoApproversOnlyOnChange(t *testing.T) {
	doc := `{
		"acls":[{"action":"accept","src":["*"],"dst":["*:443"]}],
		"ssh":[{"action":"accept","src":["*"],"dst":["tag:ops"],"users":["root"]}],
		"autoApprovers":{"routes":{"10.0.0.0/24":["*"]}}
	}`
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: doc, ETag: "rev-one"}}
	c := acl.New(api, 0, time.Now)

	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	assertRiskClasses(t, logsNamed(rec1, acl.EventRiskyRule), map[string]int{
		"unrestricted":          1,
		"ssh_wildcard":          1,
		"autoapprover_wildcard": 1,
	})

	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("unchanged Collect: %v", err)
	}
	if got := logsNamed(rec2, acl.EventRiskyRule); len(got) != 0 {
		t.Fatalf("unchanged risk findings = %d, want 0", len(got))
	}
}

func assertRiskClasses(t *testing.T, logs []telemetrytest.LogRecord, want map[string]int) {
	t.Helper()
	got := make(map[string]int)
	for _, log := range logs {
		got[log.Attrs["tailscale.acl.risk_class"]]++
	}
	if len(logs) != 3 {
		t.Fatalf("risk logs = %d, want 3: %+v", len(logs), logs)
	}
	for class, count := range want {
		if got[class] != count {
			t.Errorf("risk class %q = %d, want %d (all=%v)", class, got[class], count, got)
		}
	}
}
