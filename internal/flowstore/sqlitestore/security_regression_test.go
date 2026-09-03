package sqlitestore

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

func TestSQLiteRetentionUsesSharedObservationByteBound(t *testing.T) {
	now := time.Now().UTC()
	s := openRecentTestStore(t, func(opts *Options) {
		opts.Now = func() time.Time { return now }
		opts.FlushInterval = 5 * time.Millisecond
	})
	huge := strings.Repeat("界", 20_000)
	o := flowstore.Observation{
		Time: now, TrafficType: huge, Transport: huge, SrcAddr: huge, DstAddr: huge,
		SrcNode: huge, DstNode: huge, DstPort: huge, DstService: huge,
		SrcUser: huge, DstUser: huge, SrcTags: huge, DstTags: huge, SrcOS: huge, DstOS: huge,
		ReporterNodeID: huge, ReporterTrust: huge, ReporterConsistency: huge,
		Verdict: huge, PolicyVersion: huge, Path: huge, DERPRegion: huge,
	}
	if got := s.RecordResult(o); got != flowstore.AdmissionAccepted {
		t.Fatalf("admission = %v, want accepted", got)
	}
	waitForRows(t, s.db, 1, time.Second)
	page := s.RecentPage(flowstore.RecentQuery{Limit: 1, End: now.Add(time.Second)})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(page.Rows))
	}
	r := page.Rows[0]
	values := []string{r.TrafficType, r.Transport, r.SrcAddr, r.DstAddr, r.SrcNode, r.DstNode,
		r.DstService, r.SrcUser, r.DstUser, r.SrcTags, r.DstTags, r.SrcOS, r.DstOS,
		r.ReporterNodeID, r.ReporterTrust, r.ReporterConsistency, r.Verdict, r.PolicyVersion, r.Path, r.DERPRegion}
	total := 0
	for i, value := range values {
		if len(value) > flowstore.MaxRetainedFieldBytes {
			t.Errorf("field %d retained %d bytes, want <= %d", i, len(value), flowstore.MaxRetainedFieldBytes)
		}
		if !utf8.ValidString(value) {
			t.Errorf("field %d is invalid UTF-8", i)
		}
		total += len(value)
	}
	if total > flowstore.MaxRetainedObservationBytes {
		t.Fatalf("row retained %d string bytes, want <= %d", total, flowstore.MaxRetainedObservationBytes)
	}
}
