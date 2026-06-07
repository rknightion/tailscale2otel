package app

import (
	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// ingestObserver returns the closure the ingestion paths (poll flow/audit, stream,
// webhook) call to record tailscale2otel.ingest.{records,bytes}. It returns nil
// when self-observability is disabled, mirroring apiObserver — so a nil hook is
// the off switch and the receiver/collector packages stay agnostic. records>0
// emits ingest.records{source,signal}; bytes>0 emits ingest.bytes{source}; a call
// may carry either or both.
func (a *App) ingestObserver() func(source, signal string, records, bytes int) {
	if !a.cfg.SelfObservability.Enabled {
		return nil
	}
	e := a.emitter
	return func(source, signal string, records, bytes int) {
		if records > 0 {
			e.Counter(appcatalog.DocIngestRecords.Name, appcatalog.DocIngestRecords.Unit, appcatalog.DocIngestRecords.Description,
				float64(records), telemetry.Attrs{
					semconv.AttrIngestSource: source,
					semconv.AttrIngestSignal: signal,
				})
		}
		if bytes > 0 {
			e.Counter(appcatalog.DocIngestBytes.Name, appcatalog.DocIngestBytes.Unit, appcatalog.DocIngestBytes.Description,
				float64(bytes), telemetry.Attrs{semconv.AttrIngestSource: source})
		}
	}
}
