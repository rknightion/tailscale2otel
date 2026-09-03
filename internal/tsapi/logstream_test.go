package tsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

func TestConfigureLogStream_PutsConfigAndSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method = "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/v2/tailnet/example.com/logging/network/stream" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			http.Error(w, "content-type = "+got, http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var got tsapi.LogStreamConfig
		if err := json.Unmarshal(body, &got); err != nil {
			http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
			return
		}
		want := tsapi.LogStreamConfig{DestinationType: "splunk", URL: "https://sink.example/collect", Token: "abc123"}
		if got != want {
			http.Error(w, "body mismatch", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := tsapi.LogStreamConfig{DestinationType: "splunk", URL: "https://sink.example/collect", Token: "abc123"}
	if err := newClient(t, srv.URL).ConfigureLogStream(context.Background(), "network", cfg); err != nil {
		t.Fatalf("ConfigureLogStream: %v", err)
	}
}

func TestConfigureLogStream_ConfigurationLogType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/logging/configuration/stream" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := tsapi.LogStreamConfig{DestinationType: "splunk", URL: "https://sink.example/collect"}
	if err := newClient(t, srv.URL).ConfigureLogStream(context.Background(), "configuration", cfg); err != nil {
		t.Fatalf("ConfigureLogStream: %v", err)
	}
}

func TestConfigureLogStream_Non2xxReturnsError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom-detail", status)
			}))
			defer srv.Close()

			cfg := tsapi.LogStreamConfig{DestinationType: "splunk", URL: "https://sink.example/collect"}
			err := newClient(t, srv.URL).ConfigureLogStream(context.Background(), "network", cfg)
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", status)
			}
			msg := err.Error()
			if !strings.Contains(msg, http.StatusText(status)) && !strings.Contains(msg, statusCodeString(status)) {
				t.Fatalf("error %q does not include status %d", msg, status)
			}
			if strings.Contains(msg, "boom-detail") {
				t.Fatalf("error %q includes peer-controlled body snippet", msg)
			}
		})
	}
}

// TestConfigureLogStream_Non2xxIsTypedStatusError pins the PUT path to the same
// typed error the GET path returns (#420). Until this landed, putJSON returned a
// plain fmt.Errorf, so a 401 on the write path was indistinguishable from a 403
// or a network failure — nothing downstream could classify it at all.
func TestConfigureLogStream_Non2xxIsTypedStatusError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom-detail", status)
			}))
			defer srv.Close()

			cfg := tsapi.LogStreamConfig{DestinationType: "splunk", URL: "https://sink.example/collect"}
			err := newClient(t, srv.URL).ConfigureLogStream(context.Background(), "network", cfg)
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", status)
			}
			var se *tsapi.StatusError
			if !errors.As(err, &se) {
				t.Fatalf("error %T (%v) is not a *tsapi.StatusError", err, err)
			}
			if se.Code != status {
				t.Errorf("StatusError.Code = %d, want %d", se.Code, status)
			}
			if se.Method != http.MethodPut {
				t.Errorf("StatusError.Method = %q, want PUT", se.Method)
			}
			if !strings.Contains(se.Body, "boom-detail") {
				t.Errorf("StatusError.Body = %q, want the response snippet", se.Body)
			}
			if got, ok := tsapi.StatusCode(err); !ok || got != status {
				t.Errorf("StatusCode(err) = %d,%v, want %d,true", got, ok, status)
			}
		})
	}
}

func TestConfigureLogStream_InvalidLogTypeNeverHitsServer(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("server must not be called for invalid logType; path=%s", r.URL.Path)
	}))
	defer srv.Close()

	cfg := tsapi.LogStreamConfig{DestinationType: "splunk", URL: "https://sink.example/collect"}
	err := newClient(t, srv.URL).ConfigureLogStream(context.Background(), "bogus", cfg)
	if err == nil {
		t.Fatalf("expected error for invalid logType, got nil")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("server calls = %d, want 0", got)
	}
}

// statusCodeString renders an HTTP status code as its decimal string, used to
// tolerate error messages that include the numeric code rather than its text.
func statusCodeString(code int) string {
	return strconv.Itoa(code)
}
