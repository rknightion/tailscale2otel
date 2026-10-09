package telemetry

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
)

// wiringSentinel stands in for sensitive text a backend might echo in a
// non-2xx response body. It is synthetic.
const wiringSentinel = "SENTINEL-not-a-real-secret-wiring-7d21"

// wiringBackend is a local fake OTLP/HTTP backend that records every request
// (path + raw body) and answers with a switchable status. A failure body
// carries the sentinel.
type wiringBackend struct {
	mu       sync.Mutex
	status   int
	requests map[string]int
	bodies   map[string][]byte
}

func newWiringBackend() *wiringBackend {
	return &wiringBackend{status: http.StatusOK, requests: map[string]int{}, bodies: map[string][]byte{}}
}

func (b *wiringBackend) set(status int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = status
}

func (b *wiringBackend) count(path string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests[path]
}

func (b *wiringBackend) body(path string) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.bodies[path]...)
}

func (b *wiringBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	_, _ = fmt.Fprintf(w, "upstream rejected request, echoing Authorization: Bearer %s", wiringSentinel)
}

// syncBuffer is a goroutine-safe log sink: the exporters log from their own
// goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestProviderSetBindsDeliveryDiagnostics pins TSO-0158 at the ProviderSet
// seam app.New uses: every provider's delivery tracker is bound to
// Options.Logger (with its tailnet label) and, under self-observability, to
// that provider's own Emitter. It also pins the no-feedback contract: the
// diagnostic lines never become OTLP log records, and the suppression counter
// the Emitter records never drives a metric collection or export (TSO-0148:
// metrics leave only on the scheduled collection, which a 1h interval keeps
// from firing during the test).
func TestProviderSetBindsDeliveryDiagnostics(t *testing.T) {
	backend := newWiringBackend()
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)

	logs := &syncBuffer{}
	const tailnet = "tn-a.example.test"
	ctx := context.Background()
	ps, err := NewProviderSet(ctx, Options{
		ServiceName: "tailscale2otel", Provider: "tailscale",
		Protocol: "http", Endpoint: server.URL, Insecure: true,
		Transport:         TransportOptions{Timeout: 2 * time.Second, Compression: "none", Retry: &RetryPolicy{Enabled: false}},
		MetricInterval:    time.Hour,
		SelfObsEnabled:    true,
		PrometheusEnabled: true,
		Logger:            slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, []PerTailnetOptions{{Name: tailnet, InstanceID: "i-a"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ps.Shutdown(c)
	})
	tn, proc := ps.Tailnet(tailnet), ps.Process()

	// One log record per flush, so each flush is exactly one log export.
	flush := func(p *Provider, i int) {
		t.Helper()
		p.Emitter().LogEvent(Event{Name: "synthetic.wiring", Body: fmt.Sprintf("record %d", i), Severity: SeverityInfo})
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = p.ForceFlush(c) // the error is expected while the backend fails
	}

	backend.set(http.StatusServiceUnavailable)
	const tnFailures = 3
	for i := range tnFailures {
		flush(tn, i)
	}
	flush(proc, 0)

	backend.set(http.StatusOK)
	flush(tn, 99)
	flush(proc, 99)

	out := logs.String()
	if strings.Contains(out, wiringSentinel) || strings.Contains(strings.ToLower(out), "bearer") {
		t.Fatalf("diagnostic log leaked backend response text:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	find := func(msg string, process bool) string {
		for _, l := range lines {
			if !strings.Contains(l, msg) || !strings.Contains(l, "signal="+SignalLogs) {
				continue
			}
			labeled := strings.Contains(l, "tailnet=")
			if process && !labeled || !process && strings.Contains(l, "tailnet="+tailnet) {
				return l
			}
		}
		return ""
	}
	for _, process := range []bool{false, true} {
		who := "tailnet provider"
		if process {
			who = "process provider"
		}
		if l := find(`msg="OTLP export failing"`, process); l == "" {
			t.Errorf("%s: no first-failure line for signal=logs:\n%s", who, out)
		}
		if l := find(`msg="OTLP export recovered"`, process); l == "" {
			t.Errorf("%s: no recovery line for signal=logs:\n%s", who, out)
		}
	}
	if l := find(`msg="OTLP export recovered"`, false); !strings.Contains(l, fmt.Sprintf("failed_exports=%d", tnFailures)) {
		t.Errorf("tailnet recovery line lacks failed_exports=%d: %q", tnFailures, l)
	}

	// The suppression counter rides the tailnet provider's own Emitter: the
	// first failure logs, the next two are suppressed and counted exactly.
	var st DeliveryState
	for _, s := range tn.Delivery() {
		if s.Signal == SignalLogs {
			st = s
		}
	}
	if st.Failures != tnFailures {
		t.Fatalf("tailnet logs failures = %d, want %d (one export per flush)", st.Failures, tnFailures)
	}
	body := scrape(t, ps)
	wantSeries := fmt.Sprintf(`tailscale2otel_export_diagnostics_suppressed_total{error_type="other",signal="logs",tailscale2otel_provider="tailscale",tailscale_tailnet=%q} %d`,
		tailnet, tnFailures-1)
	if !strings.Contains(body, wantSeries) {
		t.Errorf("scrape lacks %s:\n%s", wantSeries, grepLines(body, "diagnostics_suppressed"))
	}

	// No feedback: no diagnostic text ever reached the OTLP log pipeline...
	if logBody := backend.body("/v1/logs"); bytes.Contains(logBody, []byte("OTLP export")) {
		t.Error("an export diagnostic line was exported as an OTLP log record (feedback loop)")
	}
	// ...and nothing, the suppression counter included, caused a metric
	// collection or export outside the (unreached) schedule.
	if n := backend.count("/v1/metrics"); n != 0 {
		t.Errorf("backend saw %d metric exports, want 0 (no out-of-schedule collection)", n)
	}
	for name, p := range map[string]*Provider{"process": proc, "tailnet": tn} {
		if s := p.CollectionStats(); s.ScheduledAttempts != 0 || s.SnapshotsCollected != 0 || s.TerminalAttempts != 0 {
			t.Errorf("%s provider collection stats = %+v, want no collection at all", name, s)
		}
	}
}

func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
