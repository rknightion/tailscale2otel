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

// settingsFixtureWithACLLink adds aclsExternalLink to the base fixture, as
// returned when the credential holds the policy_file:read scope.
const settingsFixtureWithACLLink = `{
  "aclsExternallyManagedOn": true,
  "aclsExternalLink": "https://github.com/example/tailnet-policy",
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
	if s.ACLsExternalLink != nil {
		t.Errorf("ACLsExternalLink = %v, want nil (key absent from this fixture)", *s.ACLsExternalLink)
	}
}

// TestTailnetSettings_ACLsExternalLinkKeyAbsentIsNil covers the
// permission-denied / unsupported case (#418): when the credential lacks
// policy_file:read, the key is omitted from the wire response entirely. The
// pointer field must decode to nil, distinct from "present but empty", so a
// caller can treat it as absence rather than a definite false.
func TestTailnetSettings_ACLsExternalLinkKeyAbsentIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(settingsFixture))
	}))
	defer srv.Close()

	s, err := newClient(t, srv.URL).TailnetSettings(context.Background())
	if err != nil {
		t.Fatalf("TailnetSettings: %v", err)
	}
	if s.ACLsExternalLink != nil {
		t.Fatalf("ACLsExternalLink = %q, want nil pointer", *s.ACLsExternalLink)
	}
}

// TestTailnetSettings_ACLsExternalLinkPresentButEmpty covers "configured
// permission, no link set": the key is present on the wire as "", meaning
// genuinely not configured (not a permission gap).
func TestTailnetSettings_ACLsExternalLinkPresentButEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"aclsExternalLink": "", "devicesKeyDurationDays": 90}`))
	}))
	defer srv.Close()

	s, err := newClient(t, srv.URL).TailnetSettings(context.Background())
	if err != nil {
		t.Fatalf("TailnetSettings: %v", err)
	}
	if s.ACLsExternalLink == nil {
		t.Fatal("ACLsExternalLink = nil, want a non-nil pointer to \"\" (key was present on the wire)")
	}
	if *s.ACLsExternalLink != "" {
		t.Errorf("ACLsExternalLink = %q, want empty string", *s.ACLsExternalLink)
	}
}

// TestTailnetSettings_ACLsExternalLinkPresentWithURI covers "configured": the
// key is present and non-empty. TailnetSettings itself still decodes the raw
// URI (the collector is responsible for deriving only a boolean and never
// emitting this value).
func TestTailnetSettings_ACLsExternalLinkPresentWithURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(settingsFixtureWithACLLink))
	}))
	defer srv.Close()

	s, err := newClient(t, srv.URL).TailnetSettings(context.Background())
	if err != nil {
		t.Fatalf("TailnetSettings: %v", err)
	}
	if s.ACLsExternalLink == nil {
		t.Fatal("ACLsExternalLink = nil, want a non-nil pointer")
	}
	if *s.ACLsExternalLink != "https://github.com/example/tailnet-policy" {
		t.Errorf("ACLsExternalLink = %q, want the fixture URI", *s.ACLsExternalLink)
	}
}
