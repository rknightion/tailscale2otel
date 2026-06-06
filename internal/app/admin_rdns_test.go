package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/rdns"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// rdnsTestApp builds an App with reverse-DNS enabled and replaces the real
// resolver-backed cache with one whose Lookup is a deterministic stub, so tests
// can seed and assert entries without real DNS.
func rdnsTestApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Enrichment.ReverseDNS.Enabled = true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	a.rdnsCache.Close() // discard the system-resolver cache newApp built
	a.rdnsCache = rdns.New(rdns.Options{
		MaxEntries:     100,
		ReportInterval: time.Hour, // keep the auto-ticker out of the test
		Lookup: func(context.Context, netip.Addr) ([]string, error) {
			return []string{"host.example.com."}, nil
		},
	})
	t.Cleanup(func() { a.rdnsCache.Close() })
	return a
}

// seedRDNS schedules a lookup and waits for the background resolution to land.
func seedRDNS(t *testing.T, a *App, ip string) {
	t.Helper()
	a.rdnsCache.LookupName(netip.MustParseAddr(ip))
	deadline := time.Now().Add(2 * time.Second)
	for a.rdnsCache.Stats().Size == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for rdns entry to populate")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRDNSPurge_ClearsCache(t *testing.T) {
	a := rdnsTestApp(t)
	seedRDNS(t, a, "203.0.113.5")
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodPost, "/api/rdns/purge", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/rdns/purge = %d, want 200", w.Code)
	}
	var got struct {
		Purged  int  `json:"purged"`
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode purge response: %v", err)
	}
	if got.Purged != 1 || !got.Enabled {
		t.Errorf("purge response = %+v, want purged=1 enabled=true", got)
	}
	if sz := a.rdnsCache.Stats().Size; sz != 0 {
		t.Errorf("cache size after purge = %d, want 0", sz)
	}
}

func TestRDNSPurge_RejectsGET(t *testing.T) {
	a := rdnsTestApp(t)
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/rdns/purge", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/rdns/purge = %d, want 405", w.Code)
	}
}

func TestRDNSPurge_RejectsCrossOrigin(t *testing.T) {
	a := rdnsTestApp(t)
	seedRDNS(t, a, "203.0.113.5")
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodPost, "/api/rdns/purge", nil)
	req.Host = "admin.local"
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", w.Code)
	}
	// A rejected request must not have purged anything.
	if sz := a.rdnsCache.Stats().Size; sz != 1 {
		t.Errorf("cache size after rejected purge = %d, want 1 (untouched)", sz)
	}
}

func TestRDNSPurge_DisabledReportsNotEnabled(t *testing.T) {
	// Default config has reverse_dns disabled, so a.rdnsCache is nil.
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodPost, "/api/rdns/purge", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/rdns/purge (disabled) = %d, want 200", w.Code)
	}
	var got struct {
		Purged  int  `json:"purged"`
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode purge response: %v", err)
	}
	if got.Enabled || got.Purged != 0 {
		t.Errorf("purge response = %+v, want purged=0 enabled=false", got)
	}
}

func TestStatusPage_HasRDNSSection(t *testing.T) {
	a := rdnsTestApp(t)
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="rdns"`,         // the reverse-DNS section anchor
		`id="rdnsPurgeBtn"`, // the purge control
		"Reverse DNS",       // section heading text
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status HTML missing %q", want)
		}
	}
}

func TestBuildStatus_IncludesRDNS(t *testing.T) {
	a := rdnsTestApp(t)
	s := a.buildStatus()
	if !s.RDNS.Enabled {
		t.Error("status.RDNS.Enabled = false, want true when reverse_dns is enabled")
	}
	if s.RDNS.Capacity != 100 {
		t.Errorf("status.RDNS.Capacity = %d, want 100", s.RDNS.Capacity)
	}

	// With reverse_dns disabled the section reports disabled.
	b := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	if b.buildStatus().RDNS.Enabled {
		t.Error("status.RDNS.Enabled = true, want false when reverse_dns is disabled")
	}
}
