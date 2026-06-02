package tsapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeRoundTripper returns canned responses in order, one per call.
type fakeRoundTripper struct {
	statuses []int
	calls    int
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s := f.statuses[f.calls]
	f.calls++
	return &http.Response{
		StatusCode: s,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestRetryTransport_ObserverSeesFinalStatusAndAttempts(t *testing.T) {
	var (
		gotEndpoint string
		gotStatus   = -1
		gotAttempts = -1
		calls       int
	)
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(endpoint string, status, attempts int) {
			calls++
			gotEndpoint, gotStatus, gotAttempts = endpoint, status, attempts
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices?fields=all", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("observer called %d times, want exactly 1", calls)
	}
	if gotStatus != http.StatusOK {
		t.Fatalf("observed status = %d, want 200", gotStatus)
	}
	if gotAttempts != 2 {
		t.Fatalf("observed attempts = %d, want 2", gotAttempts)
	}
	if gotEndpoint != "devices" {
		t.Fatalf("observed endpoint = %q, want %q", gotEndpoint, "devices")
	}
}

func TestRetryTransport_ObserverFirstTry(t *testing.T) {
	var gotAttempts, gotStatus int
	var gotEndpoint string
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(endpoint string, status, attempts int) {
			gotEndpoint, gotStatus, gotAttempts = endpoint, status, attempts
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/logging/network?start=x", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if gotAttempts != 1 {
		t.Fatalf("attempts = %d, want 1", gotAttempts)
	}
	if gotStatus != http.StatusOK {
		t.Fatalf("status = %d", gotStatus)
	}
	if gotEndpoint != "logging/network" {
		t.Fatalf("endpoint = %q, want logging/network", gotEndpoint)
	}
}

func TestRetryTransport_NilObserverNoPanic(t *testing.T) {
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/keys", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestEndpointLabel(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/v2/tailnet/example.com/devices", "devices"},
		{"/api/v2/tailnet/example.com/users", "users"},
		{"/api/v2/tailnet/example.com/keys", "keys"},
		{"/api/v2/tailnet/example.com/logging/network", "logging/network"},
		{"/api/v2/tailnet/example.com/logging/configuration", "logging/configuration"},
		{"/api/v2/tailnet/example.com/settings", "settings"},
		{"/api/v2/tailnet/example.com/user-invites", "user-invites"},
		{"/api/v2/oauth/token", "oauth/token"},
		{"/api/v2/device/dev123/attributes", "device/attributes"},
	}
	for _, c := range cases {
		if got := endpointLabel(c.path); got != c.want {
			t.Errorf("endpointLabel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
