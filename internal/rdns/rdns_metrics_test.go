package rdns

import (
	"context"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// testNoTick is large enough that the periodic sweep/report ticker never fires
// during a unit test's fake-clock advances, so tests drive sweep()/report()
// manually and assert deterministically.
const testNoTick = 1000 * time.Hour

func okLookup(name string) func(context.Context, netip.Addr) ([]string, error) {
	return func(context.Context, netip.Addr) ([]string, error) { return []string{name}, nil }
}

func TestStats_TracksLookupOutcomes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			MaxEntries:     10,
			ReportInterval: testNoTick,
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5")) // miss → schedule
		synctest.Wait()                   // background resolve stores the positive entry
		c.LookupName(addr("203.0.113.5")) // hit

		s := c.Stats()
		if s.Hits != 1 {
			t.Errorf("Hits=%d want 1", s.Hits)
		}
		if s.Misses != 1 {
			t.Errorf("Misses=%d want 1", s.Misses)
		}
		if s.QuerySuccess != 1 {
			t.Errorf("QuerySuccess=%d want 1", s.QuerySuccess)
		}
		if s.Size != 1 {
			t.Errorf("Size=%d want 1", s.Size)
		}
		if s.Capacity != 10 {
			t.Errorf("Capacity=%d want 10", s.Capacity)
		}
	})
}

func TestStats_NegativeCacheCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			NegativeTTL:    time.Minute,
			ReportInterval: testNoTick,
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				return nil, context.DeadlineExceeded
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.9")) // miss → schedule (fails)
		synctest.Wait()
		c.LookupName(addr("203.0.113.9")) // negative-cached

		s := c.Stats()
		if s.Negatives != 1 {
			t.Errorf("Negatives=%d want 1", s.Negatives)
		}
		if s.QueryFail != 1 {
			t.Errorf("QueryFail=%d want 1", s.QueryFail)
		}
	})
}

func TestSweep_RemovesExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{TTL: time.Hour, ReportInterval: testNoTick, Lookup: okLookup("h.")})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		if c.Stats().Size != 1 {
			t.Fatal("want 1 entry before expiry")
		}

		time.Sleep(time.Hour + time.Minute) // expire the entry
		c.sweep()

		s := c.Stats()
		if s.Size != 0 {
			t.Errorf("Size after sweep=%d want 0", s.Size)
		}
		if s.EvictedExpired != 1 {
			t.Errorf("EvictedExpired=%d want 1", s.EvictedExpired)
		}
	})
}

func TestOverflow_CountedAtCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{MaxEntries: 1, ReportInterval: testNoTick, Lookup: okLookup("h.")})
		defer c.Close()

		c.LookupName(addr("203.0.113.1")) // fills the single slot
		synctest.Wait()
		c.LookupName(addr("203.0.113.2")) // distinct addr, no slot → overflow

		if got := c.Stats().Overflows; got != 1 {
			t.Errorf("Overflows=%d want 1", got)
		}
	})
}

func TestPurge_ClearsAndCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{ReportInterval: testNoTick, Lookup: okLookup("h.")})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		if c.Stats().Size != 1 {
			t.Fatal("want 1 before purge")
		}

		if n := c.Purge(); n != 1 {
			t.Errorf("Purge returned %d want 1", n)
		}

		s := c.Stats()
		if s.Size != 0 {
			t.Errorf("Size=%d want 0 after purge", s.Size)
		}
		if s.EvictedPurged != 1 {
			t.Errorf("EvictedPurged=%d want 1", s.EvictedPurged)
		}
		if s.LastPurge.IsZero() {
			t.Error("LastPurge should be set after a purge")
		}
	})
}

func TestReport_EmitsMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		c := New(Options{
			MaxEntries:     50,
			ReportInterval: testNoTick,
			Emitter:        rec.Emitter(),
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5")) // miss + schedule
		synctest.Wait()                   // querySuccess; entry stored
		c.LookupName(addr("203.0.113.5")) // hit
		c.report()

		assertCounter(t, rec, MetricLookups, "result", "hit", 1)
		assertCounter(t, rec, MetricLookups, "result", "miss", 1)
		assertCounter(t, rec, MetricQueries, "result", "success", 1)
		assertGauge(t, rec, MetricEntries, 1)
		assertGauge(t, rec, MetricCapacity, 50)

		// No new activity: a second report must not add to the counter (delta=0).
		c.report()
		assertCounter(t, rec, MetricLookups, "result", "hit", 1)
	})
}

func assertCounter(t *testing.T, rec *telemetrytest.Recorder, name, key, val string, want float64) {
	t.Helper()
	for _, p := range rec.MetricPoints(name) {
		if p.Attrs[key] == val {
			if p.Value != want {
				t.Errorf("%s{%s=%s}=%v want %v", name, key, val, p.Value, want)
			}
			return
		}
	}
	t.Errorf("no metric point %s{%s=%s}", name, key, val)
}

func assertGauge(t *testing.T, rec *telemetrytest.Recorder, name string, want float64) {
	t.Helper()
	pts := rec.MetricPoints(name)
	if len(pts) == 0 {
		t.Errorf("no metric point %s", name)
		return
	}
	if pts[0].Value != want {
		t.Errorf("%s=%v want %v", name, pts[0].Value, want)
	}
}
