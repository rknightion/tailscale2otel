package flowstore_test

import (
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

var base = time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)

// The aggregation suite deliberately exercises synthetic timelines spanning
// hours and days. Keep that coverage separate from admission, which has its
// own fixed-clock tests in the flowstore package.
func newMemory(capacity int) *flowstore.Memory {
	return flowstore.NewMemory(capacity,
		flowstore.WithClock(func() time.Time { return base }),
		flowstore.WithMaxFutureSkew(7*24*time.Hour),
	)
}

func obs(at time.Time, src, dst string, tx, rx int64) flowstore.Observation {
	return flowstore.Observation{
		Time:        at,
		TrafficType: "virtual",
		Transport:   "tcp",
		SrcNode:     src,
		DstNode:     dst,
		DstPort:     "443",
		DstService:  "https",
		Counts:      flowstore.Counts{TxBytes: tx, RxBytes: rx, TxPkts: 1, RxPkts: 1, Flows: 1},
	}
}

func TestMemory_AggregatesWithinAndAcrossBuckets(t *testing.T) {
	s := newMemory(0)
	// Two observations in the same minute, one in the next.
	s.Record(obs(base, "a", "b", 100, 50))
	s.Record(obs(base.Add(30*time.Second), "a", "b", 100, 50))
	s.Record(obs(base.Add(90*time.Second), "a", "b", 200, 0))

	res := s.Query(flowstore.Query{Start: base.Add(-time.Hour), End: base.Add(time.Hour)})

	if want := int64(400); res.Totals.TxBytes != want {
		t.Errorf("total tx = %d, want %d", res.Totals.TxBytes, want)
	}
	if want := int64(100); res.Totals.RxBytes != want {
		t.Errorf("total rx = %d, want %d", res.Totals.RxBytes, want)
	}
	if want := int64(3); res.Totals.Flows != want {
		t.Errorf("flows = %d, want %d", res.Totals.Flows, want)
	}
	// Minute resolution: the first two collapse into one point, the third is its own.
	if len(res.Series) != 2 {
		t.Fatalf("series has %d points, want 2: %+v", len(res.Series), res.Series)
	}
	if got := res.Series[0].Counts.TxBytes; got != 200 {
		t.Errorf("first bucket tx = %d, want 200", got)
	}
}

// Transmit and receive are independently measured on this data and must both
// survive aggregation — collapsing them, or dropping receive as a presumed
// duplicate, would discard real measurement.
func TestMemory_PreservesBothDirections(t *testing.T) {
	s := newMemory(0)
	s.Record(obs(base, "a", "b", 1000, 700))

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if res.Totals.TxBytes != 1000 || res.Totals.RxBytes != 700 {
		t.Errorf("counts = tx %d / rx %d, want 1000 / 700", res.Totals.TxBytes, res.Totals.RxBytes)
	}
	if got, want := res.Totals.Bytes(), int64(1700); got != want {
		t.Errorf("Bytes() = %d, want %d", got, want)
	}
}

// A node's received total is what its peers sent to it, so the counters mirror
// per role rather than being copied.
func TestMemory_NodeRolesMirrorCounters(t *testing.T) {
	s := newMemory(0)
	s.Record(obs(base, "sender", "receiver", 900, 100))

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	byNode := map[string]flowstore.NodeStat{}
	for _, n := range res.Nodes {
		byNode[n.Node] = n
	}

	if got := byNode["sender"].Sent.TxBytes; got != 900 {
		t.Errorf("sender sent tx = %d, want 900", got)
	}
	// The receiver received the 900 the sender transmitted.
	if got := byNode["receiver"].Received.TxBytes; got != 100 {
		t.Errorf("receiver received tx = %d, want 100 (its own transmit back)", got)
	}
	if got := byNode["receiver"].Received.RxBytes; got != 900 {
		t.Errorf("receiver received rx = %d, want 900", got)
	}
}

// Direction is real information here: a flow is reported once, by one node.
// Normalising pairs into unordered edges would lose who initiated.
func TestMemory_PairsKeepDirection(t *testing.T) {
	s := newMemory(0)
	s.Record(obs(base, "a", "b", 100, 0))
	s.Record(obs(base, "b", "a", 300, 0))

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if len(res.Pairs) != 2 {
		t.Fatalf("got %d pairs, want 2 (direction collapsed?): %+v", len(res.Pairs), res.Pairs)
	}
	// Ranked by bytes: b->a (300) first.
	if res.Pairs[0].Src != "b" || res.Pairs[0].Dst != "a" {
		t.Errorf("top pair = %s->%s, want b->a", res.Pairs[0].Src, res.Pairs[0].Dst)
	}
}

// An observation missing an endpoint (exit traffic never carries a destination)
// must still count toward totals while contributing no topology edge.
func TestMemory_AbsentEndpointCountsButFormsNoEdge(t *testing.T) {
	s := newMemory(0)
	s.Record(flowstore.Observation{
		Time:        base,
		TrafficType: "exit",
		Transport:   "unknown",
		SrcNode:     "relay",
		Counts:      flowstore.Counts{TxBytes: 320, TxPkts: 5, Flows: 1},
	})

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if res.Totals.TxBytes != 320 {
		t.Errorf("total tx = %d, want 320 — absent destination dropped the observation", res.Totals.TxBytes)
	}
	if len(res.Pairs) != 0 {
		t.Errorf("got %d pairs from an observation with no destination: %+v", len(res.Pairs), res.Pairs)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Node != "relay" {
		t.Errorf("nodes = %+v, want just the source", res.Nodes)
	}
}

// Empty is "not carried", not a label value — it must not become a bar in a
// breakdown chart.
func TestMemory_EmptyLabelsAreNotCounted(t *testing.T) {
	s := newMemory(0)
	s.Record(flowstore.Observation{
		Time: base, TrafficType: "virtual", Transport: "tcp",
		SrcNode: "a", DstNode: "b",
		Counts: flowstore.Counts{TxBytes: 10, Flows: 1},
	})

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	for _, l := range res.Users {
		if l.Label == "" {
			t.Error("empty user label present in breakdown")
		}
	}
	if len(res.Users) != 0 {
		t.Errorf("users = %+v, want none (none were carried)", res.Users)
	}
}

// Identity is deduplicated when both endpoints share a value, so a flow between
// two devices owned by one user counts once for that user.
func TestMemory_IdentityCountedOncePerSharedValue(t *testing.T) {
	s := newMemory(0)
	o := obs(base, "a", "b", 100, 0)
	o.SrcUser, o.DstUser = "rob@example.com", "rob@example.com"
	s.Record(o)

	res := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	if len(res.Users) != 1 {
		t.Fatalf("users = %+v, want 1", res.Users)
	}
	if got := res.Users[0].Counts.TxBytes; got != 100 {
		t.Errorf("user tx = %d, want 100 (double counted?)", got)
	}
}

// The ring must not grow without bound; old buckets age out.
func TestMemory_EvictsBeyondCapacity(t *testing.T) {
	s := newMemory(3)
	for i := range 10 {
		s.Record(obs(base.Add(time.Duration(i)*time.Minute), "a", "b", 10, 0))
	}

	st := s.Stats()
	if st.Buckets != 3 {
		t.Errorf("buckets = %d, want 3", st.Buckets)
	}
	// The three most recent minutes survive.
	if want := base.Add(7 * time.Minute); !st.Earliest.Equal(want) {
		t.Errorf("earliest = %v, want %v", st.Earliest, want)
	}
	if want := base.Add(9 * time.Minute); !st.Latest.Equal(want) {
		t.Errorf("latest = %v, want %v", st.Latest, want)
	}
}

// A flood of unique keys must fold into Other rather than growing the bucket,
// and must be reported as truncation rather than silently implying full
// coverage. This is the unauthenticated-stream-ingress case.
func TestMemory_BoundsPairCardinalityAndReportsIt(t *testing.T) {
	s := newMemory(0)
	for i := range flowstore.MaxPairsPerBucket + 500 {
		s.Record(obs(base, "src", string(rune('a'+i%26))+time.Duration(i).String(), 10, 0))
	}

	res := s.Query(flowstore.Query{End: base.Add(time.Minute), TopN: -1})
	if len(res.Pairs) > flowstore.MaxPairsPerBucket+1 {
		t.Errorf("pairs = %d, want at most the cap plus the overflow key", len(res.Pairs))
	}
	if res.Truncated == 0 {
		t.Error("Truncated = 0 after exceeding the pair cap; overflow was not reported")
	}
	// Totals stay exact even though keys were folded.
	if want := int64((flowstore.MaxPairsPerBucket + 500) * 10); res.Totals.TxBytes != want {
		t.Errorf("total tx = %d, want %d — folding must not lose bytes", res.Totals.TxBytes, want)
	}
}

// A wide window must not produce an unbounded series; the step is raised to fit.
func TestMemory_TimelineIsBounded(t *testing.T) {
	s := newMemory(5000)
	for i := range 3000 {
		s.Record(obs(base.Add(time.Duration(i)*time.Minute), "a", "b", 10, 0))
	}

	res := s.Query(flowstore.Query{
		Start: base.Add(-time.Minute),
		End:   base.Add(3000 * time.Minute),
	})
	if len(res.Series) > flowstore.MaxPoints {
		t.Errorf("series = %d points, want at most %d", len(res.Series), flowstore.MaxPoints)
	}
	if want := int64(30000); res.Totals.TxBytes != want {
		t.Errorf("total tx = %d, want %d — re-bucketing must preserve totals", res.Totals.TxBytes, want)
	}
}

// Ranked output must be stable between polls, or equal-volume rows reshuffle in
// the UI on every refresh.
func TestMemory_RankingIsStableOnTies(t *testing.T) {
	s := newMemory(0)
	for _, n := range []string{"zulu", "alpha", "mike"} {
		s.Record(obs(base, n, "dst", 100, 0))
	}

	first := s.Query(flowstore.Query{End: base.Add(time.Minute)})
	for range 5 {
		next := s.Query(flowstore.Query{End: base.Add(time.Minute)})
		for i := range first.Pairs {
			if first.Pairs[i].Src != next.Pairs[i].Src {
				t.Fatalf("ranking unstable at %d: %q then %q", i, first.Pairs[i].Src, next.Pairs[i].Src)
			}
		}
	}
}

// The window reported back is the span actually covered, so the UI does not
// claim history the store never had.
func TestMemory_ReportsCoveredWindow(t *testing.T) {
	s := newMemory(0)
	s.Record(obs(base, "a", "b", 10, 0))

	res := s.Query(flowstore.Query{End: base.Add(time.Hour)})
	if !res.Window.Start.Equal(base) {
		t.Errorf("window start = %v, want %v", res.Window.Start, base)
	}
	if want := base.Add(time.Minute); !res.Window.End.Equal(want) {
		t.Errorf("window end = %v, want %v (clamped to retained data)", res.Window.End, want)
	}
}

// The emit path is concurrent: poll and stream share one processor.
func TestMemory_ConcurrentRecord(t *testing.T) {
	s := newMemory(0)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				s.Record(obs(base.Add(time.Duration(i%10)*time.Minute), "a", "b", 1, 0))
			}
			_ = s.Query(flowstore.Query{End: base.Add(time.Hour)})
			_ = w
		}()
	}
	wg.Wait()

	res := s.Query(flowstore.Query{End: base.Add(time.Hour)})
	if want := int64(8 * 200); res.Totals.TxBytes != want {
		t.Errorf("total tx = %d, want %d", res.Totals.TxBytes, want)
	}
}
