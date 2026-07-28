package app

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// maxExportRows bounds one export response at THIS store's recent-connection
// ring cap. A single RecentPage call with this Limit is therefore guaranteed
// to return every row the ring currently holds that matches Start/End/Filter:
// Matched can never exceed Retained, and Retained can never exceed the ring
// cap, so this Limit is never actually the constraint that trims a real
// response. It exists so the constraint is explicit and load-bearing rather
// than "whatever RecentPage happens to do when Limit is unset" (#299).
//
// It reads the STORE's cap rather than the package constant because
// flows.capacity_profile (#329) scales the ring per store. Pinned to the
// constant, an "expanded" operator's export would stop at the default
// profile's 2000 while their ring held 4000 — and the provenance line would
// still say truncated=true, which reads as "the ring dropped them" rather
// than "the export refused to read them".
func maxExportRows(s *flowstore.Memory) int { return s.Limits().MaxRecent }

// exportSource is the honesty label every export carries, in both formats:
// the rows come from the bounded in-memory recent-connection ring
// (internal/flowstore), never from a persistent store. #294 (SQLite
// persistence) is parked; if it lands, this is what has to change, and only
// then — until it does, an export implying more is the one failure mode this
// feature exists to avoid.
const exportSource = "recent_ring"

// flowsExportSchemaVersion is flowsExportEnvelope's contract version (#323),
// mirroring flowsdata.SchemaVersion for the sibling /api/flows.json endpoint.
// The CSV export carries the same value as a "# schema_version=" comment line
// (its column set has its own de-facto contract: csvExportHeader), so a
// consumer reading either format can tell which version produced it.
const flowsExportSchemaVersion = 1

// flowsExportQuery is the window+filter state /api/flows/export.csv and
// /api/flows/export.json parse from the SAME query parameters
// /api/flows.json already accepts (#296's ?window=/?end=/?tailnet= and its
// seven filters) — the two surfaces share one vocabulary rather than growing
// a second (#299).
type flowsExportQuery struct {
	rt         *tailnetRuntime
	tailnet    string
	start, end time.Time
	filter     flowstore.RecentFilter
	filters    flowsdata.Filters
}

// parseFlowsExportQuery parses and validates an export request. On failure it
// has already written the response (404 for an unknown tailnet, 400 for an
// oversized filter parameter, naming the offending one) and returns ok=false;
// the caller must not write anything else to w in that case.
func (a *App) parseFlowsExportQuery(w http.ResponseWriter, r *http.Request) (flowsExportQuery, bool) {
	rt := a.flowRuntime(r.URL.Query().Get("tailnet"))
	if rt == nil {
		http.NotFound(w, r)
		return flowsExportQuery{}, false
	}

	q := r.URL.Query()
	retention := a.cfg.Flows.Retention.D()
	window := clampDuration(q.Get("window"), defaultFlowsWindow, flowstore.Resolution, retention)
	now := time.Now().UTC()
	end := clampTime(q.Get("end"), now, now.Add(-retention), now)

	filter, filters, ok := parseFlowsFilter(w, q)
	if !ok {
		return flowsExportQuery{}, false // parseFlowsFilter already wrote the 400.
	}

	return flowsExportQuery{
		rt:      rt,
		tailnet: a.runtimeName(rt),
		start:   end.Add(-window),
		end:     end,
		filter:  filter,
		filters: filters,
	}, true
}

// fetch runs the bounded query against the ring. See maxExportRows for why a
// single call is always enough to drain every currently-matching row.
func (eq flowsExportQuery) fetch() flowstore.RecentPage {
	return eq.rt.flowStore.RecentPage(flowstore.RecentQuery{
		Start: eq.start, End: eq.end, Limit: maxExportRows(eq.rt.flowStore), Filter: eq.filter,
	})
}

// describeFilters renders the applied filters for a provenance line. "none"
// when nothing was applied, rather than an empty string that could be misread
// as the line being cut off.
func describeFilters(f flowsdata.Filters) string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("device", f.Device)
	add("addr", f.Addr)
	add("service", f.Service)
	add("identity", f.Identity)
	add("type", f.Type)
	add("verdict", f.Verdict)
	add("path", f.Path)
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// sanitizeFilenamePart makes s safe to embed in a Content-Disposition
// filename: a tailnet name reaches here from configuration, not from an
// untrusted request, but a header value must never carry a raw quote, CR/LF,
// or path separator regardless of how trusted the source is presumed to be.
func sanitizeFilenamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "tailnet"
	}
	return b.String()
}

// exportFilename names the downloaded file after the tailnet, the moment it
// was generated, and the format — so two exports taken minutes apart, or from
// two tailnets, never collide in a Downloads folder.
func exportFilename(tailnet, ext string) string {
	return fmt.Sprintf("tailscale2otel-flows-%s-%s.%s",
		sanitizeFilenamePart(tailnet), time.Now().UTC().Format("20060102T150405Z"), ext)
}

// csvFormulaGuard prefixes a field whose first byte would be read as a
// formula trigger by Excel, Sheets or LibreOffice (=, +, -, @, a tab, or a
// carriage return) with a single quote — every spreadsheet treats a leading
// quote as "force this cell to text" without it being visible to a human
// reading the file as plain CSV. Node names, users, tags and OS strings in
// this export come from the tailnet's control plane, not from this code, so
// they are exactly as attacker-influenceable as any other CSV-injection
// surface; this is applied to every string field uniformly rather than only
// the ones judged risky today.
func csvFormulaGuard(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// csvExportHeader is the CSV export's column order, matching flowstore.Recent
// field-for-field.
var csvExportHeader = []string{
	"time", "traffic_type", "transport",
	"src_addr", "dst_addr", "src_node", "dst_node", "dst_service",
	"src_user", "dst_user", "src_tags", "dst_tags", "src_os", "dst_os",
	"reporter_node_id", "reporter_trust", "reporter_consistency",
	"verdict", "reversed", "rule", "policy_version",
	"path", "derp_region",
	"tx_bytes", "rx_bytes", "tx_packets", "rx_packets", "flows",
}

// csvExportRow renders one connection as a CSV record. Every string field
// passes through csvFormulaGuard; the numeric, boolean and RFC3339 time
// fields cannot carry a formula trigger, so they are not.
func csvExportRow(r flowstore.Recent) []string {
	return []string{
		r.Time.UTC().Format(time.RFC3339),
		csvFormulaGuard(r.TrafficType),
		csvFormulaGuard(r.Transport),
		csvFormulaGuard(r.SrcAddr),
		csvFormulaGuard(r.DstAddr),
		csvFormulaGuard(r.SrcNode),
		csvFormulaGuard(r.DstNode),
		csvFormulaGuard(r.DstService),
		csvFormulaGuard(r.SrcUser),
		csvFormulaGuard(r.DstUser),
		csvFormulaGuard(r.SrcTags),
		csvFormulaGuard(r.DstTags),
		csvFormulaGuard(r.SrcOS),
		csvFormulaGuard(r.DstOS),
		csvFormulaGuard(r.ReporterNodeID),
		csvFormulaGuard(r.ReporterTrust),
		csvFormulaGuard(r.ReporterConsistency),
		csvFormulaGuard(r.Verdict),
		strconv.FormatBool(r.Reversed),
		strconv.Itoa(r.Rule),
		csvFormulaGuard(r.PolicyVersion),
		csvFormulaGuard(r.Path),
		csvFormulaGuard(r.DERPRegion),
		strconv.FormatInt(r.Counts.TxBytes, 10),
		strconv.FormatInt(r.Counts.RxBytes, 10),
		strconv.FormatInt(r.Counts.TxPkts, 10),
		strconv.FormatInt(r.Counts.RxPkts, 10),
		strconv.FormatInt(r.Counts.Flows, 10),
	}
}

// handleFlowsExportCSV streams the filtered recent-connection ring as CSV.
// Read-only, GET-only, behind the same admin gate as the rest of the flow
// view. Provenance travels IN the file as leading #-comment lines — not only
// in a header an operator might strip once the file has left this page —
// because the rows are a bounded recent ring, never persistent full history,
// and a file that silently looked complete would be the one failure mode
// this feature exists to avoid (#299).
//
// Line endings are LF throughout (encoding/csv's default; UseCRLF is left
// false), matching the leading comment lines this handler writes by hand.
func (a *App) handleFlowsExportCSV(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	eq, ok := a.parseFlowsExportQuery(w, r)
	if !ok {
		return
	}
	if r.Context().Err() != nil {
		return
	}
	page := eq.fetch()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename(eq.tailnet, "csv")+`"`)

	fmt.Fprintf(w, "# tailscale2otel flow export\n")
	fmt.Fprintf(w, "# schema_version=%d\n", flowsExportSchemaVersion)
	fmt.Fprintf(w, "# generated_at=%s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "# tailnet=%s\n", oneLine(eq.tailnet))
	fmt.Fprintf(w, "# window_start=%s window_end=%s\n", eq.start.UTC().Format(time.RFC3339), eq.end.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "# filters=%s\n", oneLine(describeFilters(eq.filters)))
	fmt.Fprintf(w, "# source=%s (bounded in-memory ring, NOT persistent full history)\n", exportSource)
	fmt.Fprintf(w, "# matched=%d returned=%d retained=%d truncated=%t\n", page.Matched, len(page.Rows), page.Retained, page.Truncated)

	cw := csv.NewWriter(w)
	if err := cw.Write(csvExportHeader); err != nil {
		a.logger.Error("write flows export csv header", "error", err)
		return
	}
	for _, row := range page.Rows {
		select {
		case <-r.Context().Done():
			// A client disconnect stops the work rather than finishing a
			// response nobody will read; whatever rows are already flushed
			// stand as a (labeled, via matched/returned above) partial file.
			cw.Flush()
			return
		default:
		}
		if err := cw.Write(csvExportRow(row)); err != nil {
			a.logger.Error("write flows export csv row", "error", err)
			cw.Flush()
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		a.logger.Error("flush flows export csv", "error", err)
	}
}

// oneLine collapses CR/LF in a value bound for a #-comment line, so a
// pathological tailnet name or filter value cannot inject a fake comment line
// or break out into what would be read as CSV data.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// flowsExportEnvelope is /api/flows/export.json's response shape: the same
// provenance the CSV export carries as comment lines, structured, plus Rows.
type flowsExportEnvelope struct {
	// SchemaVersion is flowsExportSchemaVersion at build time (#323).
	SchemaVersion int               `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Tailnet       string            `json:"tailnet"`
	Window        flowstore.Range   `json:"window"`
	Filters       flowsdata.Filters `json:"filters,omitzero"`
	// Source is the fixed machine-readable label (exportSource); SourceNote
	// spells out in words what an integration reading only Source might miss.
	Source     string `json:"source"`
	SourceNote string `json:"source_note"`
	Matched    int    `json:"matched"`
	Returned   int    `json:"returned"`
	Retained   int    `json:"retained"`
	Truncated  bool   `json:"truncated"`
	// Rows is never null: an empty export is a real, distinct answer from a
	// missing field, and every consumer of this envelope should be able to
	// range over Rows unconditionally.
	Rows []flowstore.Recent `json:"rows"`
}

// handleFlowsExportJSON serves the filtered recent-connection ring as a
// provenance-carrying JSON envelope. Read-only, GET-only, behind the same
// admin gate as the rest of the flow view.
//
// The envelope is built and encoded in one bounded step rather than streamed
// element-by-element, matching how /api/flows.json's own (also
// ring-bounded, also up-to-a-few-thousand-row) Recent response is built —
// see writeIndentedJSON. That bound is maxExportRows (== flowstore.MaxRecent),
// so this can never grow past what the ring itself is capped at.
func (a *App) handleFlowsExportJSON(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	eq, ok := a.parseFlowsExportQuery(w, r)
	if !ok {
		return
	}
	if r.Context().Err() != nil {
		return
	}
	page := eq.fetch()
	rows := page.Rows
	if rows == nil {
		rows = []flowstore.Recent{}
	}

	env := flowsExportEnvelope{
		SchemaVersion: flowsExportSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Tailnet:       eq.tailnet,
		Window:        flowstore.Range{Start: eq.start, End: eq.end},
		Filters:       eq.filters,
		Source:        exportSource,
		SourceNote:    "bounded in-memory recent-connection ring; NOT persistent full history",
		Matched:       page.Matched,
		Returned:      len(page.Rows),
		Retained:      page.Retained,
		Truncated:     page.Truncated,
		Rows:          rows,
	}

	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename(eq.tailnet, "json")+`"`)
	writeIndentedJSON(w, env, a.logger, "encode flows export json")
}
