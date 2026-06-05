package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContacts_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/contacts" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{
			"account":{"email":"a@b.com","needsVerification":false},
			"support":{"email":"s@b.com","needsVerification":true},
			"security":{"email":"sec@b.com","needsVerification":false}
		}`))
	}))
	defer srv.Close()

	c, err := newClient(t, srv.URL).Contacts(context.Background())
	if err != nil {
		t.Fatalf("Contacts: %v", err)
	}
	if c.Account.NeedsVerification {
		t.Errorf("Account.NeedsVerification = true, want false")
	}
	if !c.Support.NeedsVerification {
		t.Errorf("Support.NeedsVerification = false, want true")
	}
	if c.Security.NeedsVerification {
		t.Errorf("Security.NeedsVerification = true, want false")
	}
}
