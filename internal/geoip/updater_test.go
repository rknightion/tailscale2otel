package geoip

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// fetchFunc adapts a closure to the Fetcher interface the updater depends on, so
// the schedule can be tested without an HTTP server.
type fetchFunc func(context.Context, string) (DownloadResult, error)

func (f fetchFunc) Fetch(ctx context.Context, edition string) (DownloadResult, error) {
	return f(ctx, edition)
}

func TestUpdater_DownloadsOnStartThenOnInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		db := mustOpen(t, Options{})
		u := NewUpdater(UpdaterOptions{
			DB: db,
			Fetcher: fetchFunc(func(context.Context, string) (DownloadResult, error) {
				calls.Add(1)
				return DownloadResult{Updated: true}, nil
			}),
			Editions:         []string{"GeoLite2-Country", "GeoLite2-ASN"},
			DownloadInterval: 24 * time.Hour,
			ReloadInterval:   6 * time.Hour,
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go u.Run(ctx)

		// An update runs immediately at start: waiting a whole interval before
		// the first fetch would leave a fresh container with no database for a
		// day.
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("fetches after start = %d, want 2 (one per edition)", got)
		}

		// Nothing more until the download interval elapses.
		time.Sleep(23 * time.Hour)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("fetches after 23h = %d, want 2", got)
		}
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if got := calls.Load(); got != 4 {
			t.Fatalf("fetches after 25h = %d, want 4", got)
		}
	})
}

// The reload tick has to run on its own schedule even with no downloader
// configured: that is the whole operator-managed path, where a geoipupdate cron
// or a sidecar rewrites the file and this process is expected to notice.
func TestUpdater_ReloadsWithoutADownloader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	db := mustOpen(t, Options{CountryPath: path})

	synctest.Test(t, func(t *testing.T) {
		u := NewUpdater(UpdaterOptions{DB: db, ReloadInterval: time.Hour})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go u.Run(ctx)
		synctest.Wait()

		if !db.Empty() {
			t.Fatal("database loaded before the file appeared")
		}
		copyFile(t, countryDB, path)

		time.Sleep(time.Hour)
		synctest.Wait()
		if got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok || got.CountryISO != "GB" {
			t.Fatalf("after the reload tick, Lookup = %+v, ok = %v; want GB", got, ok)
		}
	})
}

// A failing download must not stop the loop, must not take the process down, and
// must leave any already-loaded database serving. Fail-open is the contract.
func TestUpdater_FetchFailureIsSurvivable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	copyFile(t, countryDB, path)
	db := mustOpen(t, Options{CountryPath: path})

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		u := NewUpdater(UpdaterOptions{
			DB: db,
			Fetcher: fetchFunc(func(context.Context, string) (DownloadResult, error) {
				calls.Add(1)
				return DownloadResult{}, errors.New("HTTP 401 Unauthorized")
			}),
			Editions:         []string{"GeoLite2-Country"},
			DownloadInterval: time.Hour,
			ReloadInterval:   time.Hour,
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go u.Run(ctx)
		synctest.Wait()

		// It keeps trying on the next tick rather than latching off.
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := calls.Load(); got < 2 {
			t.Errorf("fetches = %d, want the loop to keep retrying", got)
		}
		if got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok || got.CountryISO != "GB" {
			t.Fatalf("Lookup = %+v, ok = %v; a failed download disturbed the loaded database", got, ok)
		}
	})
}

// Run must return promptly when its context is canceled, or shutdown hangs.
func TestUpdater_StopsOnContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := mustOpen(t, Options{})
		u := NewUpdater(UpdaterOptions{DB: db, ReloadInterval: time.Hour})
		ctx, cancel := context.WithCancel(context.Background())

		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); u.Run(ctx) }()
		synctest.Wait()
		cancel()
		wg.Wait()
	})
}

// A zero interval disables that half of the loop rather than panicking
// (time.NewTicker(0) panics) or busy-looping.
func TestUpdater_ZeroIntervalsDisable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		db := mustOpen(t, Options{})
		u := NewUpdater(UpdaterOptions{
			DB:       db,
			Fetcher:  fetchFunc(func(context.Context, string) (DownloadResult, error) { calls.Add(1); return DownloadResult{}, nil }),
			Editions: []string{"GeoLite2-Country"},
			// Both intervals zero: the initial download still runs, then the
			// loop simply waits for cancellation.
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go u.Run(ctx)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("fetches = %d, want 1 initial download and no scheduled repeats", got)
		}
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("fetches after 48h with a zero interval = %d, want 1", got)
		}
	})
}

func TestReport_EmitsMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	copyFile(t, countryDB, path)
	db := mustOpen(t, Options{CountryPath: path, ASNPath: asnDB})

	db.Lookup(netip.MustParseAddr("81.2.69.142")) // country hit, asn miss
	db.Lookup(netip.MustParseAddr("1.0.0.1"))     // country miss, asn hit
	db.Lookup(netip.MustParseAddr("10.0.0.1"))    // skipped

	rec := telemetrytest.New()
	db.Report(rec.Emitter())

	want := map[string]map[string]float64{
		MetricLookups: {
			"country/hit":     1,
			"country/miss":    1,
			"asn/hit":         1,
			"asn/miss":        1,
			"skipped/skipped": 1,
		},
	}
	got := map[string]map[string]float64{MetricLookups: {}}
	for _, p := range rec.MetricPoints(MetricLookups) {
		key := p.Attrs[attrDatabase] + "/" + p.Attrs[attrResult]
		got[MetricLookups][key] += p.Value
	}
	for k, v := range want[MetricLookups] {
		if got[MetricLookups][k] != v {
			t.Errorf("%s{%s} = %v, want %v", MetricLookups, k, got[MetricLookups][k], v)
		}
	}

	// The build-time gauge is what an operator alerts a stale database on, so it
	// has to carry a real epoch, not a zero.
	for _, database := range []string{databaseCountry, databaseASN} {
		var seen bool
		for _, p := range rec.MetricPoints(MetricBuildTime) {
			if p.Attrs[attrDatabase] == database {
				seen = true
				if p.Value <= 0 {
					t.Errorf("%s{%s} = %v, want a unix build epoch", MetricBuildTime, database, p.Value)
				}
			}
		}
		if !seen {
			t.Errorf("%s has no point for database=%s", MetricBuildTime, database)
		}
	}

	// A second report emits only the delta, so counters do not double-count what
	// the previous flush already sent.
	rec2 := telemetrytest.New()
	db.Report(rec2.Emitter())
	if pts := rec2.MetricPoints(MetricLookups); len(pts) != 0 {
		t.Errorf("second Report emitted %d lookup points, want 0 (no new activity)", len(pts))
	}
}

// Every metric the package emits must be declared in its catalog, or it lands in
// telemetry without ever reaching docs/metrics.md.
func TestCatalogMatchesEmission(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: countryDB})
	db.Lookup(netip.MustParseAddr("81.2.69.142"))
	if _, err := db.Reload(); err != nil {
		t.Fatal(err)
	}
	rec := telemetrytest.New()
	db.Report(rec.Emitter())
	db.ObserveDownload(rec.Emitter(), "GeoLite2-Country", DownloadResult{Updated: true}, nil)

	declared := map[string]bool{}
	for _, m := range Catalog() {
		declared[m.Name] = true
	}
	for _, name := range rec.MetricNames() {
		if !declared[name] {
			t.Errorf("emitted metric %q is not declared in Catalog()", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("Catalog() is empty")
	}
	// And every attribute actually emitted is declared, so docs/metrics.md
	// cannot silently omit a dimension.
	telemetrytest.AssertCatalogAttrs(t, rec, Catalog(), nil)
}
