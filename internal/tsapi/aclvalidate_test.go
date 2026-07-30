package tsapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// TestValidatePolicyFile_SendsNoBodyToValidateEndpoint asserts the request
// shape: POST to /tailnet/{tailnet}/acl/validate with an EMPTY body. A
// populated body would either run externally supplied ACL tests (JSON array)
// or validate a hypothetical replacement policy (JSON object/string) — this
// call wants neither; it validates the tailnet's CURRENTLY ACTIVE policy.
func TestValidatePolicyFile_SendsNoBodyToValidateEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background())
	if err != nil {
		t.Fatalf("ValidatePolicyFile: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v2/tailnet/example.com/acl/validate" {
		t.Errorf("path = %q, want /api/v2/tailnet/example.com/acl/validate", gotPath)
	}
	if len(gotBody) != 0 {
		t.Errorf("request body = %q, want empty (validating the CURRENT policy, not a supplied test/policy body)", gotBody)
	}
	if !v.OK {
		t.Errorf("OK = false, want true for a bare {} success response")
	}
	if v.Errors != 0 || v.Warnings != 0 || v.TestFailures != 0 {
		t.Errorf("got %+v, want all-zero counts on success", v)
	}
}

// TestValidatePolicyFile_TestsFailedCountsAsTestFailures mirrors the spec's
// documented "test(s) failed" example: each data[] element's errors[] entries
// are embedded-test failures, not generic validation errors.
func TestValidatePolicyFile_TestsFailedCountsAsTestFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"message": "test(s) failed",
			"data": [
				{"user": "user1@example.com", "errors": ["address \"2.2.2.2:22\": want: Drop, got: Accept"]},
				{"user": "user2@example.com", "errors": ["one", "two"]}
			]
		}`))
	}))
	defer srv.Close()

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background())
	if err != nil {
		t.Fatalf("ValidatePolicyFile: %v", err)
	}
	if v.OK {
		t.Error("OK = true, want false when tests failed")
	}
	if v.TestFailures != 3 {
		t.Errorf("TestFailures = %d, want 3 (1 + 2 error entries)", v.TestFailures)
	}
	if v.Errors != 0 {
		t.Errorf("Errors = %d, want 0 (this is a test-failure response, not a generic error)", v.Errors)
	}
	if v.Warnings != 0 {
		t.Errorf("Warnings = %d, want 0", v.Warnings)
	}
}

// TestValidatePolicyFile_WarningsFoundCountsAsWarnings mirrors the spec's
// documented "warning(s) found" example (e.g. a SCIM group not syncing).
func TestValidatePolicyFile_WarningsFoundCountsAsWarnings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"message": "warning(s) found",
			"data": [
				{"user": "group:unknown@example.com", "warnings": ["group is not syncing from SCIM and will be ignored by rules in the policy file"]}
			]
		}`))
	}))
	defer srv.Close()

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background())
	if err != nil {
		t.Fatalf("ValidatePolicyFile: %v", err)
	}
	if v.OK {
		t.Error("OK = true, want false when warnings were found")
	}
	if v.Warnings != 1 {
		t.Errorf("Warnings = %d, want 1", v.Warnings)
	}
	if v.Errors != 0 || v.TestFailures != 0 {
		t.Errorf("Errors/TestFailures = %d/%d, want 0/0", v.Errors, v.TestFailures)
	}
}

// TestValidatePolicyFile_ForbiddenReturnsStatusError asserts the error is a
// *tsapi.StatusError (so apistate.Classify can turn a 403 into scope_denied
// rather than a healthy zero), not a plain error.
func TestValidatePolicyFile_ForbiddenReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "policy_file:read scope required", http.StatusForbidden)
	}))
	defer srv.Close()

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background())
	if err == nil {
		t.Fatal("ValidatePolicyFile: expected error, got nil")
	}
	if v != nil {
		t.Errorf("got non-nil result %+v on error", v)
	}
	var statusErr *tsapi.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error is %T (%v), want *tsapi.StatusError", err, err)
	}
	if statusErr.Code != http.StatusForbidden {
		t.Errorf("StatusError.Code = %d, want 403", statusErr.Code)
	}
}

// TestValidatePolicyFile_MalformedBodyReturnsError covers a 200 response whose
// body is not valid JSON at all — a decode error, distinct from the
// *StatusError case above.
func TestValidatePolicyFile_MalformedBodyReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background())
	if err == nil {
		t.Fatal("ValidatePolicyFile: expected a decode error, got nil")
	}
	if v != nil {
		t.Errorf("got non-nil result %+v on decode error", v)
	}
	var statusErr *tsapi.StatusError
	if errors.As(err, &statusErr) {
		t.Errorf("malformed-body error unexpectedly matched *tsapi.StatusError (status was 200)")
	}
}
