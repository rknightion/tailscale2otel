package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/provider"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// twoTailnetFlowApp builds an App fanned out over two tailnets with the flow
// view enabled, exercising the addRuntime path New() uses in multi-tailnet mode.
func twoTailnetFlowApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Flows.Enabled = true
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(),
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	a.addRuntime("one.example.com", telemetrytest.New().Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("two.example.com", telemetrytest.New().Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	return a
}

// flowsTestApp builds an App with the flow view enabled on a loopback admin
// bind (which stays reachable with no token — #227).
func flowsTestApp(t *testing.T, tune func(*config.Config)) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Flows.Enabled = true
	if tune != nil {
		tune(cfg)
	}
	return baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
}

// seedFlows pushes a flow record through the runtime's own processor, so the
// test exercises the real path from record to store rather than writing to the
// store directly.
func seedFlows(t *testing.T, a *App) {
	t.Helper()
	rec := telemetrytest.New()
	a.runtimes[0].flowProc.Process(flowlog.FlowLog{
		NodeID: "nSrc",
		Start:  time.Now().Add(-30 * time.Second).UTC(),
		End:    time.Now().Add(-25 * time.Second).UTC(),
		Logged: time.Now().UTC(),
		SrcNode: &flowlog.NodeRef{
			NodeID: "nSrc", Name: "camden.example.ts.net",
			Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:servers"},
		},
		DstNodes: []flowlog.NodeRef{
			{NodeID: "nDst", Name: "mbp16.example.ts.net", Addresses: []string{"100.64.0.2"}, OS: "macOS", User: "rob@example.com"},
		},
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:52000", Dst: "100.64.0.2:443", TxBytes: 1000, RxBytes: 800, TxPkts: 10, RxPkts: 8},
		},
	}, rec.Emitter())
}

// getFlows issues a GET against the real admin mux and decodes the response.
func getFlows(t *testing.T, a *App, query string) (*httptest.ResponseRecorder, flowsdata.Response) {
	t.Helper()
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/api/flows.json"+query))
	var got flowsdata.Response
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode /api/flows.json: %v\nbody: %s", err, w.Body.String())
		}
	}
	return w, got
}

func TestFlowsJSON_ServesWhatTheProcessorRecorded(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	w, got := getFlows(t, a, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/flows.json = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got.Result.Totals.TxBytes != 1000 || got.Result.Totals.RxBytes != 800 {
		t.Errorf("totals = %+v, want tx 1000 / rx 800", got.Result.Totals)
	}
	if len(got.Result.Pairs) != 1 {
		t.Fatalf("pairs = %+v, want 1", got.Result.Pairs)
	}
	// The fixture has no authoritative device cache, so both names could only be
	// learned from the flow record itself and carry the unverified marker
	// (GHSA-pjfv-prc8-4fc9). With the devices collector running these are bare.
	if got.Result.Pairs[0].Src != unverifiedName("camden") || got.Result.Pairs[0].Dst != unverifiedName("mbp16") {
		t.Errorf("pair = %s -> %s, want %s -> %s", got.Result.Pairs[0].Src, got.Result.Pairs[0].Dst, unverifiedName("camden"), unverifiedName("mbp16"))
	}
	if len(got.Recent) != 1 {
		t.Fatalf("recent = %+v, want 1 connection", got.Recent)
	}
	if got.Recent[0].DstAddr != "100.64.0.2:443" {
		t.Errorf("recent dst = %q, want the raw endpoint", got.Recent[0].DstAddr)
	}
	if got.Tailnet != "example.com" {
		t.Errorf("tailnet = %q, want example.com", got.Tailnet)
	}
	if got.Stats.Observations != 1 {
		t.Errorf("stats.observations = %d, want 1", got.Stats.Observations)
	}
	if got.Retention != (6 * time.Hour).String() {
		t.Errorf("retention = %q, want the configured 6h", got.Retention)
	}
}

// pii_filter governs the telemetry this process EXPORTS. /flows is local,
// admin-authenticated introspection that never crosses a process boundary, so it
// shows what the record carried — an operator who has switched a category off for
// their backend still sees their own tailnet in full here (#241).
func TestFlowsJSON_IdentityIsNotFilteredByPIIFilter(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) {
		// Every category that could touch a flow endpoint, off.
		c.PIIFilter.Emails = false
		c.PIIFilter.Hostnames = false
		c.PIIFilter.TailscaleIPs = false
		c.PIIFilter.UserDisplayNames = false
		c.PIIFilter.NodeIDs = false
	})
	seedFlows(t, a)

	_, got := getFlows(t, a, "")
	if len(got.Recent) != 1 {
		t.Fatalf("recent = %+v, want 1 connection", got.Recent)
	}
	r := got.Recent[0]
	if r.DstUser != "rob@example.com" {
		t.Errorf("recent dst user = %q, want it shown in full", r.DstUser)
	}
	if r.SrcNode != unverifiedName("camden") || r.DstNode != unverifiedName("mbp16") {
		t.Errorf("recent nodes = %q -> %q, want them shown in full", r.SrcNode, r.DstNode)
	}
	if r.SrcAddr != "100.64.0.1:52000" || r.DstAddr != "100.64.0.2:443" {
		t.Errorf("recent endpoints = %q -> %q, want them shown in full", r.SrcAddr, r.DstAddr)
	}
	// The aggregates are keyed off the same identity, so a filtered view would
	// also collapse the topology graph into unnamed endpoints.
	if len(got.Result.Pairs) != 1 || got.Result.Pairs[0].Src != unverifiedName("camden") {
		t.Errorf("pairs = %+v, want the named pair", got.Result.Pairs)
	}
}

// The window and page sizes come from the URL, so they are attacker-adjacent
// even behind the admin gate: they must clamp rather than allocate whatever is
// asked for.
func TestFlowsJSON_ClampsQueryParameters(t *testing.T) {
	a := flowsTestApp(t, nil)
	for range 40 {
		seedFlows(t, a)
	}

	tests := []struct {
		name  string
		query string
		check func(*testing.T, flowsdata.Response)
	}{
		{
			name:  "top is capped",
			query: "?top=100000",
			check: func(t *testing.T, r flowsdata.Response) {
				if len(r.Result.Nodes) > maxFlowsTopN {
					t.Errorf("nodes = %d, want at most %d", len(r.Result.Nodes), maxFlowsTopN)
				}
			},
		},
		{
			name:  "recent is capped",
			query: "?recent=100000",
			check: func(t *testing.T, r flowsdata.Response) {
				if len(r.Recent) > maxFlowsRecent {
					t.Errorf("recent = %d, want at most %d", len(r.Recent), maxFlowsRecent)
				}
			},
		},
		{
			name:  "window beyond retention is clamped to it",
			query: "?window=720h",
			check: func(t *testing.T, r flowsdata.Response) {
				if r.Window != (6 * time.Hour).String() {
					t.Errorf("window = %q, want it clamped to the 6h retention", r.Window)
				}
			},
		},
		{
			name:  "unparseable values fall back to the defaults",
			query: "?window=banana&top=nope&recent=-",
			check: func(t *testing.T, r flowsdata.Response) {
				if r.Window != defaultFlowsWindow.String() {
					t.Errorf("window = %q, want the default %v", r.Window, defaultFlowsWindow)
				}
				if len(r.Result.Nodes) == 0 {
					t.Error("no ranked nodes: a bad top value should not empty the result")
				}
			},
		},
		{
			name:  "recent can be switched off",
			query: "?recent=0",
			check: func(t *testing.T, r flowsdata.Response) {
				if len(r.Recent) != 0 {
					t.Errorf("recent = %d entries, want none", len(r.Recent))
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, got := getFlows(t, a, tc.query)
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200: %s", tc.query, w.Code, w.Body.String())
			}
			tc.check(t, got)
		})
	}
}

// An unknown tailnet must not silently return another tailnet's traffic.
func TestFlowsJSON_UnknownTailnetIsRejected(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	w, _ := getFlows(t, a, "?tailnet=someone-else.com")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET with an unknown tailnet = %d, want 404", w.Code)
	}
}

// With the view disabled the route must not exist at all, rather than serving an
// empty result that looks like "no traffic".
func TestFlowsRoutes_AbsentWhenDisabled(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) { c.Flows.Enabled = false })
	srv := a.buildAdminServer()

	for _, path := range []string{"/api/flows.json", "/flows"} {
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, loopbackReq(http.MethodGet, path))
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s with flows disabled = %d, want 404", path, w.Code)
		}
	}
	if a.runtimes[0].flowStore != nil {
		t.Error("a store was built for a disabled flow view")
	}
}

// The store exists only to feed the admin page, so it must not be built when
// that page is not served — the config advisory promises exactly this.
func TestFlowStore_NotBuiltWithoutTheAdminPage(t *testing.T) {
	for _, tc := range []struct {
		name string
		tune func(*config.Config)
	}{
		{name: "admin server off", tune: func(c *config.Config) { c.Admin.Enabled = false }},
		{name: "landing page off", tune: func(c *config.Config) { c.Admin.LandingPage = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := flowsTestApp(t, tc.tune)
			if a.runtimes[0].flowStore != nil {
				t.Error("flow store built even though /flows cannot be served")
			}
		})
	}
}

func TestNewFlowStoreWiresConfiguredFutureSkew(t *testing.T) {
	cfg := config.Default()
	cfg.Flows.MaxFutureSkew = config.Duration(time.Minute)
	store := newFlowStore(cfg)
	if store == nil {
		t.Fatal("newFlowStore returned nil")
	}
	if got := store.RecordResult(flowstore.Observation{Time: time.Now().Add(30 * time.Minute)}); got != flowstore.AdmissionFuture {
		t.Fatalf("RecordResult() = %v, want future rejection from configured skew", got)
	}
}

// The page and its API sit behind the same gate as everything else on the admin
// server. #227 is why this is asserted rather than assumed.
func TestFlowsRoutes_RequireAdminAuth(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) {
		c.Admin.Listen = "0.0.0.0:9091" // network-reachable
		c.Admin.Auth.Token = "s3cret"
	})
	srv := a.buildAdminServer()

	for _, path := range []string{"/api/flows.json", "/flows"} {
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, loopbackReq(http.MethodGet, path))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET %s = %d, want 401", path, w.Code)
		}

		w = httptest.NewRecorder()
		req := loopbackReq(http.MethodGet, path)
		req.Header.Set("Authorization", "Bearer s3cret")
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("authenticated GET %s = %d, want 200", path, w.Code)
		}
	}
}

// A network-reachable bind with no token fails closed for /flows exactly as it
// does for the status page.
func TestFlowsRoutes_FailClosedWithoutToken(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) { c.Admin.Listen = "0.0.0.0:9091" })
	srv := a.buildAdminServer()

	for _, path := range []string{"/api/flows.json", "/flows"} {
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, loopbackReq(http.MethodGet, path))
		if w.Code != http.StatusForbidden {
			t.Errorf("GET %s on a tokenless network bind = %d, want 403", path, w.Code)
		}
	}
}

func TestFlowsRoutes_GetOnly(t *testing.T) {
	a := flowsTestApp(t, nil)
	srv := a.buildAdminServer()

	for _, path := range []string{"/api/flows.json", "/flows"} {
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, loopbackReq(http.MethodPost, path))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, w.Code)
		}
	}
}

func TestFlowsPage_RendersSelfContainedHTML(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/flows"))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /flows = %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := w.Body.String()
	if len(body) < 1000 {
		t.Fatalf("page is suspiciously short (%d bytes)", len(body))
	}
	// Air-gapped rendering: the page must fetch nothing over the network. An
	// xmlns="http://www.w3.org/..." is a namespace identifier, not a request, so
	// the check targets the constructs that actually cause one.
	for _, bad := range []string{
		`src="http`, `src='http`, `src="//`,
		`href="http`, `href='http`, `href="//`,
		"@import", "url(http", "url(//",
	} {
		if strings.Contains(body, bad) {
			t.Errorf("page fetches an external resource (%q); it must render on an air-gapped tailnet", bad)
		}
	}
}

// Multi-tailnet keeps one store per tailnet: node names are only unique within a
// tailnet, so a shared store would merge two different devices into one vertex.
func TestFlowStore_IsPerTailnet(t *testing.T) {
	a := twoTailnetFlowApp(t)

	if len(a.runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2", len(a.runtimes))
	}
	if a.runtimes[0].flowStore == nil || a.runtimes[1].flowStore == nil {
		t.Fatal("every tailnet needs its own store")
	}
	if a.runtimes[0].flowStore == a.runtimes[1].flowStore {
		t.Error("both tailnets share one store; node names would collide across tailnets")
	}

	seedFlows(t, a)
	w, got := getFlows(t, a, "?tailnet=two.example.com")
	if w.Code != http.StatusOK {
		t.Fatalf("GET for the second tailnet = %d: %s", w.Code, w.Body.String())
	}
	if got.Result.Totals.Bytes() != 0 {
		t.Errorf("second tailnet reports %d bytes; the stores are not isolated", got.Result.Totals.Bytes())
	}
	if len(got.Tailnets) != 2 {
		t.Errorf("tailnets = %v, want both listed so the page can offer a selector", got.Tailnets)
	}
}

// A fresh process has no history at all; the page's first load must still work.
func TestFlowsJSON_EmptyStore(t *testing.T) {
	a := flowsTestApp(t, nil)

	w, got := getFlows(t, a, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/flows.json on an empty store = %d", w.Code)
	}
	if got.Result.Totals != (flowstore.Counts{}) {
		t.Errorf("totals = %+v, want zero", got.Result.Totals)
	}
	if got.Recent == nil {
		t.Error("recent is null; the page expects an array it can iterate")
	}
}

// unverifiedName tags a name the way the flow processor does when the identity
// could only be learned from the flow record itself — i.e. when no authoritative
// device cache entry exists for it (GHSA-pjfv-prc8-4fc9). Test fixtures here
// drive the processor without a devices collector, so every flow-derived name
// they assert on carries the marker.
func unverifiedName(name string) string {
	return enrich.Mark(name, enrich.ProvenanceUnverified)
}
