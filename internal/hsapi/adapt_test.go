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

// TestAdaptNode_AuthorizedDefaultsTrue guards issue #64 sub-item 2: Headscale
// has no "authorized" concept, but every node the API returns is registered by
// definition, so Authorized must default true (the only correct value) rather
// than the zero-value false, which would bucket every Headscale device as
// unauthorized in tailscale.devices.count.
func TestAdaptNode_AuthorizedDefaultsTrue(t *testing.T) {
	d := adaptNode(loadNodes(t)[0])
	if !d.Authorized {
		t.Error("Authorized must default true for Headscale nodes (registered by definition), got false")
	}
}

// TestAdaptNode_NoDataFieldsStayZero pins down the full set of Tailscale-only
// RichDevice fields that Headscale cannot populate: they must stay at their
// zero value (never fabricated), and it is the collector's job (gated via
// devices.WithUpdateAvailableData / WithEphemeralData) to avoid emitting them
// as if they were real data.
func TestAdaptNode_NoDataFieldsStayZero(t *testing.T) {
	d := adaptNode(loadNodes(t)[0])
	if d.UpdateAvailable {
		t.Error("UpdateAvailable must stay false (no Headscale source field) — gate emission in the devices collector, don't fabricate")
	}
	if d.IsEphemeral {
		t.Error("IsEphemeral must stay false (no Headscale source field) — gate emission in the devices collector, don't fabricate")
	}
	if d.KeyExpiryDisabled {
		t.Error("KeyExpiryDisabled must stay false (no Headscale source field)")
	}
	if d.ClientVersion != "" || d.TailnetLockKey != "" || d.TailnetLockError != "" {
		t.Error("ClientVersion/TailnetLock* must stay empty (no Headscale source field)")
	}
	if d.HardNAT || len(d.Endpoints) != 0 {
		t.Error("connectivity fields must stay zero (no Headscale source field)")
	}
	if d.ClientSupports.UDP != nil || d.ClientSupports.IPv6 != nil || d.ClientSupports.PCP != nil ||
		d.ClientSupports.PMP != nil || d.ClientSupports.UPnP != nil {
		t.Error("ClientSupports tri-states must stay nil/unknown (no Headscale source field)")
	}
}

// TestAdaptNode_IsExternalDefaultsFalse documents that IsExternal=false is a
// correct constant under Headscale (there is no device-sharing feature, so no
// node can ever be "external"), not a fabricated absent-data zero — unlike
// UpdateAvailable/IsEphemeral above, this one needs no collector-side gating.
func TestAdaptNode_IsExternalDefaultsFalse(t *testing.T) {
	d := adaptNode(loadNodes(t)[0])
	if d.IsExternal {
		t.Error("IsExternal should be false for a Headscale node")
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
	if err != nil || len(attrs.Attributes) != 0 || len(attrs.Expiries) != 0 {
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

// TestAdaptPreAuthKey_UsedOneTimeIsInvalid guards issue #64 sub-item 1: a spent
// (used) non-reusable pre-auth key can never be redeemed again, so it must be
// mapped to tsapi.Key.Invalid so the keys collector stops reporting a live
// expiry gauge / expiring warning for it.
func TestAdaptPreAuthKey_UsedOneTimeIsInvalid(t *testing.T) {
	pk := PreAuthKey{ID: "1", Reusable: false, Used: true, Expiration: "2026-09-08T10:00:00Z"}
	k := adaptPreAuthKey(pk)
	if !k.Invalid {
		t.Error("a used, non-reusable (one-time) key must be mapped to Invalid=true")
	}
}

// TestAdaptPreAuthKey_UsedReusableStaysValid: a reusable key being used once
// does not spend it — it must NOT be marked Invalid.
func TestAdaptPreAuthKey_UsedReusableStaysValid(t *testing.T) {
	pk := PreAuthKey{ID: "2", Reusable: true, Used: true, Expiration: "2026-09-08T10:00:00Z"}
	k := adaptPreAuthKey(pk)
	if k.Invalid {
		t.Error("a reusable key that has been used must NOT be marked Invalid")
	}
}

// TestAdaptPreAuthKey_UnusedOneTimeStaysValid: an unused one-time key is still
// live and must not be marked Invalid.
func TestAdaptPreAuthKey_UnusedOneTimeStaysValid(t *testing.T) {
	pk := PreAuthKey{ID: "3", Reusable: false, Used: false, Expiration: "2026-09-08T10:00:00Z"}
	k := adaptPreAuthKey(pk)
	if k.Invalid {
		t.Error("an unused one-time key must not be marked Invalid")
	}
}

// TestAdaptUser_NoActivityData guards issue #64 sub-item 3: Headscale's user
// API has no device-count or connection-state concept, so adaptUser must leave
// DeviceCount/CurrentlyConnected at their zero value — it is the users
// collector's job (gated via users.WithActivityData(false)) to avoid emitting
// them as if they were real data.
func TestAdaptUser_NoActivityData(t *testing.T) {
	u := adaptUser(User{ID: "1", Name: "alice", Email: "alice@example.org"})
	if u.DeviceCount != 0 {
		t.Errorf("DeviceCount must stay 0 (no Headscale source field), got %d", u.DeviceCount)
	}
	if u.CurrentlyConnected {
		t.Error("CurrentlyConnected must stay false (no Headscale source field)")
	}
}
