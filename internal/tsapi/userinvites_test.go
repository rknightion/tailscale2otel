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

func TestUserInvites_DecodesPopulated(t *testing.T) {
	const body = `[
	  {"id":"inv-1","role":"member","tailnetId":"123","inviterId":"u-1","email":"a@b.com","inviteUrl":"https://login.tailscale.com/admin/invite/abc","created":"2026-01-01T10:00:00Z","accepted":false,"extraIgnored":"x"},
	  {"id":"inv-2","role":"admin","tailnetId":"123","inviterId":"u-2","email":"c@d.com","inviteUrl":"https://login.tailscale.com/admin/invite/def","created":"2026-02-02T11:30:00Z","accepted":true}
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
	if len(invites) != 2 {
		t.Fatalf("len(invites) = %d, want 2", len(invites))
	}

	i0 := invites[0]
	if i0.ID != "inv-1" || i0.Role != "member" || i0.TailnetID != "123" || i0.InviterID != "u-1" {
		t.Fatalf("i0 = %+v", i0)
	}
	if i0.Email != "a@b.com" || i0.InviteURL != "https://login.tailscale.com/admin/invite/abc" {
		t.Fatalf("i0 url/email = %+v", i0)
	}
	wantCreated, _ := time.Parse(time.RFC3339, "2026-01-01T10:00:00Z")
	if !i0.Created.Equal(wantCreated) {
		t.Fatalf("i0.Created = %v", i0.Created)
	}
	if i0.Accepted {
		t.Fatalf("i0.Accepted = true, want false")
	}

	i1 := invites[1]
	if i1.ID != "inv-2" || i1.Role != "admin" || !i1.Accepted {
		t.Fatalf("i1 = %+v", i1)
	}
}
