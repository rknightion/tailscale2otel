package tsapi

import (
	"context"
	"errors"
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

// errRoundTripper always fails with a transport error, exercising the err!=0,
// status==0 path of observe.
type errRoundTripper struct {
	err   error
	calls int
}

func (f *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return nil, f.err
}

func TestRetryTransport_ObserverSeesFinalStatusAndAttempts(t *testing.T) {
	var (
		got   RequestInfo
		calls int
	)
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusTooManyRequests, http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(i RequestInfo) {
			calls++
			got = i
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
	if got.Status != http.StatusOK {
		t.Fatalf("observed status = %d, want 200", got.Status)
	}
	if got.Attempts != 2 {
		t.Fatalf("observed attempts = %d, want 2", got.Attempts)
	}
	if got.Endpoint != "devices" {
		t.Fatalf("observed endpoint = %q, want %q", got.Endpoint, "devices")
	}
	if got.Duration <= 0 {
		t.Fatalf("observed duration = %v, want > 0", got.Duration)
	}
	if got.Err != "" {
		t.Fatalf("observed err = %q, want empty", got.Err)
	}
}

func TestRetryTransport_ObserverFirstTry(t *testing.T) {
	var got RequestInfo
	rt := &retryTransport{
		base:      &fakeRoundTripper{statuses: []int{http.StatusOK}},
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/logging/network?start=x", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if got.Status != http.StatusOK {
		t.Fatalf("status = %d", got.Status)
	}
	if got.Endpoint != "logging/network" {
		t.Fatalf("endpoint = %q, want logging/network", got.Endpoint)
	}
	if got.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", got.Duration)
	}
	if got.Err != "" {
		t.Fatalf("err = %q, want empty", got.Err)
	}
}

// TestRetryTransport_ObserverTransportError verifies that when every attempt
// fails at the transport layer (no HTTP response), the observer sees Status==0
// and a non-empty Err carrying the transport error text.
func TestRetryTransport_ObserverTransportError(t *testing.T) {
	var got RequestInfo
	base := &errRoundTripper{err: errors.New("dial tcp: connection refused")}
	rt := &retryTransport{
		base:      base,
		max:       2,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		onRequest: func(i RequestInfo) {
			got = i
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatalf("RoundTrip err = nil, want transport error")
	}
	if got.Status != 0 {
		t.Fatalf("status = %d, want 0 on transport error", got.Status)
	}
	if got.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", got.Attempts)
	}
	if got.Endpoint != "devices" {
		t.Fatalf("endpoint = %q, want devices", got.Endpoint)
	}
	if got.Err == "" {
		t.Fatalf("err = empty, want transport error text")
	}
	if got.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", got.Duration)
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

// bodyCapturingRoundTripper records each attempt's request body and returns
// canned statuses, so a retried request's re-sent body can be asserted.
type bodyCapturingRoundTripper struct {
	statuses []int
	bodies   []string
	calls    int
}

func (f *bodyCapturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	f.bodies = append(f.bodies, body)
	s := f.statuses[f.calls]
	f.calls++
	return &http.Response{
		StatusCode: s,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestRetryTransport_ResendsBodyOnRetry verifies that a request carrying a body
// (e.g. the log-stream-config PUT) re-sends its full payload on a retried
// attempt instead of an empty body: the retry loop must rewind Body from
// GetBody each attempt. Without that, the second attempt would see "".
func TestRetryTransport_ResendsBodyOnRetry(t *testing.T) {
	capt := &bodyCapturingRoundTripper{statuses: []int{http.StatusInternalServerError, http.StatusOK}}
	rt := &retryTransport{
		base:      capt,
		max:       3,
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
	}
	const payload = `{"destinationType":"splunk","url":"https://x"}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"https://api.tailscale.com/api/v2/tailnet/example.com/logging/network/stream",
		strings.NewReader(payload))
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
	if capt.calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", capt.calls)
	}
	for i, b := range capt.bodies {
		if b != payload {
			t.Fatalf("attempt %d body = %q, want full payload re-sent", i+1, b)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		in    string
		want  time.Duration // used when exact; -1 means "small positive"
		exact bool
	}{
		{"empty", "", 0, true},
		{"seconds", "5", 5 * time.Second, true},
		{"zero seconds", "0", 0, true},
		{"negative seconds", "-3", 0, true},
		{"garbage", "soon", 0, true},
		{"http date future", now.Add(7 * time.Second).UTC().Format(http.TimeFormat), -1, false},
		{"http date past", now.Add(-7 * time.Second).UTC().Format(http.TimeFormat), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := retryAfter(c.in)
			switch {
			case c.exact:
				if got != c.want {
					t.Fatalf("retryAfter(%q) = %v, want %v", c.in, got, c.want)
				}
			case c.want == -1:
				if got <= 0 || got > 8*time.Second {
					t.Fatalf("retryAfter(%q) = %v, want a small positive duration", c.in, got)
				}
			default: // past date -> 0 (clamped)
				if got != 0 {
					t.Fatalf("retryAfter(%q) = %v, want 0", c.in, got)
				}
			}
		})
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
