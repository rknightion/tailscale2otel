package audit_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/audit"
)

// Sample shaped per the Tailscale GET /tailnet/{tailnet}/logging/configuration response.
const configBody = `{
  "version": "1.1",
  "tailnet": "example.com",
  "logs": [
    {
      "eventTime": "2024-06-06T15:25:26Z",
      "type": "CONFIG",
      "eventGroupID": "abc123",
      "origin": "ADMIN_CONSOLE",
      "actor": {"id":"u1","type":"USER","loginName":"a@example.com","displayName":"Lion"},
      "target": {"id":"n1","name":"node.ts.net","type":"NODE","property":"ALLOWED_IPS"},
      "action": "CREATE",
      "old": null,
      "new": null,
      "actionDetails": "x",
      "error": ""
    }
  ]
}`

func TestDecodeConfigurationResponse(t *testing.T) {
	var resp audit.ConfigurationResponse
	if err := json.Unmarshal([]byte(configBody), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Tailnet != "example.com" {
		t.Fatalf("tailnet = %q, want example.com", resp.Tailnet)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(resp.Logs))
	}
	e := resp.Logs[0]
	if e.EventTime.IsZero() {
		t.Fatal("eventTime not parsed")
	}
	if e.EventGroupID != "abc123" {
		t.Fatalf("eventGroupID = %q, want abc123", e.EventGroupID)
	}
	if e.Origin != "ADMIN_CONSOLE" {
		t.Fatalf("origin = %q", e.Origin)
	}
	if e.Actor.LoginName != "a@example.com" || e.Actor.Type != "USER" {
		t.Fatalf("actor wrong: %+v", e.Actor)
	}
	if e.Target.Type != "NODE" || e.Target.Property != "ALLOWED_IPS" {
		t.Fatalf("target wrong: %+v", e.Target)
	}
	if e.Action != "CREATE" {
		t.Fatalf("action = %q, want CREATE", e.Action)
	}
}
