package geoip

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation: name, unit, instrument, description, and the attribute keys
// carried. Report()/ObserveDownload() reference these fields (so a
// description/unit cannot drift from what is documented) and
// tools/metricscatalog renders them into docs/metrics.md.
// TestCatalogMatchesEmission asserts that what is emitted matches what is
// declared, in both directions that matter.
//
// All series are gated on self_observability.enabled (the updater is handed an
// Emitter only then); the admin status page reads Stats() directly and shows the
// same numbers either way.
const groupGeoIP = "GeoIP"

// Metric names emitted by the GeoIP enrichment databases.
const (
	MetricLookups   = "tailscale.geoip.lookups"
	MetricReloads   = "tailscale.geoip.reloads"
	MetricDownloads = "tailscale.geoip.downloads"
	MetricBuildTime = "tailscale.geoip.database.build_time"
)

// Attribute keys and their closed value sets. Every one is bounded: `database`
// is one of three constants, `result` a small enum, and `edition` is drawn from
// the operator's configured editions list (a handful at most).
const (
	attrDatabase = "geoip.database"
	attrResult   = "result"
	attrEdition  = "geoip.edition"

	databaseCountry = "country"
	databaseASN     = "asn"
	// databaseSkipped is not a database — it labels the lookups that never
	// reached one because the address was not globally routable. It rides the
	// same counter so "how many addresses did we see, and what happened to
	// them" is a single query rather than a join.
	databaseSkipped = "skipped"

	resultHit     = "hit"
	resultMiss    = "miss"
	resultSkipped = "skipped"
	resultSuccess = "success"
	resultFailure = "failure"
	// resultUnmodified is a successful conditional request that found nothing
	// newer — the steady state of a healthy daily updater, and worth telling
	// apart from an update that actually replaced a file.
	resultUnmodified = "unmodified"
	resultUpdated    = "updated"
)

var (
	docLookups = metricdoc.Metric{
		Name:       MetricLookups,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "GeoIP enrichment lookups, by database (`country`, `asn`, or `skipped`) and result. " +
			"`hit` means a record was found, `miss` means the loaded database has no record for that address, and " +
			"`skipped` (always with `geoip.database=skipped`) counts addresses that never reached a database because " +
			"they are not globally routable — loopback, RFC 1918, link-local, and in particular the Tailscale CGNAT " +
			"range and ULA, which are never geolocated. A database that is not loaded contributes neither hits nor misses.",
		Attributes: []string{attrDatabase, attrResult},
		Group:      groupGeoIP,
	}
	docReloads = metricdoc.Metric{
		Name:       MetricReloads,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "GeoIP database hot-swaps, by database and result. A `success` means a changed file on disk was " +
			"loaded and swapped in atomically; a `failure` means the new file could not be read or parsed, in which case " +
			"the previously loaded database keeps serving. An unchanged file is not counted at all.",
		Attributes: []string{attrDatabase, attrResult},
		Group:      groupGeoIP,
	}
	docDownloads = metricdoc.Metric{
		Name:       MetricDownloads,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "MaxMind database downloads, by edition and result: `updated` (a newer build was fetched, verified " +
			"against its published SHA-256, and installed), `unmodified` (the conditional request returned 304 — the " +
			"healthy steady state, and the thing that keeps a daily updater inside MaxMind's download limit), or " +
			"`failure`. Emitted only when `enrichment.geoip.download.enabled` is set.",
		Attributes: []string{attrEdition, attrResult},
		Group:      groupGeoIP,
	}
	docBuildTime = metricdoc.Metric{
		Name:       MetricBuildTime,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Gauge,
		Description: "Unix timestamp of the loaded GeoIP database's build, per database. This is MaxMind's build date, " +
			"not when the file was downloaded, so it is the right thing to alert staleness on: `time() - " +
			"tailscale_geoip_database_build_time_seconds > 14 * 86400` catches an updater that has silently stopped " +
			"working. Absent for a database that is not loaded.",
		Attributes: []string{attrDatabase},
		Group:      groupGeoIP,
		TimeSource: metricdoc.TimestampSource,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docLookups, docReloads, docDownloads, docBuildTime}
}
