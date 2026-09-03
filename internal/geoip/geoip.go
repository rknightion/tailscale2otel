// Package geoip provides optional, purely LOCAL geolocation and autonomous-system
// enrichment of external (non-Tailscale) IP addresses, backed by MaxMind DB
// (.mmdb) files on disk.
//
// The hot path never touches the network. A lookup is a radix-tree walk costing
// well under a microsecond; decoding into the fixed structs below allocates
// nothing. That is why this package — deliberately, unlike its sibling
// internal/rdns — has NO result cache. rdns caches because a PTR lookup is a
// network round trip that must never block flow processing; here there is
// nothing slow to hide, and an LRU in front would add locking, memory and a
// whole TTL surface for no gain. Do not add one.
//
// # Databases are read into memory, NOT memory-mapped
//
// maxminddb.Open mmaps the file, which is faster to load and cheaper in RSS
// (the pages are evictable page cache). It is also a way to crash the process:
// if anything TRUNCATES the file while a lookup is walking the mapping — a
// plain `curl -o db.mmdb`, an editor, a rsync without --inplace — every
// in-flight read faults with SIGBUS and takes the whole exporter down. That is
// not hypothetical; it is what the first version of this package did, and the
// reload test reproduced it immediately.
//
// So Open reads each file fully into the heap and uses maxminddb.OpenBytes. The
// cost is real and worth stating: roughly 9 MB resident for GeoLite2-Country and
// 12 MB for GeoLite2-ASN, held for the process lifetime, and a City database is
// several times larger again. That is the price of a feature that is off by
// default and must never be able to kill a running exporter. Do not "optimize"
// this back to mmap.
//
// Databases are swapped atomically by Reload (see updater.go for the schedule),
// so an operator's geoipupdate cron — or this package's own MaxMind downloader
// (maxmind.go) — can rewrite the files under a running process.
//
// Everything here is fail-open. A missing, unreadable or stale database means
// the geo attributes are simply absent from the emitted telemetry; it is never a
// startup failure and never blocks a lookup.
package geoip

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// Lookup is the narrow, fakeable interface consumers depend on. A nil *DB
// satisfies it and reports a miss for every address, which is the value the app
// hands out when enrichment.geoip is disabled.
type Lookup interface {
	Lookup(addr netip.Addr) (Result, bool)
}

// Result is everything this package reports about an address.
//
// The fields split into two very different cardinality classes, and callers must
// respect the split:
//
//   - CountryISO and ContinentCode are BOUNDED (~250 and 7 values). They are the
//     only fields that may go on a metric, and only when the operator opts in
//     via cardinality.flow.geo_dims.
//   - ASN, ASOrg, City, RegionISO and the coordinates are NOT usefully bounded.
//     They are confined to LOG records, which are not series and carry the
//     full-fidelity view by design.
//
// The city fields require a City database (GeoLite2-City / GeoIP2-City). A
// Country database is a strict subset and simply leaves them empty, so the same
// configuration key accepts either.
type Result struct {
	// CountryISO is the ISO 3166-1 alpha-2 code, e.g. "US". Empty when unknown.
	CountryISO string
	// ContinentCode is the two-letter continent code, e.g. "NA". Empty when unknown.
	ContinentCode string
	// City is the English city name, e.g. "London". City database only.
	City string
	// RegionISO is the ISO 3166-2 subdivision code of the most specific
	// subdivision, e.g. "ENG" for England or "CA" for California. City database
	// only. MaxMind orders subdivisions least- to most-specific, so this is the
	// LAST entry, not the first.
	RegionISO string
	// Latitude and Longitude are the WGS84 coordinates of the record's location.
	// City database only, and meaningful only when HasLocation is set: (0, 0) is
	// a real point in the Gulf of Guinea, so a zero pair cannot be read as
	// "unknown".
	Latitude, Longitude float64
	// HasLocation reports whether Latitude/Longitude were present in the record.
	HasLocation bool
	// ASN is the autonomous-system number, e.g. 15169. Zero when unknown.
	ASN uint
	// ASOrg is the autonomous-system organization, e.g. "Google LLC". Empty when
	// unknown — the ASN database genuinely carries records with a number and no
	// organization name.
	ASOrg string
}

// Empty reports whether the result carries no usable fact at all.
func (r Result) Empty() bool { return r == Result{} }

// countryRecord is the subset of a GeoLite2/GeoIP2 Country (or City) record this
// package reads.
//
// The three country objects are not redundant. `country` is where the address is
// physically located and is the field to prefer — but MaxMind genuinely omits it
// for a large slice of the internet, carrying only `registered_country` (the
// country of the registering ISP). 1.1.1.1 is the canonical example, verified
// against the live database: no `country` object at all. Without the fallback in
// countryFrom, every Cloudflare-fronted address reads as unknown.
type countryRecord struct {
	Continent struct {
		Code string `maxminddb:"code"`
	} `maxminddb:"continent"`
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	RegisteredCountry struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"registered_country"`
	RepresentedCountry struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"represented_country"`

	// City-database-only fields. A Country database omits them entirely and they
	// decode to their zero values, which is exactly the intended behavior: the
	// same configured path accepts either edition.
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Subdivisions []struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"subdivisions"`
	Location struct {
		Latitude  float64 `maxminddb:"latitude"`
		Longitude float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

// asnRecord is the whole of a GeoLite2-ASN record.
type asnRecord struct {
	Number uint   `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

// countryFrom picks the best country code available in a record, preferring the
// physical location over the registering ISP's country over the represented
// country (embassies, military bases). See the countryRecord doc for why the
// fallback chain is load-bearing rather than defensive.
func countryFrom(rec countryRecord) (iso, continent string) {
	iso = rec.Country.ISOCode
	if iso == "" {
		iso = rec.RegisteredCountry.ISOCode
	}
	if iso == "" {
		iso = rec.RepresentedCountry.ISOCode
	}
	return iso, rec.Continent.Code
}

// cityFrom extracts the City-database-only fields, all of which are empty when a
// Country database is loaded instead.
//
// Only the English name is read. MaxMind ships eight localisations of every
// place name, and mixing them would split what should be one value ("London" vs
// "Londres") across a log corpus for no benefit.
func cityFrom(rec countryRecord) (city, regionISO string, lat, lon float64, hasLocation bool) {
	city = rec.City.Names["en"]
	// Subdivisions run least- to most-specific ("England" then a county), so the
	// LAST entry is the one worth carrying. Taking the first would report the
	// coarsest division under a name that says region.
	if n := len(rec.Subdivisions); n > 0 {
		regionISO = rec.Subdivisions[n-1].ISOCode
	}
	// A record with no location object decodes to (0, 0) — which is a real point
	// in the Gulf of Guinea, not a sentinel. Presence is inferred from the city
	// record having any location-bearing content at all, so a genuine (0, 0) is
	// never confused with an absent one.
	if rec.Location.Latitude != 0 || rec.Location.Longitude != 0 {
		lat, lon, hasLocation = rec.Location.Latitude, rec.Location.Longitude, true
	}
	return city, regionISO, lat, lon, hasLocation
}

// Options configures a DB. Both paths are optional and independent: an operator
// may ship only a Country database, only an ASN database, or neither (the app
// builds the DB before the first download has necessarily landed).
type Options struct {
	CountryPath string
	ASNPath     string
}

// reader pairs an open maxminddb reader with the (mtime, size) it was opened at,
// so Reload can skip an untouched file without re-reading it.
type reader struct {
	db    *maxminddb.Reader
	mtime time.Time
	size  int64
}

// maxDatabaseBytes caps how large a file this package will load. It is not a
// security boundary — the operator names the path — but it turns "pointed at the
// wrong file" and "the download wrote a 40 GB sparse file" into a clear error
// instead of an OOM kill. 512 MiB is far above any real MaxMind database
// (GeoIP2-City, the largest MaxMind publishes, is well under 200 MB).
const maxDatabaseBytes = 512 << 20

// stats holds the cumulative counters surfaced by Stats and flushed as OTEL
// counter deltas by report(). Guarded by DB.mu.
type stats struct {
	countryHits, countryMisses int64
	asnHits, asnMisses         int64
	skipped                    int64
	countryReloads, asnReloads int64
	reloadFailures             int64
}

// Stats is an absolute snapshot for the admin status page. report() emits the
// same counters as OTEL metrics.
type Stats struct {
	CountryHits, CountryMisses int64
	ASNHits, ASNMisses         int64
	Skipped                    int64
	CountryReloads, ASNReloads int64
	ReloadFailures             int64

	// Loaded database identity, for the status page and the build-time gauge. A
	// zero build time means the database is not loaded.
	CountryPath, ASNPath           string
	CountryType, ASNType           string
	CountryBuildTime, ASNBuildTime time.Time
}

// DB holds the loaded databases and swaps them atomically on Reload.
//
// The lock is a plain RWMutex rather than an atomic pointer pair. A lookup
// consults both databases and must see a consistent pair; the counters need
// guarding anyway; and an uncontended RLock is a couple of atomics, negligible
// beside the tree walk it guards. Correctness beats the nanosecond.
type DB struct {
	countryPath string
	asnPath     string

	mu      sync.RWMutex
	country *reader
	asn     *reader
	stats   stats

	// reported is report()'s delta baseline; single-writer (the updater's
	// goroutine), guarded by mu like everything else.
	reported stats
}

// Open builds a DB from the configured paths. A path that does not exist yet is
// remembered and left unloaded — a cold start whose scheduled download has not
// landed must not fail. A path that exists but is not a usable MaxMind database
// IS reported as an error: the operator pointed at something real and wrong, and
// treating that as "not downloaded yet" would hide the mistake forever.
//
// The returned *DB is ALWAYS usable, even alongside a non-nil error. Whatever
// loaded is serving, whatever did not is simply absent, and the configured paths
// are retained either way so a later Reload can pick up a corrected file. A
// caller that wants to be strict can still treat the error as fatal; the app
// deliberately does not, because degraded enrichment must not become a failed
// startup.
func Open(opts Options) (*DB, error) {
	db := &DB{countryPath: opts.CountryPath, asnPath: opts.ASNPath}
	var errs []error
	var err error
	if db.country, err = openIfPresent(opts.CountryPath); err != nil {
		errs = append(errs, err)
	}
	if db.asn, err = openIfPresent(opts.ASNPath); err != nil {
		errs = append(errs, err)
	}
	return db, errors.Join(errs...)
}

// openIfPresent loads path, returning (nil, nil) when path is empty or absent.
func openIfPresent(path string) (*reader, error) {
	if path == "" {
		return nil, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat geoip database %s: %w", path, err)
	}
	return openAt(path, fi)
}

// openAt loads the database at path, whose already-taken stat result is fi (the
// callers all need it anyway, for the change check). See the package doc for why
// this reads the whole file rather than mapping it.
func openAt(path string, fi os.FileInfo) (*reader, error) {
	if fi.Size() > maxDatabaseBytes {
		return nil, fmt.Errorf("geoip database %s is %d bytes, above the %d-byte limit", path, fi.Size(), int64(maxDatabaseBytes))
	}
	b, err := safefile.ReadRegular(path, safefile.MaxDatabaseBytes, safefile.AllowSymlink)
	if err != nil {
		return nil, fmt.Errorf("read geoip database %s: %w", path, err)
	}
	r, err := maxminddb.OpenBytes(b)
	if err != nil {
		return nil, fmt.Errorf("open geoip database %s: %w", path, err)
	}
	return &reader{db: r, mtime: fi.ModTime(), size: fi.Size()}, nil
}

// Empty reports whether no database at all is loaded, so callers can skip work
// (and the app can log a startup notice) without probing with a lookup.
func (d *DB) Empty() bool {
	if d == nil {
		return true
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.country == nil && d.asn == nil
}

// Lookup returns what the loaded databases know about addr. The boolean reports
// whether any record was found — distinct from a found record whose fields are
// blank, so the caller can count a genuine miss.
//
// Addresses that are not globally routable (loopback, RFC 1918, link-local,
// multicast, and in particular the Tailscale CGNAT range and ULA) are skipped
// without consulting the databases. The MMDBs happen to hold no records for
// them, but making the contract depend on MaxMind's data rather than on our own
// code is exactly the kind of accident that turns into a tailnet-address leak
// when the data changes.
func (d *DB) Lookup(addr netip.Addr) (Result, bool) {
	if d == nil {
		return Result{}, false
	}
	if !Enrichable(addr) {
		d.mu.Lock()
		d.stats.skipped++
		d.mu.Unlock()
		return Result{}, false
	}

	d.mu.RLock()
	var (
		res   Result
		found bool
		crec  countryRecord
		arec  asnRecord
		chit  bool
		ahit  bool
	)
	if d.country != nil {
		if r := d.country.db.Lookup(addr); r.Found() {
			if err := r.Decode(&crec); err == nil {
				chit = true
			}
		}
	}
	if d.asn != nil {
		if r := d.asn.db.Lookup(addr); r.Found() {
			if err := r.Decode(&arec); err == nil {
				ahit = true
			}
		}
	}
	d.mu.RUnlock()

	if chit {
		res.CountryISO, res.ContinentCode = countryFrom(crec)
		res.City, res.RegionISO, res.Latitude, res.Longitude, res.HasLocation = cityFrom(crec)
		found = true
	}
	if ahit {
		res.ASN, res.ASOrg = arec.Number, arec.Org
		found = true
	}

	d.mu.Lock()
	if d.country != nil {
		if chit {
			d.stats.countryHits++
		} else {
			d.stats.countryMisses++
		}
	}
	if d.asn != nil {
		if ahit {
			d.stats.asnHits++
		} else {
			d.stats.asnMisses++
		}
	}
	d.mu.Unlock()

	return res, found
}

// Reload re-stats each configured path and hot-swaps any database whose
// (mtime, size) has changed since it was opened, reporting whether anything was
// swapped. It is what lets an operator's geoipupdate cron — or this package's
// own downloader — rewrite the files under a running process.
//
// Change detection is (mtime, size) rather than a content hash so the common
// case (nothing changed) costs one stat per file instead of re-reading tens of
// megabytes on every tick. A rewrite that preserves both within the filesystem's
// timestamp granularity would be missed until the next genuine change; the
// downloader renames a freshly-written file into place, so in practice mtime
// always moves.
//
// A file that fails to open leaves the currently-loaded database serving. That
// is the fail-open contract: a bad update costs freshness, never availability.
// Errors for both databases are joined so one bad file does not hide the other.
func (d *DB) Reload() (bool, error) {
	if d == nil {
		return false, nil
	}
	var changed bool
	var errs []error

	swap := func(path string, cur **reader, reloads *int64) {
		if path == "" {
			return
		}
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Not an error: the scheduled download has not landed yet.
				return
			}
			errs = append(errs, fmt.Errorf("stat geoip database %s: %w", path, err))
			return
		}
		if *cur != nil && (*cur).mtime.Equal(fi.ModTime()) && (*cur).size == fi.Size() {
			return
		}
		next, err := openAt(path, fi)
		if err != nil {
			errs = append(errs, fmt.Errorf("reload geoip database: %w", err))
			return
		}
		old := *cur
		*cur = next
		if old != nil {
			// Safe under the write lock, and safe regardless: the reader owns a
			// heap slice, not a mapping, so a stale reference could not fault
			// even if one escaped.
			// Close releases the decoded buffers; it cannot fail for an
			// OpenBytes reader (there is no file handle or mapping to release),
			// and there is nothing a caller could do about it if it did.
			_ = old.db.Close()
		}
		*reloads++
		changed = true
	}

	d.mu.Lock()
	swap(d.countryPath, &d.country, &d.stats.countryReloads)
	swap(d.asnPath, &d.asn, &d.stats.asnReloads)
	if len(errs) > 0 {
		d.stats.reloadFailures += int64(len(errs))
	}
	d.mu.Unlock()

	// errors.Join returns nil for an all-empty slice, so a clean reload reports
	// no error, and a failure on one database never hides a failure on the other.
	return changed, errors.Join(errs...)
}

// Stats returns an absolute snapshot of the counters and the loaded databases'
// identity, for the admin status page.
func (d *DB) Stats() Stats {
	if d == nil {
		return Stats{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := Stats{
		CountryHits:    d.stats.countryHits,
		CountryMisses:  d.stats.countryMisses,
		ASNHits:        d.stats.asnHits,
		ASNMisses:      d.stats.asnMisses,
		Skipped:        d.stats.skipped,
		CountryReloads: d.stats.countryReloads,
		ASNReloads:     d.stats.asnReloads,
		ReloadFailures: d.stats.reloadFailures,
		CountryPath:    d.countryPath,
		ASNPath:        d.asnPath,
	}
	if d.country != nil {
		md := d.country.db.Metadata
		s.CountryType = md.DatabaseType
		s.CountryBuildTime = buildTime(md.BuildEpoch)
	}
	if d.asn != nil {
		md := d.asn.db.Metadata
		s.ASNType = md.DatabaseType
		s.ASNBuildTime = buildTime(md.BuildEpoch)
	}
	return s
}

// Report flushes the cumulative counters as OTEL counter DELTAS (since the last
// report) plus the current build-time gauges. It is a no-op for a nil emitter or
// a nil DB. Only the updater's single goroutine calls it in production, so the
// delta baseline has one writer.
func (d *DB) Report(e telemetry.Emitter) {
	if d == nil || e == nil {
		return
	}
	d.mu.Lock()
	cur := d.stats
	prev := d.reported
	d.reported = cur
	countryLoaded, asnLoaded := d.country != nil, d.asn != nil
	var countryBuild, asnBuild time.Time
	if countryLoaded {
		countryBuild = buildTime(d.country.db.Metadata.BuildEpoch)
	}
	if asnLoaded {
		asnBuild = buildTime(d.asn.db.Metadata.BuildEpoch)
	}
	d.mu.Unlock()

	delta := func(metric string, doc metricdoc.Metric, attrs telemetry.Attrs, now, before int64) {
		if n := now - before; n > 0 {
			e.Counter(metric, doc.Unit, doc.Description, float64(n), attrs)
		}
	}
	delta(MetricLookups, docLookups, telemetry.Attrs{attrDatabase: databaseCountry, attrResult: resultHit}, cur.countryHits, prev.countryHits)
	delta(MetricLookups, docLookups, telemetry.Attrs{attrDatabase: databaseCountry, attrResult: resultMiss}, cur.countryMisses, prev.countryMisses)
	delta(MetricLookups, docLookups, telemetry.Attrs{attrDatabase: databaseASN, attrResult: resultHit}, cur.asnHits, prev.asnHits)
	delta(MetricLookups, docLookups, telemetry.Attrs{attrDatabase: databaseASN, attrResult: resultMiss}, cur.asnMisses, prev.asnMisses)
	delta(MetricLookups, docLookups, telemetry.Attrs{attrDatabase: databaseSkipped, attrResult: resultSkipped}, cur.skipped, prev.skipped)

	delta(MetricReloads, docReloads, telemetry.Attrs{attrDatabase: databaseCountry, attrResult: resultSuccess}, cur.countryReloads, prev.countryReloads)
	delta(MetricReloads, docReloads, telemetry.Attrs{attrDatabase: databaseASN, attrResult: resultSuccess}, cur.asnReloads, prev.asnReloads)
	// Reload failures are not attributable to one database without threading the
	// path back out of Reload, and the log line already names the file. The
	// counter's job here is "is reloading working at all", so it carries the
	// aggregate under a database value that cannot be confused with a real one.
	delta(MetricReloads, docReloads, telemetry.Attrs{attrDatabase: databaseSkipped, attrResult: resultFailure}, cur.reloadFailures, prev.reloadFailures)

	// Build time is a gauge of MaxMind's build date, not the download time, so
	// it is the correct staleness signal. Omitted entirely for a database that
	// is not loaded — a zero would read as "built in 1970" and fire every
	// staleness alert ever written against it.
	if countryLoaded {
		e.Gauge(MetricBuildTime, docBuildTime.Unit, docBuildTime.Description,
			float64(countryBuild.Unix()), telemetry.Attrs{attrDatabase: databaseCountry})
	}
	if asnLoaded {
		e.Gauge(MetricBuildTime, docBuildTime.Unit, docBuildTime.Description,
			float64(asnBuild.Unix()), telemetry.Attrs{attrDatabase: databaseASN})
	}
}

// ObserveDownload records the outcome of one edition's download attempt. It is a
// no-op for a nil emitter, so the updater can call it unconditionally.
func (d *DB) ObserveDownload(e telemetry.Emitter, edition string, res DownloadResult, err error) {
	if e == nil {
		return
	}
	result := resultUpdated
	switch {
	case err != nil:
		result = resultFailure
	case !res.Updated:
		result = resultUnmodified
	}
	e.Counter(MetricDownloads, docDownloads.Unit, docDownloads.Description, 1,
		telemetry.Attrs{attrEdition: edition, attrResult: result})
}

// Close releases the loaded databases.
func (d *DB) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closeReadersLocked()
}

func (d *DB) closeReadersLocked() {
	if d.country != nil {
		_ = d.country.db.Close() // see the Reload comment: cannot fail for OpenBytes
		d.country = nil
	}
	if d.asn != nil {
		_ = d.asn.db.Close()
		d.asn = nil
	}
}

// buildTime converts an mmdb metadata build epoch to a time.
//
// BuildEpoch is a uint the database file supplies, so a corrupt or hostile file
// could carry a value past math.MaxInt64. Converting that unchecked wraps to a
// negative, which would surface as a build date in 1901 and fire the staleness
// alert permanently. Anything that absurd is treated as "unknown" (zero), which
// callers already handle by omitting the gauge entirely.
func buildTime(epoch uint) time.Time {
	if epoch > math.MaxInt64 {
		return time.Time{}
	}
	return time.Unix(int64(epoch), 0).UTC()
}

// Enrichable reports whether addr is a globally routable unicast address worth
// looking up. Everything else — loopback, unspecified, link-local, multicast,
// RFC 1918 / unique-local, and the RFC 6598 CGNAT range Tailscale carves tailnet
// IPv4 addresses out of — is skipped.
//
// An IPv4-mapped IPv6 address is unwrapped first: ::ffff:192.168.1.1 is a
// private address wearing a v6 costume, and netip's predicates would not see
// through it.
func Enrichable(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	switch {
	case addr.IsUnspecified(), addr.IsLoopback(), addr.IsMulticast(),
		addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast(),
		addr.IsInterfaceLocalMulticast(), addr.IsPrivate():
		return false
	case enrich.IsTailscaleAddr(addr):
		// The shared default set keeps "never geolocate a tailnet address"
		// explicit. Its ULA is already covered by IsPrivate, while CGNAT is not.
		return false
	}
	return addr.IsGlobalUnicast()
}
