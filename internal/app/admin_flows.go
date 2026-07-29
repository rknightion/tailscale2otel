package app

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v3/internal/app/flowhtml"
	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore/sqlitestore"
)

// Bounds on what /api/flows.json will do for one request. The window, page size
// and ranked-list length all come from the URL; the admin gate keeps strangers
// out, but a mistyped or hostile value should still cost a bounded amount of
// work rather than whatever was asked for.
const (
	defaultFlowsWindow = time.Hour
	defaultFlowsTopN   = 20
	maxFlowsTopN       = 200
	defaultFlowsRecent = 200
	maxFlowsRecent     = 1000
	// maxFlowsFilterLen bounds each recent-connection filter parameter. Unlike
	// window/top/recent, a filter is the one input where CLAMPING would be
	// wrong: silently truncating it could turn a specific search into one that
	// matches more than the operator asked for, which reads as an answer
	// rather than as "your input was too long" (#296). So an oversized filter
	// is a 400, not a clamp.
	maxFlowsFilterLen = 128
)

// flowsCursorPrefix versions the opaque wire format of the ?cursor= parameter
// (base64url, no padding, of "v1:<seq>"). It exists so a future format change
// has something to key on; decodeFlowsCursor rejects anything else.
const flowsCursorPrefix = "v1:"

// encodeFlowsCursor renders seq as the opaque cursor /api/flows.json hands
// back in NextCursor. A zero seq (no further page) encodes to the empty
// string, which the page's absent-cursor convention already treats as
// "no more data".
func encodeFlowsCursor(seq uint64) string {
	if seq == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(flowsCursorPrefix + strconv.FormatUint(seq, 10)))
}

// decodeFlowsCursor parses the ?cursor= parameter. An absent, malformed,
// wrongly-versioned, or unparseable cursor decodes to 0 ("from the newest")
// rather than an error: a cursor is a value the page merely round-trips, and
// one gone stale must fail safe to the start of the list, never break the view
// (#296).
//
// Whether a cursor CAN go stale depends on the backend. The in-memory ring's
// seq counter restarts with the process, so a cursor held across a restart is
// meaningless and lands back at the newest row. The persistent backend (#294)
// numbers rows with an AUTOINCREMENT rowid that survives restarts and is never
// reused, so a cursor stays valid across one — a shared /flows URL keeps its
// place. Neither case needs handling here; both are already safe.
func decodeFlowsCursor(s string) uint64 {
	if s == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	text, ok := strings.CutPrefix(string(raw), flowsCursorPrefix)
	if !ok {
		return 0
	}
	seq, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0
	}
	return seq
}

// newFlowStore builds this process's per-tailnet flow store, or nil when the
// view is switched off or cannot be reached. The store's only consumer is
// /flows on the admin landing page, so with that surface disabled it would
// accumulate memory nobody could ever look at — config.Warnings() tells the
// operator the setting is doing nothing.
// The nil return is a LITERAL nil, never a typed nil pointer: the return type is
// the interface, and a (*flowstore.Memory)(nil) in an interface compares
// non-nil, so every "view disabled" check downstream would pass and then panic.
//
// With flows.store.directory set it builds the opt-in persistent backend instead
// (#294). If that fails to open, the flow view is switched OFF rather than
// quietly falling back to memory: an operator who configured persistence and
// then sees a working /flows would reasonably conclude they had history, and
// would find out otherwise only after the restart they were retaining across.
// A disabled view 404s, which is the honest answer.
//
// The failure is not fatal to the process. This exporter's job is OTLP export;
// the flow view is an admin convenience on top of it. Taking the telemetry
// pipeline down because an auxiliary view's directory is unwritable would be a
// worse outcome than serving telemetry without the view, so this logs at ERROR
// and continues.
func newFlowStore(cfg *config.Config, tailnet string, logger *slog.Logger) flowstore.Store {
	if !cfg.Flows.Enabled || !cfg.Admin.Enabled || !cfg.Admin.LandingPage {
		return nil
	}

	if dir := cfg.Flows.Store.Directory; dir != "" {
		store, err := sqlitestore.Open(sqlitestore.Options{
			Dir:           dir,
			Tailnet:       tailnet,
			Retention:     cfg.Flows.Store.Retention.D(),
			MaxRows:       cfg.Flows.Store.MaxRows,
			MaxExportRows: cfg.Flows.Store.MaxExportRows,
			MaxFutureSkew: cfg.Flows.MaxFutureSkew.D(),
			QueueSize:     cfg.Flows.Store.QueueSize,
			BatchSize:     cfg.Flows.Store.BatchSize,
			FlushInterval: cfg.Flows.Store.FlushInterval.D(),
			QueryTimeout:  cfg.Flows.Store.QueryTimeout.D(),
			SweepInterval: cfg.Flows.Store.SweepInterval.D(),
			// Unlike the memory ring, rows here outlive the process and land in
			// backups, so pii_filter applies (see flowRedactor).
			Redact: flowRedactor(piiCategories(cfg.PIIFilter)),
		})
		if err != nil {
			logger.Error("flow view disabled: persistent flow store failed to open",
				"error", err, "path", dir, "tailnet", tailnet)
			return nil
		}
		return store
	}

	return flowstore.NewMemory(
		int(cfg.Flows.Retention.D()/flowstore.Resolution),
		flowstore.WithMaxFutureSkew(cfg.Flows.MaxFutureSkew.D()),
		flowstore.WithCapacityProfile(cfg.Flows.CapacityProfile),
	)
}

// flowRetention is how far back a runtime's store can actually answer, and is
// what the window and end-time clamps must use.
//
// It is NOT always flows.retention. That setting sizes the in-memory ring and is
// capped at 24h, so clamping against it with the persistent backend enabled
// would hold every query and export to a day against a database retaining
// thirty — the multi-day history #294 exists to provide would be unreachable
// through the only UI that reads it.
//
// Presence of the path is the test rather than the store's reported Kind: a
// persistent store that failed to open leaves flowStore nil and the view off, so
// a non-nil store with a configured path is a sqlite one, and Stats() on that
// backend costs real queries this is called on every request to avoid.
func (a *App) flowRetention() time.Duration {
	if a.cfg.Flows.Store.Directory != "" {
		return a.cfg.Flows.Store.Retention.D()
	}
	return a.cfg.Flows.Retention.D()
}

// flowsEnabled reports whether any runtime is feeding a flow store, which is
// what decides whether the /flows routes are registered at all. An unregistered
// route 404s, which is the honest answer: an empty result would read as "no
// traffic" rather than "not collecting".
func (a *App) flowsEnabled() bool {
	for _, rt := range a.runtimes {
		if rt.flowStore != nil {
			return true
		}
	}
	return false
}

// flowTailnets lists the observed tailnet names in runtime order, so the page's
// selector is stable between polls.
func (a *App) flowTailnets() []string {
	out := make([]string, 0, len(a.runtimes))
	for _, rt := range a.runtimes {
		if rt.flowStore != nil {
			out = append(out, a.runtimeName(rt))
		}
	}
	return out
}

// flowRuntime resolves the ?tailnet= parameter to a runtime. An empty parameter
// selects the first; an unknown one resolves to nothing, so the caller can 404
// rather than quietly answering about a different tailnet.
func (a *App) flowRuntime(name string) *tailnetRuntime {
	for _, rt := range a.runtimes {
		if rt.flowStore == nil {
			continue
		}
		if name == "" || a.runtimeName(rt) == name {
			return rt
		}
	}
	return nil
}

// flowStoreInfo summarizes the flow store for the status page, combining every
// tailnet's: the memory is one process's, so one set of numbers is what an
// operator is actually asking about. Covered spans the earliest and latest
// minute held anywhere, which is the honest answer to "how far back can I look".
func (a *App) flowStoreInfo() statusdata.FlowStoreInfo {
	info := statusdata.FlowStoreInfo{Enabled: a.flowsEnabled()}
	if !info.Enabled {
		return info
	}
	info.Retention = a.flowRetention().String()

	var earliest, latest time.Time
	healthy := true
	for _, rt := range a.runtimes {
		if rt.flowStore == nil {
			continue
		}
		s := rt.flowStore.Stats()
		info.Buckets += s.Buckets
		info.Capacity = s.Capacity // identical across runtimes: one config value
		// Also identical across runtimes, and for the same reason: every store
		// is built from the one flows.capacity_profile. Reported per store
		// rather than read back off the config so the page shows what is
		// actually in force. EstimatedBytes stays PER TAILNET rather than
		// summed — it is a worst-case bound on one store, and adding worst
		// cases across tailnets compounds an over-estimate into a number no
		// deployment could reach.
		lim := rt.flowStore.Limits()
		info.CapacityProfile = lim.Profile
		info.MaxRecent = lim.MaxRecent
		info.EstimatedBytes = lim.EstimatedBytes
		info.Observations += s.Observations
		info.Truncated += s.Truncated

		// Backend state is summed where summing means something (queue depth,
		// drops, rows, bytes are per-file quantities) and ANDed where it does
		// not: one unhealthy tailnet must not be hidden by healthy peers, so
		// Healthy is false if any store has failed and every distinct reason is
		// carried. Kind is uniform by construction — there is one global
		// flows.store.directory, so every runtime builds the same backend.
		b := s.Backend
		info.Backend.Kind = b.Kind
		info.Backend.Persistent = b.Kind == flowstore.BackendSQLite
		info.Backend.Queued += b.Queued
		info.Backend.QueueDrops += b.QueueDrops
		info.Backend.Rows += b.Rows
		info.Backend.SizeBytes += b.SizeBytes
		if !b.Healthy {
			healthy = false
			if b.Error != "" && !slices.Contains(info.Backend.Errors, b.Error) {
				info.Backend.Errors = append(info.Backend.Errors, b.Error)
			}
		}
		if !s.Earliest.IsZero() && (earliest.IsZero() || s.Earliest.Before(earliest)) {
			earliest = s.Earliest
		}
		if s.Latest.After(latest) {
			latest = s.Latest
		}
	}
	if !earliest.IsZero() {
		info.Covered = latest.Add(flowstore.Resolution).Sub(earliest).Round(time.Minute).String()
	}
	info.Backend.Healthy = healthy
	return info
}

// handleFlowsJSON serves the flow view's data. Read-only, GET-only, and behind
// the same admin gate as the rest of the admin surface.
func (a *App) handleFlowsJSON(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	rt := a.flowRuntime(r.URL.Query().Get("tailnet"))
	if rt == nil {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	retention := a.flowRetention()
	window := clampDuration(q.Get("window"), defaultFlowsWindow, flowstore.Resolution, retention)
	topN := clampInt(q.Get("top"), defaultFlowsTopN, 1, maxFlowsTopN)
	recent := clampInt(q.Get("recent"), defaultFlowsRecent, 0, maxFlowsRecent)

	filter, filters, ok := parseFlowsFilter(w, q)
	if !ok {
		return // parseFlowsFilter already wrote the 400.
	}

	// The timeline scrubber selects a span ending somewhere in the past, so the
	// window's right-hand edge is a parameter too. It cannot run ahead of now or
	// behind what the store retains.
	now := time.Now().UTC()
	end := clampTime(q.Get("end"), now, now.Add(-retention), now)

	result := rt.flowStore.Query(flowstore.Query{
		Start: end.Add(-window),
		End:   end,
		TopN:  topN,
	})

	// The SAME window the aggregates just used. Asking for the newest rows
	// outright put a live connection list beside a historical chart whenever
	// the scrubber was pinned to the past (#295). Filtering and cursor
	// pagination move server-side here so a connection matching the filter but
	// outside the returned page is still reachable via NextCursor, rather than
	// invisible the way client-side filtering over one fixed page was (#296).
	page := rt.flowStore.RecentPage(flowstore.RecentQuery{
		Start:  end.Add(-window),
		End:    end,
		Limit:  recent,
		Cursor: decodeFlowsCursor(q.Get("cursor")),
		Filter: filter,
	})

	resp := flowsdata.Response{
		SchemaVersion:   flowsdata.SchemaVersion,
		Tailnet:         a.runtimeName(rt),
		Tailnets:        a.flowTailnets(),
		Window:          window.String(),
		Retention:       retention.String(),
		Stats:           rt.flowStore.Stats(),
		Result:          result,
		Recent:          page.Rows,
		Policy:          policyInfo(rt.policy),
		Exercised:       exercisedRules(rt.policy, result.Rules),
		GeneratedAt:     now.Format(time.RFC3339),
		Filters:         filters,
		RecentMatched:   page.Matched,
		RecentReturned:  len(page.Rows),
		RecentRetained:  page.Retained,
		RecentTruncated: page.Truncated,
		NextCursor:      encodeFlowsCursor(page.NextCursor),
	}
	// The page iterates Recent unconditionally, and a nil slice marshals to null.
	if resp.Recent == nil {
		resp.Recent = []flowstore.Recent{}
	}
	writeIndentedJSON(w, resp, a.logger, "encode flows json")
}

// flowsFilterParam is one recent-connection filter query parameter and the
// flowstore.RecentFilter/flowsdata.Filters field it feeds.
type flowsFilterParam struct {
	query string
	store *string
	wire  *string
}

// parseFlowsFilter reads the seven recent-connection filter parameters off q.
// A value over maxFlowsFilterLen writes a 400 naming the offending parameter
// to w and returns ok=false; the caller must not write anything else to w in
// that case. Silently clamping here (the way window/top/recent do) would be
// wrong for a filter specifically: it would let a request that typed a long,
// specific search silently fall back to a shorter, less specific one and
// return MORE rows than asked for, which reads as an answer rather than as
// "your input was rejected" (#296).
func parseFlowsFilter(w http.ResponseWriter, q url.Values) (filter flowstore.RecentFilter, wire flowsdata.Filters, ok bool) {
	for _, p := range []flowsFilterParam{
		{"device", &filter.Device, &wire.Device},
		{"addr", &filter.Addr, &wire.Addr},
		{"service", &filter.Service, &wire.Service},
		{"identity", &filter.Identity, &wire.Identity},
		{"type", &filter.Type, &wire.Type},
		{"verdict", &filter.Verdict, &wire.Verdict},
		{"path", &filter.Path, &wire.Path},
	} {
		v := q.Get(p.query)
		if len(v) > maxFlowsFilterLen {
			http.Error(w, fmt.Sprintf("query parameter %q exceeds %d bytes", p.query, maxFlowsFilterLen), http.StatusBadRequest)
			return flowstore.RecentFilter{}, flowsdata.Filters{}, false
		}
		*p.store = v
		*p.wire = v
	}
	return filter, wire, true
}

// policyInfo describes the policy this tailnet's traffic was reconciled
// against. It reports the rules themselves; what the policy SAID about the
// traffic lives in the store's aggregates.
//
// Rules are always sent in full — the list is a tailnet's own ACL, so tens of
// entries, and truncating it would report a live rule as one that permitted
// nothing.
func policyInfo(s *aclpolicy.Store) flowsdata.PolicyInfo {
	// Always an array, never null: the page and every jq one-liner in the docs
	// iterate it unconditionally.
	info := flowsdata.PolicyInfo{Rules: []flowsdata.PolicyRule{}}
	if s == nil {
		return info
	}
	if err := s.Err(); err != nil {
		info.Error = err.Error()
	}
	p := s.Policy()
	if p == nil {
		return info
	}
	info.Available = true
	info.Version = p.Version()
	rules := p.Rules()
	info.Rules = make([]flowsdata.PolicyRule, 0, len(rules))
	for i, r := range rules {
		info.Rules = append(info.Rules, flowsdata.PolicyRule{Index: i, Kind: r.Kind, Source: r.Source})
	}
	return info
}

// exercisedRules names the rule behind every per-rule count, resolving each row
// against the snapshot of the policy version that produced it.
//
// This is done here, once, rather than left to whoever reads the JSON. A rule
// index is only meaningful against the exact rule list it came from, so a
// consumer joining indexes to the CURRENT policy is right until the ACL is
// edited and quietly wrong afterwards — and an edited ACL is exactly when
// someone is looking at this page (#302).
//
// A version whose snapshot is no longer retained, or an index outside it, yields
// Available=false with no rule text. That is the honest answer; substituting the
// current rule would put a name an operator recognizes next to traffic it never
// carried, which is worse than an admitted gap because it looks like an answer.
func exercisedRules(s *aclpolicy.Store, stats []flowstore.RuleStat) []flowsdata.ExercisedRule {
	out := make([]flowsdata.ExercisedRule, 0, len(stats))
	for _, st := range stats {
		row := flowsdata.ExercisedRule{
			PolicyVersion: st.PolicyVersion,
			Index:         st.Rule,
			Counts:        st.Counts,
		}
		if s != nil {
			if rules, ok := s.Snapshot(st.PolicyVersion); ok && st.Rule >= 0 && st.Rule < len(rules) {
				row.Available = true
				row.Kind = rules[st.Rule].Kind
				row.Source = rules[st.Rule].Source
			}
		}
		out = append(out, row)
	}
	return out
}

// handleFlowsPage renders the /flows shell. It carries no traffic data — the
// page polls /api/flows.json for that — so a slow or empty store never delays
// the first paint.
func (a *App) handleFlowsPage(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	rt := a.flowRuntime("")
	if rt == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := flowsdata.Page{
		ServiceName: serviceName,
		Version:     a.version,
		Tailnets:    a.flowTailnets(),
		Tailnet:     a.runtimeName(rt),
		Retention:   a.flowRetention().String(),
		RefreshMs:   int(a.cfg.Admin.StatusRefreshInterval.D().Milliseconds()),
	}
	if err := flowhtml.Render(w, page); err != nil {
		// Headers are already committed by this point; there is nothing to send
		// but a log line.
		a.logger.Error("render flows page", "error", err)
	}
}

// clampDuration parses a duration parameter, falling back to def when it is
// absent or unparseable and clamping it into [minD, maxD]. A window wider than
// what the store retains is pointless, and one narrower than a bucket returns
// nothing at all.
func clampDuration(s string, def, minD, maxD time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if s == "" || err != nil || d <= 0 {
		d = def
	}
	return min(max(d, minD), maxD)
}

// clampTime parses an RFC3339 timestamp parameter, falling back to def when it
// is absent or unparseable and clamping it into [earliest, latest].
func clampTime(s string, def, earliest, latest time.Time) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if s == "" || err != nil {
		t = def
	}
	if t.Before(earliest) {
		return earliest
	}
	if t.After(latest) {
		return latest
	}
	return t.UTC()
}

// clampInt parses an integer parameter, falling back to def when it is absent or
// unparseable and clamping it into [minN, maxN].
func clampInt(s string, def, minN, maxN int) int {
	n, err := strconv.Atoi(s)
	if s == "" || err != nil {
		n = def
	}
	return min(max(n, minN), maxN)
}
