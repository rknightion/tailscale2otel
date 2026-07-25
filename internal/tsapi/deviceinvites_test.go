package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// deviceInvitesTimestampFixture exercises the #413 lifecycle fields: one invite
// with both created and lastEmailSentAt, one with created only (a link-only
// share is never emailed, so upstream never sets lastEmailSentAt), and one with
// an empty-string created — the shape that took the whole devices decode down in
// #48 and which a plain time.Time would reject here too.
const deviceInvitesTimestampFixture = `[
  {"id":"di1","accepted":false,"email":"a@example.com",
   "created":"2026-04-03T21:38:49Z","lastEmailSentAt":"2026-04-05T09:00:00Z",
   "inviteUrl":"https://login.tailscale.com/admin/invite/aaa"},
  {"id":"di2","accepted":false,"created":"2026-04-01T00:00:00Z"},
  {"id":"di3","accepted":false,"created":"","lastEmailSentAt":""}
]`

func TestDeviceInvites_DecodesLifecycleTimestamps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(deviceInvitesTimestampFixture))
	}))
	defer srv.Close()

	invs, err := newClient(t, srv.URL).DeviceInvites(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DeviceInvites: %v", err)
	}
	if len(invs) != 3 {
		t.Fatalf("len(invs) = %d, want 3", len(invs))
	}

	wantCreated := time.Date(2026, 4, 3, 21, 38, 49, 0, time.UTC)
	if !invs[0].Created.Equal(wantCreated) {
		t.Errorf("invs[0].Created = %v, want %v", invs[0].Created, wantCreated)
	}
	wantSent := time.Date(2026, 4, 5, 9, 0, 0, 0, time.UTC)
	if !invs[0].LastEmailSentAt.Equal(wantSent) {
		t.Errorf("invs[0].LastEmailSentAt = %v, want %v", invs[0].LastEmailSentAt, wantSent)
	}

	if invs[1].Created.IsZero() {
		t.Error("invs[1].Created is zero, want the decoded creation time")
	}
	if !invs[1].LastEmailSentAt.IsZero() {
		t.Errorf("invs[1].LastEmailSentAt = %v, want zero (never emailed)", invs[1].LastEmailSentAt)
	}

	if !invs[2].Created.IsZero() || !invs[2].LastEmailSentAt.IsZero() {
		t.Errorf("invs[2] = %+v, want zero times for empty-string wire values", invs[2])
	}
}
