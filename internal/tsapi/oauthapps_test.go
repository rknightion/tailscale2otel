package tsapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// oauthAppsFixture mirrors a trimmed GET /oauth-apps response. Shapes follow
// the vendored OpenAPI spec's OAuthApp example (spec/tailscale-api.json);
// .capture/oauth-apps-live-20260713.json has no apps on the lab tailnet, so
// there is no real capture to mirror redirectURIs/clientSecret shapes from.
// clientSecret is included on the wire to prove the decoder never surfaces it.
const oauthAppsFixture = `{"oauthApps":[
  {
    "id":"a123456CNTRL","name":"my-oauth-app","description":"provisioner",
    "redirectURIs":["https://example.com/oauth/callback","https://example.com/oauth/callback2"],
    "scopes":["auth_keys:create"],"allowedNodeAttributes":["custom:myattribute"],
    "clientSecret":"xxxxx",
    "created":"2026-02-01T00:00:00Z","updated":"2026-02-02T00:00:00Z"
  },
  {
    "id":"a789012CNTRL","name":"no-redirects",
    "scopes":["all:read"],
    "created":"2026-03-01T00:00:00Z"
  }
]}`

func TestOAuthApps_DecodesRedirectURICountNeverSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/oauth-apps" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(oauthAppsFixture))
	}))
	defer srv.Close()

	apps, err := newClient(t, srv.URL).OAuthApps(context.Background())
	if err != nil {
		t.Fatalf("OAuthApps: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("len(apps) = %d, want 2", len(apps))
	}

	byID := map[string]int{}
	for i, a := range apps {
		byID[a.ID] = i
	}

	a := apps[byID["a123456CNTRL"]]
	if got := a.RedirectURIs; len(got) != 2 || got[0] != "https://example.com/oauth/callback" || got[1] != "https://example.com/oauth/callback2" {
		t.Errorf("a.RedirectURIs = %v, want the two fixture URIs", got)
	}
	if !a.Created.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a.Created = %v, want 2026-02-01", a.Created)
	}
	if !a.Updated.Equal(time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a.Updated = %v, want 2026-02-02", a.Updated)
	}

	noRedirects := apps[byID["a789012CNTRL"]]
	if len(noRedirects.RedirectURIs) != 0 {
		t.Errorf("noRedirects.RedirectURIs = %v, want none", noRedirects.RedirectURIs)
	}

	// OAuthApp exposes no field capable of carrying clientSecret at all — this
	// is a compile-time guarantee (the struct simply has no such field), but the
	// %+v assertion below is a belt-and-braces regression guard: if a future
	// edit ever adds a ClientSecret field to the struct, this test starts
	// failing loudly rather than the secret silently reaching telemetry.
	dump := fmt.Sprintf("%+v", a)
	if strings.Contains(dump, "xxxxx") {
		t.Errorf("decoded OAuthApp must never carry the client secret, got %s", dump)
	}
}
