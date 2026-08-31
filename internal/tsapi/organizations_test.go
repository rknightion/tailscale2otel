package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrganizationTailnets_PaginatesAtAPIMaximum(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/organizations/example-org/tailnets" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			http.Error(w, "limit = "+got, http.StatusBadRequest)
			return
		}

		requests++
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"tailnets":[{"id":"tailnet-one"}],"cursor":"next-page","totalCount":2}`))
		case "next-page":
			_, _ = w.Write([]byte(`{"tailnets":[{"id":"tailnet-two"}],"totalCount":2}`))
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tailnets, err := newClient(t, srv.URL).OrganizationTailnets(context.Background(), "example-org")
	if err != nil {
		t.Fatalf("OrganizationTailnets: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if len(tailnets) != 2 || tailnets[0].ID != "tailnet-one" || tailnets[1].ID != "tailnet-two" {
		t.Fatalf("tailnets = %+v, want two pages in order", tailnets)
	}
}

func TestOrganizationTailnets_RejectsRepeatedCursor(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"tailnets":[],"cursor":"same-cursor"}`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).OrganizationTailnets(context.Background(), "example-org")
	if err == nil || !strings.Contains(err.Error(), "repeated cursor") {
		t.Fatalf("OrganizationTailnets error = %v, want repeated-cursor rejection", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 before cursor rejection", requests)
	}
}

// TestOrganizationTailnets_CannotEscapeItsPathSegment pins the traversal
// finding from the post-Wave-3 review: organizationTailnetsURL built its path
// with path.Join, which CLEANS the result. An organization containing ".."
// walks the path up so the request lands on a different endpoint entirely,
// and one containing "/" silently adds a segment. The organization comes from
// config rather than a remote caller, so this is hardening rather than an
// exploitable hole — but a roster call that quietly targets another endpoint
// should be refused, not issued.
func TestOrganizationTailnets_CannotEscapeItsPathSegment(t *testing.T) {
	for _, org := range []string{"../../evil", "a/b", "..", "./x"} {
		t.Run(org, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.EscapedPath()
				_, _ = w.Write([]byte(`{"tailnets":[]}`))
			}))
			defer srv.Close()

			_, err := newClient(t, srv.URL).OrganizationTailnets(context.Background(), org)
			if err != nil {
				// Refusing the organization outright is a correct outcome.
				return
			}
			const prefix = "/api/v2/organizations/"
			if !strings.HasPrefix(gotPath, prefix) || !strings.HasSuffix(gotPath, "/tailnets") {
				t.Fatalf("organization %q requested %q, which left the organizations path", org, gotPath)
			}
			seg := strings.TrimSuffix(strings.TrimPrefix(gotPath, prefix), "/tailnets")
			if seg == "" || strings.Contains(seg, "/") {
				t.Fatalf("organization %q requested %q: the organization must stay ONE path segment", org, seg)
			}
		})
	}
}
