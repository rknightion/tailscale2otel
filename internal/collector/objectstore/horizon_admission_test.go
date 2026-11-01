package objectstore_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

func seedHorizonScan(t *testing.T, h *harness, prefix string) {
	t.Helper()
	scope := objectstore.CheckpointScope{Tailnet: "test.example", Provider: "s3", Signal: semconv.IngestSignalFlow, Feed: objectstore.FeedID("test", "flow")}
	ns, err := scope.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.cp.Set(ns+"/scan/"+base64.RawURLEncoding.EncodeToString([]byte(prefix))+"/-", now); err != nil {
		t.Fatal(err)
	}
}

// Saved partition walks must not admit expired history ahead of either budget.
func TestCollect_HorizonAdmissionBeforeSavedPartitionBudgets(t *testing.T) {
	for _, oldPartitions := range []int{1, 14} {
		t.Run(fmt.Sprint(oldPartitions), func(t *testing.T) {
			h := newHarness(t, func(o *objectstore.Options) { o.LogMaxRecordAge = 6 * 24 * time.Hour; o.MaxObjects = 1 })
			for i := range oldPartitions {
				at := now.Add(-time.Duration(8+i) * 24 * time.Hour)
				prefix := "flow/" + at.Format("2006/01/02") + "/"
				seedHorizonScan(t, h, prefix)
				h.store.put(keyAt(at, ".ndjson"), []byte(record(fmt.Sprint("expired", i), at)+"\n"))
			}
			fresh := now.Add(-time.Minute)
			freshKey := keyAt(fresh, ".ndjson")
			h.store.put(freshKey, []byte(record("current", fresh)+"\n"))
			h.collect(t)
			if got := h.flowRecords(); got != 1 {
				t.Errorf("current records=%d, want 1 with %d saved expired partitions", got, oldPartitions)
			}
			if len(h.store.getCalls) != 1 || h.store.getCalls[0] != freshKey {
				t.Errorf("fetches=%v, want current object only", h.store.getCalls)
			}
			for _, call := range h.store.listCalls {
				if call.Prefix != "flow/"+fresh.Format("2006/01/02")+"/" && strings.Contains(call.Prefix, "flow/") {
					// A listing may include other current days, but never an expired saved day.
					day, err := time.Parse("2006/01/02/", strings.TrimPrefix(call.Prefix, "flow/"))
					if err == nil && !day.AddDate(0, 0, 1).After(now.Add(-6*24*time.Hour)) {
						t.Errorf("listed wholly expired partition %s", call.Prefix)
					}
				}
			}
			for _, lr := range h.rec.LogRecords() {
				if lr.Timestamp.Before(now.Add(-6 * 24 * time.Hour)) {
					t.Errorf("expired log emitted: %+v", lr)
				}
			}
			for _, key := range h.cp.Keys() {
				for i := range oldPartitions {
					at := now.Add(-time.Duration(8+i) * 24 * time.Hour)
					if strings.Contains(key, scanRowFragment("flow/"+at.Format("2006/01/02")+"/")) {
						t.Errorf("expired scan remains: %s", key)
					}
				}
			}
		})
	}
}

func TestCollect_HorizonAdmissionPreservesPartialDayOverlapAndZero(t *testing.T) {
	t.Run("partial day overlap", func(t *testing.T) {
		floor := now.Add(-6 * 24 * time.Hour)
		h := newHarness(t, func(o *objectstore.Options) { o.LogMaxRecordAge = 6 * 24 * time.Hour; o.MaxObjects = 1 })
		seedHorizonScan(t, h, "flow/"+floor.Format("2006/01/02")+"/")
		scope := objectstore.CheckpointScope{Tailnet: "test.example", Provider: "s3", Signal: semconv.IngestSignalFlow, Feed: objectstore.FeedID("test", "flow")}
		ns, _ := scope.Namespace()
		if err := h.cp.Set(ns+"/cursor", now.Add(-10*time.Minute)); err != nil {
			t.Fatal(err)
		}
		expired := floor.Add(-time.Hour)
		valid := floor.Add(time.Hour)
		fresh := now.Add(-time.Minute)
		for _, at := range []time.Time{expired, valid, fresh} {
			h.store.put(keyAt(at, ".ndjson"), []byte(record(at.String(), at)+"\n"))
		}
		h.collect(t)
		h.collect(t)
		if got := h.flowRecords(); got != 2 {
			t.Errorf("records=%d, want valid saved overlap and current object", got)
		}
		for _, key := range h.store.getCalls {
			if key == keyAt(expired, ".ndjson") {
				t.Error("expired boundary-day object consumed budget")
			}
		}
		for _, lr := range h.rec.LogRecords() {
			if lr.Timestamp.Before(floor) {
				t.Errorf("expired log emitted: %+v", lr)
			}
		}
	})
	t.Run("zero disables", func(t *testing.T) {
		h := newHarness(t, func(o *objectstore.Options) { o.LogMaxRecordAge = 0; o.MaxObjects = 1 })
		old := now.Add(-8 * 24 * time.Hour)
		seedHorizonScan(t, h, "flow/"+old.Format("2006/01/02")+"/")
		h.store.put(keyAt(old, ".ndjson"), []byte(record("historical", old)+"\n"))
		fresh := now.Add(-time.Minute)
		h.store.put(keyAt(fresh, ".ndjson"), []byte(record("current", fresh)+"\n"))
		h.collect(t)
		h.collect(t)
		if got := h.flowRecords(); got != 2 {
			t.Errorf("disabled horizon records=%d, want historical and current", got)
		}
	})
}
