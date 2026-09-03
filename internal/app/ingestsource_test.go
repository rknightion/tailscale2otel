package app

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// allIngestSources is the closed vocabulary of tailscale2otel.ingest.{records,size}
// `source` values -- one per ingestion path -- built from the semconv constants
// (never string literals) so it can only drift from semconv deliberately. Every
// value here MUST appear verbatim in both appcatalog.DocIngestRecords.Description
// and appcatalog.DocIngestBytes.Description, and boundedIngestSource must admit
// each one unchanged rather than folding it to "other". This is the catalog/
// emit-site parity guard #450 asked for: a new ingestion path (or a descriptor
// edit that drops one) fails this test loudly instead of silently going
// undocumented, the way objectstore's did.
var allIngestSources = []string{
	semconv.IngestSourcePoll,
	semconv.IngestSourceStream,
	semconv.IngestSourceWebhook,
	semconv.IngestSourceObjectStore,
}

func TestBoundedIngestSourceAdmitsTheFullClosedVocabulary(t *testing.T) {
	for _, source := range allIngestSources {
		if got := boundedIngestSource(source); got != source {
			t.Errorf("boundedIngestSource(%q) = %q, want it admitted unchanged", source, got)
		}
	}
	if got := boundedIngestSource("bogus"); got != "other" {
		t.Errorf(`boundedIngestSource("bogus") = %q, want "other" -- the set must stay closed`, got)
	}
}

func TestIngestDescriptorsDocumentEveryClosedSourceValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		desc string
	}{
		{appcatalog.DocIngestRecords.Name, appcatalog.DocIngestRecords.Description},
		{appcatalog.DocIngestBytes.Name, appcatalog.DocIngestBytes.Description},
	} {
		for _, source := range allIngestSources {
			if !strings.Contains(tc.desc, source) {
				t.Errorf("%s description does not mention source %q; got: %s", tc.name, source, tc.desc)
			}
		}
	}
}
