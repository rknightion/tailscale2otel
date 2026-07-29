package sqlitestore

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// fixtureRow is a raw `flows` row, named field-for-field with the schema
// rather than with flowstore.Observation: query_test.go inserts directly via
// SQL (writer.go/recent.go are owned by other lanes and are not a dependency
// this test needs), so the fixture must speak the table's own shape,
// including columns Observation does not have (dst_port survives even though
// Recent has no such field — see columns' doc comment in schema.go).
type fixtureRow struct {
	Time                 time.Time
	TrafficType          string
	Transport            string
	SrcAddr, DstAddr     string
	SrcNode, DstNode     string
	DstPort              string
	DstService           string
	SrcUser, DstUser     string
	SrcTags, DstTags     string
	SrcOS, DstOS         string
	ReporterNodeID       string
	ReporterTrust        string
	ReporterConsistency  string
	Verdict              string
	Reversed             bool
	Rule                 int
	PolicyVersion        string
	Path                 string
	DERPRegion           string
	TxBytes, RxBytes     int64
	TxPackets, RxPackets int64
	Flows                int64
}

// newRow returns a fixture with the same "not applicable" defaults the real
// writer would use: Rule -1 (schema.go's own column default), everything
// else the empty string/zero. Individual tests override only what they need,
// so a forgotten field can never silently mean something else (e.g. Rule's
// Go zero value, 0, is a REAL rule index and must never leak in as a default).
func newRow(at time.Time) fixtureRow {
	return fixtureRow{Time: at, Rule: -1}
}

// insertRow inserts one fixture row by walking the package's own `columns`
// slice (schema.go) rather than hand-writing an INSERT statement, so the
// fixture cannot silently drift from the real insert column list the writer
// lane binds against.
func insertRow(t *testing.T, s *Store, r fixtureRow) {
	t.Helper()
	placeholders := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, col := range columns {
		placeholders[i] = "?"
		args[i] = fixtureValue(t, r, col)
	}
	stmt := "INSERT INTO flows (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if _, err := s.db.Exec(stmt, args...); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}
}

func fixtureValue(t *testing.T, r fixtureRow, col string) any {
	t.Helper()
	switch col {
	case "time":
		return timeToDB(r.Time)
	case "traffic_type":
		return r.TrafficType
	case "transport":
		return r.Transport
	case "src_addr":
		return r.SrcAddr
	case "dst_addr":
		return r.DstAddr
	case "src_node":
		return r.SrcNode
	case "dst_node":
		return r.DstNode
	case "dst_port":
		return r.DstPort
	case "dst_service":
		return r.DstService
	case "src_user":
		return r.SrcUser
	case "dst_user":
		return r.DstUser
	case "src_tags":
		return r.SrcTags
	case "dst_tags":
		return r.DstTags
	case "src_os":
		return r.SrcOS
	case "dst_os":
		return r.DstOS
	case "reporter_node_id":
		return r.ReporterNodeID
	case "reporter_trust":
		return r.ReporterTrust
	case "reporter_consistency":
		return r.ReporterConsistency
	case "verdict":
		return r.Verdict
	case "reversed":
		return boolToDB(r.Reversed)
	case "rule":
		return r.Rule
	case "policy_version":
		return r.PolicyVersion
	case "path":
		return r.Path
	case "derp_region":
		return r.DERPRegion
	case "tx_bytes":
		return r.TxBytes
	case "rx_bytes":
		return r.RxBytes
	case "tx_packets":
		return r.TxPackets
	case "rx_packets":
		return r.RxPackets
	case "flows":
		return r.Flows
	default:
		t.Fatalf("fixtureValue: column %q has no fixture mapping (columns drifted?)", col)
		return nil
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{Dir: t.TempDir(), Tailnet: "test"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var base = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

func wideWindow() flowstore.Query {
	return flowstore.Query{Start: base.Add(-time.Hour), End: base.Add(time.Hour)}
}

func TestQuery_Totals(t *testing.T) {
	s := newTestStore(t)

	r1 := newRow(base)
	r1.TxBytes, r1.RxBytes, r1.TxPackets, r1.RxPackets, r1.Flows = 100, 50, 10, 5, 1
	insertRow(t, s, r1)

	r2 := newRow(base.Add(time.Minute))
	r2.TxBytes, r2.RxBytes, r2.TxPackets, r2.RxPackets, r2.Flows = 200, 75, 20, 8, 2
	insertRow(t, s, r2)

	res := s.Query(wideWindow())

	want := flowstore.Counts{TxBytes: 300, RxBytes: 125, TxPkts: 30, RxPkts: 13, Flows: 3}
	if res.Totals != want {
		t.Fatalf("Totals = %+v, want %+v", res.Totals, want)
	}
}

func TestQuery_PairsRankingAndTopN(t *testing.T) {
	s := newTestStore(t)

	// Five distinct pairs with strictly decreasing byte totals, so ranking
	// order is unambiguous.
	pairs := []struct {
		src, dst string
		bytes    int64
	}{
		{"a", "b", 500},
		{"c", "d", 400},
		{"e", "f", 300},
		{"g", "h", 200},
		{"i", "j", 100},
	}
	for idx, p := range pairs {
		row := newRow(base.Add(time.Duration(idx) * time.Second))
		row.SrcNode, row.DstNode = p.src, p.dst
		row.TrafficType = "overlay"
		row.TxBytes = p.bytes
		insertRow(t, s, row)
	}

	q := wideWindow()
	q.TopN = 3
	res := s.Query(q)

	if len(res.Pairs) != 3 {
		t.Fatalf("len(Pairs) = %d, want 3", len(res.Pairs))
	}
	wantOrder := []string{"a", "c", "e"}
	for i, want := range wantOrder {
		if got := res.Pairs[i].Src; got != want {
			t.Errorf("Pairs[%d].Src = %q, want %q", i, got, want)
		}
	}
	if res.Pairs[0].Counts.Bytes() != 500 {
		t.Errorf("Pairs[0].Counts.Bytes() = %d, want 500", res.Pairs[0].Counts.Bytes())
	}
}

func TestQuery_PairsRequireBothEndpoints(t *testing.T) {
	s := newTestStore(t)

	row := newRow(base)
	row.SrcNode = "solo" // DstNode left empty
	row.TrafficType = "overlay"
	row.TxBytes = 999
	insertRow(t, s, row)

	res := s.Query(wideWindow())
	if len(res.Pairs) != 0 {
		t.Fatalf("Pairs = %+v, want empty (one endpoint missing)", res.Pairs)
	}
	// Totals must still see it.
	if res.Totals.TxBytes != 999 {
		t.Fatalf("Totals.TxBytes = %d, want 999", res.Totals.TxBytes)
	}
}

func TestQuery_NodesSentReceivedSplit(t *testing.T) {
	s := newTestStore(t)

	row := newRow(base)
	row.SrcNode, row.DstNode = "alpha", "beta"
	row.TxBytes, row.RxBytes, row.TxPackets, row.RxPackets, row.Flows = 1000, 200, 100, 20, 1
	insertRow(t, s, row)

	res := s.Query(wideWindow())

	var alpha, beta *flowstore.NodeStat
	for i := range res.Nodes {
		switch res.Nodes[i].Node {
		case "alpha":
			alpha = &res.Nodes[i]
		case "beta":
			beta = &res.Nodes[i]
		}
	}
	if alpha == nil || beta == nil {
		t.Fatalf("Nodes = %+v, missing alpha/beta", res.Nodes)
	}

	wantAlphaSent := flowstore.Counts{TxBytes: 1000, RxBytes: 200, TxPkts: 100, RxPkts: 20, Flows: 1}
	if alpha.Sent != wantAlphaSent {
		t.Errorf("alpha.Sent = %+v, want %+v", alpha.Sent, wantAlphaSent)
	}
	if alpha.Received != (flowstore.Counts{}) {
		t.Errorf("alpha.Received = %+v, want zero", alpha.Received)
	}

	// Received mirrors the row's counters: what beta received is what alpha
	// transmitted (row.TxBytes), and what beta "sent back" is row.RxBytes.
	wantBetaReceived := flowstore.Counts{TxBytes: 200, RxBytes: 1000, TxPkts: 20, RxPkts: 100, Flows: 1}
	if beta.Received != wantBetaReceived {
		t.Errorf("beta.Received = %+v, want %+v", beta.Received, wantBetaReceived)
	}
	if beta.Sent != (flowstore.Counts{}) {
		t.Errorf("beta.Sent = %+v, want zero", beta.Sent)
	}
}

func TestQuery_PortsExcludePhysical(t *testing.T) {
	s := newTestStore(t)

	overlay := newRow(base)
	overlay.TrafficType = "overlay"
	overlay.DstPort = "443"
	overlay.Transport = "tcp"
	overlay.TxBytes = 111
	insertRow(t, s, overlay)

	physical := newRow(base)
	physical.TrafficType = flowstore.TrafficPhysical
	physical.DstPort = "41641"
	physical.Transport = "udp"
	physical.TxBytes = 222
	insertRow(t, s, physical)

	res := s.Query(wideWindow())

	if len(res.Ports) != 1 {
		t.Fatalf("Ports = %+v, want exactly the overlay entry", res.Ports)
	}
	if res.Ports[0].Port != "443" {
		t.Errorf("Ports[0].Port = %q, want 443", res.Ports[0].Port)
	}
	// Physical bytes still land in Totals.
	if res.Totals.TxBytes != 333 {
		t.Errorf("Totals.TxBytes = %d, want 333", res.Totals.TxBytes)
	}
}

func TestQuery_LabelBreakdown(t *testing.T) {
	s := newTestStore(t)

	r1 := newRow(base)
	r1.Transport = "tcp"
	r1.TxBytes = 10
	insertRow(t, s, r1)

	r2 := newRow(base.Add(time.Second))
	r2.Transport = "tcp"
	r2.TxBytes = 20
	insertRow(t, s, r2)

	r3 := newRow(base.Add(2 * time.Second))
	r3.Transport = "udp"
	r3.TxBytes = 5
	insertRow(t, s, r3)

	res := s.Query(wideWindow())

	got := map[string]int64{}
	for _, l := range res.Transports {
		got[l.Label] = l.Counts.TxBytes
	}
	if got["tcp"] != 30 {
		t.Errorf("tcp = %d, want 30", got["tcp"])
	}
	if got["udp"] != 5 {
		t.Errorf("udp = %d, want 5", got["udp"])
	}
	// Ranked by bytes descending.
	if len(res.Transports) == 0 || res.Transports[0].Label != "tcp" {
		t.Errorf("Transports[0] missing/wrong, got %+v, want first label tcp", res.Transports)
	}
}

func TestQuery_TagSplit(t *testing.T) {
	s := newTestStore(t)

	row := newRow(base)
	row.SrcTags = "tag:a,tag:b"
	row.DstTags = "tag:b,tag:c"
	row.TxBytes = 100
	insertRow(t, s, row)

	res := s.Query(wideWindow())

	got := map[string]int64{}
	for _, l := range res.Tags {
		got[l.Label] = l.Counts.TxBytes
	}
	// tag:b is on BOTH endpoints of the same flow, so it counts once, not
	// twice, exactly like Memory's addLabel(dst) skip-if-already-in-src.
	for _, tag := range []string{"tag:a", "tag:b", "tag:c"} {
		if got[tag] != 100 {
			t.Errorf("Tags[%s] = %d, want 100", tag, got[tag])
		}
	}
	if len(res.Tags) != 3 {
		t.Fatalf("len(Tags) = %d, want 3 (a, b, c) got %+v", len(res.Tags), res.Tags)
	}
}

func TestQuery_VerdictGrouping(t *testing.T) {
	s := newTestStore(t)

	forward := newRow(base)
	forward.Verdict = flowstore.VerdictPermitted
	forward.Reversed = false
	forward.Rule = -1
	forward.TxBytes = 10
	insertRow(t, s, forward)

	reverse := newRow(base.Add(time.Second))
	reverse.Verdict = flowstore.VerdictPermitted
	reverse.Reversed = true
	reverse.Rule = -1
	reverse.TxBytes = 20
	insertRow(t, s, reverse)

	noRule := newRow(base.Add(2 * time.Second))
	noRule.Verdict = flowstore.VerdictNoRule
	noRule.Rule = -1
	noRule.TxBytes = 30
	insertRow(t, s, noRule)

	res := s.Query(wideWindow())

	got := map[string]int64{}
	for _, l := range res.Verdicts {
		got[l.Label] = l.Counts.TxBytes
	}
	if got[flowstore.VerdictPermitted] != 10 {
		t.Errorf("permitted = %d, want 10", got[flowstore.VerdictPermitted])
	}
	if got[flowstore.VerdictPermittedReverse] != 20 {
		t.Errorf("permitted_reverse = %d, want 20", got[flowstore.VerdictPermittedReverse])
	}
	if got[flowstore.VerdictNoRule] != 30 {
		t.Errorf("no_rule = %d, want 30", got[flowstore.VerdictNoRule])
	}
}

func TestQuery_RulesNotTruncated(t *testing.T) {
	s := newTestStore(t)

	const n = 25
	for i := 0; i < n; i++ {
		row := newRow(base.Add(time.Duration(i) * time.Second))
		row.Verdict = flowstore.VerdictPermitted
		row.Rule = i
		row.PolicyVersion = "v1"
		row.TxBytes = int64(i + 1)
		insertRow(t, s, row)
	}

	q := wideWindow()
	q.TopN = 5 // Rules must ignore this.
	res := s.Query(q)

	if len(res.Rules) != n {
		t.Fatalf("len(Rules) = %d, want %d (Rules is never truncated by TopN)", len(res.Rules), n)
	}
}

func TestQuery_RulesRequirePermittedAndNonNegative(t *testing.T) {
	s := newTestStore(t)

	noRule := newRow(base)
	noRule.Verdict = flowstore.VerdictNoRule
	noRule.Rule = -1
	insertRow(t, s, noRule)

	undetermined := newRow(base.Add(time.Second))
	undetermined.Verdict = flowstore.VerdictUndetermined
	undetermined.Rule = -1
	insertRow(t, s, undetermined)

	res := s.Query(wideWindow())
	if len(res.Rules) != 0 {
		t.Fatalf("Rules = %+v, want empty", res.Rules)
	}
}

func TestQuery_SeriesBucketingRaisesStepForMaxPoints(t *testing.T) {
	s := newTestStore(t)

	start := base
	// A window wide enough that a 1-minute step would exceed MaxPoints
	// (720), forcing Memory.Query's doubling loop to raise it. Reproduce the
	// exact same loop here so the test asserts the real invariant rather than
	// a hardcoded number.
	end := start.Add(2000 * time.Minute)
	insertRow(t, s, newRow(start))
	insertRow(t, s, newRow(end.Add(-time.Minute)))

	wantStep := flowstore.Resolution
	for span := end.Sub(start); span/wantStep > flowstore.MaxPoints; span = end.Sub(start) {
		wantStep *= 2
	}

	res := s.Query(flowstore.Query{Start: start, End: end})

	if res.Step != wantStep.String() {
		t.Fatalf("Step = %q, want %q", res.Step, wantStep.String())
	}
	if len(res.Series) > flowstore.MaxPoints {
		t.Fatalf("len(Series) = %d, exceeds MaxPoints %d", len(res.Series), flowstore.MaxPoints)
	}
}

func TestQuery_EmptyWindow(t *testing.T) {
	s := newTestStore(t)

	res := s.Query(wideWindow())

	if res.Totals != (flowstore.Counts{}) {
		t.Errorf("Totals = %+v, want zero", res.Totals)
	}
	if len(res.Series) != 0 || len(res.Pairs) != 0 || len(res.Nodes) != 0 || len(res.Ports) != 0 {
		t.Errorf("expected every list empty on an empty store, got %+v", res)
	}
	if res.Truncated != 0 {
		t.Errorf("Truncated = %d, want 0 (this backend never folds into Other)", res.Truncated)
	}
}

func TestQuery_UserMatrixAndOSMatrix(t *testing.T) {
	s := newTestStore(t)

	row := newRow(base)
	row.SrcUser, row.DstUser = "alice@example.com", "bob@example.com"
	row.SrcOS, row.DstOS = "linux", "macos"
	row.TxBytes = 77
	insertRow(t, s, row)

	res := s.Query(wideWindow())

	if len(res.UserMatrix) != 1 {
		t.Fatalf("UserMatrix = %+v, want 1 cell", res.UserMatrix)
	}
	cell := res.UserMatrix[0]
	if cell.Src != "alice@example.com" || cell.Dst != "bob@example.com" || cell.Counts.TxBytes != 77 {
		t.Errorf("UserMatrix[0] = %+v, want alice->bob 77", cell)
	}

	if len(res.OSMatrix) != 1 {
		t.Fatalf("OSMatrix = %+v, want 1 cell", res.OSMatrix)
	}
	osCell := res.OSMatrix[0]
	if osCell.Src != "linux" || osCell.Dst != "macos" {
		t.Errorf("OSMatrix[0] = %+v, want linux->macos", osCell)
	}
}
