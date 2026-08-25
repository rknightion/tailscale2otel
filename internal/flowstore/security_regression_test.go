package flowstore

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func hostileObservation(now time.Time) Observation {
	huge := strings.Repeat("界", 20_000)
	return Observation{
		Time: now, TrafficType: huge, Transport: huge, SrcAddr: huge, DstAddr: huge,
		SrcNode: huge, DstNode: huge, DstPort: huge, DstService: huge,
		SrcUser: huge, DstUser: huge, SrcTags: huge, DstTags: huge,
		SrcOS: huge, DstOS: huge, ReporterNodeID: huge, ReporterTrust: huge,
		ReporterConsistency: huge, Verdict: huge, PolicyVersion: huge, Path: huge, DERPRegion: huge,
	}
}

func TestMemoryRetentionBoundsObservationStrings(t *testing.T) {
	now := time.Now()
	m := NewMemory(1, WithClock(func() time.Time { return now }))
	if got := m.RecordResult(hostileObservation(now)); got != AdmissionAccepted {
		t.Fatalf("admission = %v, want accepted", got)
	}
	page := m.RecentPage(RecentQuery{Limit: 1, End: now.Add(time.Second)})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(page.Rows))
	}
	r := page.Rows[0]
	values := []string{r.TrafficType, r.Transport, r.SrcAddr, r.DstAddr, r.SrcNode, r.DstNode,
		r.DstService, r.SrcUser, r.DstUser, r.SrcTags, r.DstTags, r.SrcOS, r.DstOS,
		r.ReporterNodeID, r.ReporterTrust, r.ReporterConsistency, r.Verdict, r.PolicyVersion, r.Path, r.DERPRegion}
	total := 0
	for i, value := range values {
		if len(value) > MaxRetainedFieldBytes {
			t.Errorf("field %d retained %d bytes, want <= %d", i, len(value), MaxRetainedFieldBytes)
		}
		if !utf8.ValidString(value) {
			t.Errorf("field %d is invalid UTF-8", i)
		}
		total += len(value)
	}
	if total > MaxRetainedObservationBytes {
		t.Fatalf("observation retained %d bytes, want <= %d", total, MaxRetainedObservationBytes)
	}
}
