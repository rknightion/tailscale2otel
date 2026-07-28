package geoip

import (
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	countryDB = "testdata/GeoIP2-Country-Test.mmdb"
	asnDB     = "testdata/GeoLite2-ASN-Test.mmdb"
)

func mustOpen(t *testing.T, opts Options) *DB {
	t.Helper()
	db, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestLookup_CountryAndASN(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: countryDB, ASNPath: asnDB})

	cases := []struct {
		addr string
		want Result
	}{
		// Country DB only.
		{"81.2.69.142", Result{CountryISO: "GB", ContinentCode: "EU"}},
		// The one fixture address present in BOTH databases: the two lookups
		// have to compose into a single Result, not shadow each other.
		{"89.160.20.128", Result{CountryISO: "SE", ContinentCode: "EU", ASN: 29518, ASOrg: "Bredband2 AB"}},
		{"2001:218::1", Result{CountryISO: "JP", ContinentCode: "AS"}},
		// ASN DB only: no country record for 1.0.0.1 in the country fixture.
		{"1.0.0.1", Result{ASN: 15169, ASOrg: "Google Inc."}},
		// An ASN record carrying a number but no organization name.
		{"12.81.96.1", Result{ASN: 7018}},
		// A country record with a continent but no country object at all.
		{"2a02:d500::1", Result{ContinentCode: "EU"}},
	}
	for _, c := range cases {
		got, ok := db.Lookup(netip.MustParseAddr(c.addr))
		if !ok {
			t.Errorf("Lookup(%s) ok = false, want true", c.addr)
			continue
		}
		if got != c.want {
			t.Errorf("Lookup(%s) = %+v, want %+v", c.addr, got, c.want)
		}
	}
}

// An address present in NEITHER database is a clean miss, not an empty hit: the
// caller must be able to tell "we looked and found nothing" from "we found a
// record whose fields happened to be blank", because only the former should be
// counted as a miss.
func TestLookup_NoRecord(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: countryDB, ASNPath: asnDB})
	if got, ok := db.Lookup(netip.MustParseAddr("4.4.4.4")); ok {
		t.Fatalf("Lookup(4.4.4.4) = %+v, ok = true; want a miss", got)
	}
}

// The country fallback is the single most consequential decode decision here:
// GeoLite2-Country genuinely omits the `country` object for a large share of the
// internet (1.1.1.1 among them, verified against the real database) and carries
// only `registered_country`. Without the fallback those addresses read as
// unknown and the feature looks broken. The vendored test fixture happens to
// have no such record, so the fallback is asserted at the decode function --
// which is exactly the logic under test.
func TestCountryFrom_RegisteredFallback(t *testing.T) {
	var rec countryRecord
	rec.Continent.Code = "OC"
	rec.RegisteredCountry.ISOCode = "AU"
	iso, continent := countryFrom(rec)
	if iso != "AU" {
		t.Errorf("iso = %q, want AU (registered_country fallback)", iso)
	}
	if continent != "OC" {
		t.Errorf("continent = %q, want OC", continent)
	}

	// A present country object always wins over registered_country.
	rec.Country.ISOCode = "NZ"
	if iso, _ := countryFrom(rec); iso != "NZ" {
		t.Errorf("iso = %q, want NZ (country wins over registered_country)", iso)
	}

	// represented_country is the last resort (military bases and similar).
	var only countryRecord
	only.RepresentedCountry.ISOCode = "US"
	if iso, _ := countryFrom(only); iso != "US" {
		t.Errorf("iso = %q, want US (represented_country fallback)", iso)
	}
}

// Non-global addresses never reach the databases. The MMDBs have no records for
// them anyway, but relying on that would make the contract depend on MaxMind's
// data rather than on our code -- and the tailnet ranges in particular must be
// skipped by construction, not by luck.
func TestLookup_SkipsNonGlobal(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: countryDB, ASNPath: asnDB})
	for _, s := range []string{
		"100.64.0.1",         // Tailscale CGNAT (RFC 6598)
		"fd7a:115c:a1e0::1",  // Tailscale ULA
		"192.168.1.1",        // RFC 1918
		"10.0.0.1",           //
		"172.16.0.1",         //
		"127.0.0.1",          // loopback
		"::1",                //
		"169.254.1.1",        // link-local
		"fe80::1",            //
		"224.0.0.1",          // multicast
		"0.0.0.0",            // unspecified
		"fc00::1",            // unique-local v6
		"::ffff:192.168.1.1", // v4-mapped private
	} {
		if got, ok := db.Lookup(netip.MustParseAddr(s)); ok {
			t.Errorf("Lookup(%s) = %+v, ok = true; want skipped", s, got)
		}
	}
	if got := db.Stats().Skipped; got != 13 {
		t.Errorf("Stats().Skipped = %d, want 13", got)
	}
}

// A globally routable address is not skipped -- the guard above must not be so
// broad that it swallows the addresses the feature exists for.
func TestLookup_GlobalNotSkipped(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: countryDB, ASNPath: asnDB})
	if _, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok {
		t.Fatal("81.2.69.142 was skipped or missed; want a hit")
	}
	if got := db.Stats().Skipped; got != 0 {
		t.Errorf("Stats().Skipped = %d, want 0", got)
	}
}

// Either database may be absent; the other still enriches. Configuring neither
// is a usable (if pointless) zero state rather than an error, so the app can
// build the DB before it knows whether the download has landed.
func TestOpen_PartialAndEmpty(t *testing.T) {
	t.Run("country only", func(t *testing.T) {
		db := mustOpen(t, Options{CountryPath: countryDB})
		got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142"))
		if !ok || got.CountryISO != "GB" || got.ASN != 0 {
			t.Fatalf("Lookup = %+v, ok = %v; want GB with no ASN", got, ok)
		}
	})
	t.Run("asn only", func(t *testing.T) {
		db := mustOpen(t, Options{ASNPath: asnDB})
		got, ok := db.Lookup(netip.MustParseAddr("1.0.0.1"))
		if !ok || got.ASN != 15169 || got.CountryISO != "" {
			t.Fatalf("Lookup = %+v, ok = %v; want AS15169 with no country", got, ok)
		}
	})
	t.Run("neither", func(t *testing.T) {
		db := mustOpen(t, Options{})
		if _, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); ok {
			t.Fatal("Lookup with no databases returned a hit")
		}
		if !db.Empty() {
			t.Error("Empty() = false, want true with no databases loaded")
		}
	})
}

// A configured path that does not exist must not fail Open: the download may not
// have landed yet on a cold start, and refusing to start would turn an
// enrichment nicety into an outage. The path is remembered so a later Reload
// picks the file up.
func TestOpen_MissingFileIsNotFatal(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: filepath.Join(t.TempDir(), "absent.mmdb")})
	if !db.Empty() {
		t.Error("Empty() = false, want true when the configured file is absent")
	}
	if _, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); ok {
		t.Error("Lookup returned a hit with no database loaded")
	}
}

// A file that exists but is not a MaxMind database is an Open error, unlike an
// absent one: the operator pointed at something real and wrong, and silently
// treating it as "not downloaded yet" would hide the mistake forever.
func TestOpen_CorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.mmdb")
	if err := os.WriteFile(path, []byte("definitely not an mmdb"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(Options{CountryPath: path, ASNPath: asnDB})
	if err == nil {
		t.Fatal("Open with a corrupt database returned nil error")
	}
	// ...but the DB is still usable, and the database that DID load keeps
	// serving. Startup must not fail over an enrichment nicety, and the good
	// half must not be taken down by the bad one.
	if db == nil {
		t.Fatal("Open returned a nil DB alongside its error")
	}
	t.Cleanup(db.Close)
	if got, ok := db.Lookup(netip.MustParseAddr("1.0.0.1")); !ok || got.ASN != 15169 {
		t.Fatalf("Lookup = %+v, ok = %v; want the ASN database still serving", got, ok)
	}
	// And the corrupt path is remembered, so replacing the file recovers without
	// a restart.
	copyFile(t, countryDB, path)
	if changed, err := db.Reload(); err != nil || !changed {
		t.Fatalf("Reload after replacing the corrupt file: changed = %v, err = %v", changed, err)
	}
	if got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok || got.CountryISO != "GB" {
		t.Fatalf("Lookup = %+v, ok = %v; want GB after the corrupt file was replaced", got, ok)
	}
}

func TestReload_PicksUpANewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	db := mustOpen(t, Options{CountryPath: path})
	if !db.Empty() {
		t.Fatal("Empty() = false before the file exists")
	}

	copyFile(t, countryDB, path)
	changed, err := db.Reload()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !changed {
		t.Fatal("Reload reported no change after the file appeared")
	}
	if got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok || got.CountryISO != "GB" {
		t.Fatalf("after Reload, Lookup = %+v, ok = %v; want GB", got, ok)
	}

	// An unchanged file must not be reopened: Reload runs on a timer, and
	// remapping an untouched 30 MB database every tick is pure waste.
	changed, err = db.Reload()
	if err != nil {
		t.Fatalf("second Reload: %v", err)
	}
	if changed {
		t.Fatal("Reload reported a change for an untouched file")
	}
}

// Reloading a file that has been replaced with garbage must leave the working
// database in place. Fail-open is the whole contract: a bad update degrades
// freshness, never availability.
func TestReload_KeepsWorkingDBOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	copyFile(t, countryDB, path)
	db := mustOpen(t, Options{CountryPath: path})

	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Reload(); err == nil {
		t.Fatal("Reload over a corrupt file returned nil error")
	}
	if got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok || got.CountryISO != "GB" {
		t.Fatalf("after a failed Reload, Lookup = %+v, ok = %v; want the previous database still serving GB", got, ok)
	}
}

// The reader swap is the one place in this package that can crash the process
// rather than merely return a wrong answer: maxminddb's Close unmaps the file,
// so closing a swapped-out reader while a lookup is still walking it is a
// segfault -- a rare production crash that no ordinary test would catch. Run
// under -race (CI always does).
func TestReloadConcurrentWithLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "country.mmdb")
	copyFile(t, countryDB, path)
	db := mustOpen(t, Options{CountryPath: path})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := netip.MustParseAddr("81.2.69.142")
			for {
				select {
				case <-stop:
					return
				default:
					db.Lookup(addr)
				}
			}
		}()
	}
	for range 50 {
		// Rewrite the file so each Reload actually swaps a reader in.
		copyFile(t, countryDB, path)
		bumpMtime(t, path)
		if _, err := db.Reload(); err != nil {
			t.Errorf("Reload: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestStats_CountsResults(t *testing.T) {
	db := mustOpen(t, Options{CountryPath: countryDB, ASNPath: asnDB})
	db.Lookup(netip.MustParseAddr("81.2.69.142")) // country hit, asn miss
	db.Lookup(netip.MustParseAddr("1.0.0.1"))     // country miss, asn hit
	db.Lookup(netip.MustParseAddr("4.4.4.4"))     // both miss
	db.Lookup(netip.MustParseAddr("10.0.0.1"))    // skipped

	s := db.Stats()
	if s.CountryHits != 1 || s.CountryMisses != 2 {
		t.Errorf("country hits/misses = %d/%d, want 1/2", s.CountryHits, s.CountryMisses)
	}
	if s.ASNHits != 1 || s.ASNMisses != 2 {
		t.Errorf("asn hits/misses = %d/%d, want 1/2", s.ASNHits, s.ASNMisses)
	}
	if s.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", s.Skipped)
	}
	if s.CountryBuildTime.IsZero() || s.ASNBuildTime.IsZero() {
		t.Error("build times are zero; want the databases' build epochs")
	}
	if s.CountryType != "GeoIP2-Country" || s.ASNType != "GeoLite2-ASN" {
		t.Errorf("database types = %q/%q, want GeoIP2-Country/GeoLite2-ASN", s.CountryType, s.ASNType)
	}
}

// A nil *DB is the "geoip disabled" value the app hands to every consumer, so
// every method has to tolerate it -- otherwise each call site needs its own nil
// check and one of them will eventually be missed.
func TestNilDB(t *testing.T) {
	var db *DB
	if _, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); ok {
		t.Error("nil DB returned a hit")
	}
	if !db.Empty() {
		t.Error("nil DB Empty() = false")
	}
	if _, err := db.Reload(); err != nil {
		t.Errorf("nil DB Reload: %v", err)
	}
	db.Stats()
	db.Close()
}

// bumpMtime moves a file's modification time forward by a whole second.
// Reload's change detection is (mtime, size), and rewriting the same bytes can
// land inside the filesystem's timestamp granularity -- which would make the
// concurrency test above quietly stop swapping readers and stop testing
// anything. Forcing a distinct mtime keeps every iteration a real swap.
func bumpMtime(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	next := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
