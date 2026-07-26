package flowstore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// tagged returns an observation between two tagged endpoints. Tags arrive as the
// comma-joined set the flow record carries.
func tagged(srcTags, dstTags string, tx int64) flowstore.Observation {
	o := obs(base, "a", "b", tx, 0)
	o.SrcTags, o.DstTags = srcTags, dstTags
	return o
}

func labels(rows []flowstore.LabelStat) map[string]int64 {
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Label] = r.Counts.Bytes()
	}
	return out
}

func cells(rows []flowstore.MatrixCell) map[string]int64 {
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Src+"->"+r.Dst] = r.Counts.Bytes()
	}
	return out
}

// A device carries a SET of tags. Aggregating the joined set as one label makes
// "tag:servers" invisible whenever it appears alongside another tag, which is
// exactly the question an operator is asking.
func TestMemory_TagsBreakDownIndividually(t *testing.T) {
	s := newMemory(0)
	s.Record(tagged("tag:servers,tag:sshrecorder", "tag:laptops", 100))

	got := labels(s.Query(flowstore.Query{End: base.Add(time.Minute)}).Tags)
	want := map[string]int64{"tag:servers": 100, "tag:sshrecorder": 100, "tag:laptops": 100}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %q = %d, want %d (all tags: %v)", k, got[k], v, got)
		}
	}
	for k := range got {
		if strings.Contains(k, ",") {
			t.Errorf("tag label %q is a joined set, not an individual tag", k)
		}
	}
}

// A tag on both ends of one flow describes one flow, not two.
func TestMemory_TagOnBothEndpointsCountsOnce(t *testing.T) {
	s := newMemory(0)
	s.Record(tagged("tag:servers,tag:prod", "tag:servers", 100))

	got := labels(s.Query(flowstore.Query{End: base.Add(time.Minute)}).Tags)
	if got["tag:servers"] != 100 {
		t.Errorf("tag:servers = %d, want 100 (double counted?)", got["tag:servers"])
	}
	if got["tag:prod"] != 100 {
		t.Errorf("tag:prod = %d, want 100", got["tag:prod"])
	}
}

// Whitespace and empty entries in a joined set are formatting, not tags.
func TestMemory_TagSplittingIsTolerant(t *testing.T) {
	s := newMemory(0)
	s.Record(tagged("tag:a, tag:b,,  ,tag:c ", "", 10))

	got := labels(s.Query(flowstore.Query{End: base.Add(time.Minute)}).Tags)
	if len(got) != 3 {
		t.Fatalf("tags = %v, want exactly tag:a, tag:b, tag:c", got)
	}
	for _, want := range []string{"tag:a", "tag:b", "tag:c"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q; got %v", want, got)
		}
	}
}

// The matrix is the cross product of the two endpoints' tags: a flow from a
// two-tagged device to a one-tagged device populates two cells.
func TestMemory_TagMatrixIsTheCrossProduct(t *testing.T) {
	s := newMemory(0)
	s.Record(tagged("tag:servers,tag:prod", "tag:laptops", 100))

	got := cells(s.Query(flowstore.Query{End: base.Add(time.Minute)}).TagMatrix)
	want := map[string]int64{
		"tag:servers->tag:laptops": 100,
		"tag:prod->tag:laptops":    100,
	}
	if len(got) != len(want) {
		t.Fatalf("matrix = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("cell %q = %d, want %d", k, got[k], v)
		}
	}
}

// Direction is preserved here for the same reason it is in the pair table: a
// flow is reported once, by one node, and which way it went is real.
func TestMemory_TagMatrixKeepsDirection(t *testing.T) {
	s := newMemory(0)
	s.Record(tagged("tag:a", "tag:b", 100))
	s.Record(tagged("tag:b", "tag:a", 300))

	got := cells(s.Query(flowstore.Query{End: base.Add(time.Minute)}).TagMatrix)
	if got["tag:a->tag:b"] != 100 || got["tag:b->tag:a"] != 300 {
		t.Errorf("matrix = %v, want the two directions kept apart", got)
	}
}

// A flow whose endpoints are not both tagged cannot occupy a cell. It must not
// be invented into one — the page states matrix coverage against the window
// total, and a fabricated cell would make that statement wrong.
func TestMemory_MatrixOmitsFlowsMissingAnEndpoint(t *testing.T) {
	s := newMemory(0)
	s.Record(tagged("tag:servers", "", 100)) // untagged (user-owned) destination
	s.Record(tagged("", "tag:laptops", 50))  // untagged source

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if len(res.TagMatrix) != 0 {
		t.Errorf("matrix = %v, want no cells when an endpoint carries no tags", res.TagMatrix)
	}
	// The traffic still counts everywhere else.
	if res.Totals.TxBytes != 150 {
		t.Errorf("total tx = %d, want 150", res.Totals.TxBytes)
	}
	if got := labels(res.Tags); got["tag:servers"] != 100 || got["tag:laptops"] != 50 {
		t.Errorf("one-sided tags missing from the breakdown: %v", got)
	}
}

// The same machinery serves users and operating systems, which are single values
// rather than sets.
func TestMemory_UserAndOSMatrices(t *testing.T) {
	s := newMemory(0)
	o := obs(base, "a", "b", 100, 0)
	o.SrcUser, o.DstUser = "rob@example.com", "sam@example.com"
	o.SrcOS, o.DstOS = "linux", "macOS"
	s.Record(o)

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if got := cells(res.UserMatrix); got["rob@example.com->sam@example.com"] != 100 {
		t.Errorf("user matrix = %v", got)
	}
	if got := cells(res.OSMatrix); got["linux->macOS"] != 100 {
		t.Errorf("os matrix = %v", got)
	}
}

// A device talking to another device owned by the same person is a real, common
// flow — the diagonal of the matrix is meaningful and must not be suppressed the
// way the single-dimension breakdown deduplicates it.
func TestMemory_MatrixKeepsTheDiagonal(t *testing.T) {
	s := newMemory(0)
	o := obs(base, "laptop", "desktop", 100, 0)
	o.SrcUser, o.DstUser = "rob@example.com", "rob@example.com"
	s.Record(o)

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if got := cells(res.UserMatrix)["rob@example.com->rob@example.com"]; got != 100 {
		t.Errorf("diagonal cell = %d, want 100", got)
	}
	// The one-dimensional breakdown still counts the shared user once.
	if got := labels(res.Users)["rob@example.com"]; got != 100 {
		t.Errorf("user breakdown = %d, want 100 (counted twice?)", got)
	}
}

// The cross product is where a matrix can blow up, and tags come from the
// control plane rather than from us. Cap it like every other dimension.
func TestMemory_MatrixIsBoundedAndReportsTruncation(t *testing.T) {
	s := newMemory(0)
	for i := range flowstore.MaxMatrixCellsPerBucket + 200 {
		s.Record(tagged("tag:s"+time.Duration(i).String(), "tag:d"+time.Duration(i).String(), 10))
	}

	res := s.Query(flowstore.Query{End: base.Add(time.Minute), TopN: -1})
	if len(res.TagMatrix) > flowstore.MaxMatrixCellsPerBucket+1 {
		t.Errorf("matrix returned %d cells, want at most the cap plus the overflow key", len(res.TagMatrix))
	}
	if res.Truncated == 0 {
		t.Error("Truncated = 0 after overflowing the matrix cap")
	}
	// Totals stay exact regardless of folding.
	if want := int64((flowstore.MaxMatrixCellsPerBucket + 200) * 10); res.Totals.TxBytes != want {
		t.Errorf("total tx = %d, want %d", res.Totals.TxBytes, want)
	}
}

// Ranked like every other list, with a tiebreak so equal-volume cells do not
// reshuffle between polls.
func TestMemory_MatrixRankingIsStable(t *testing.T) {
	s := newMemory(0)
	for _, tag := range []string{"tag:zulu", "tag:alpha", "tag:mike"} {
		s.Record(tagged(tag, "tag:dst", 100))
	}

	first := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	for range 5 {
		next := s.Query(flowstore.Query{End: base.Add(time.Minute)})
		for i := range first.TagMatrix {
			if first.TagMatrix[i].Src != next.TagMatrix[i].Src {
				t.Fatalf("matrix order unstable at %d: %q then %q",
					i, first.TagMatrix[i].Src, next.TagMatrix[i].Src)
			}
		}
	}
}

// Identity on the connection ring is what lets the flow list answer "show me
// what this user's devices actually did", which no aggregate can.
func TestMemory_RecentCarriesIdentity(t *testing.T) {
	s := newMemory(0)
	o := conn(base, "100.64.0.1:52000", "100.64.0.2:443", 120)
	o.SrcUser, o.DstUser = "rob@example.com", "sam@example.com"
	o.SrcTags, o.DstTags = "tag:servers", "tag:laptops"
	o.SrcOS, o.DstOS = "linux", "macOS"
	s.Record(o)

	got := s.Recent(1)
	if len(got) != 1 {
		t.Fatalf("Recent returned %d entries", len(got))
	}
	r := got[0]
	if r.SrcUser != "rob@example.com" || r.DstUser != "sam@example.com" {
		t.Errorf("users = %q -> %q", r.SrcUser, r.DstUser)
	}
	if r.SrcTags != "tag:servers" || r.DstTags != "tag:laptops" {
		t.Errorf("tags = %q -> %q", r.SrcTags, r.DstTags)
	}
	if r.SrcOS != "linux" || r.DstOS != "macOS" {
		t.Errorf("oses = %q -> %q", r.SrcOS, r.DstOS)
	}
}

// Redaction upstream leaves identity empty; that must stay empty rather than
// becoming a placeholder the operator could mistake for a real value.
func TestMemory_RecentIdentityAbsentStaysAbsent(t *testing.T) {
	s := newMemory(0)
	s.Record(conn(base, "100.64.0.1:1", "100.64.0.2:2", 10))

	r := s.Recent(1)[0]
	if r.SrcUser != "" || r.DstUser != "" || r.SrcTags != "" || r.SrcOS != "" {
		t.Errorf("absent identity was filled in: %+v", r)
	}
}
