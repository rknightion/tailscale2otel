package audit_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
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

func TestConfigurationResponseTailnetName(t *testing.T) {
	// Live API form: the org name comes back under "tailnetId" (the published
	// OpenAPI's "tailnet" field name is inaccurate — verified against live API).
	var r audit.ConfigurationResponse
	if err := json.Unmarshal([]byte(`{"version":"1.1","tailnetId":"m7kni.io","logs":null}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.TailnetName(); got != "m7kni.io" {
		t.Errorf("tailnetId form: TailnetName()=%q, want m7kni.io", got)
	}
	// Spec form: legacy "tailnet" key still honored as a fallback.
	r = audit.ConfigurationResponse{}
	_ = json.Unmarshal([]byte(`{"tailnet":"example.com","logs":[]}`), &r)
	if got := r.TailnetName(); got != "example.com" {
		t.Errorf("tailnet form: TailnetName()=%q, want example.com", got)
	}
	// Neither present -> empty.
	r = audit.ConfigurationResponse{}
	_ = json.Unmarshal([]byte(`{"logs":[]}`), &r)
	if got := r.TailnetName(); got != "" {
		t.Errorf("empty: TailnetName()=%q, want empty", got)
	}
}
