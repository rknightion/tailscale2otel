package rdns

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// Cache must satisfy the narrow Resolver interface the flow processor depends on.
var _ Resolver = (*Cache)(nil)

func TestLookupName_AsyncPopulation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := New(Options{
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				calls.Add(1)
				return []string{"host.example.com."}, nil
			},
		})
		defer c.Close()

		// First sighting: miss, kicks off the async lookup, returns immediately.
		if name, ok := c.LookupName(addr("203.0.113.5")); ok || name != "" {
			t.Fatalf("first LookupName = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait() // let the background lookup finish

		// Second sighting: cached, with the trailing dot trimmed.
		if name, ok := c.LookupName(addr("203.0.113.5")); !ok || name != "host.example.com" {
			t.Fatalf("second LookupName = (%q,%v), want (host.example.com,true)", name, ok)
		}
		if calls.Load() != 1 {
			t.Fatalf("lookup called %d times, want 1 (result cached)", calls.Load())
		}
	})
}

func TestLookupName_NegativeCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := New(Options{
			NegativeTTL: time.Minute,
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				calls.Add(1)
				return nil, errors.New("no PTR")
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.9"))
		synctest.Wait()
		if name, ok := c.LookupName(addr("203.0.113.9")); ok || name != "" {
			t.Fatalf("after failed lookup = (%q,%v), want (\"\",false)", name, ok)
		}
		// The negative entry suppresses repeat lookups within negTTL.
		c.LookupName(addr("203.0.113.9"))
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("lookup called %d times, want 1 (negative cached)", calls.Load())
		}
	})
}

func TestLookupName_TTLExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := New(Options{
			TTL: time.Hour,
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				calls.Add(1)
				return []string{"a.example.com."}, nil
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.7"))
		synctest.Wait()
		if _, ok := c.LookupName(addr("203.0.113.7")); !ok {
			t.Fatal("want cached hit before TTL")
		}
		if calls.Load() != 1 {
			t.Fatalf("calls = %d, want 1 before expiry", calls.Load())
		}

		// Advance past the TTL; the next sighting re-resolves.
		time.Sleep(time.Hour + time.Minute)
		if _, ok := c.LookupName(addr("203.0.113.7")); ok {
			t.Fatal("expired entry should miss and re-trigger")
		}
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("calls = %d, want 2 after TTL expiry", calls.Load())
		}
	})
}

func TestLookupName_MaxEntriesBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := New(Options{
			MaxEntries: 1,
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				calls.Add(1)
				return []string{"x.example.com."}, nil
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.1")) // fills the single slot
		synctest.Wait()
		// A second distinct address has no slot and must not trigger a lookup.
		if name, ok := c.LookupName(addr("203.0.113.2")); ok || name != "" {
			t.Fatalf("over-cap addr = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("calls = %d, want 1 (second addr exceeds max_entries)", calls.Load())
		}
	})
}

func TestNormalizeServer(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"10.0.0.53", "10.0.0.53:53"},
		{"10.0.0.53:5353", "10.0.0.53:5353"},
		{"2001:db8::1", "[2001:db8::1]:53"},
	}
	for _, c := range cases {
		if got := normalizeServer(c.in); got != c.want {
			t.Errorf("normalizeServer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
