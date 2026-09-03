package sqlitestore

import (
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// likeEscaper escapes SQLite's two LIKE wildcard characters, and the escape
// character itself, in a user-supplied filter value BEFORE it is wrapped in
// %...% and matched with `LIKE ? ESCAPE '\'` below. Without this a filter
// value that happens to contain a literal '%' or '_' (a device name, an
// address) would be read as a wildcard instead of matched literally, and
// silently match far more rows than the operator typed.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern turns a raw filter value into the escaped %...% pattern used by
// every case-insensitive substring predicate below. Every caller pairs it
// with `LIKE ? ESCAPE '\'`.
func likePattern(v string) string {
	return "%" + likeEscaper.Replace(v) + "%"
}

// recentPredicate is the ONE place RecentQuery becomes a SQL WHERE clause. It
// covers the window (mirroring RecentPage's [Start, End] inclusive convention)
// and every RecentFilter field, with the exact column sets
// flowstore.RecentFilter.matches tests:
//
//   - Device:   src_node OR dst_node       (substring)
//   - Addr:     src_addr OR dst_addr       (substring)
//   - Service:  dst_service                (substring)
//   - Identity: src_user, dst_user, src_tags, dst_tags (substring)
//   - Type:     traffic_type               (exact)
//   - Verdict:  verdict                    (exact)
//   - Path:     path                       (exact)
//
// The page query and the Matched COUNT(*) both call this and get the SAME
// clause and args, so they cannot drift apart the way two hand-written WHEREs
// could.
func recentPredicate(q flowstore.RecentQuery, now time.Time) (string, []any) {
	cond := make([]string, 0, 9)
	args := make([]any, 0, 16)

	if !q.Start.IsZero() {
		cond = append(cond, "time >= ?")
		args = append(args, timeToDB(q.Start))
	}
	end := q.End
	if end.IsZero() {
		end = now
	}
	cond = append(cond, "time <= ?")
	args = append(args, timeToDB(end))

	f := q.Filter
	if f.Device != "" {
		cond = append(cond, "(src_node LIKE ? ESCAPE '\\' COLLATE NOCASE OR dst_node LIKE ? ESCAPE '\\' COLLATE NOCASE)")
		p := likePattern(f.Device)
		args = append(args, p, p)
	}
	if f.Addr != "" {
		cond = append(cond, "(src_addr LIKE ? ESCAPE '\\' COLLATE NOCASE OR dst_addr LIKE ? ESCAPE '\\' COLLATE NOCASE)")
		p := likePattern(f.Addr)
		args = append(args, p, p)
	}
	if f.Service != "" {
		cond = append(cond, "dst_service LIKE ? ESCAPE '\\' COLLATE NOCASE")
		args = append(args, likePattern(f.Service))
	}
	if f.Identity != "" {
		cond = append(cond, "(src_user LIKE ? ESCAPE '\\' COLLATE NOCASE OR dst_user LIKE ? ESCAPE '\\' COLLATE NOCASE"+
			" OR src_tags LIKE ? ESCAPE '\\' COLLATE NOCASE OR dst_tags LIKE ? ESCAPE '\\' COLLATE NOCASE)")
		p := likePattern(f.Identity)
		args = append(args, p, p, p, p)
	}
	if f.Type != "" {
		cond = append(cond, "traffic_type = ? COLLATE NOCASE")
		args = append(args, f.Type)
	}
	if f.Verdict != "" {
		cond = append(cond, "verdict = ? COLLATE NOCASE")
		args = append(args, f.Verdict)
	}
	if f.Path != "" {
		cond = append(cond, "path = ? COLLATE NOCASE")
		args = append(args, f.Path)
	}

	// cond always has at least the End clause, so this is never empty.
	return strings.Join(cond, " AND "), args
}

// scannedRecent pairs a decoded row with its seq. seq drives NextCursor;
// flowstore.Recent's own seq field is unexported (ordering metadata private
// to the memory store's ring), so it cannot be set from this package and is
// carried alongside the struct instead.
type scannedRecent struct {
	rec flowstore.Recent
	seq uint64
}

// RecentPage implements flowstore.Store over the persisted raw rows. It
// reproduces flowstore.Memory.RecentPage's semantics exactly (newest-first,
// [Start, End] inclusive window, strict Cursor resume, Matched/Retained
// independent of Limit/Cursor) but against disk instead of the in-memory
// ring: see recentPredicate for the shared WHERE clause and columns.go /
// recentColumns for the projection.
//
// Errors never propagate to the caller (the flowstore.Store contract this
// package satisfies): a failure is recorded via s.fail and a
// zero/partial page is returned so the admin page keeps rendering whatever
// it already has.
func (s *Store) RecentPage(q flowstore.RecentQuery) flowstore.RecentPage {
	ctx, cancel := s.queryCtx()
	defer cancel()

	now := s.opts.Now()
	where, args := recentPredicate(q, now)

	var page flowstore.RecentPage

	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM flows").Scan(&page.Retained); err != nil {
		s.fail(err)
		return flowstore.RecentPage{}
	}
	page.Truncated = int64(page.Retained) >= s.opts.MaxRows

	matchedSQL := "SELECT COUNT(*) FROM flows WHERE " + where
	if err := s.db.QueryRowContext(ctx, matchedSQL, args...).Scan(&page.Matched); err != nil {
		s.fail(err)
		return flowstore.RecentPage{}
	}

	limit := q.Limit
	if limit <= 0 {
		// Mirrors Memory.RecentPage: a non-positive Limit returns no rows (the
		// caller is always a UI page size, and defaulting an unset one to "all"
		// is the wrong direction to fail in), but Matched/Retained are still
		// meaningful and already computed above.
		return page
	}
	if limit > s.opts.MaxExportRows {
		limit = s.opts.MaxExportRows
	}

	pageWhere := where
	pageArgs := append([]any{}, args...)
	if q.Cursor != 0 {
		pageWhere += " AND seq < ?"
		pageArgs = append(pageArgs, int64(q.Cursor)) //nolint:gosec // seq is a positive AUTOINCREMENT rowid
	}
	// Fetch one extra row so "more remain beyond this page" is a length check
	// against the SAME result set, rather than a second query that could
	// disagree with the first about what matched (e.g. a concurrent write
	// landing between the two).
	pageArgs = append(pageArgs, limit+1)

	// Concatenation here joins this package's own `recentColumns` constant with a
	// predicate built solely from "?" placeholders by recentPredicate — every
	// operator-supplied filter value travels as a bound argument, so no request
	// input reaches the statement text.
	pageSQL := "SELECT " + strings.Join(recentColumns, ", ") + //nolint:gosec // G202: identifiers are package constants; filter values are bound parameters
		" FROM flows WHERE " + pageWhere +
		" ORDER BY time DESC, seq DESC LIMIT ?"

	rows, err := s.db.QueryContext(ctx, pageSQL, pageArgs...)
	if err != nil {
		s.fail(err)
		return page
	}
	defer func() { _ = rows.Close() }()

	scanned := make([]scannedRecent, 0, limit+1)
	for rows.Next() {
		r, seq, err := scanRecent(rows)
		if err != nil {
			s.fail(err)
			return page
		}
		scanned = append(scanned, scannedRecent{rec: r, seq: seq})
	}
	if err := rows.Err(); err != nil {
		s.fail(err)
		return page
	}

	haveMore := len(scanned) > limit
	if haveMore {
		scanned = scanned[:limit]
	}
	out := make([]flowstore.Recent, len(scanned))
	for i, sc := range scanned {
		out[i] = sc.rec
	}
	page.Rows = out
	if haveMore && len(scanned) > 0 {
		page.NextCursor = scanned[len(scanned)-1].seq
	}
	return page
}

// rowScanner is the subset of *sql.Rows scanRecent needs, so it stays
// testable against anything that scans one recentColumns row (currently only
// *sql.Rows implements it).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanRecent decodes one recentColumns-projected row. dst_port is scanned and
// discarded: it is stored (Query groups ports by PortKey{Port, Transport,
// Service}) but flowstore.Recent carries no such field — the raw connection
// list never shows a bare port, per columns.go's doc comment.
func scanRecent(row rowScanner) (flowstore.Recent, uint64, error) {
	var (
		seq                                                         int64
		timeNS                                                      int64
		trafficType, transport, srcAddr, dstAddr, srcNode, dstNode  string
		dstPort, dstService, srcUser, dstUser, srcTags, dstTags     string
		srcOS, dstOS                                                string
		reporterNodeID, reporterTrust, reporterConsistency, verdict string
		reversed, rule                                              int64
		policyVersion, path, derpRegion                             string
		txBytes, rxBytes, txPackets, rxPackets, flows               int64
	)

	err := row.Scan(
		&seq, &timeNS, &trafficType, &transport, &srcAddr, &dstAddr, &srcNode, &dstNode,
		&dstPort, &dstService, &srcUser, &dstUser, &srcTags, &dstTags, &srcOS, &dstOS,
		&reporterNodeID, &reporterTrust, &reporterConsistency, &verdict,
		&reversed, &rule, &policyVersion, &path, &derpRegion,
		&txBytes, &rxBytes, &txPackets, &rxPackets, &flows,
	)
	if err != nil {
		return flowstore.Recent{}, 0, err
	}

	rec := flowstore.Recent{
		Time:                dbToTime(timeNS),
		TrafficType:         trafficType,
		Transport:           transport,
		SrcAddr:             srcAddr,
		DstAddr:             dstAddr,
		SrcNode:             srcNode,
		DstNode:             dstNode,
		DstService:          dstService,
		SrcUser:             srcUser,
		DstUser:             dstUser,
		SrcTags:             srcTags,
		DstTags:             dstTags,
		SrcOS:               srcOS,
		DstOS:               dstOS,
		ReporterNodeID:      reporterNodeID,
		ReporterTrust:       reporterTrust,
		ReporterConsistency: reporterConsistency,
		Verdict:             verdict,
		Reversed:            reversed != 0,
		Rule:                int(rule),
		PolicyVersion:       policyVersion,
		Path:                path,
		DERPRegion:          derpRegion,
		Counts: flowstore.Counts{
			TxBytes: txBytes,
			RxBytes: rxBytes,
			TxPkts:  txPackets,
			RxPkts:  rxPackets,
			Flows:   flows,
		},
	}
	// seq is an AUTOINCREMENT rowid: SQLite only ever assigns it positive values,
	// and it is the same column this package wrote, so the conversion cannot wrap.
	return rec, uint64(seq), nil //nolint:gosec // G115: AUTOINCREMENT rowid is always positive
}
