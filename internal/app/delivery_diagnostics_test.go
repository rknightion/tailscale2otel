package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// deliveryDiagSentinel stands in for sensitive text a backend might echo in a
// non-2xx response body (a credential, a signed URL). It is synthetic.
const deliveryDiagSentinel = "SENTINEL-not-a-real-secret-app-4b8e"

// deliveryDiagBackend is a local fake OTLP/HTTP backend: it records every
// request (path + raw body) and answers with a switchable status, putting the
// sentinel in every failure body.
type deliveryDiagBackend struct {
	mu       sync.Mutex
	status   int
	requests map[string]int
	bodies   map[string][]byte
}

func (b *deliveryDiagBackend) set(status int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = status
}

func (b *deliveryDiagBackend) snapshot(path string) (int, []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests[path], append([]byte(nil), b.bodies[path]...)
}

func (b *deliveryDiagBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests[r.URL.Path]++
	b.bodies[r.URL.Path] = append(b.bodies[r.URL.Path], raw...)
	status := b.status
	b.mu.Unlock()
	if status/100 == 2 {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "upstream rejected request, echoing Authorization: Bearer %s", deliveryDiagSentinel)
}

// lockedLogBuffer is a goroutine-safe slog sink: exporters log from their own
// goroutines.
type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLogBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// TestNewLogsDeliveryOutageAndRecoveryPerProvider pins TSO-0158 through the
// real New() path: with a local OTLP backend that fails and then recovers, the
// app logger carries a first-failure and a recovery line for the logs signal
// from every provider (the process provider, unlabeled, and each tailnet
// provider, labeled), and no line carries the backend's response body.
//
// It also pins the no-feedback contract on the production wiring: no
// diagnostic line becomes an OTLP log record, and nothing the diagnostics do
// (including the suppression counter on each provider's Emitter) collects or
// exports metrics outside the schedule (TSO-0148). The 1h metric interval
// keeps the schedule from firing, and self-observability puts build_info on
// the process provider, so any collection there would reach the backend.
func TestNewLogsDeliveryOutageAndRecoveryPerProvider(t *testing.T) {
	backend := &deliveryDiagBackend{status: http.StatusServiceUnavailable, requests: map[string]int{}, bodies: map[string][]byte{}}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)

	tailnets := []string{"alpha.example.com", "beta.example.com"}
	cfg := config.Default()
	cfg.OTLP.Protocol = "http"
	cfg.OTLP.Endpoint = server.URL
	cfg.OTLP.TLS.Insecure = true
	cfg.OTLP.Compression = "none"
	cfg.OTLP.Timeout = config.Duration(2 * time.Second)
	retryOff := false
	cfg.OTLP.Retry.Enabled = &retryOff
	cfg.OTLP.MetricInterval = config.Duration(time.Hour)
	cfg.OTLP.Batch.Logs.ExportInterval = config.Duration(50 * time.Millisecond)
	cfg.SelfObservability.Enabled = true
	// Keep the test off the host's state directory.
	cfg.Checkpoint.Store = "memory"
	cfg.Checkpoint.EvidenceStore = "memory"
	for _, name := range tailnets {
		cfg.Tailnets = append(cfg.Tailnets, config.TailnetConfig{
			Name: name, Auth: config.TailscaleAuth{Method: "apikey", APIKey: "synthetic-key"},
		})
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg.Validate: %v", err)
	}

	logs := &lockedLogBuffer{}
	a, err := New(context.Background(), cfg, "v-test",
		slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if a.restore != nil {
			a.restore()
		}
		if a.shutdown != nil {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.shutdown(c)
		}
	})
	if len(a.runtimes) != len(tailnets) {
		t.Fatalf("runtimes = %d, want %d", len(a.runtimes), len(tailnets))
	}

	emitAll := func(phase string) {
		ev := telemetry.Event{Name: "synthetic.delivery", Body: phase, Severity: telemetry.SeverityInfo}
		a.procEmitter.LogEvent(ev)
		for _, rt := range a.runtimes {
			rt.emitter.LogEvent(ev)
		}
	}
	// lineFor returns the first log line carrying msg for the logs signal from
	// the provider named by tailnet ("" = the process provider, which carries
	// no tailnet field).
	lineFor := func(out, msg, tailnet string) string {
		for _, l := range strings.Split(out, "\n") {
			if !strings.Contains(l, `msg="`+msg+`"`) || !strings.Contains(l, "signal="+telemetry.SignalLogs) {
				continue
			}
			if tailnet == "" && !strings.Contains(l, "tailnet=") || tailnet != "" && strings.Contains(l, "tailnet="+tailnet) {
				return l
			}
		}
		return ""
	}
	providers := append([]string{""}, tailnets...)
	waitFor := func(msg string) string {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			out := logs.String()
			missing := 0
			for _, tn := range providers {
				if lineFor(out, msg, tn) == "" {
					missing++
				}
			}
			if missing == 0 {
				return out
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d of %d providers never logged %q for signal=logs:\n%s", missing, len(providers), msg, out)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	emitAll("during outage")
	waitFor("OTLP export failing")

	backend.set(http.StatusOK)
	emitAll("after recovery")
	out := waitFor("OTLP export recovered")

	if strings.Contains(out, deliveryDiagSentinel) || strings.Contains(strings.ToLower(out), "bearer") {
		t.Fatalf("app log leaked backend response text:\n%s", out)
	}
	for _, tn := range providers {
		if l := lineFor(out, "OTLP export failing", tn); !strings.Contains(l, "error_class=") {
			t.Errorf("provider %q first-failure line lacks a bounded error_class: %q", tn, l)
		}
	}

	if _, body := backend.snapshot("/v1/logs"); bytes.Contains(body, []byte("OTLP export")) {
		t.Error("an export diagnostic line was exported as an OTLP log record (feedback loop)")
	}
	if n, _ := backend.snapshot("/v1/metrics"); n != 0 {
		t.Errorf("backend saw %d metric exports, want 0 (no out-of-schedule collection)", n)
	}
	for _, rt := range a.runtimes {
		if s := rt.delivery.CollectionStats(); s.ScheduledAttempts != 0 || s.SnapshotsCollected != 0 || s.TerminalAttempts != 0 {
			t.Errorf("tailnet %q collection stats = %+v, want no collection", rt.name, s)
		}
	}
}
