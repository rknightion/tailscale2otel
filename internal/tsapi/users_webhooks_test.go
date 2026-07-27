package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

// Users and Webhooks were the last two list operations still delegating to
// tsclient, which decodes both responses into a `map[string][]T` and then reads
// one key out of it. That makes the WHOLE call fail on a purely additive upstream
// change: a new top-level key whose value is not an array of the element type
// cannot be unmarshalled into the map, so `{"users":[…],"nextPageToken":"x"}`
// returns an error and the collector goes dark rather than degrading.
//
// Found by the boundary matrix (#433) — these were the only two real findings it
// produced. Every other list operation here already decodes through getJSON into a
// wire struct, which ignores unknown keys; this brings the last two into line.

func TestUsers_ToleratesAdditiveTopLevelKeys(t *testing.T) {
	const body = `{
	  "users": [{"id":"u-1","loginName":"a@example.com","role":"member"}],
	  "nextPageToken": "abc",
	  "total": 1,
	  "meta": {"page": 1}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/users" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		// Moving off tsclient must not lose authentication: the header comes from
		// the transport, which every other getJSON-based method already relies on.
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	us, err := newClient(t, srv.URL).Users(context.Background())
	if err != nil {
		t.Fatalf("Users: %v — an additive top-level key must not fail the call", err)
	}
	if len(us) != 1 || us[0].LoginName != "a@example.com" {
		t.Fatalf("Users = %+v, want the single user decoded", us)
	}
}

func TestWebhooks_ToleratesAdditiveTopLevelKeys(t *testing.T) {
	const body = `{
	  "webhooks": [{"endpointId":"w-1","endpointUrl":"https://example.com/hook","providerType":"slack"}],
	  "nextPageToken": "abc",
	  "total": 1
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/webhooks" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	ws, err := newClient(t, srv.URL).Webhooks(context.Background())
	if err != nil {
		t.Fatalf("Webhooks: %v — an additive top-level key must not fail the call", err)
	}
	if len(ws) != 1 || ws[0].EndpointID != "w-1" {
		t.Fatalf("Webhooks = %+v, want the single webhook decoded", ws)
	}
}

// The happy path still has to work, including an absent wrapper key yielding no
// rows rather than an error.
func TestUsersAndWebhooks_EmptyAndAbsentWrapper(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		call func(*tsapi.Client) (int, error)
	}{
		{"users empty array", `{"users":[]}`, countUsers},
		{"users absent key", `{}`, countUsers},
		{"webhooks empty array", `{"webhooks":[]}`, countWebhooks},
		{"webhooks absent key", `{}`, countWebhooks},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			n, err := tc.call(newClient(t, srv.URL))
			if err != nil {
				t.Fatalf("decode %s: %v", tc.body, err)
			}
			if n != 0 {
				t.Fatalf("got %d rows from %s, want 0", n, tc.body)
			}
		})
	}
}

func countUsers(c *tsapi.Client) (int, error) {
	us, err := c.Users(context.Background())
	return len(us), err
}

func countWebhooks(c *tsapi.Client) (int, error) {
	ws, err := c.Webhooks(context.Background())
	return len(ws), err
}
