package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestEmitReceiverConfigMarksOnlyFailClosedNetworkReceiver(t *testing.T) {
	cfg := config.Default()
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = ":8088"
	cfg.Streaming.Token = ""
	cfg.Webhook.Enabled = true
	cfg.Webhook.Listen = "127.0.0.1:8089"
	cfg.Webhook.Secret = ""
	rec := telemetrytest.New()

	emitReceiverConfig(rec.Emitter(), cfg)
	pts := rec.MetricPoints(appcatalog.MetricReceiverMisconfigured)
	if len(pts) != 2 {
		t.Fatalf("receiver misconfiguration points = %+v, want two enabled receivers", pts)
	}
	values := map[string]float64{}
	for _, point := range pts {
		values[point.Attrs["receiver"]] = point.Value
	}
	if values["streaming"] != 1 || values["webhook"] != 0 {
		t.Fatalf("receiver misconfiguration values = %v, want streaming=1 webhook=0", values)
	}
}
