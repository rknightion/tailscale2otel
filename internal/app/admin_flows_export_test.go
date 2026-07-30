package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// exportReq builds a direct request for one of the two export handlers.
// Routing (mux.HandleFunc + requireAdminAuth) lives in admin.go, which this
// lane does not own — these tests exercise the handler function itself, the
// same way the rest of this package unit-tests a handler method directly.
func exportReq(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

func TestFlowsExportCSV_GetOnly(t *testing.T) {
	a := flowsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodPost, "/api/flows/export.csv"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/flows/export.csv = %d, want 405", w.Code)
	}
}

func TestFlowsExportJSON_GetOnly(t *testing.T) {
	a := flowsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.handleFlowsExportJSON(w, exportReq(http.MethodPost, "/api/flows/export.json"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/flows/export.json = %d, want 405", w.Code)
	}
}

func TestFlowsExportCSV_UnknownTailnetIs404(t *testing.T) {
	a := flowsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodGet, "/api/flows/export.csv?tailnet=nope.example.com"))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown tailnet = %d, want 404", w.Code)
	}
}

// The export path parses filters through the SAME parseFlowsFilter as
// /api/flows.json (#296's rule: an oversized value is a 400, never a silent
// clamp that would return MORE than was asked for).
func TestFlowsExportCSV_OversizedFilterIsRejected(t *testing.T) {
	a := flowsTestApp(t, nil)
	long := strings.Repeat("x", maxFlowsFilterLen+1)
	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodGet, "/api/flows/export.csv?device="+long))
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized device filter = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "device") {
		t.Errorf("400 body %q does not name the offending parameter", w.Body.String())
	}
}

// parseCSVExport splits a rendered export into its leading #-comment lines
// and the encoding/csv-parsed table (header + rows).
func parseCSVExport(t *testing.T, body string) (comments []string, records [][]string) {
	t.Helper()
	lines := strings.Split(body, "\n")
	var i int
	for i = 0; i < len(lines) && strings.HasPrefix(lines[i], "#"); i++ {
		comments = append(comments, lines[i])
	}
	rest := strings.Join(lines[i:], "\n")
	r := csv.NewReader(strings.NewReader(rest))
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse csv table: %v\nbody:\n%s", err, body)
	}
	return comments, recs
}

func TestFlowsExportCSV_ProvenanceAndRows(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodGet, "/api/flows/export.csv"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET export.csv = %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/csv; charset=utf-8", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="`) || !strings.HasSuffix(cd, `.csv"`) {
		t.Errorf("Content-Disposition = %q, want an attachment filename ending .csv", cd)
	}

	comments, recs := parseCSVExport(t, w.Body.String())
	joined := strings.Join(comments, "\n")
	for _, want := range []string{
		"source=recent_ring",
		"NOT persistent full history",
		"matched=1 returned=1 retained=1 truncated=false",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("csv provenance comments missing %q; got:\n%s", want, joined)
		}
	}

	if len(recs) != 2 { // header + one data row
		t.Fatalf("csv table has %d records, want 2 (header + 1 row):\n%v", len(recs), recs)
	}
	if recs[0][0] != "time" || recs[0][len(recs[0])-1] != "flows" {
		t.Errorf("csv header = %v, want it to start with time and end with flows", recs[0])
	}
	row := recs[1]
	if !strings.Contains(row[5], "camden") { // src_node column
		t.Errorf("csv row src_node = %q, want it to name the seeded device", row[5])
	}
	if row[3] != "100.64.0.1:52000" { // src_addr column
		t.Errorf("csv row src_addr = %q, want the raw endpoint", row[3])
	}
}

func TestFlowsExportJSON_EnvelopeAndRows(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	w := httptest.NewRecorder()
	a.handleFlowsExportJSON(w, exportReq(http.MethodGet, "/api/flows/export.json"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET export.json = %d, want 200: %s", w.Code, w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="`) || !strings.HasSuffix(cd, `.json"`) {
		t.Errorf("Content-Disposition = %q, want an attachment filename ending .json", cd)
	}

	var env flowsExportEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode export.json: %v\nbody: %s", err, w.Body.String())
	}
	if env.Source != "recent_ring" {
		t.Errorf("source = %q, want recent_ring", env.Source)
	}
	if !strings.Contains(env.SourceNote, "NOT persistent full history") {
		t.Errorf("source_note = %q, does not say this is not persistent full history", env.SourceNote)
	}
	if env.Matched != 1 || env.Returned != 1 || env.Retained != 1 || env.Truncated {
		t.Errorf("counts = matched=%d returned=%d retained=%d truncated=%t, want 1/1/1/false",
			env.Matched, env.Returned, env.Retained, env.Truncated)
	}
	if len(env.Rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1", env.Rows)
	}
	if env.Rows[0].DstAddr != "100.64.0.2:443" {
		t.Errorf("row dst_addr = %q, want the raw endpoint", env.Rows[0].DstAddr)
	}
	if env.Tailnet != "example.com" {
		t.Errorf("tailnet = %q, want example.com", env.Tailnet)
	}
}

// A missing store (e.g. an export.json request whose window+filter matches
// nothing) must still carry an empty array, never null: every consumer of
// this envelope ranges over Rows unconditionally.
func TestFlowsExportJSON_EmptyRingIsAnEmptyArrayNotNull(t *testing.T) {
	a := flowsTestApp(t, nil) // no seedFlows: nothing recorded
	w := httptest.NewRecorder()
	a.handleFlowsExportJSON(w, exportReq(http.MethodGet, "/api/flows/export.json"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET export.json = %d, want 200: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"rows":null`) {
		t.Error(`export.json rows marshaled to null on an empty ring`)
	}
	var env flowsExportEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Rows == nil || len(env.Rows) != 0 {
		t.Errorf("rows = %+v, want a non-nil empty slice", env.Rows)
	}
}

// A field whose first character is one Excel/Sheets/LibreOffice reads as a
// formula trigger (=, +, -, @, tab, CR) must be prefixed with a single quote
// in the CSV export, or opening the file runs whatever the tailnet's control
// plane put in a device/user/tag/service name (#299).
//
// This seeds straight into the store (flowstore.Observation), bypassing the
// flow processor's node-identity resolution: a node NAME specifically always
// arrives through enrich's trust-tier logic, which prefixes anything not
// authoritatively verified with "unverified:" — coincidentally neutralizing
// a leading "=" before csvFormulaGuard ever sees it. SrcUser/DstService carry
// no such prefix and are exactly as attacker-influenceable (a user string or
// an ACL-defined VIP service name), which is the realistic injection surface
// this guard exists for.
//
// This is the one guard in this file worth negative-testing by hand: with
// csvFormulaGuard's switch body emptied out, this test fails because the
// row's src_user/dst_service cells come back as the raw formula instead of
// quote-prefixed.
func TestFlowsExportCSV_FormulaInjectionGuard(t *testing.T) {
	a := flowsTestApp(t, nil)
	hostileUser := `=cmd|' /c calc'!A0`
	hostileService := `+HYPERLINK("http://evil","click")`
	a.runtimes[0].flowStore.Record(flowstore.Observation{
		Time:        time.Now().Add(-time.Minute),
		TrafficType: "virtual",
		Transport:   "tcp",
		SrcNode:     "src", DstNode: "dst",
		SrcUser:    hostileUser,
		DstService: hostileService,
		DstPort:    "443",
		Counts:     flowstore.Counts{TxBytes: 1, RxBytes: 1, TxPkts: 1, RxPkts: 1, Flows: 1},
	})

	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodGet, "/api/flows/export.csv"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET export.csv = %d, want 200: %s", w.Code, w.Body.String())
	}
	_, recs := parseCSVExport(t, w.Body.String())
	if len(recs) != 2 {
		t.Fatalf("csv table has %d records, want 2 (header + 1 row)", len(recs))
	}
	row := recs[1]
	srcUser, dstService := row[8], row[7] // src_user, dst_service columns
	if srcUser == hostileUser {
		t.Errorf("src_user %q was NOT guarded against formula injection — a spreadsheet would execute it", srcUser)
	}
	if srcUser != "'"+hostileUser {
		t.Errorf("src_user = %q, want it prefixed with a single quote (%q)", srcUser, "'"+hostileUser)
	}
	if dstService == hostileService {
		t.Errorf("dst_service %q was NOT guarded against formula injection — a spreadsheet would execute it", dstService)
	}
	if dstService != "'"+hostileService {
		t.Errorf("dst_service = %q, want it prefixed with a single quote (%q)", dstService, "'"+hostileService)
	}
}

// Every other field the exporter renders is passed through the same guard,
// so this proves the +, -, @, tab and CR triggers are ALSO covered, not just
// the leading = this package's other test exercises via a real seeded flow.
func TestCSVFormulaGuard_CoversEveryTriggerCharacter(t *testing.T) {
	for _, bad := range []string{"=1+1", "+1", "-1", "@SUM(A1)", "\ttab", "\rcr"} {
		got := csvFormulaGuard(bad)
		if got != "'"+bad {
			t.Errorf("csvFormulaGuard(%q) = %q, want %q", bad, got, "'"+bad)
		}
	}
	for _, safe := range []string{"", "camden", "tag:prod", "100.64.0.1:443", "no_rule"} {
		if got := csvFormulaGuard(safe); got != safe {
			t.Errorf("csvFormulaGuard(%q) = %q, want it left untouched", safe, got)
		}
	}
}

// The export's row count can never exceed flowstore.MaxRecent: maxExportRows
// is defined as exactly that, and the ring itself never holds more. This
// seeds well past the ring's capacity directly into the store (bypassing the
// processor, which would be needlessly slow for ~2500 inserts) and checks the
// export reports the ring as full and truncated rather than silently
// returning every match.
func TestFlowsExportCSV_RowCountIsBoundedByTheRing(t *testing.T) {
	a := flowsTestApp(t, nil)
	rt := a.runtimes[0]
	base := time.Now().Add(-time.Hour / 2)
	const n = flowstore.MaxRecent + 500
	for i := range n {
		rt.flowStore.Record(flowstore.Observation{
			Time:        base.Add(time.Duration(i) * time.Millisecond),
			TrafficType: "virtual",
			Transport:   "tcp",
			SrcNode:     "seed-src",
			DstNode:     "seed-dst",
			DstPort:     "443",
			Counts:      flowstore.Counts{TxBytes: 1, RxBytes: 1, TxPkts: 1, RxPkts: 1, Flows: 1},
		})
	}

	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodGet, "/api/flows/export.csv"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET export.csv = %d, want 200: %s", w.Code, w.Body.String())
	}
	comments, recs := parseCSVExport(t, w.Body.String())
	if len(recs)-1 != flowstore.MaxRecent { // -1 for the header row
		t.Errorf("csv exported %d data rows, want exactly the ring cap %d", len(recs)-1, flowstore.MaxRecent)
	}
	joined := strings.Join(comments, "\n")
	if !strings.Contains(joined, "truncated=true") {
		t.Errorf("provenance does not say the ring was truncated; comments:\n%s", joined)
	}
}

// A client that has already gone away must not pay for (or receive) rows it
// will never read: the CSV writer checks request-context cancellation before
// every row and stops rather than finishing a response nobody wants.
func TestFlowsExportCSV_StopsOnClientDisconnect(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)
	seedFlows(t, a)
	seedFlows(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already gone before the handler even starts reading rows

	req := exportReq(http.MethodGet, "/api/flows/export.csv").WithContext(ctx)
	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, req)

	// The context was already canceled before the handler read anything, so
	// it bails before writing even the provenance comments or header — the
	// cheapest possible "the client is gone, do no work" response.
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: an already-canceled request should do no work at all", w.Body.String())
	}
}

// The export bound has to follow the STORE's ring, not a package constant.
// flows.capacity_profile (#329) scales MaxRecent per store, so a constant
// maxExportRows pinned to the default profile would silently trim an
// "expanded" operator's export to half the rows their ring actually holds —
// and the provenance line would still say truncated=true, which reads as "the
// ring dropped them" rather than "the export refused to read them".
func TestFlowsExportCSV_RowBoundFollowsTheConfiguredProfile(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) {
		c.Flows.CapacityProfile = flowstore.ProfileExpanded
	})
	rt := a.runtimes[0]
	want := rt.flowStore.Limits().MaxRecent
	if want <= flowstore.MaxRecent {
		t.Fatalf("test premise broken: expanded ring is %d, want more than the default %d",
			want, flowstore.MaxRecent)
	}

	base := time.Now().Add(-time.Hour / 2)
	for i := range want + 500 {
		rt.flowStore.Record(flowstore.Observation{
			Time:        base.Add(time.Duration(i) * time.Millisecond),
			TrafficType: "virtual",
			Transport:   "tcp",
			SrcNode:     "seed-src",
			DstNode:     "seed-dst",
			DstPort:     "443",
			Counts:      flowstore.Counts{TxBytes: 1, RxBytes: 1, TxPkts: 1, RxPkts: 1, Flows: 1},
		})
	}

	w := httptest.NewRecorder()
	a.handleFlowsExportCSV(w, exportReq(http.MethodGet, "/api/flows/export.csv"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET export.csv = %d, want 200: %s", w.Code, w.Body.String())
	}
	_, recs := parseCSVExport(t, w.Body.String())
	if got := len(recs) - 1; got != want { // -1 for the header row
		t.Errorf("csv exported %d data rows, want the configured ring cap %d", got, want)
	}
}
