package app

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/geoip"
)

// testLogger discards output: these tests assert behavior, and buildGeoIP is
// deliberately chatty about degraded states.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A missing database must not stop the app from building the GeoIP set: the
// scheduled download may not have landed on a cold start, and refusing to come
// up would turn an opt-in enrichment nicety into an outage.
func TestBuildGeoIP_MissingDatabaseStillBuilds(t *testing.T) {
	a := &App{cfg: config.Default(), logger: testLogger()}
	a.cfg.Enrichment.GeoIP.Enabled = true
	a.cfg.Enrichment.GeoIP.CountryDatabase = filepath.Join(t.TempDir(), "absent.mmdb")

	a.buildGeoIP()

	if a.geoDB == nil {
		t.Fatal("geoDB is nil after buildGeoIP with a missing database")
	}
	t.Cleanup(a.geoDB.Close)
	if !a.geoDB.Empty() {
		t.Error("geoDB reports a loaded database when the file does not exist")
	}
	if a.geoUpdater == nil {
		t.Error("geoUpdater is nil; the reload loop is what picks the file up later")
	}
}

// Disabled means nothing is built at all, so every consumer sees the nil *DB
// that reports a miss for every address.
func TestBuildGeoIP_DisabledBuildsNothing(t *testing.T) {
	a := &App{cfg: config.Default(), logger: testLogger()}
	a.buildGeoIP()
	if a.geoDB != nil || a.geoUpdater != nil {
		t.Fatalf("geoip disabled but geoDB=%v geoUpdater=%v", a.geoDB != nil, a.geoUpdater != nil)
	}
	// The nil DB must still be safe for every consumer to call.
	if !a.geoDB.Empty() {
		t.Error("nil geoDB Empty() = false")
	}
}

// The status snapshot has to work off Stats() directly, so it shows real numbers
// whether or not self-observability (which gates the OTEL metrics) is on.
func TestGeoipInfo(t *testing.T) {
	a := &App{cfg: config.Default(), logger: testLogger()}

	if got := a.geoipInfo(); got.Enabled {
		t.Error("geoipInfo reports enabled with no database set")
	}

	a.cfg.Enrichment.GeoIP.Enabled = true
	a.cfg.Enrichment.GeoIP.CountryDatabase = "../geoip/testdata/GeoIP2-Country-Test.mmdb"
	a.cfg.Cardinality.Flow.GeoDims = true
	a.buildGeoIP()
	t.Cleanup(a.geoDB.Close)

	info := a.geoipInfo()
	if !info.Enabled {
		t.Fatal("geoipInfo reports disabled after buildGeoIP")
	}
	if info.CountryType != "GeoIP2-Country" {
		t.Errorf("CountryType = %q, want GeoIP2-Country", info.CountryType)
	}
	// The build time is the staleness signal an operator reads, so it must be a
	// real date rather than the zero value dressed up as one.
	if info.CountryBuildTime == "" || strings.HasPrefix(info.CountryBuildTime, "1970") {
		t.Errorf("CountryBuildTime = %q, want the database's build date", info.CountryBuildTime)
	}
	if info.ASNType != "" {
		t.Errorf("ASNType = %q, want empty (no ASN database configured)", info.ASNType)
	}
	if !info.GeoDims {
		t.Error("GeoDims = false; the status page must show that country labels are on flow metrics")
	}
}

// geoDownloadClientTimeout must stay looser than the per-fetch context deadline,
// or the client timeout fires first and reports a limit the operator never set.
func TestGeoDownloadClientTimeout(t *testing.T) {
	if got := geoDownloadClientTimeout(0); got <= 0 {
		t.Errorf("geoDownloadClientTimeout(0) = %v, want a positive fallback", got)
	}
	cfg := config.Default().Enrichment.GeoIP.Download.Timeout.D()
	if got := geoDownloadClientTimeout(cfg); got <= cfg {
		t.Errorf("geoDownloadClientTimeout(%v) = %v, want it strictly looser than the fetch deadline", cfg, got)
	}
}

// The app hands *geoip.DB straight to the flow processor and the devices
// collector, so it has to satisfy the narrow interface both depend on.
func TestGeoDBSatisfiesLookup(t *testing.T) {
	var _ geoip.Lookup = (*geoip.DB)(nil)
}
