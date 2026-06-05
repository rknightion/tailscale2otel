package contacts

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation; the emit site (contacts.go) references these descriptors so a
// description/unit cannot drift, and catalog_test.go asserts the emission matches.
const groupContacts = "Contacts"

var docNeedsVerification = metricdoc.Metric{
	Name:        metricNeedsVerification,
	Unit:        semconv.UnitDimensionless,
	Instrument:  metricdoc.Gauge,
	Description: "`1` if the tailnet contact email still needs verification, else `0`; one series per contact type (`account`/`support`/`security`). The email address is never emitted.",
	Attributes:  []string{attrContactType},
	Group:       groupContacts,
}

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric { return []metricdoc.Metric{docNeedsVerification} }

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent { return nil }
