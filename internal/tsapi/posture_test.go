package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postureFixture mirrors a real GET /tailnet/{tn}/posture/integrations response,
// including the status{} block the official tsclient type omits, plus the
// clientId/tenantId fields the collector must NOT surface.
const postureFixture = `{"integrations":[
  {
    "id":"p8czQ7yM1uJCCNTRL","provider":"intune","cloudId":"global",
    "clientId":"d67d159b-secret","tenantId":"4b8c18bd-secret",
    "configUpdated":"2026-04-08T15:49:35.718640583Z",
    "status":{"lastSync":"2026-06-05T10:29:15.57400268Z","matchedCount":4,"possibleMatchedCount":5,"providerHostCount":10}
  }
]}`

func TestPostureIntegrations_DecodesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/posture/integrations" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(postureFixture))
	}))
	defer srv.Close()

	ints, err := newClient(t, srv.URL).PostureIntegrations(context.Background())
	if err != nil {
		t.Fatalf("PostureIntegrations: %v", err)
	}
	if len(ints) != 1 {
		t.Fatalf("len = %d, want 1", len(ints))
	}
	i := ints[0]
	if i.ID != "p8czQ7yM1uJCCNTRL" {
		t.Errorf("ID = %q", i.ID)
	}
	if i.Provider != "intune" {
		t.Errorf("Provider = %q, want intune", i.Provider)
	}
	if i.Status.MatchedCount != 4 || i.Status.PossibleMatchedCount != 5 || i.Status.ProviderHostCount != 10 {
		t.Errorf("status counts = %+v", i.Status)
	}
	if i.Status.LastSync.IsZero() || i.Status.LastSync.Year() != 2026 {
		t.Errorf("LastSync = %v, want a 2026 timestamp", i.Status.LastSync)
	}
}
