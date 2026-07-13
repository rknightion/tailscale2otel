package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// keysAllFixture mirrors a trimmed GET /keys?all=true response: one reusable
// preauthorized auth key (with expiry), one OAuth client (scopes, no expiry, no
// capabilities), one API token (scopes + expiry), and one revoked+invalid auth
// key. Shapes match .capture/keys.json. userId/capabilities.devices.create.tags
// values are not present in .capture/ on this machine (no local captures); they
// are taken from the vendored OpenAPI spec's listTailnetKeys example
// (spec/tailscale-api.json, "userId":"uscwcTtzzo11DEVEL", tags ["tag:example"]).
const keysAllFixture = `{"keys":[
  {
    "id":"kAuth11CNTRL","keyType":"auth","description":"ci runner",
    "created":"2026-01-01T00:00:00Z","expires":"2026-04-01T00:00:00Z",
    "invalid":false,"userId":"uscwcTtzzo11DEVEL",
    "capabilities":{"devices":{"create":{"reusable":true,"ephemeral":false,"preauthorized":true,"tags":["tag:ci","tag:example"]}}}
  },
  {
    "id":"kClient11CNTRL","keyType":"client","description":"terraform",
    "created":"2026-02-01T00:00:00Z","updated":"2026-02-02T00:00:00Z",
    "scopes":["all:read","devices:core"],"invalid":false
  },
  {
    "id":"kApi11CNTRL","keyType":"api","description":"prod token",
    "created":"2026-03-01T00:00:00Z","expires":"2026-06-01T00:00:00Z",
    "scopes":["all"],"invalid":false,"userId":"uscwcTtzzo11DEVEL"
  },
  {
    "id":"kRevoked11CNTRL","keyType":"auth","description":"revoked runner",
    "created":"2026-01-15T00:00:00Z","revoked":"2026-05-01T00:00:00Z",
    "invalid":true,
    "capabilities":{"devices":{"create":{"reusable":true}}}
  }
]}`

func TestKeysRich_DecodesUnifiedKeyModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/keys" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("all"); got != "true" {
			http.Error(w, "all = "+got, http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(keysAllFixture))
	}))
	defer srv.Close()

	ks, err := newClient(t, srv.URL).KeysRich(context.Background())
	if err != nil {
		t.Fatalf("KeysRich: %v", err)
	}
	if len(ks) != 4 {
		t.Fatalf("len(ks) = %d, want 4", len(ks))
	}

	byID := map[string]int{}
	for i, k := range ks {
		byID[k.ID] = i
	}

	auth := ks[byID["kAuth11CNTRL"]]
	if auth.Type != "auth" {
		t.Errorf("auth.Type = %q, want auth", auth.Type)
	}
	if !auth.Reusable || auth.Ephemeral || !auth.Preauthorized {
		t.Errorf("auth caps = reusable:%v ephemeral:%v preauthorized:%v, want true/false/true", auth.Reusable, auth.Ephemeral, auth.Preauthorized)
	}
	if !auth.Expires.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("auth.Expires = %v, want 2026-04-01", auth.Expires)
	}
	if len(auth.Scopes) != 0 {
		t.Errorf("auth.Scopes = %v, want none", auth.Scopes)
	}
	if !auth.Revoked.IsZero() {
		t.Errorf("auth.Revoked = %v, want zero (not revoked)", auth.Revoked)
	}
	if !auth.Updated.IsZero() {
		t.Errorf("auth.Updated = %v, want zero (absent on wire)", auth.Updated)
	}
	if auth.UserID != "uscwcTtzzo11DEVEL" {
		t.Errorf("auth.UserID = %q, want uscwcTtzzo11DEVEL", auth.UserID)
	}
	if got := auth.Tags; len(got) != 2 || got[0] != "tag:ci" || got[1] != "tag:example" {
		t.Errorf("auth.Tags = %v, want [tag:ci tag:example]", got)
	}

	client := ks[byID["kClient11CNTRL"]]
	if client.Type != "client" {
		t.Errorf("client.Type = %q, want client", client.Type)
	}
	if len(client.Scopes) != 2 {
		t.Errorf("client.Scopes = %v, want 2", client.Scopes)
	}
	if !client.Expires.IsZero() {
		t.Errorf("client.Expires = %v, want zero (OAuth clients do not expire)", client.Expires)
	}
	if !client.Updated.Equal(time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("client.Updated = %v, want 2026-02-02", client.Updated)
	}
	if !client.Revoked.IsZero() {
		t.Errorf("client.Revoked = %v, want zero (not revoked)", client.Revoked)
	}
	if client.UserID != "" {
		t.Errorf("client.UserID = %q, want empty (OAuth clients have no owning user)", client.UserID)
	}

	api := ks[byID["kApi11CNTRL"]]
	if api.Type != "api" {
		t.Errorf("api.Type = %q, want api", api.Type)
	}
	if api.Expires.IsZero() {
		t.Errorf("api.Expires is zero, want populated")
	}
	if api.UserID != "uscwcTtzzo11DEVEL" {
		t.Errorf("api.UserID = %q, want uscwcTtzzo11DEVEL", api.UserID)
	}

	revoked := ks[byID["kRevoked11CNTRL"]]
	if !revoked.Revoked.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("revoked.Revoked = %v, want 2026-05-01", revoked.Revoked)
	}
	if !revoked.Invalid {
		t.Errorf("revoked.Invalid = false, want true")
	}
	if revoked.UserID != "" {
		t.Errorf("revoked.UserID = %q, want empty (not on wire)", revoked.UserID)
	}
	if len(revoked.Tags) != 0 {
		t.Errorf("revoked.Tags = %v, want none (no tags on wire)", revoked.Tags)
	}
}
