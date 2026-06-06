package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// appWithLog builds an App via the newApp seam writing logs to buf.
func appWithLog(t *testing.T, buf *bytes.Buffer) *App {
	t.Helper()
	cfg := config.Default()
	cfg.SelfObservability.Enabled = true
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rec := telemetrytest.New()
	return newApp(cfg, "vtest", log, rec.Emitter(), func(context.Context) error { return nil },
		newTestClient(t, "http://example.invalid"), collector.NewMemoryStore(), NewAPIStats())
}

// TestRecordReceiverStop_CleanShutdownSilent verifies a normal stop signal
// (context cancel / closed server) is NOT logged as an error, matching the
// admin server's behavior — only a genuine failure logs at ERROR.
func TestRecordReceiverStop_CleanShutdownSilent(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, http.ErrServerClosed, nil} {
		var buf bytes.Buffer
		a := appWithLog(t, &buf)
		a.recordReceiverStop(appcatalog.ComponentStream, err)
		if buf.Len() != 0 {
			t.Fatalf("clean shutdown err=%v logged %q, want nothing", err, buf.String())
		}
	}
}

// TestRecordReceiverStop_RealErrorLogsWithComponent verifies a genuine failure
// logs at ERROR and carries the component tag for per-subsystem filtering.
func TestRecordReceiverStop_RealErrorLogsWithComponent(t *testing.T) {
	var buf bytes.Buffer
	a := appWithLog(t, &buf)
	a.recordReceiverStop(appcatalog.ComponentWebhook, errors.New("boom"))
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Fatalf("log = %q, want level=ERROR", out)
	}
	if !strings.Contains(out, "component=webhook") {
		t.Fatalf("log = %q, want component=webhook", out)
	}
}
