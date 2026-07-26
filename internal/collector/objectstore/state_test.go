package objectstore

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
)

func TestScanStateRoundTripsAndReplacesOnePrefix(t *testing.T) {
	cp := collector.NewMemoryStore()
	prefix := "flow ü/2026/07/24/"
	firstKey := prefix + "10:00:00 one.json"
	secondKey := prefix + "10:05:00 two.json"
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)

	batch := newCheckpointBatch()
	batch.setScanPosition(cp, prefix, firstKey, at)
	if err := batch.apply(cp); err != nil {
		t.Fatalf("apply first position: %v", err)
	}

	got, err := loadScanState(cp, "flow ü")
	if err != nil {
		t.Fatalf("load first position: %v", err)
	}
	if got.Positions[prefix] != firstKey {
		t.Fatalf("position = %q, want %q", got.Positions[prefix], firstKey)
	}

	batch = newCheckpointBatch()
	batch.setScanPosition(cp, prefix, secondKey, at.Add(time.Minute))
	if err := batch.apply(cp); err != nil {
		t.Fatalf("replace position: %v", err)
	}
	got, err = loadScanState(cp, "flow ü")
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if got.Positions[prefix] != secondKey {
		t.Fatalf("replacement = %q, want %q", got.Positions[prefix], secondKey)
	}

	var rows int
	for _, key := range cp.Keys() {
		if strings.HasPrefix(key, scanPrefix) {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("scan rows = %d, want exactly one", rows)
	}
}

func TestScanStateClearLeavesUnrelatedKeys(t *testing.T) {
	cp := collector.NewMemoryStore()
	prefix := "flow/2026/07/24/"
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	if err := cp.Set("unrelated", at); err != nil {
		t.Fatal(err)
	}
	batch := newCheckpointBatch()
	batch.setScanPosition(cp, prefix, prefix+"10:00:00.json", at)
	if err := batch.apply(cp); err != nil {
		t.Fatal(err)
	}

	batch = newCheckpointBatch()
	batch.clearScanPosition(cp, prefix)
	if err := batch.apply(cp); err != nil {
		t.Fatal(err)
	}
	if _, ok := cp.Get("unrelated"); !ok {
		t.Fatal("clearing scan position removed an unrelated checkpoint")
	}
	got, err := loadScanState(cp, "flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Positions) != 0 {
		t.Fatalf("positions = %v, want empty", got.Positions)
	}
}

func TestScanStateRoundTripsPrefixStart(t *testing.T) {
	cp := collector.NewMemoryStore()
	prefix := "flow/2026/07/24/"
	batch := newCheckpointBatch()
	batch.setScanPosition(cp, prefix, "", time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC))
	if err := batch.apply(cp); err != nil {
		t.Fatal(err)
	}

	got, err := loadScanState(cp, "flow")
	if err != nil {
		t.Fatal(err)
	}
	position, ok := got.Positions[prefix]
	if !ok || position != "" {
		t.Fatalf("prefix-start position = %q, present=%v; want empty and present", position, ok)
	}
}

func TestLoadScanStateRejectsDuplicateRows(t *testing.T) {
	cp := collector.NewMemoryStore()
	prefix := "flow/2026/07/24/"
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	encodedPrefix := base64.RawURLEncoding.EncodeToString([]byte(prefix))
	for _, key := range []string{
		prefix + "10:00:00.json",
		prefix + "10:05:00.json",
	} {
		row := scanPrefix + encodedPrefix + "/" + base64.RawURLEncoding.EncodeToString([]byte(key))
		if err := cp.Set(row, at); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := loadScanState(cp, "flow"); err == nil {
		t.Fatal("loadScanState accepted duplicate rows for one prefix")
	}
}

func TestLoadScanStateReturnsRowsOutsideConfiguredPrefixAsStale(t *testing.T) {
	cp := collector.NewMemoryStore()
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	batch := newCheckpointBatch()
	batch.setScanPosition(cp, "old/2026/07/24/", "old/2026/07/24/10:00:00.json", at)
	if err := batch.apply(cp); err != nil {
		t.Fatal(err)
	}

	got, err := loadScanState(cp, "flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Positions) != 0 || len(got.StaleKeys) != 1 {
		t.Fatalf("state = %+v, want one stale row and no active position", got)
	}
}
