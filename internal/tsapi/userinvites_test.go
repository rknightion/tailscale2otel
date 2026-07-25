package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUserInvites_NullBodyReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/user-invites" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`null`))
	}))
	defer srv.Close()

	invites, err := newClient(t, srv.URL).UserInvites(context.Background())
	if err != nil {
		t.Fatalf("UserInvites: %v", err)
	}
	if len(invites) != 0 {
		t.Fatalf("invites = %v, want empty", invites)
	}
}

func TestUserInvites_EmptyArrayReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	invites, err := newClient(t, srv.URL).UserInvites(context.Background())
	if err != nil {
		t.Fatalf("UserInvites: %v", err)
	}
	if len(invites) != 0 {
		t.Fatalf("invites = %v, want empty", invites)
	}
}

// TestUserInvites_DecodesManualLinkInvite covers a shareable-link invite: the
// wire body carries no lastEmailSentAt at all (the invite was never emailed),
// which must decode to the zero time rather than erroring.
func TestUserInvites_DecodesManualLinkInvite(t *testing.T) {
	const body = `[
	  {"id":"inv-1","role":"member","tailnetId":"123","inviterId":"u-1","email":"","inviteUrl":"https://login.tailscale.com/admin/invite/abc"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	invites, err := newClient(t, srv.URL).UserInvites(context.Background())
	if err != nil {
		t.Fatalf("UserInvites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("len(invites) = %d, want 1", len(invites))
	}

	i0 := invites[0]
	if i0.ID != "inv-1" || i0.Role != "member" || i0.TailnetID != "123" || i0.InviterID != "u-1" {
		t.Fatalf("i0 = %+v", i0)
	}
	if i0.Email != "" || i0.InviteURL != "https://login.tailscale.com/admin/invite/abc" {
		t.Fatalf("i0 url/email = %+v", i0)
	}
	if !i0.LastEmailSentAt.IsZero() {
		t.Fatalf("i0.LastEmailSentAt = %v, want zero (manual-link invite)", i0.LastEmailSentAt)
	}
}

// TestUserInvites_DecodesEmailedInvite covers an invite that was sent by
// email: lastEmailSentAt is populated on the wire and must decode to the
// matching time.Time (#412's raw material).
func TestUserInvites_DecodesEmailedInvite(t *testing.T) {
	const body = `[
	  {"id":"inv-2","role":"admin","tailnetId":"123","inviterId":"u-2","email":"c@d.com","inviteUrl":"https://login.tailscale.com/admin/invite/def","lastEmailSentAt":"2026-02-02T11:30:00Z","extraIgnored":"x"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	invites, err := newClient(t, srv.URL).UserInvites(context.Background())
	if err != nil {
		t.Fatalf("UserInvites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("len(invites) = %d, want 1", len(invites))
	}

	i1 := invites[0]
	if i1.ID != "inv-2" || i1.Role != "admin" || i1.Email != "c@d.com" {
		t.Fatalf("i1 = %+v", i1)
	}
	wantSent, _ := time.Parse(time.RFC3339, "2026-02-02T11:30:00Z")
	if !i1.LastEmailSentAt.Equal(wantSent) {
		t.Fatalf("i1.LastEmailSentAt = %v, want %v", i1.LastEmailSentAt, wantSent)
	}
}
