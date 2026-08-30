package rdns

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestSnapshotPath(t *testing.T) {
	dir := t.TempDir()
	checkpoint := filepath.Join(dir, "checkpoints.json")

	if got, want := SnapshotPath(checkpoint), filepath.Join(dir, "rdns.json"); got != want {
		t.Fatalf("SnapshotPath(%q) = %q, want %q", checkpoint, got, want)
	}
	for _, empty := range []string{"", "   "} {
		if got := SnapshotPath(empty); got != "" {
			t.Errorf("SnapshotPath(%q) = %q, want empty", empty, got)
		}
	}
}

func TestCache_WarmStartsFromSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rdns.json")
		at := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
		clock := at
		ip := addr("203.0.113.5")

		var firstCalls atomic.Int32
		first := New(Options{
			TTL:            time.Hour,
			MaxEntries:     4,
			SnapshotPath:   path,
			ReportInterval: testNoTick,
			Now:            func() time.Time { return clock },
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				firstCalls.Add(1)
				return []string{"warm.example.com."}, nil
			},
		})
		first.LookupName(ip)
		synctest.Wait()
		first.Close()

		if got := firstCalls.Load(); got != 1 {
			t.Fatalf("initial lookup calls = %d, want 1", got)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("snapshot was not persisted: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("snapshot mode = %04o, want 0600", got)
		}

		var restartCalls atomic.Int32
		restarted := New(Options{
			TTL:            time.Hour,
			MaxEntries:     4,
			SnapshotPath:   path,
			ReportInterval: testNoTick,
			Now:            func() time.Time { return clock.Add(time.Minute) },
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				restartCalls.Add(1)
				return nil, context.Canceled
			},
		})
		defer restarted.Close()

		if name, ok := restarted.LookupName(ip); !ok || name != "warm.example.com" {
			t.Fatalf("warm LookupName = (%q,%v), want (warm.example.com,true)", name, ok)
		}
		synctest.Wait()
		if got := restartCalls.Load(); got != 0 {
			t.Fatalf("restart lookup calls = %d, want 0 for a warm entry", got)
		}
	})
}

func TestCache_DoesNotRestoreExpiredSnapshotEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rdns.json")
		at := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
		clock := at
		ip := addr("203.0.113.6")

		seed := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			SnapshotPath:   path,
			ReportInterval: testNoTick,
			Now:            func() time.Time { return clock },
			Lookup:         okLookup("expired.example.com."),
		})
		seed.LookupName(ip)
		synctest.Wait()
		seed.Close()

		// Exactly at the persisted expiry, the entry is expired. In particular,
		// the normal stale-serving window must not resurrect it across a restart.
		clock = at.Add(time.Hour)
		var calls atomic.Int32
		restarted := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			SnapshotPath:   path,
			ReportInterval: testNoTick,
			Now:            func() time.Time { return clock },
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				calls.Add(1)
				return []string{"new.example.com."}, nil
			},
		})
		defer restarted.Close()

		if name, ok := restarted.LookupName(ip); ok || name != "" {
			t.Fatalf("expired warm LookupName = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("expired entry lookup calls = %d, want 1", got)
		}
	})
}

func TestCache_CorruptOrMissingSnapshotStartsCold(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "missing"},
		{name: "corrupt", body: []byte(`{"version":1,"entries":[`)},
		{name: "invalid-entry", body: []byte(`{"version":1,"entries":[{"addr":"203.0.113.7","name":" bad.example.com","expires":"2099-01-01T00:00:00Z"}]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "rdns.json")
				if tc.body != nil {
					if err := os.WriteFile(path, tc.body, 0o600); err != nil {
						t.Fatal(err)
					}
				}

				var calls atomic.Int32
				c := New(Options{
					SnapshotPath:   path,
					ReportInterval: testNoTick,
					Lookup: func(context.Context, netip.Addr) ([]string, error) {
						calls.Add(1)
						return []string{"cold.example.com."}, nil
					},
				})
				defer c.Close()

				if name, ok := c.LookupName(addr("203.0.113.7")); ok || name != "" {
					t.Fatalf("cold LookupName = (%q,%v), want (\"\",false)", name, ok)
				}
				synctest.Wait()
				if got := calls.Load(); got != 1 {
					t.Fatalf("cold-start lookup calls = %d, want 1", got)
				}
			})
		})
	}
}

func TestCache_SnapshotNeverExceedsConfiguredCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rdns.json")
		c := New(Options{
			MaxEntries:     2,
			SnapshotPath:   path,
			ReportInterval: testNoTick,
			Lookup:         okLookup("bounded.example.com."),
		})

		for _, text := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
			c.LookupName(addr(text))
			synctest.Wait()
		}
		c.Close()

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var disk struct {
			Entries []json.RawMessage `json:"entries"`
		}
		if err := json.Unmarshal(data, &disk); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if got := len(disk.Entries); got > 2 {
			t.Fatalf("snapshot entries = %d, want <= 2", got)
		}
	})
}

func TestCache_OverCapacitySnapshotStartsCold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rdns.json")
		future := time.Now().Add(time.Hour).UTC()
		body, err := json.Marshal(diskSnapshot{
			Version: snapshotVersion,
			Entries: []diskSnapshotEntry{
				{Addr: "203.0.113.20", Name: "one.example.com", Expires: future},
				{Addr: "203.0.113.21", Name: "two.example.com", Expires: future},
				{Addr: "203.0.113.22", Name: "three.example.com", Expires: future},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}

		var calls atomic.Int32
		c := New(Options{
			MaxEntries:     2,
			SnapshotPath:   path,
			ReportInterval: testNoTick,
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				calls.Add(1)
				return []string{"cold.example.com."}, nil
			},
		})
		defer c.Close()

		if name, ok := c.LookupName(addr("203.0.113.20")); ok || name != "" {
			t.Fatalf("over-capacity snapshot LookupName = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("over-capacity snapshot lookup calls = %d, want 1", got)
		}
	})
}
