package hsapi

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func loadNodes(t *testing.T) []Node {
	t.Helper()
	b, err := os.ReadFile("testdata/nodes.json")
	if err != nil {
		t.Fatal(err)
	}
	var r nodesResponse
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r.Nodes
}

func TestAdaptNode(t *testing.T) {
	d := adaptNode(loadNodes(t)[0])
	if d.Name != "laptop" || d.Hostname != "laptop.example.ts.net" {
		t.Errorf("name/hostname = %q/%q", d.Name, d.Hostname)
	}
	if !d.ConnectedToControl {
		t.Error("online should map to ConnectedToControl")
	}
	if d.User != "alice@example.org" {
		t.Errorf("User = %q, want alice@example.org", d.User)
	}
	if len(d.AdvertisedRoutes) != 3 || len(d.EnabledRoutes) != 1 {
		t.Errorf("routes adv=%v enabled=%v", d.AdvertisedRoutes, d.EnabledRoutes)
	}
	if d.LastSeen.IsZero() || d.Expires.IsZero() {
		t.Error("timestamps should parse")
	}
	if d.OS != "" || d.DERPLatency != nil || len(d.Endpoints) != 0 {
		t.Error("tailscale-only fields must be zero/empty under headscale")
	}
}

func TestProviderControlPlaneEmptyAuxiliaries(t *testing.T) {
	p := &Provider{} // hsapi.Provider wraps a *Client; nil client ok for these
	inv, err := p.DeviceInvites(context.Background(), "1")
	if err != nil || len(inv) != 0 {
		t.Errorf("DeviceInvites should be empty,nil; got %v,%v", inv, err)
	}
	ui, err := p.UserInvites(context.Background())
	if err != nil || len(ui) != 0 {
		t.Errorf("UserInvites should be empty,nil; got %v,%v", ui, err)
	}
	attrs, err := p.DevicePostureAttributes(context.Background(), "1")
	if err != nil || len(attrs) != 0 {
		t.Errorf("DevicePostureAttributes should be empty,nil; got %v,%v", attrs, err)
	}
}

func TestAdaptKeysAndACL(t *testing.T) {
	pk := PreAuthKey{ID: "3", Reusable: true, Ephemeral: false, Expiration: "2026-09-08T10:00:00Z", CreatedAt: "2026-01-01T00:00:00Z"}
	k := adaptPreAuthKey(pk)
	if k.Type != "auth" || !k.Reusable || k.Expires.IsZero() {
		t.Errorf("preauth key adapt = %+v", k)
	}
	ak := APIKey{ID: "9", Prefix: "abcd", Expiration: "2026-09-08T10:00:00Z"}
	k2 := adaptAPIKey(ak)
	if k2.Type != "api" || k2.Description != "abcd" {
		t.Errorf("api key adapt = %+v", k2)
	}
	raw := adaptPolicy(&Policy{Policy: "{ \"acls\": [] }", UpdatedAt: "2026-06-08T10:00:00Z"})
	if raw.HuJSON == "" || raw.ETag == "" {
		t.Errorf("policy adapt = %+v", raw)
	}
}
