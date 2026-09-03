package catalog_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/catalog"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
)

func TestAbsoluteTimestampMetricsDeclareProvenance(t *testing.T) {
	want := map[string]metricdoc.TimestampProvenance{
		"tailscale.acl.last_audit_change":                     metricdoc.TimestampSource,
		"tailscale.acl.last_changed":                          metricdoc.TimestampPersistedObservation,
		"tailscale.device.attribute.expiry":                   metricdoc.TimestampSource,
		"tailscale.device.key.expiry":                         metricdoc.TimestampSource,
		"tailscale.device.last_seen":                          metricdoc.TimestampSource,
		"tailscale.geoip.database.build_time":                 metricdoc.TimestampSource,
		"tailscale.key.expiry":                                metricdoc.TimestampSource,
		"tailscale.logstream.last_activity":                   metricdoc.TimestampSource,
		"tailscale.posture_integration.last_sync":             metricdoc.TimestampSource,
		"tailscale.user.last_seen":                            metricdoc.TimestampSource,
		"tailscale2otel.api.last_probe":                       metricdoc.TimestampProcessLocal,
		"tailscale2otel.flow_store.last_checkpoint_timestamp": metricdoc.TimestampProcessLocal,
		"tailscale2otel.ingest.last_event_timestamp":          metricdoc.TimestampSource,
		"tailscale2otel.profiling.upload.last_success":        metricdoc.TimestampProcessLocal,
		"tailscale2otel.scrape.last_timestamp":                metricdoc.TimestampProcessLocal,
		"tailscale2otel.tls.cert.not_before":                  metricdoc.TimestampSource,
		"tailscale2otel.tls.cert.not_after":                   metricdoc.TimestampSource,
		"tailscale2otel.tls.cert.reload.last_success":         metricdoc.TimestampProcessLocal,
	}

	seen := make(map[string]bool, len(want))
	for _, m := range catalog.Metrics() {
		absolute := strings.Contains(m.Description, "Unix timestamp") ||
			strings.Contains(m.Description, "Unix seconds") ||
			strings.Contains(m.Description, "Unix epoch seconds")
		provenance, inventoried := want[m.Name]
		if absolute && !inventoried {
			t.Errorf("absolute timestamp metric %s is missing from the provenance inventory", m.Name)
			continue
		}
		if !inventoried {
			continue
		}
		seen[m.Name] = true
		if m.TimeSource != provenance {
			t.Errorf("%s timestamp provenance = %q, want %q", m.Name, m.TimeSource, provenance)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("timestamp provenance inventory names missing metric %s", name)
		}
	}
}
