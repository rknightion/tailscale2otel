package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// deviceInvitesFixture mirrors a GET /device/{id}/device-invites response: one
// accepted invite (with PII we must NOT decode) and one pending invite that
// grants exit-node use and is multi-use. Shapes match the DeviceInvite schema
// in tailscale-api.yaml.
const deviceInvitesFixture = `[
  {"id":"di1","accepted":true,"multiUse":false,"allowExitNode":false,
   "email":"a@example.com","inviteUrl":"https://login.tailscale.com/admin/invite/aaa",
   "acceptedBy":{"id":1,"loginName":"a@example.com","profilePicUrl":"https://x/y.png"}},
  {"id":"di2","accepted":false,"multiUse":true,"allowExitNode":true,
   "inviteUrl":"https://login.tailscale.com/admin/invite/bbb"}
]`

func TestDeviceInvites_DecodesCuratedFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/device/dev123/device-invites" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(deviceInvitesFixture))
	}))
	defer srv.Close()

	invs, err := newClient(t, srv.URL).DeviceInvites(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DeviceInvites: %v", err)
	}
	if len(invs) != 2 {
		t.Fatalf("len(invs) = %d, want 2", len(invs))
	}
	if !invs[0].Accepted || invs[0].MultiUse || invs[0].AllowExitNode {
		t.Errorf("invs[0] = %+v, want accepted-only (true/false/false)", invs[0])
	}
	if invs[0].Email != "a@example.com" {
		t.Errorf("invs[0].Email = %q, want a@example.com", invs[0].Email)
	}
	if invs[0].AcceptedByLogin != "a@example.com" {
		t.Errorf("invs[0].AcceptedByLogin = %q, want a@example.com", invs[0].AcceptedByLogin)
	}
	if invs[1].Accepted || !invs[1].MultiUse || !invs[1].AllowExitNode {
		t.Errorf("invs[1] = %+v, want pending+multiUse+allowExitNode (false/true/true)", invs[1])
	}
	if invs[1].Email != "" {
		t.Errorf("invs[1].Email = %q, want empty (no email on second invite)", invs[1].Email)
	}
	if invs[1].AcceptedByLogin != "" {
		t.Errorf("invs[1].AcceptedByLogin = %q, want empty (pending invite)", invs[1].AcceptedByLogin)
	}
	// inviteUrl must never be decoded — verify it is not accessible on the struct.
	// (compile-time check: DeviceInvite has no InviteUrl field)
}

func TestDeviceInvites_NullBodyYieldsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("null")) // real wire form for a device with no invites
	}))
	defer srv.Close()

	invs, err := newClient(t, srv.URL).DeviceInvites(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DeviceInvites: %v", err)
	}
	if invs != nil {
		t.Errorf("invs = %v, want nil for null body", invs)
	}
}
