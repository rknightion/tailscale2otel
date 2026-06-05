package tsapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// logStreamStatusFixture mirrors a real /logging/{type}/stream/status response,
// including fields the collector ignores (rate*, the nanosec array) to prove the
// decode tolerates them.
const logStreamStatusFixture = `{
  "lastActivity":"2026-06-05T10:35:43.387299122Z","lastError":"",
  "maxBodySize":11059200,"maxNumEntries":3375,"numSpoofedEntries":0,
  "numBytesSent":247478943,"numEntriesSent":207754,"numFailedRequests":23,
  "numTotalRequests":1508,"numMaxBodyRequests":35,
  "numTotalRequestNanoSecs":1168290815249,
  "numTotalNanoSecsPerProgress":[521907439415,522700590926],
  "rateBytesSent":2311.69
}`

func TestLogStreamStatus_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/logging/network/stream/status" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(logStreamStatusFixture))
	}))
	defer srv.Close()

	st, err := newClient(t, srv.URL).LogStreamStatus(context.Background(), "network")
	if err != nil {
		t.Fatalf("LogStreamStatus: %v", err)
	}
	if st.NumBytesSent != 247478943 || st.NumEntriesSent != 207754 {
		t.Errorf("bytes/entries = %d/%d", st.NumBytesSent, st.NumEntriesSent)
	}
	if st.NumFailedRequests != 23 || st.NumTotalRequests != 1508 || st.NumMaxBodyRequests != 35 {
		t.Errorf("requests = failed %d total %d maxbody %d", st.NumFailedRequests, st.NumTotalRequests, st.NumMaxBodyRequests)
	}
	if st.MaxNumEntries != 3375 || st.NumSpoofedEntries != 0 {
		t.Errorf("maxNumEntries/spoofed = %d/%d", st.MaxNumEntries, st.NumSpoofedEntries)
	}
	if st.LastError != "" {
		t.Errorf("LastError = %q, want empty", st.LastError)
	}
	if st.LastActivity.IsZero() {
		t.Errorf("LastActivity is zero, want a timestamp")
	}
}

func TestLogStreamStatus_404IsTypedStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not configured", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).LogStreamStatus(context.Background(), "configuration")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	var se *tsapi.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *tsapi.StatusError", err)
	}
	if se.Code != http.StatusNotFound {
		t.Errorf("StatusError.Code = %d, want 404", se.Code)
	}
}

func TestLogStreamStatus_InvalidType(t *testing.T) {
	if _, err := newClient(t, "http://unused.invalid").LogStreamStatus(context.Background(), "bogus"); err == nil {
		t.Fatal("expected error for invalid logType")
	}
}
