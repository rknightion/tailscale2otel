package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
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

// #296: server-side filtering and cursor pagination for the recent-connection
// list. Before this, /api/flows.json returned at most maxFlowsRecent rows and
// the browser filtered only that returned tail — a connection matching the
// operator's filter but sitting outside the returned window was invisible.

// An unfiltered request (no filter/cursor params at all) must be byte-for-byte
// what the pre-#296 response looked like on every existing field, with the
// new fields simply appended and empty/zero. This is the compatibility
// contract: adding filtering must not perturb a caller that doesn't use it.
func TestFlowsJSON_UnfilteredResponseIsUnchanged(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	w, got := getFlows(t, a, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/flows.json = %d: %s", w.Code, w.Body.String())
	}

	// Every pre-existing field still populated exactly as the dedicated tests
	// above already assert; here we assert the NEW fields take their
	// documented "nothing requested" values rather than perturbing anything.
	if got.Filters != (flowsdata.Filters{}) {
		t.Errorf("Filters = %+v, want the zero value with no filter params", got.Filters)
	}
	if got.RecentMatched != len(got.Recent) {
		t.Errorf("RecentMatched = %d, want %d (equal to RecentReturned with no filter/cursor)", got.RecentMatched, len(got.Recent))
	}
	if got.RecentReturned != len(got.Recent) {
		t.Errorf("RecentReturned = %d, want %d", got.RecentReturned, len(got.Recent))
	}
	if got.RecentRetained != len(got.Recent) {
		t.Errorf("RecentRetained = %d, want %d (only one flow was ever seeded)", got.RecentRetained, len(got.Recent))
	}
	if got.RecentTruncated {
		t.Error("RecentTruncated = true with a single seeded flow")
	}
	if got.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty (everything fit on one page)", got.NextCursor)
	}

	// And the raw body must still decode the OLD fields exactly as before:
	// a map-level check that nothing already-shipped silently changed shape.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw body: %v", err)
	}
	for _, key := range []string{
		"tailnet", "tailnets", "window", "retention", "stats", "result",
		"recent", "policy", "exercised", "generated_at",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response is missing pre-existing field %q", key)
		}
	}
}

// A filter matching a connection OUTSIDE the first page must still be found
// by paging through NextCursor — the exact defect #296 reports: client-side
// filtering over a fixed 1000-row tail made an out-of-page match invisible.
func TestFlowsJSON_FilterReachesBeyondTheFirstPage(t *testing.T) {
	a := flowsTestApp(t, nil)
	now := time.Now().UTC()
	store := a.runtimes[0].flowStore

	// The needle is the OLDEST row, so on a small page size it never appears
	// on page 1 unless the store itself excludes non-matching rows.
	store.Record(flowstore.Observation{
		Time: now.Add(-50 * time.Minute), SrcNode: "needle-device", DstNode: "b",
		Counts: flowstore.Counts{Flows: 1, TxBytes: 1},
	})
	for i := range 20 {
		store.Record(flowstore.Observation{
			Time: now.Add(-time.Duration(49-i) * time.Minute), SrcNode: "haystack", DstNode: "b",
			Counts: flowstore.Counts{Flows: 1, TxBytes: 1},
		})
	}

	w, got := getFlows(t, a, "?window=1h&recent=5&device=needle-device")
	if w.Code != http.StatusOK {
		t.Fatalf("GET with a device filter = %d: %s", w.Code, w.Body.String())
	}
	if len(got.Recent) != 1 || got.Recent[0].SrcNode != "needle-device" {
		t.Fatalf("recent = %+v, want exactly the needle row", got.Recent)
	}
	if got.RecentMatched != 1 {
		t.Errorf("RecentMatched = %d, want 1", got.RecentMatched)
	}
	if got.Filters.Device != "needle-device" {
		t.Errorf("Filters.Device = %q, want the applied filter echoed back", got.Filters.Device)
	}
}

// Paging via NextCursor must walk the whole matching set without gaps or
// duplicates, and the last page must report no further cursor.
func TestFlowsJSON_CursorPaginatesWithoutGapsOrDuplicates(t *testing.T) {
	a := flowsTestApp(t, nil)
	now := time.Now().UTC()
	store := a.runtimes[0].flowStore
	for i := range 10 {
		store.Record(flowstore.Observation{
			Time: now.Add(-time.Duration(9-i) * time.Minute), SrcNode: "paged", DstNode: "b",
			Counts: flowstore.Counts{Flows: 1, TxBytes: int64(i)},
		})
	}

	var seen []int64
	cursor := ""
	for page := 0; page < 20; page++ {
		q := "?window=1h&recent=3&device=paged"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		w, got := getFlows(t, a, q)
		if w.Code != http.StatusOK {
			t.Fatalf("page %d: GET = %d: %s", page, w.Code, w.Body.String())
		}
		for _, r := range got.Recent {
			seen = append(seen, r.Counts.TxBytes)
		}
		if got.NextCursor == "" {
			break
		}
		cursor = got.NextCursor
	}

	if len(seen) != 10 {
		t.Fatalf("paginated through %d rows, want all 10: %v", len(seen), seen)
	}
	dup := map[int64]bool{}
	for _, tx := range seen {
		if dup[tx] {
			t.Fatalf("tx %d returned on more than one page: %v", tx, seen)
		}
		dup[tx] = true
	}
}

// A cursor is opaque to the caller; a garbage value must fail safe to "from
// the newest" rather than 400 or panic — the page round-trips this value
// unmodified, and a stale one after a restart must not break the view.
func TestFlowsJSON_MalformedCursorFallsBackToNewest(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	// A base64url-valid payload whose decoded text does not start with the
	// versioned "v1:" prefix, exercising the version-mismatch branch of
	// decodeFlowsCursor distinctly from "not base64 at all".
	wrongVersion := base64.RawURLEncoding.EncodeToString([]byte("v99:1"))
	// Right prefix, non-numeric payload: exercises the ParseUint failure
	// branch distinctly from "wrong prefix" and "not base64 at all".
	nonNumeric := base64.RawURLEncoding.EncodeToString([]byte("v1:not-a-number"))

	for _, cursor := range []string{"not-base64!!!", "", wrongVersion, nonNumeric} {
		w, got := getFlows(t, a, "?cursor="+cursor)
		if w.Code != http.StatusOK {
			t.Errorf("cursor %q: GET = %d, want 200 (fail safe, never an error)", cursor, w.Code)
		}
		if len(got.Recent) != 1 {
			t.Errorf("cursor %q: recent = %d entries, want the one seeded flow (fell back to newest)", cursor, len(got.Recent))
		}
	}
}

// The cursor is opaque on the wire and never leaks the plain seq. Round-trip
// coverage for the encode/decode pair the HTTP-level tests above exercise
// indirectly through the whole handler.
func TestFlowsCursor_RoundTrips(t *testing.T) {
	for _, seq := range []uint64{1, 42, 999999999} {
		wire := encodeFlowsCursor(seq)
		if wire == "" {
			t.Fatalf("seq %d encoded to empty string", seq)
		}
		if got := decodeFlowsCursor(wire); got != seq {
			t.Errorf("decodeFlowsCursor(encodeFlowsCursor(%d)) = %d", seq, got)
		}
	}
}

// A zero seq (no further page) must encode to the empty string, matching the
// absent-cursor convention decodeFlowsCursor already treats as "from the
// newest" — so a page whose NextCursor is 0 produces a URL the page can
// simply omit ?cursor= from rather than round-tripping a sentinel value.
func TestFlowsCursor_ZeroEncodesToEmpty(t *testing.T) {
	if got := encodeFlowsCursor(0); got != "" {
		t.Errorf("encodeFlowsCursor(0) = %q, want empty", got)
	}
}

// A cursor claiming a version this build does not speak must be ignored
// (fail safe to "from the newest"), not parsed anyway — protects a future
// format change from having to keep parsing the old shape forever.
func TestFlowsCursor_RejectsAnUnknownVersion(t *testing.T) {
	wire := base64.RawURLEncoding.EncodeToString([]byte("v2:42"))
	if got := decodeFlowsCursor(wire); got != 0 {
		t.Errorf("decodeFlowsCursor with an unknown version = %d, want 0 (ignored, not parsed)", got)
	}
}

// A filter value over the 128-byte bound is a 400, not a silent clamp: this
// is the one input where clamping would return MORE than was asked for and
// look like an honest answer.
func TestFlowsJSON_OversizedFilterIsRejected(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	long := strings.Repeat("x", maxFlowsFilterLen+1)
	for _, param := range []string{"device", "addr", "service", "identity", "type", "verdict", "path"} {
		w := httptest.NewRecorder()
		a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/api/flows.json?"+param+"="+long))
		if w.Code != http.StatusBadRequest {
			t.Errorf("param %q at %d bytes: status = %d, want 400", param, len(long), w.Code)
		}
		if !strings.Contains(w.Body.String(), param) {
			t.Errorf("param %q: 400 body %q does not name the offending parameter", param, w.Body.String())
		}
	}

	// Exactly at the bound must still be accepted.
	w := httptest.NewRecorder()
	ok := strings.Repeat("x", maxFlowsFilterLen)
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/api/flows.json?device="+ok))
	if w.Code != http.StatusOK {
		t.Errorf("device filter at exactly %d bytes = %d, want 200", maxFlowsFilterLen, w.Code)
	}
}

// The exact/substring split matters: Verdict, Type and Path are closed
// enumerations, so a filter value must match in full, not as a fragment.
func TestFlowsJSON_ExactFiltersDoNotSubstringMatch(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	// seedFlows records virtual traffic; "virt" must not match "virtual".
	_, got := getFlows(t, a, "?type=virt")
	if len(got.Recent) != 0 {
		t.Errorf("type=virt matched %d rows against traffic_type virtual; type must be an exact match", len(got.Recent))
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

// TestStatusJSON_FlowStoreLimitsAreWired asserts the flow store's effective
// capacity policy reaches /api/status.json from the REAL composition root
// (#329's "status reports configured/effective limits and estimated usage"),
// not from a value a test hand-assigned. It runs with a non-default profile so
// a builder that reported the package constants instead of the store's own
// caps would still fail — asserting the default profile would pass either way.
func TestStatusJSON_FlowStoreLimitsAreWired(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) {
		c.Flows.CapacityProfile = flowstore.ProfileExpanded
	})
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = adminLoopbackHost
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	var got statusdata.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status json: %v", err)
	}
	want := a.runtimes[0].flowStore.Limits()
	if got.Flows.CapacityProfile != want.Profile {
		t.Errorf("flow_store.capacity_profile = %q, want %q", got.Flows.CapacityProfile, want.Profile)
	}
	if got.Flows.MaxRecent != want.MaxRecent {
		t.Errorf("flow_store.max_recent = %d, want %d", got.Flows.MaxRecent, want.MaxRecent)
	}
	if got.Flows.EstimatedBytes != want.EstimatedBytes {
		t.Errorf("flow_store.estimated_bytes = %d, want %d", got.Flows.EstimatedBytes, want.EstimatedBytes)
	}
	if got.Flows.EstimatedBytes <= 0 {
		t.Errorf("flow_store.estimated_bytes = %d, want a positive planning estimate", got.Flows.EstimatedBytes)
	}
}
