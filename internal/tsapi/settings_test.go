package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// settingsFixture mirrors a real GET /tailnet/{tn}/settings response, including
// the fields the official tsclient struct omits (httpsEnabled,
// aclsExternallyManagedOn) plus the enum the spec surfaces.
const settingsFixture = `{
  "aclsExternallyManagedOn": false,
  "devicesApprovalOn": false,
  "devicesAutoUpdatesOn": true,
  "devicesKeyDurationDays": 180,
  "usersApprovalOn": false,
  "usersRoleAllowedToJoinExternalTailnets": "none",
  "networkFlowLoggingOn": true,
  "regionalRoutingOn": false,
  "postureIdentityCollectionOn": true,
  "httpsEnabled": true
}`

func TestTailnetSettings_DecodesAllFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/settings" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(settingsFixture))
	}))
	defer srv.Close()

	s, err := newClient(t, srv.URL).TailnetSettings(context.Background())
	if err != nil {
		t.Fatalf("TailnetSettings: %v", err)
	}

	if !s.HTTPSEnabled {
		t.Errorf("HTTPSEnabled = false, want true")
	}
	if s.ACLsExternallyManagedOn {
		t.Errorf("ACLsExternallyManagedOn = true, want false")
	}
	if s.UsersRoleAllowedToJoinExternalTailnets != "none" {
		t.Errorf("UsersRoleAllowedToJoinExternalTailnets = %q, want none", s.UsersRoleAllowedToJoinExternalTailnets)
	}
	if !s.DevicesAutoUpdatesOn {
		t.Errorf("DevicesAutoUpdatesOn = false, want true")
	}
	if !s.NetworkFlowLoggingOn {
		t.Errorf("NetworkFlowLoggingOn = false, want true")
	}
	if !s.PostureIdentityCollectionOn {
		t.Errorf("PostureIdentityCollectionOn = false, want true")
	}
	if s.DevicesKeyDurationDays != 180 {
		t.Errorf("DevicesKeyDurationDays = %d, want 180", s.DevicesKeyDurationDays)
	}
}
