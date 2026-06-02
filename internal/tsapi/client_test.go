package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

func newClient(t *testing.T, srvURL string) *tsapi.Client {
	t.Helper()
	c, err := tsapi.NewClient(tsapi.Options{
		Tailnet:     "example.com",
		BaseURL:     srvURL,
		APIKey:      "testkey",
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNetworkFlowLogs_DecodesAndSendsWindowAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/logging/network" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("start") == "" || r.URL.Query().Get("end") == "" {
			http.Error(w, "missing window", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"logs":[{"nodeId":"n1","virtualTraffic":[{"proto":"tcp","src":"100.64.0.1:1","dst":"100.64.0.2:2","txBytes":5,"rxBytes":7}]}]}`))
	}))
	defer srv.Close()

	resp, err := newClient(t, srv.URL).NetworkFlowLogs(context.Background(), time.Unix(0, 0), time.Unix(60, 0))
	if err != nil {
		t.Fatalf("NetworkFlowLogs: %v", err)
	}
	if len(resp.Logs) != 1 || resp.Logs[0].NodeID != "n1" {
		t.Fatalf("logs = %+v", resp.Logs)
	}
	if resp.Logs[0].VirtualTraffic[0].TxBytes != 5 || resp.Logs[0].VirtualTraffic[0].RxBytes != 7 {
		t.Fatalf("counts = %+v", resp.Logs[0].VirtualTraffic[0])
	}
}

func TestConfigAuditLogs_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/logging/configuration" {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"tailnet":"example.com","logs":[{"action":"CREATE","actor":{"loginName":"a@b.com"}}]}`))
	}))
	defer srv.Close()

	resp, err := newClient(t, srv.URL).ConfigAuditLogs(context.Background(), time.Unix(0, 0), time.Unix(60, 0))
	if err != nil {
		t.Fatalf("ConfigAuditLogs: %v", err)
	}
	if len(resp.Logs) != 1 || resp.Logs[0].Action != "CREATE" || resp.Logs[0].Actor.LoginName != "a@b.com" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestDevices_DecodesViaTSClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"devices":[{"nodeId":"n1","hostname":"laptop","addresses":["100.64.0.1"],"os":"linux"}]}`))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).Devices(context.Background())
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devs) != 1 || devs[0].Hostname != "laptop" {
		t.Fatalf("devices = %+v", devs)
	}
}

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "slow down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"logs":[]}`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).NetworkFlowLogs(context.Background(), time.Unix(0, 0), time.Unix(60, 0))
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("server calls = %d, want 3 (2 retries)", got)
	}
}
