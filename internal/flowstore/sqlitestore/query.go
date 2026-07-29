package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// This file answers flowstore.Query with SQL GROUP BY over the raw `flows`
// table instead of mirroring Memory.Query's per-minute bucket walk (see
// store.go's package doc for why: one row per connection, aggregated at
// query time, no cap-folding). Every ranked list below reproduces the exact
// SAME semantics as the corresponding helper in flowstore.go — sort order,
// which rows count towards which dimension, and any per-row special-casing
// (dual-endpoint labels, tag splitting, the reversed-verdict split) — so a
// caller cannot tell which backend answered short of Result.Truncated always
// reading zero here.
//
// Two of flowstore's helpers are unexported (Counts.add, splitTags,
// endpointIdentity, peerIdentity, the external/unknown sentinels) and are
// re-implemented locally below rather than exported just for this package;
// each has a comment pointing at its flowstore.go original so the two never
// drift silently.

// sumExpr is the five-counter aggregate every dimension below sums. NULL
// (no matching rows) becomes zero rather than a Go-side nil check at every
// call site.
const sumExpr = "COALESCE(SUM(tx_bytes),0), COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_packets),0), COALESCE(SUM(rx_packets),0), COALESCE(SUM(flows),0)"

// external/unknown mirror the unexported sentinels in flowstore.go (used by
// peerIdentity and endpointIdentity there). They are not exported, so the
// values are duplicated here — see flowstore.go's own comment on them for
// why a live tailnet's identity fields collapse to these two names.
const (
	externalSentinel = "external"
	unknownSentinel  = "unknown"
)

// window is one resolved Query: the [start,end] bound, the SQL WHERE
// fragment matching it, and the step/topN every dimension below shares. It
// is resolved exactly once per Query call so every dimension answers against
// the identical window — resolving it per-dimension could let two lists
// disagree with Totals over a race with the sweep goroutine.
type window struct {
	hasStart bool
	start    time.Time
	end      time.Time
	startNS  int64
	endNS    int64
	step     time.Duration
	topN     int
}

// whereClause returns the WHERE fragment and its positional args, in the
// order they appear in the fragment text — every caller below appends its
// own extra predicates and args AFTER these, so the two stay aligned.
func (w window) whereClause() (string, []any) {
	if w.hasStart {
		return "time >= ? AND time <= ?", []any{w.startNS, w.endNS}
	}
	return "time <= ?", []any{w.endNS}
}

// resolveWindow mirrors Memory.Query's own resolution byte-for-byte
// (defaults, and the step-doubling loop that keeps a wide window's series
// under MaxPoints), so the two backends answer identical Querys identically.
func (s *Store) resolveWindow(q flowstore.Query) window {
	end := q.End
	if end.IsZero() {
		end = s.opts.Now()
	}
	topN := q.TopN
	if topN <= 0 {
		topN = 20
	}
	step := q.Step
	if step < flowstore.Resolution {
		step = flowstore.Resolution
	}
	if !q.Start.IsZero() {
		for span := end.Sub(q.Start); span/step > flowstore.MaxPoints; span = end.Sub(q.Start) {
			step *= 2
		}
	}

	w := window{end: end, step: step, topN: topN, endNS: timeToDB(end)}
	if !q.Start.IsZero() {
		w.hasStart = true
		w.start = q.Start
		w.startNS = timeToDB(q.Start)
	}
	return w
}

// addCounts accumulates src into *dst. flowstore.Counts.add exists but is
// unexported, so every merge in this file goes through this instead.
func addCounts(dst *flowstore.Counts, src flowstore.Counts) {
	dst.TxBytes += src.TxBytes
	dst.RxBytes += src.RxBytes
	dst.TxPkts += src.TxPkts
	dst.RxPkts += src.RxPkts
	dst.Flows += src.Flows
}

// scanCounts reads the five sumExpr columns from the current row into c,
// after any caller-supplied leading columns have already been scanned via
// dest.
func scanCounts(rows *sql.Rows, c *flowstore.Counts, dest ...any) error {
	dest = append(dest, &c.TxBytes, &c.RxBytes, &c.TxPkts, &c.RxPkts, &c.Flows)
	return rows.Scan(dest...)
}

// Query answers a flowstore.Query with SQL aggregates over the retained
// rows. Every sub-query runs inside the same bounded context, and a failing
// sub-query calls s.fail and contributes zero/empty to Result rather than
// aborting the rest — one bad dimension must not blank the whole page.
func (s *Store) Query(q flowstore.Query) flowstore.Result {
	w := s.resolveWindow(q)
	ctx, cancel := s.queryCtx()
	defer cancel()

	res := flowstore.Result{
		Window: flowstore.Range{Start: w.start, End: w.end},
		Step:   w.step.String(),
	}

	if totals, earliest, latest, ok := s.queryTotals(ctx, w); ok {
		res.Totals = totals
		// Report the window actually covered, exactly as Memory.Query does:
		// an unbounded Start resolves to the earliest retained row, and End
		// is trimmed back to the latest row seen (+Resolution, so the last
		// row's own minute is still inside the reported window) when that is
		// tighter than what was asked for.
		if w.start.IsZero() {
			res.Window.Start = earliest
		}
		if !latest.IsZero() && latest.Add(flowstore.Resolution).Before(res.Window.End) {
			res.Window.End = latest.Add(flowstore.Resolution)
		}
	}

	res.Series = s.querySeries(ctx, w)
	res.Pairs = s.queryPairs(ctx, w)
	res.Nodes = s.queryNodes(ctx, w)
	res.Ports = s.queryPorts(ctx, w)
	res.Transports = s.queryLabel(ctx, w, "transport", "")
	res.TrafficTypes = s.queryLabel(ctx, w, "traffic_type", "")
	res.Users = s.queryDualEndpointLabels(ctx, w, "src_user", "dst_user")
	res.Tags = s.queryTagLabels(ctx, w)
	res.OSes = s.queryDualEndpointLabels(ctx, w, "src_os", "dst_os")
	res.TagMatrix = s.queryTagMatrix(ctx, w)
	res.UserMatrix = s.querySimpleMatrix(ctx, w, "src_user", "dst_user")
	res.OSMatrix = s.querySimpleMatrix(ctx, w, "src_os", "dst_os")
	res.Verdicts = s.queryVerdicts(ctx, w)
	res.Unexplained = s.queryUnexplained(ctx, w)
	res.Rules = s.queryRules(ctx, w)
	res.Paths = s.queryLabel(ctx, w, "path", "")
	res.DERPRegions = s.queryLabel(ctx, w, "derp_region", " AND path = ?", flowstore.PathDERP)
	res.PeerPaths = s.queryPeerPaths(ctx, w)

	// Truncated stays zero: this backend stores raw rows and aggregates
	// exactly, so nothing is ever folded into flowstore.Other — see the
	// package doc in store.go.
	return res
}

// queryTotals sums the whole window and reports the earliest/latest row
// time seen, which Query uses to report the window actually covered.
func (s *Store) queryTotals(ctx context.Context, w window) (c flowstore.Counts, earliest, latest time.Time, ok bool) {
	whereSQL, args := w.whereClause()
	query := "SELECT " + sumExpr + ", MIN(time), MAX(time) FROM flows WHERE " + whereSQL
	var minNS, maxNS sql.NullInt64
	err := s.db.QueryRowContext(ctx, query, args...).
		Scan(&c.TxBytes, &c.RxBytes, &c.TxPkts, &c.RxPkts, &c.Flows, &minNS, &maxNS)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query totals: %w", err))
		return flowstore.Counts{}, time.Time{}, time.Time{}, false
	}
	if minNS.Valid {
		earliest = dbToTime(minNS.Int64)
	}
	if maxNS.Valid {
		latest = dbToTime(maxNS.Int64)
	}
	return c, earliest, latest, true
}

// querySeries buckets the window at w.step by integer-dividing the
// nanosecond time column, the SQL analog of Memory's
// `b.start.Truncate(step).Unix()`. The two agree for every step this package
// actually produces (Resolution doubled some number of times, so always a
// power-of-two number of minutes): the Unix epoch sits on a whole-day
// boundary, and every such step divides a day evenly, so floor-dividing
// nanoseconds since epoch lands on the same instant Truncate would from
// Go's zero time.
func (s *Store) querySeries(ctx context.Context, w window) []flowstore.Point {
	whereSQL, args := w.whereClause()
	stepNS := w.step.Nanoseconds()
	query := "SELECT (time / ?) AS bucket, " + sumExpr + " FROM flows WHERE " + whereSQL + " GROUP BY bucket" //nolint:gosec // G201/G202: SQL built from fixed internal column-name literals and query shapes; every value is a bound '?' param, never interpolated
	rows, err := s.db.QueryContext(ctx, query, append([]any{stepNS}, args...)...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query series: %w", err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.Point
	for rows.Next() {
		var bucket int64
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &bucket); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan series: %w", err))
			return nil
		}
		out = append(out, flowstore.Point{Time: time.Unix(0, bucket*stepNS).UTC(), Counts: c})
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: series rows: %w", err))
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// queryPairs groups directed node-to-node relationships. A row missing
// either endpoint still contributes to Totals and every label breakdown but
// forms no edge — mirrors bucket.record's own `if o.SrcNode != "" &&
// o.DstNode != ""` gate.
func (s *Store) queryPairs(ctx context.Context, w window) []flowstore.PairStat {
	whereSQL, args := w.whereClause()
	query := `SELECT src_node, dst_node, traffic_type, ` + sumExpr + ` FROM flows WHERE ` + whereSQL + ` AND src_node <> '' AND dst_node <> '' GROUP BY src_node, dst_node, traffic_type` //nolint:gosec // G202: built from fixed internal literals and the window's own WHERE fragment; every value is a bound '?' param
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query pairs: %w", err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.PairStat
	for rows.Next() {
		var p flowstore.PairStat
		if err := scanCounts(rows, &p.Counts, &p.Src, &p.Dst, &p.TrafficType); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan pairs: %w", err))
			return nil
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: pairs rows: %w", err))
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].TrafficType < out[j].TrafficType
	})
	return truncate(out, w.topN)
}

// queryNodes reproduces bucket.addNode's role split: a node's Sent is the
// direct sum of rows where it is the source, but its Received MIRRORS the
// row's counters (what it received is what the other end transmitted) —
// see addNode's own comment in flowstore.go. Two independent GROUP BYs
// (one per role) are folded together in Go rather than attempted as one
// query, since the two roles read different columns with swapped meanings.
func (s *Store) queryNodes(ctx context.Context, w window) []flowstore.NodeStat {
	whereSQL, args := w.whereClause()

	sent, ok := s.queryNodeCounts(ctx, "src_node", whereSQL, args, false)
	if !ok {
		return nil
	}
	received, ok := s.queryNodeCounts(ctx, "dst_node", whereSQL, args, true)
	if !ok {
		return nil
	}

	names := make(map[string]struct{}, len(sent)+len(received))
	for n := range sent {
		names[n] = struct{}{}
	}
	for n := range received {
		names[n] = struct{}{}
	}
	out := make([]flowstore.NodeStat, 0, len(names))
	for n := range names {
		out = append(out, flowstore.NodeStat{Node: n, Sent: sent[n], Received: received[n]})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Bytes(), out[j].Bytes(); a != b {
			return a > b
		}
		return out[i].Node < out[j].Node
	})
	return truncate(out, w.topN)
}

// queryNodeCounts runs "SELECT col, sumExpr FROM flows WHERE <whereSQL> AND
// col <> ” GROUP BY col" and returns a name->Counts map. mirror is true for
// the dst_node/"received" side: each row's tx/rx are swapped before storing,
// reproducing addNode's role-mirroring — see queryNodes' doc comment above.
// col is always one of the fixed "src_node"/"dst_node" literals this package
// passes in, never external input, so building the query with fmt.Sprintf is
// safe despite gosec's blanket G201 rule.
func (s *Store) queryNodeCounts(ctx context.Context, col, whereSQL string, args []any, mirror bool) (map[string]flowstore.Counts, bool) { //nolint:unparam // ok is always meaningful even though today every call site returns true or bails
	query := fmt.Sprintf(`SELECT %s, %s FROM flows WHERE %s AND %s <> '' GROUP BY %s`, col, sumExpr, whereSQL, col, col) //nolint:gosec // G201: col is a fixed internal literal ("src_node"/"dst_node"), never external input
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query nodes (%s): %w", col, err))
		return nil, false
	}
	defer rows.Close()

	out := map[string]flowstore.Counts{}
	for rows.Next() {
		var node string
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &node); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan nodes (%s): %w", col, err))
			return nil, false
		}
		if mirror {
			c = flowstore.Counts{TxBytes: c.RxBytes, RxBytes: c.TxBytes, TxPkts: c.RxPkts, RxPkts: c.TxPkts, Flows: c.Flows}
		}
		out[node] = c
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: nodes rows (%s): %w", col, err))
		return nil, false
	}
	return out, true
}

// queryPorts mirrors recordPorts: destination-service breakdown from
// OVERLAY traffic only, since a physical row's "port" is either an
// ephemeral WireGuard port or (on a relayed path) not a port at all.
func (s *Store) queryPorts(ctx context.Context, w window) []flowstore.PortStat {
	whereSQL, args := w.whereClause()
	query := `SELECT dst_port, transport, dst_service, ` + sumExpr + ` FROM flows WHERE ` + whereSQL + ` AND dst_port <> '' AND traffic_type <> ? GROUP BY dst_port, transport, dst_service` //nolint:gosec // G202: built from fixed internal literals and the window's own WHERE fragment; every value is a bound '?' param
	rows, err := s.db.QueryContext(ctx, query, append(append([]any{}, args...), flowstore.TrafficPhysical)...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query ports: %w", err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.PortStat
	for rows.Next() {
		var p flowstore.PortStat
		if err := scanCounts(rows, &p.Counts, &p.Port, &p.Transport, &p.Service); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan ports: %w", err))
			return nil
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: ports rows: %w", err))
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Transport < out[j].Transport
	})
	return truncate(out, w.topN)
}

// queryLabel is the generic single-column breakdown: GROUP BY col, skipping
// the empty value (addLabel's own "not carried is not a label" rule), plus
// whatever extra WHERE predicate and args a caller needs (e.g. DERPRegions
// scoping to path = 'derp').
func (s *Store) queryLabel(ctx context.Context, w window, col, extraWhere string, extraArgs ...any) []flowstore.LabelStat {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT %s, %s FROM flows WHERE %s AND %s <> ''%s GROUP BY %s`, //nolint:gosec // G201/G202: SQL built from fixed internal column-name literals and query shapes; every value is a bound '?' param, never interpolated
		col, sumExpr, whereSQL, col, extraWhere, col)
	rows, err := s.db.QueryContext(ctx, query, append(args, extraArgs...)...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query %s: %w", col, err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.LabelStat
	for rows.Next() {
		var ls flowstore.LabelStat
		if err := scanCounts(rows, &ls.Counts, &ls.Label); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan %s: %w", col, err))
			return nil
		}
		out = append(out, ls)
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: %s rows: %w", col, err))
		return nil
	}
	sortLabels(out)
	return truncate(out, w.topN)
}

// queryDualEndpointLabels reproduces bucket.record's
// `addLabel(m, o.SrcUser, ...); if o.DstUser != o.SrcUser { addLabel(m,
// o.DstUser, ...) }` pattern (same shape for OS): a row's FULL counts land
// under its src value, and again under its dst value only when that
// differs — so a row talking to itself under one identity counts once, not
// twice.
func (s *Store) queryDualEndpointLabels(ctx context.Context, w window, srcCol, dstCol string) []flowstore.LabelStat {
	whereSQL, args := w.whereClause()
	totals := map[string]flowstore.Counts{}

	srcQuery := fmt.Sprintf(`SELECT %s, %s FROM flows WHERE %s AND %s <> '' GROUP BY %s`,
		srcCol, sumExpr, whereSQL, srcCol, srcCol)
	if !s.accumulateLabelSums(ctx, srcQuery, args, totals) {
		return nil
	}

	dstQuery := fmt.Sprintf(`SELECT %s, %s FROM flows WHERE %s AND %s <> '' AND %s <> %s GROUP BY %s`,
		dstCol, sumExpr, whereSQL, dstCol, dstCol, srcCol, dstCol)
	if !s.accumulateLabelSums(ctx, dstQuery, args, totals) {
		return nil
	}

	out := make([]flowstore.LabelStat, 0, len(totals))
	for k, c := range totals {
		out = append(out, flowstore.LabelStat{Label: k, Counts: c})
	}
	sortLabels(out)
	return truncate(out, w.topN)
}

// accumulateLabelSums runs a "label, sumExpr GROUP BY label" query and adds
// each row's counts into dst, returning false (after calling s.fail) on any
// error.
func (s *Store) accumulateLabelSums(ctx context.Context, query string, args []any, dst map[string]flowstore.Counts) bool {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query label sums: %w", err))
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &label); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan label sums: %w", err))
			return false
		}
		e := dst[label]
		addCounts(&e, c)
		dst[label] = e
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: label sums rows: %w", err))
		return false
	}
	return true
}

// splitTagsLocal is flowstore.splitTags, duplicated because that helper is
// unexported. Splits a comma-joined tag set, tolerating blank/whitespace
// entries a hand-edited ACL produces; an absent set yields nil.
func splitTagsLocal(joined string) []string {
	if joined == "" {
		return nil
	}
	parts := strings.Split(joined, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// containsStr reports whether s contains v, used in place of slices.Contains
// to keep this file's helper set self-contained.
func containsStr(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// queryTagLabels reproduces the tag breakdown: unlike the single-valued
// label dimensions, a device's tags are a SET, so each individual tag gets
// its own row, and a tag present on both endpoints of one flow counts once
// (mirrors the `!slices.Contains(srcTags, tag)` skip in bucket.record).
// Grouping by the raw (src_tags, dst_tags) STRING PAIR first, then applying
// the split once per distinct pair weighted by that group's summed counts,
// is equivalent to doing it per-row (the split is a pure function of the
// two strings, and Counts addition is commutative) but touches far fewer
// distinct combinations on a real tailnet.
func (s *Store) queryTagLabels(ctx context.Context, w window) []flowstore.LabelStat {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT src_tags, dst_tags, %s FROM flows WHERE %s AND (src_tags <> '' OR dst_tags <> '') GROUP BY src_tags, dst_tags`, //nolint:gosec // G201/G202: SQL built from fixed internal column-name literals and query shapes; every value is a bound '?' param, never interpolated
		sumExpr, whereSQL)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query tags: %w", err))
		return nil
	}
	defer rows.Close()

	totals := map[string]flowstore.Counts{}
	for rows.Next() {
		var srcTags, dstTags string
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &srcTags, &dstTags); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan tags: %w", err))
			return nil
		}
		src := splitTagsLocal(srcTags)
		dst := splitTagsLocal(dstTags)
		for _, tag := range src {
			e := totals[tag]
			addCounts(&e, c)
			totals[tag] = e
		}
		for _, tag := range dst {
			if containsStr(src, tag) {
				continue
			}
			e := totals[tag]
			addCounts(&e, c)
			totals[tag] = e
		}
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: tags rows: %w", err))
		return nil
	}

	out := make([]flowstore.LabelStat, 0, len(totals))
	for k, c := range totals {
		out = append(out, flowstore.LabelStat{Label: k, Counts: c})
	}
	sortLabels(out)
	return truncate(out, w.topN)
}

// queryTagMatrix is addMatrix's cross product for tags: every (src tag, dst
// tag) pair from the SAME flow gets the flow's full counts, same
// group-then-expand reasoning as queryTagLabels above. Filtering on "either
// side non-empty" rather than "both split non-empty" is deliberately loose
// — a group whose split is empty on one side simply contributes no cells
// when the nested loop below runs zero times for it, so it costs nothing to
// let it through the SQL filter.
func (s *Store) queryTagMatrix(ctx context.Context, w window) []flowstore.MatrixCell {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT src_tags, dst_tags, %s FROM flows WHERE %s AND (src_tags <> '' OR dst_tags <> '') GROUP BY src_tags, dst_tags`, //nolint:gosec // G201/G202: SQL built from fixed internal column-name literals and query shapes; every value is a bound '?' param, never interpolated
		sumExpr, whereSQL)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query tag matrix: %w", err))
		return nil
	}
	defer rows.Close()

	cells := map[flowstore.MatrixKey]flowstore.Counts{}
	for rows.Next() {
		var srcTags, dstTags string
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &srcTags, &dstTags); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan tag matrix: %w", err))
			return nil
		}
		for _, sv := range splitTagsLocal(srcTags) {
			for _, dv := range splitTagsLocal(dstTags) {
				k := flowstore.MatrixKey{Src: sv, Dst: dv}
				e := cells[k]
				addCounts(&e, c)
				cells[k] = e
			}
		}
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: tag matrix rows: %w", err))
		return nil
	}
	return rankMatrix(cells)
}

// querySimpleMatrix handles the single-valued matrices (user, OS): unlike
// tags, addMatrix's oneOrNone cross product of one src value against one dst
// value is exactly one cell, so a plain GROUP BY on both columns (each
// required non-empty, matching oneOrNone's "absent means no cells") already
// IS the matrix — no Go-side expansion needed.
func (s *Store) querySimpleMatrix(ctx context.Context, w window, srcCol, dstCol string) []flowstore.MatrixCell {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT %s, %s, %s FROM flows WHERE %s AND %s <> '' AND %s <> '' GROUP BY %s, %s`, //nolint:gosec // G201/G202: SQL built from fixed internal column-name literals and query shapes; every value is a bound '?' param, never interpolated
		srcCol, dstCol, sumExpr, whereSQL, srcCol, dstCol, srcCol, dstCol)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query %s/%s matrix: %w", srcCol, dstCol, err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.MatrixCell
	for rows.Next() {
		var cell flowstore.MatrixCell
		if err := scanCounts(rows, &cell.Counts, &cell.Src, &cell.Dst); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan %s/%s matrix: %w", srcCol, dstCol, err))
			return nil
		}
		out = append(out, cell)
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: %s/%s matrix rows: %w", srcCol, dstCol, err))
		return nil
	}
	sortMatrixCells(out)
	return truncate(out, flowstore.MaxMatrixCellsReturned)
}

// rankMatrix sorts a matrix cell map and caps it at MaxMatrixCellsReturned,
// shared by queryTagMatrix (the only matrix built from a Go-side map rather
// than scanned straight into a slice).
func rankMatrix(cells map[flowstore.MatrixKey]flowstore.Counts) []flowstore.MatrixCell {
	out := make([]flowstore.MatrixCell, 0, len(cells))
	for k, c := range cells {
		out = append(out, flowstore.MatrixCell{MatrixKey: k, Counts: c})
	}
	sortMatrixCells(out)
	return truncate(out, flowstore.MaxMatrixCellsReturned)
}

func sortMatrixCells(out []flowstore.MatrixCell) {
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		return out[i].Dst < out[j].Dst
	})
}

func sortLabels(out []flowstore.LabelStat) {
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		return out[i].Label < out[j].Label
	})
}

// queryVerdicts reproduces recordPolicy's presentation-only split: a
// permitted verdict matched in the REVERSE direction is reported as
// VerdictPermittedReverse instead of VerdictPermitted. The CASE expression
// performs that split in SQL so the GROUP BY folds the two families
// correctly without a Go-side merge step.
func (s *Store) queryVerdicts(ctx context.Context, w window) []flowstore.LabelStat {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT CASE WHEN verdict = ? AND reversed = 1 THEN ? ELSE verdict END AS v, %s FROM flows WHERE %s AND verdict <> '' GROUP BY v`, sumExpr, whereSQL) //nolint:gosec // G201: built from fixed internal literals and the window's own WHERE fragment; every value is a bound '?' param
	fullArgs := append([]any{flowstore.VerdictPermitted, flowstore.VerdictPermittedReverse}, args...)
	rows, err := s.db.QueryContext(ctx, query, fullArgs...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query verdicts: %w", err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.LabelStat
	for rows.Next() {
		var ls flowstore.LabelStat
		if err := scanCounts(rows, &ls.Counts, &ls.Label); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan verdicts: %w", err))
			return nil
		}
		out = append(out, ls)
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: verdicts rows: %w", err))
		return nil
	}
	sortLabels(out)
	return truncate(out, w.topN)
}

// addrHostLocal is flowstore.addrHost, duplicated because it is unexported:
// strips the port from an "addr:port" endpoint, "" when there is nothing to
// strip it from.
func addrHostLocal(addrPort string) string {
	if addrPort == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addrPort); err == nil {
		return host
	}
	return addrPort
}

// endpointIdentityLocal is flowstore.endpointIdentity, duplicated because it
// is unexported: names one end of an unexplained relationship by what it IS
// (tags, then owner, then device name) before falling back to WHERE it is
// (bare address), then Unidentified.
func endpointIdentityLocal(tags, user, node, addrPort string) string {
	switch {
	case tags != "":
		return tags
	case user != "":
		return user
	case node != "" && node != externalSentinel && node != unknownSentinel:
		return node
	}
	if host := addrHostLocal(addrPort); host != "" {
		return host
	}
	if node != "" {
		return node
	}
	return flowstore.Unidentified
}

// queryUnexplained groups by every column endpointIdentityLocal reads, then
// computes the UnexplainedKey per distinct combination in Go — the identity
// function has conditional precedence (and a net.SplitHostPort call)
// SQL cannot express cleanly, but grouping first keeps the number of rows
// touched by that Go-side step down to the distinct identity tuples rather
// than every raw connection.
func (s *Store) queryUnexplained(ctx context.Context, w window) []flowstore.UnexplainedStat {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT src_tags, src_user, src_node, src_addr, dst_tags, dst_user, dst_node, dst_addr, transport, dst_port, %s FROM flows WHERE %s AND verdict = ? GROUP BY src_tags, src_user, src_node, src_addr, dst_tags, dst_user, dst_node, dst_addr, transport, dst_port`, //nolint:gosec // G201: built from fixed internal literals and the window's own WHERE fragment; every value is a bound '?' param
		sumExpr, whereSQL)
	fullArgs := append(append([]any{}, args...), flowstore.VerdictNoRule)
	rows, err := s.db.QueryContext(ctx, query, fullArgs...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query unexplained: %w", err))
		return nil
	}
	defer rows.Close()

	cells := map[flowstore.UnexplainedKey]flowstore.Counts{}
	for rows.Next() {
		var srcTags, srcUser, srcNode, srcAddr string
		var dstTags, dstUser, dstNode, dstAddr string
		var transport, dstPort string
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &srcTags, &srcUser, &srcNode, &srcAddr, &dstTags, &dstUser, &dstNode, &dstAddr, &transport, &dstPort); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan unexplained: %w", err))
			return nil
		}
		k := flowstore.UnexplainedKey{
			Src:       endpointIdentityLocal(srcTags, srcUser, srcNode, srcAddr),
			Dst:       endpointIdentityLocal(dstTags, dstUser, dstNode, dstAddr),
			Transport: transport,
			Port:      dstPort,
		}
		e := cells[k]
		addCounts(&e, c)
		cells[k] = e
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: unexplained rows: %w", err))
		return nil
	}

	out := make([]flowstore.UnexplainedStat, 0, len(cells))
	for k, c := range cells {
		out = append(out, flowstore.UnexplainedStat{UnexplainedKey: k, Counts: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		if out[i].Transport != out[j].Transport {
			return out[i].Transport < out[j].Transport
		}
		return out[i].Port < out[j].Port
	})
	return truncate(out, w.topN)
}

// queryRules groups exercised rules by (policy_version, rule). It is
// deliberately NOT truncated by TopN — allRules in flowstore.go never is
// either, because a caller subtracts this list from the tailnet's compiled
// policy to find rules that permitted nothing, and a truncated list would
// misreport a live rule as dead.
func (s *Store) queryRules(ctx context.Context, w window) []flowstore.RuleStat {
	whereSQL, args := w.whereClause()
	query := fmt.Sprintf(`SELECT policy_version, rule, %s FROM flows WHERE %s AND verdict = ? AND rule >= 0 GROUP BY policy_version, rule`, sumExpr, whereSQL) //nolint:gosec // G201: built from fixed internal literals and the window's own WHERE fragment; every value is a bound '?' param
	fullArgs := append(append([]any{}, args...), flowstore.VerdictPermitted)
	rows, err := s.db.QueryContext(ctx, query, fullArgs...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query rules: %w", err))
		return nil
	}
	defer rows.Close()

	var out []flowstore.RuleStat
	for rows.Next() {
		var rs flowstore.RuleStat
		if err := scanCounts(rows, &rs.Counts, &rs.PolicyVersion, &rs.Rule); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan rules: %w", err))
			return nil
		}
		out = append(out, rs)
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: rules rows: %w", err))
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PolicyVersion != out[j].PolicyVersion {
			return out[i].PolicyVersion < out[j].PolicyVersion
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// peerIdentityLocal is flowstore.peerIdentity, duplicated because it is
// unexported: names the far end of an underlay connection, preferring the
// device name over a tag (unlike endpointIdentityLocal) since this table
// exists to name the ONE device to go look at.
func peerIdentityLocal(node, addrPort string) string {
	if node != "" && node != externalSentinel && node != unknownSentinel {
		return node
	}
	if host := addrHostLocal(addrPort); host != "" {
		return host
	}
	if node != "" {
		return node
	}
	return flowstore.Unidentified
}

// queryPeerPaths groups physical (path <> ”) traffic by (src_node,
// src_addr, path), computes each row's peer identity in Go, then folds the
// per-path split into {Direct, Relayed} per peer — same shape as
// topPeerPaths, ranked relayed-first so a peer being relayed surfaces over
// one merely moving a lot of traffic directly.
func (s *Store) queryPeerPaths(ctx context.Context, w window) []flowstore.PeerPathStat {
	whereSQL, args := w.whereClause()
	query := `SELECT src_node, src_addr, path, ` + sumExpr + ` FROM flows WHERE ` + whereSQL + ` AND path <> '' GROUP BY src_node, src_addr, path` //nolint:gosec // G202: built from fixed internal literals and the window's own WHERE fragment; every value is a bound '?' param
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.fail(fmt.Errorf("sqlitestore: query peer paths: %w", err))
		return nil
	}
	defer rows.Close()

	byPeer := map[string]*flowstore.PeerPathStat{}
	for rows.Next() {
		var srcNode, srcAddr, path string
		var c flowstore.Counts
		if err := scanCounts(rows, &c, &srcNode, &srcAddr, &path); err != nil {
			s.fail(fmt.Errorf("sqlitestore: scan peer paths: %w", err))
			return nil
		}
		peer := peerIdentityLocal(srcNode, srcAddr)
		e, ok := byPeer[peer]
		if !ok {
			e = &flowstore.PeerPathStat{Peer: peer}
			byPeer[peer] = e
		}
		if path == flowstore.PathDERP {
			addCounts(&e.Relayed, c)
		} else {
			addCounts(&e.Direct, c)
		}
	}
	if err := rows.Err(); err != nil {
		s.fail(fmt.Errorf("sqlitestore: peer paths rows: %w", err))
		return nil
	}

	out := make([]flowstore.PeerPathStat, 0, len(byPeer))
	for _, e := range byPeer {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Relayed.Bytes(), out[j].Relayed.Bytes(); a != b {
			return a > b
		}
		if a, b := out[i].Direct.Bytes(), out[j].Direct.Bytes(); a != b {
			return a > b
		}
		return out[i].Peer < out[j].Peer
	})
	return truncate(out, w.topN)
}

// truncate is flowstore.truncate, duplicated because it is unexported (and
// generic, so this package cannot just call it via a value receiver trick).
func truncate[T any](s []T, n int) []T {
	if n > 0 && len(s) > n {
		return s[:n]
	}
	return s
}
