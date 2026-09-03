package tsapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// testPolicy is a minimal non-blank HuJSON policy for call sites that only care
// about the RESPONSE handling, not the request shape.
const testPolicy = `{"acls": []}`

// TestValidatePolicyFile_SendsThePolicyAsTheBody pins the request shape against
// what the LIVE API actually requires. Verified 2026-07-31 against
// api.tailscale.com with a read-only OAuth client:
//
//	no body                      -> HTTP 400 {"message":"unexpected end of JSON input"}
//	{}                           -> HTTP 200 {} (validates an EMPTY policy: always passes)
//	the tailnet's live policy    -> HTTP 200 {}
//	a policy naming a bogus tag  -> HTTP 200 {"message":"src=tag not found: ..."}
//
// The last line is why the body must be the real policy: the endpoint validates
// what you SEND, not what is live. This test previously asserted the opposite
// (an empty body), which is how #523 shipped — the fake server accepted the
// empty body because it was written from the same wrong assumption as the code.
func TestValidatePolicyFile_SendsThePolicyAsTheBody(t *testing.T) {
	const policy = "{\n\t// comment\n\t\"acls\": [],\n}"
	var gotBody []byte
	var gotMethod, gotPath, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	got, err := c.ValidatePolicyFile(context.Background(), policy)
	if err != nil {
		t.Fatalf("ValidatePolicyFile: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/api/v2/tailnet/example.com/acl/validate"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if string(gotBody) != policy {
		t.Errorf("request body = %q, want the policy verbatim %q", gotBody, policy)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if !got.OK {
		t.Errorf("OK = false, want true for a bare {} response")
	}
}

// TestValidatePolicyFile_EmptyPolicyIsRefusedLocally proves we never resurrect
// the empty-body call by accident. An empty policy would return 200 upstream
// while validating nothing, so it must fail loudly here instead of quietly
// reporting a healthy validation of nothing at all (#523).
func TestValidatePolicyFile_EmptyPolicyIsRefusedLocally(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	if _, err := c.ValidatePolicyFile(context.Background(), "   "); err == nil {
		t.Error("ValidatePolicyFile(blank policy) = nil error, want a refusal")
	}
	if called {
		t.Error("a blank policy reached the API; it must be refused before the request")
	}
}

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

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background(), testPolicy)
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

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background(), testPolicy)
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

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background(), testPolicy)
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

	v, err := newClient(t, srv.URL).ValidatePolicyFile(context.Background(), testPolicy)
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
