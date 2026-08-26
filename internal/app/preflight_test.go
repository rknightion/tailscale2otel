package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

func TestRunPrometheusOnceReturnsProcessGathererWithoutStartingAListener(t *testing.T) {
	gatherer := prometheus.NewRegistry()
	a := &App{promGatherer: gatherer}
	results, got := a.RunPrometheusOnce(context.Background())
	if len(results) != 0 {
		t.Errorf("RunPrometheusOnce results = %v, want no results from an App with no runtimes", results)
	}
	if got != gatherer {
		t.Errorf("RunPrometheusOnce gatherer = %T %p, want process gatherer %T %p", got, got, gatherer, gatherer)
	}
	if a.metricsSrv != nil {
		t.Error("RunPrometheusOnce started a listener")
	}
}

// preflightTestConfig returns a config that would, unmodified, start every
// listener/durable side effect -preflight/-once must never trigger: admin,
// prometheus, streaming, webhook, and the ingress WAL, plus a file-backed
// checkpoint and continuous profiling. It targets provider=headscale at
// serverURL (a fake control plane) so it can drive app.New — the real
// construction path, not the newApp test seam — without any real network
// call: unlike Tailscale, Headscale's base URL comes straight from config
// (internal/config has no equivalent knob for the Tailscale API, which is why
// the rest of this package's tests use the newApp seam instead). Only the
// devices collector is enabled, so the fake server needs to answer exactly
// one endpoint (GET /api/v1/node).
func preflightTestConfig(t *testing.T, serverURL, checkpointPath string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Provider = "headscale"
	cfg.Headscale.URL = serverURL
	cfg.Headscale.APIKey = "k"

	cfg.Collectors.Devices.Enabled = true
	cfg.Collectors.Users.Enabled = false
	cfg.Collectors.Keys.Enabled = false
	cfg.Collectors.Settings.Enabled = false
	cfg.Collectors.Acl.Enabled = false
	cfg.Collectors.Dns.Enabled = false
	cfg.Collectors.Contacts.Enabled = false
	cfg.Collectors.Webhooks.Enabled = false
	cfg.Collectors.PostureIntegrations.Enabled = false
	cfg.Collectors.LogStream.Enabled = false
	cfg.Collectors.Services.Enabled = false
	cfg.Collectors.NodeMetrics.Enabled = false
	cfg.Collectors.OAuthApps.Enabled = false
	cfg.Collectors.Flowlogs.Enabled = false
	cfg.Collectors.Auditlogs.Enabled = false

	cfg.Admin.Enabled = true
	cfg.Admin.Listen = "127.0.0.1:0"
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Listen = "127.0.0.1:0"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Path = "/hec"
	cfg.Webhook.Enabled = true
	cfg.Webhook.Listen = "127.0.0.1:0"
	cfg.Webhook.Path = "/hook"
	cfg.Webhook.Secret = config.Secret("s")
	cfg.IngressWAL.Enabled = true
	cfg.IngressWAL.Directory = t.TempDir()

	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = checkpointPath

	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "http://127.0.0.1:1" // never dialed: New() would start pushing if left on

	return cfg
}

// methodRecordingHeadscaleServer answers GET /api/v1/node with an empty node
// list (so the devices collector succeeds with zero devices) and records the
// HTTP method of every request it receives, so a test can assert every call
// the preflight path makes is a read (see TestRunOnce_NoNonGETRequests).
func methodRecordingHeadscaleServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

// buildPreflightApp is the shared construction path for the tests below: it
// applies PrepareConfig exactly as cmd/tailscale2otel/preflight.go does for
// -preflight (forceMemoryCheckpoint and disablePyroscope both true), builds
// the App via the real app.New, and registers cleanup via t.Cleanup so a
// forgotten Close() in a test body can't leak into another test.
func buildPreflightApp(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	prepped := PrepareConfig(cfg, true)
	opt := WithTelemetryOverride(func(o telemetry.Options) telemetry.Options {
		o.Protocol = "stdout"
		o.StdoutWriter = discardWriter{}
		return o
	})
	a, err := New(context.Background(), prepped, "vtest", nil, opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Close(ctx)
	})
	return a
}

// discardWriter is a minimal io.Writer that throws everything away, used so
// the "stdout" telemetry protocol never actually writes anywhere observable
// during a test.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestPrepareConfig_ForcesListenersCheckpointAndProfilingOff is the negative-
// testable unit check behind PrepareConfig itself, independent of app.New:
// every field it is documented to force off actually changes, and the input
// cfg is never mutated (PrepareConfig must return a COPY).
func TestPrepareConfig_ForcesListenersCheckpointAndExternalWritersOff(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Enabled = true
	cfg.Prometheus.Enabled = true
	cfg.Streaming.Enabled = true
	cfg.Webhook.Enabled = true
	cfg.IngressWAL.Enabled = true
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = "/tmp/should-not-matter.json"
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.GrafanaAnnotations.URL = "http://127.0.0.1:1"
	cfg.GrafanaAnnotations.Token = "must-not-be-used"

	out := PrepareConfig(cfg, true)

	for name, got := range map[string]bool{
		"Admin.Enabled":      out.Admin.Enabled,
		"Prometheus.Enabled": out.Prometheus.Enabled,
		"Streaming.Enabled":  out.Streaming.Enabled,
		"Webhook.Enabled":    out.Webhook.Enabled,
		"IngressWAL.Enabled": out.IngressWAL.Enabled,
	} {
		if got {
			t.Errorf("%s = true, want false after PrepareConfig", name)
		}
	}
	if out.Checkpoint.Store != "memory" {
		t.Errorf("Checkpoint.Store = %q, want memory", out.Checkpoint.Store)
	}
	if out.Profiling.Pyroscope.Enabled {
		t.Error("Profiling.Pyroscope.Enabled = true, want false")
	}
	if out.GrafanaAnnotations.Enabled() {
		t.Error("GrafanaAnnotations remains enabled, want preflight to suppress its startup POST")
	}

	// The input cfg must be untouched: PrepareConfig returns a copy.
	if !cfg.Admin.Enabled || !cfg.Prometheus.Enabled || !cfg.Streaming.Enabled || !cfg.Webhook.Enabled || !cfg.IngressWAL.Enabled {
		t.Fatal("PrepareConfig mutated the caller's cfg in place")
	}
	if !cfg.GrafanaAnnotations.Enabled() {
		t.Fatal("PrepareConfig mutated the caller's GrafanaAnnotations in place")
	}
	if cfg.Checkpoint.Store != "file" {
		t.Fatalf("PrepareConfig mutated the caller's Checkpoint.Store to %q", cfg.Checkpoint.Store)
	}

	// forceMemoryCheckpoint=false / disablePyroscope=false (the -once shape)
	// must leave both alone — this is what makes the two bools load-bearing
	// rather than dead parameters.
	onceOut := PrepareConfig(cfg, false)
	if onceOut.Checkpoint.Store != "file" {
		t.Errorf("-once shape: Checkpoint.Store = %q, want file (unchanged)", onceOut.Checkpoint.Store)
	}
	if !onceOut.Profiling.Pyroscope.Enabled {
		t.Error("-once shape: Profiling.Pyroscope.Enabled = false, want true (unchanged)")
	}
	// Listeners/WAL are forced off unconditionally in both shapes.
	if onceOut.Admin.Enabled || onceOut.IngressWAL.Enabled {
		t.Error("-once shape must still force listeners/WAL off")
	}
}

// TestNew_PreparedConfig_NoListenersOrWAL is the safety-property test: with
// PrepareConfig applied, New() (the real production construction path, not
// the newApp test seam) never builds an admin server, a Prometheus server, a
// stream/webhook receiver, or a live ingress WAL — even though cfg enables
// every one of them. The un-prepared counterpart proves this isn't
// vacuously true: without PrepareConfig, the same cfg DOES build all of them.
func TestNew_PreparedConfig_NoListenersOrWAL(t *testing.T) {
	srv, _ := methodRecordingHeadscaleServer(t)
	cfg := preflightTestConfig(t, srv.URL, filepath.Join(t.TempDir(), "checkpoint.json"))

	a := buildPreflightApp(t, cfg)
	if a.adminSrv != nil {
		t.Error("adminSrv should be nil under a prepared config")
	}
	if a.metricsSrv != nil {
		t.Error("metricsSrv should be nil under a prepared config")
	}
	if a.streamSrv != nil {
		t.Error("streamSrv should be nil under a prepared config")
	}
	if a.webhookSrv != nil {
		t.Error("webhookSrv should be nil under a prepared config")
	}
	if a.ingressWAL == nil || a.ingressWAL.wal != nil {
		t.Error("ingressWAL should be the disabled (nil-wal) coordinator under a prepared config")
	}

	// Negative check: the SAME cfg, unprepared, must build all four — proving
	// the assertions above are actually exercising suppression, not a cfg
	// that never enabled these in the first place.
	raw, err := New(context.Background(), cfg, "vtest", nil)
	if err != nil {
		t.Fatalf("New (unprepared): %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = raw.Close(ctx)
	}()
	if raw.adminSrv == nil || raw.metricsSrv == nil || raw.streamSrv == nil || raw.webhookSrv == nil {
		t.Fatal("unprepared cfg should build every listener; PrepareConfig's effect could not be observed otherwise")
	}
	if raw.ingressWAL == nil || raw.ingressWAL.wal == nil {
		t.Fatal("unprepared cfg should build a live ingress WAL; PrepareConfig's effect could not be observed otherwise")
	}
}

// TestCheckpointStore_MemoryNeverTouchesDisk pins the "-preflight never
// writes the on-disk checkpoint file" property at the level that actually
// matters: whatever RunOnce does or doesn't do with a CheckpointStore, a
// store built from Checkpoint.Store=="memory" (what PrepareConfig forces)
// cannot write to disk — collector.NewMemoryStore holds state in a process
// map with no path at all. checkpointStore is app.go's own real construction
// function (used by New()), not a reimplementation.
func TestCheckpointStore_MemoryNeverTouchesDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cfg := config.Default()
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = path

	prepped := PrepareConfig(cfg, true)
	if prepped.Checkpoint.Store != "memory" {
		t.Fatalf("Checkpoint.Store = %q, want memory", prepped.Checkpoint.Store)
	}

	store, outcome, err := checkpointStore(prepped, nil)
	if err != nil {
		t.Fatalf("checkpointStore: %v", err)
	}
	if outcome.Kind != "memory" {
		t.Fatalf("outcome.Kind = %q, want memory", outcome.Kind)
	}
	if err := store.Set("devices", time.Now()); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file was created at %s (or Stat errored unexpectedly: %v); the memory store must never touch disk", path, err)
	}

	// Negative check: without forceMemoryCheckpoint, the SAME cfg resolves to
	// a real file store — proving the assertion above is not vacuous.
	rawStore, rawOutcome, err := checkpointStore(cfg, nil)
	if err != nil {
		t.Fatalf("checkpointStore (unprepared): %v", err)
	}
	if rawOutcome.Kind != "file" {
		t.Fatalf("unprepared outcome.Kind = %q, want file", rawOutcome.Kind)
	}
	if err := rawStore.Set("devices", time.Now()); err != nil {
		t.Fatalf("Set (unprepared): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unprepared file store should have created %s: %v", path, err)
	}
}

// TestRunOnce_NoNonGETRequests pins "no control-plane mutation": running one
// full RunOnce cycle against a prepared, headscale-backed App issues only GET
// requests to the fake control plane.
func TestRunOnce_NoNonGETRequests(t *testing.T) {
	srv, methods := methodRecordingHeadscaleServer(t)
	cfg := preflightTestConfig(t, srv.URL, filepath.Join(t.TempDir(), "checkpoint.json"))

	a := buildPreflightApp(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := a.RunOnce(ctx)

	if len(results) != 1 {
		t.Fatalf("results = %d entries, want 1 (devices only)", len(results))
	}
	if !results[0].OK {
		t.Fatalf("devices result = %+v, want OK", results[0])
	}
	for _, m := range methods() {
		if m != http.MethodGet {
			t.Errorf("preflight issued a %s request; want GET-only (no control-plane mutation)", m)
		}
	}
	if len(methods()) == 0 {
		t.Fatal("no requests recorded at all; the fake server was never reached, so this test proves nothing")
	}
}

// TestRunOnce_PreflightWithoutExport_DoesNotExport verifies that -preflight
// without -preflight-export (a "stdout, discarded" telemetry override) never
// hands anything to a real exporter — asserted here by using
// telemetrytest.Recorder as the "export destination" a real Options.Protocol
// would otherwise reach, and confirming it stays empty. Since telemetry.New
// (ProviderSet) does not accept a telemetrytest.Recorder directly, this test
// instead pins the documented mechanism: WithTelemetryOverride forces
// Protocol="stdout" with a StdoutWriter that is never os.Stdout, so New()'s
// normal exporter construction can never reach the configured OTLP endpoint.
// (End-to-end proof that stdout output is actually discarded, not merely
// redirected, lives in buildPreflightApp: every test in this file uses a
// discardWriter and none observes its output.)
func TestRunOnce_PreflightWithoutExport_DoesNotExport(t *testing.T) {
	srv, _ := methodRecordingHeadscaleServer(t)
	cfg := preflightTestConfig(t, srv.URL, filepath.Join(t.TempDir(), "checkpoint.json"))
	cfg.OTLP.Protocol = "grpc"
	cfg.OTLP.Endpoint = "127.0.0.1:1" // would refuse a real dial if ever used

	a := buildPreflightApp(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := a.RunOnce(ctx)
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("results = %+v, want one OK result (proves the run itself succeeded despite the bogus grpc endpoint, i.e. it was never dialed)", results)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := a.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v (a real dial to the bogus grpc endpoint would surface here)", err)
	}
}

// blockingCollector never returns from Collect, regardless of ctx — it exists
// to prove RunOnce's own deadline (not collector cooperation) is what bounds
// -preflight/-once, per the "deadline is honored" safety property.
type blockingCollector struct{ started chan struct{} }

func (blockingCollector) Name() string                   { return "blocker" }
func (blockingCollector) DefaultInterval() time.Duration { return time.Minute }
func (c blockingCollector) Collect(context.Context, telemetry.Emitter) error {
	close(c.started)
	select {} // deliberately ignores ctx
}

// TestRunOnceEntry_HonorsDeadline deliberately does NOT use testing/synctest:
// runOnceEntry's whole point is to ABANDON a collector goroutine that ignores
// ctx (see RunOnce's doc comment) rather than wait for it, and synctest's
// bubble panics with "deadlock: main bubble goroutine has exited but blocked
// goroutines remain" if any goroutine it spawned is still durably blocked
// when the bubble function returns — which a deliberately-abandoned,
// ctx-ignoring goroutine always is, by construction. A real, short timeout is
// the correct tool for this specific case.
func TestRunOnceEntry_HonorsDeadline(t *testing.T) {
	const budget = 100 * time.Millisecond

	entry := collector.Entry{Collector: blockingCollector{started: make(chan struct{})}}
	rec := telemetrytest.New()

	// The deadline clock starts at WithTimeout, so `start` must be taken no
	// later than that. Building entry/rec in between costs real time that is
	// then subtracted from the measured budget: on a loaded CI runner that
	// read 99.75ms against a 100ms budget and failed a correct implementation.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	res := runOnceEntry(ctx, "acme", entry, rec.Emitter())
	elapsed := time.Since(start)

	if res.OK {
		t.Fatalf("result = %+v, want a failed (timed out) result", res)
	}
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want context.DeadlineExceeded", res.Err)
	}
	if elapsed < budget {
		t.Fatalf("elapsed = %v, want >= %v (the collector must not have returned early)", elapsed, budget)
	}
	if elapsed > 5*budget {
		t.Fatalf("elapsed = %v, want roughly %v (not a hang)", elapsed, budget)
	}
	if res.AuthFailure {
		t.Fatal("a timeout must not be classified as an auth failure")
	}
}

// TestIsAuthFailure_ClassifiesOnly401 pins the exact boundary documented on
// isAuthFailure: a 401 tsapi.StatusError is an auth failure; a 403 is not
// (this repo already uses 403 for "feature not enabled on this plan" in
// several collectors; conflating it with a bad credential would misdirect an
// operator into rotating a working one).
func TestIsAuthFailure_ClassifiesOnly401(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"401 status error", &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/x", Code: http.StatusUnauthorized, Body: "unauthorized"}, true},
		{"403 status error", &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/x", Code: http.StatusForbidden, Body: "forbidden"}, false},
		{"404 status error", &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/x", Code: http.StatusNotFound, Body: "not found"}, false},
		{"401 wrapped status error", fmt.Errorf("collect: %w", &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/x", Code: http.StatusUnauthorized}), true},
		{
			"oauth 401 retrieve error",
			&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}},
			true,
		},
		{
			"oauth 403 retrieve error",
			&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusForbidden}},
			false,
		},
		// Headscale reaches the same classification through its own status type.
		// Until hsapi carried one, a revoked Headscale API key was reported as a
		// collector failure (exit 4, "the credential works, a collector doesn't")
		// for every Headscale deployment (#311).
		{"headscale 401", &hsapi.StatusError{Path: "/api/v1/node", Code: http.StatusUnauthorized, Body: "unauthorized"}, true},
		{"headscale 403", &hsapi.StatusError{Path: "/api/v1/node", Code: http.StatusForbidden, Body: "forbidden"}, false},
		{"headscale 401 wrapped", fmt.Errorf("collect: %w", &hsapi.StatusError{Path: "/api/v1/node", Code: http.StatusUnauthorized}), true},
	}
	for _, tc := range cases {
		if got := isAuthFailure(tc.err); got != tc.want {
			t.Errorf("%s: isAuthFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The ACL collector's Validate issues this repo's ONE non-GET control-plane
// request: POST /tailnet/{t}/acl/validate (see internal/tsapi/aclvalidate.go).
// It mutates nothing, but -preflight is sold as a run an operator can point at
// production knowing it only reads — and "only reads, except for one POST you
// have to know about" is not that guarantee (#311). It stays on for -once,
// which is a real cycle and makes no such promise.
func TestPrepareConfig_PreflightSuppressesTheOneNonGETRequest(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.Acl.Enabled = true
	cfg.Collectors.Acl.Validate = true

	if got := PrepareConfig(cfg, true); got.Collectors.Acl.Validate {
		t.Error("preflight must force collectors.acl.validate off: it is the only non-GET request the exporter makes")
	}
	if got := PrepareConfig(cfg, false); !got.Collectors.Acl.Validate {
		t.Error("-once runs a real cycle and must leave collectors.acl.validate alone")
	}
	if !cfg.Collectors.Acl.Validate {
		t.Error("PrepareConfig mutated the caller's cfg in place")
	}
}
