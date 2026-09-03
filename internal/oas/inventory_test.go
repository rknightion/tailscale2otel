package oas_test

import (
	"os"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/oas"
)

const multiVerbSpec = `{
  "paths": {
    "/tailnet/{tailnet}/acl": {
      "get":  {"operationId": "getPolicyFile", "summary": "Get the policy file"},
      "post": {"operationId": "setPolicyFile"}
    },
    "/tailnet/{tailnet}/acl/validate": {
      "post": {"operationId": "validateAndTestPolicyFile", "summary": "Validate and test"}
    },
    "/device/{deviceId}": {
      "delete": {"operationId": "deleteDevice"},
      "patch":  {"operationId": "patchDevice"},
      "put":    {"operationId": "putDevice"}
    },
    "/nameless": {"get": {"summary": "no operationId — must be skipped"}}
  }
}`

func TestAllOperations_CoversEveryVerb(t *testing.T) {
	s, err := oas.ParseSpec([]byte(multiVerbSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	all := s.AllOperations()
	want := map[string]struct{ method, path string }{
		"getPolicyFile":             {"get", "/tailnet/{tailnet}/acl"},
		"setPolicyFile":             {"post", "/tailnet/{tailnet}/acl"},
		"validateAndTestPolicyFile": {"post", "/tailnet/{tailnet}/acl/validate"},
		"deleteDevice":              {"delete", "/device/{deviceId}"},
		"patchDevice":               {"patch", "/device/{deviceId}"},
		"putDevice":                 {"put", "/device/{deviceId}"},
	}
	if len(all) != len(want) {
		t.Fatalf("AllOperations() has %d entries, want %d: %+v", len(all), len(want), all)
	}
	for id, w := range want {
		got, ok := all[id]
		if !ok {
			t.Errorf("missing %q", id)
			continue
		}
		if got.Method != w.method || got.Path != w.path {
			t.Errorf("%s = %s %s, want %s %s", id, got.Method, got.Path, w.method, w.path)
		}
	}
	if all["getPolicyFile"].Summary != "Get the policy file" {
		t.Errorf("summary not captured: %q", all["getPolicyFile"].Summary)
	}
}

// TestAllOperations_DoesNotDisturbOps: Ops stays GET-only. Widening it would
// change what the decode-drift classifier compares and what the fuzz lane
// synthesizes bodies for.
func TestAllOperations_DoesNotDisturbOps(t *testing.T) {
	s, err := oas.ParseSpec([]byte(multiVerbSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if len(s.Ops) != 1 {
		t.Fatalf("Ops = %d entries, want 1 (GET only): %v", len(s.Ops), s.Ops)
	}
	if _, ok := s.Ops["getPolicyFile"]; !ok {
		t.Error("Ops lost the GET operation")
	}
}

// TestAllOperations_VendoredSpecShape pins the real spec's verb census, so a
// refresh that silently drops half the surface is visible.
func TestAllOperations_VendoredSpecShape(t *testing.T) {
	b, err := os.ReadFile("../../spec/tailscale-api.json")
	if err != nil {
		t.Fatalf("read vendored spec: %v", err)
	}
	s, err := oas.ParseSpec(b)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	all := s.AllOperations()
	if len(all) < 80 {
		t.Fatalf("AllOperations() = %d ops, expected >=80 in the vendored spec", len(all))
	}
	if len(all) <= len(s.Ops) {
		t.Fatalf("AllOperations() (%d) must exceed the GET-only Ops (%d)", len(all), len(s.Ops))
	}
	if op, ok := all["validateAndTestPolicyFile"]; !ok || op.Method != "post" {
		t.Errorf("vendored spec: validateAndTestPolicyFile = %+v, want a POST entry", op)
	}
	for id, op := range all {
		if op.Method == "" || op.Path == "" {
			t.Errorf("%s has empty method/path: %+v", id, op)
		}
	}
}

// TestReadCapable classifies which verbs can be treated as read operations.
// GET always is; POST only when explicitly allowlisted by the caller, because
// Tailscale publishes at least one read-only POST (/acl/validate).
func TestReadCapable(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   bool
	}{
		{"get", true},
		{"GET", true},
		{"head", true},
		{"post", false},
		{"put", false},
		{"patch", false},
		{"delete", false},
	} {
		if got := oas.ReadCapable(tc.method); got != tc.want {
			t.Errorf("ReadCapable(%q) = %v, want %v", tc.method, got, tc.want)
		}
	}
}
