package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// The timeline scrubber pins a historical window and the aggregates honor it.
// The raw connection list did not: handleFlowsJSON asked the store for the newest
// rows outright, ignoring the same start/end it had just applied to the query. So
// scrubbing back an hour produced a historical chart beside a live connection
// list — two different time ranges rendered as one view (#295).

func TestFlowsJSON_RecentRowsHonorThePinnedWindow(t *testing.T) {
	a := flowsTestApp(t, nil)
	now := time.Now().UTC()

	store := a.runtimes[0].flowStore
	store.Record(flowstore.Observation{
		Time: now.Add(-90 * time.Minute), SrcNode: "historical", DstNode: "b",
		Counts: flowstore.Counts{Flows: 1, TxBytes: 1},
	})
	store.Record(flowstore.Observation{
		Time: now.Add(-1 * time.Minute), SrcNode: "live", DstNode: "b",
		Counts: flowstore.Counts{Flows: 1, TxBytes: 1},
	})

	// Pin a 10-minute window around the historical row, the way the scrubber does.
	end := now.Add(-90 * time.Minute).Add(5 * time.Minute)
	q := fmt.Sprintf("?window=10m&end=%s", end.Format(time.RFC3339))
	w, got := getFlows(t, a, q)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(got.Recent) == 0 {
		t.Fatal("the pinned window returned no rows at all, so this test cannot distinguish " +
			"'window respected' from 'nothing matched'")
	}
	for _, r := range got.Recent {
		if r.SrcNode == "live" {
			t.Errorf("a row from outside the pinned window (%s) was served with it. The rows must "+
				"use the same start/end as the aggregates, or the page shows a historical chart "+
				"next to a live connection list.", r.Time)
		}
	}

	// The default (unpinned) view must still show the newest.
	_, live := getFlows(t, a, "")
	var sawLive bool
	for _, r := range live.Recent {
		if r.SrcNode == "live" {
			sawLive = true
		}
	}
	if !sawLive {
		t.Error("the default view no longer shows the newest connections")
	}
}
