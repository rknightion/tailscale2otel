package nodemetrics

import "github.com/rknightion/tailscale2otel/internal/metricdoc"

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this collector's own,
// statically-enumerable metric documentation. The scraper also FORWARDS every
// scraped tailscaled_* series VERBATIM (their names and units come from the
// scraped node at runtime), which is not statically enumerable and is documented
// as prose in docs/metrics.md, not in this catalog. The only static metric is
// the per-target tailscale.node.up gauge. The emit site (nodemetrics.go)
// references this descriptor; catalog_test.go asserts it.
const groupNodeMetrics = "Node metrics"

var docNodeUp = metricdoc.Metric{
	Name:        metricUp,
	Unit:        "1",
	Instrument:  metricdoc.Gauge,
	Description: "Per-target scrape health: `1` if the last scrape of that node succeeded, else `0`.",
	Attributes:  []string{attrInstance},
	Group:       groupNodeMetrics,
}

// Catalog returns the statically-enumerable metrics this package emits, for the
// doc generator. The forwarded tailscaled_* series are runtime-named and not
// included.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docNodeUp}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
