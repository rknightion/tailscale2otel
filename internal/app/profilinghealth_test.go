package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// canaries that must never reach a metric, the status page, or a log line.
const (
	uploadBodyCanary   = "PYROBODY-leak-canary-do-not-emit"
	uploadSecretCanary = "PYROSECRET-leak-canary-do-not-emit"
)

// postTo builds a request shaped like the one pyroscope-go's remote uploader
// sends, so the health client under test sees a realistic request.
func postTo(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"/ingest", strings.NewReader("profile-bytes"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data")
	return req
}

// TestProfilingUploadHealth_SuccessFailureThenRecovery drives the health client
// against a fake server that returns 200, then a streak of 500s, then 200 again,
// and asserts the tracker and the emitted metrics both show the streak AND its
// recovery. Recovering visibly is the point: a tracker that only ever counts up
// cannot tell an operator that profiles are flowing again.
func TestProfilingUploadHealth_SuccessFailureThenRecovery(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, uploadBodyCanary)
	}))
	defer srv.Close()

	rec := telemetrytest.New()
	health := newProfilingHealth()
	client, err := newProfilingUploadClient(pyroscopeTransportOptions{}, health, rec.Emitter(), nil)
	if err != nil {
		t.Fatalf("newProfilingUploadClient: %v", err)
	}

	do := func() {
		resp, err := client.Do(postTo(t, srv.URL))
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		_ = err
	}

	// 1 success.
	do()
	snap := health.snapshot()
	if snap.Attempts != 1 || snap.Failures != 0 || snap.ConsecutiveFailures != 0 {
		t.Fatalf("after one 200: %+v, want attempts=1 failures=0 streak=0", snap)
	}
	if snap.LastSuccessAt == "" {
		t.Error("after one 200: LastSuccessAt is empty")
	}
	if !snap.Healthy {
		t.Error("after one 200: Healthy = false")
	}

	// 3 failures -> unhealthy, streak visible, error class recorded.
	status = http.StatusInternalServerError
	do()
	do()
	do()
	snap = health.snapshot()
	if snap.Attempts != 4 || snap.Failures != 3 || snap.ConsecutiveFailures != 3 {
		t.Fatalf("after three 500s: %+v, want attempts=4 failures=3 streak=3", snap)
	}
	if snap.LastErrorClass != "unavailable" {
		t.Errorf("LastErrorClass = %q, want unavailable (500)", snap.LastErrorClass)
	}
	if snap.Healthy {
		t.Error("after a 3-failure streak: Healthy = true, want false")
	}
	if snap.LastFailureAt == "" {
		t.Error("LastFailureAt is empty after failures")
	}

	// Recovery: the streak clears and health flips back, but the failure history
	// is retained (an operator needs "recovered after failing", not amnesia).
	status = http.StatusOK
	do()
	snap = health.snapshot()
	if snap.ConsecutiveFailures != 0 {
		t.Errorf("after recovery: streak = %d, want 0", snap.ConsecutiveFailures)
	}
	if !snap.Healthy {
		t.Error("after recovery: Healthy = false, want true")
	}
	if snap.Failures != 3 || snap.LastFailureAt == "" || snap.LastErrorClass != "unavailable" {
		t.Errorf("after recovery the failure history was erased: %+v", snap)
	}

	// The same story must be visible in metrics, not just the status page.
	attempts := rec.MetricPoints(appcatalog.MetricProfilingUploadAttempts)
	if len(attempts) != 1 || attempts[0].Value != 5 {
		t.Errorf("attempts points = %+v, want a single point valued 5", attempts)
	}
	failures := rec.MetricPoints(appcatalog.MetricProfilingUploadFailures)
	if len(failures) != 1 || failures[0].Value != 3 {
		t.Errorf("failures points = %+v, want a single point valued 3", failures)
	}
	if got := failures[0].Attrs[semconv.AttrErrorType]; got != "unavailable" {
		t.Errorf("failures %s = %q, want unavailable", semconv.AttrErrorType, got)
	}
	streak := rec.MetricPoints(appcatalog.MetricProfilingUploadConsecutiveFailures)
	if len(streak) != 1 || streak[0].Value != 0 {
		t.Errorf("consecutive_failures = %+v, want a single point valued 0 after recovery", streak)
	}
	last := rec.MetricPoints(appcatalog.MetricProfilingUploadLastSuccess)
	if len(last) != 1 || last[0].Value <= 0 {
		t.Errorf("last_success = %+v, want a single positive point", last)
	}
	dur := rec.MetricPoints(appcatalog.MetricProfilingUploadDuration)
	if len(dur) != 1 || dur[0].Count != 5 {
		t.Errorf("duration histogram = %+v, want one histogram with 5 observations", dur)
	}
}

// TestProfilingUploadHealth_NoBodyOrCredentialLeak is the PII/secret gate. A
// Pyroscope error response body is echoed verbatim by the upstream SDK into its
// Errorf logger, so the client must strip it before the SDK ever sees it, and
// nothing but the closed error class may reach a metric or the status page.
func TestProfilingUploadHealth_NoBodyOrCredentialLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "unauthorized: token "+uploadBodyCanary+" rejected")
	}))
	defer srv.Close()

	rec := telemetrytest.New()
	health := newProfilingHealth()
	client, err := newProfilingUploadClient(pyroscopeTransportOptions{
		Headers: map[string]string{"X-Team": uploadSecretCanary},
	}, health, rec.Emitter(), nil)
	if err != nil {
		t.Fatalf("newProfilingUploadClient: %v", err)
	}

	resp, err := client.Do(postTo(t, srv.URL))
	if err != nil {
		t.Fatalf("Do returned an error for an HTTP 401: %v (the status must come back as a response)", err)
	}
	// The upstream SDK does io.ReadAll(resp.Body) and formats it into its error
	// message. Whatever it reads must be empty.
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read stripped body: %v", readErr)
	}
	if len(body) != 0 {
		t.Fatalf("response body handed to the SDK = %q, want empty (it is echoed into logs)", body)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401 preserved", resp.StatusCode)
	}

	snap := health.snapshot()
	if snap.LastErrorClass != "unauthenticated" {
		t.Errorf("LastErrorClass = %q, want unauthenticated", snap.LastErrorClass)
	}
	for _, s := range []string{uploadBodyCanary, uploadSecretCanary} {
		for _, p := range rec.MetricPoints(appcatalog.MetricProfilingUploadFailures) {
			for k, v := range p.Attrs {
				if strings.Contains(k, s) || strings.Contains(v, s) {
					t.Errorf("canary %q leaked into a metric attribute %s=%s", s, k, v)
				}
			}
		}
		if strings.Contains(snap.LastErrorClass, s) {
			t.Errorf("canary %q leaked into the status snapshot", s)
		}
	}
}

// TestProfilingUploadHealth_TransportErrorClassOnly proves a transport-level
// failure is reported as a class and never as the wrapped error, which can carry
// the request URL (and therefore anything embedded in it).
func TestProfilingUploadHealth_TransportErrorClassOnly(t *testing.T) {
	rec := telemetrytest.New()
	health := newProfilingHealth()
	client, err := newProfilingUploadClient(pyroscopeTransportOptions{}, health, rec.Emitter(), nil)
	if err != nil {
		t.Fatalf("newProfilingUploadClient: %v", err)
	}
	// A closed listener: connection refused, reported without the address.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := closed.URL
	closed.Close()

	resp, err := client.Do(postTo(t, url))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("Do against a closed listener returned no error")
	}
	if strings.Contains(err.Error(), url) {
		t.Errorf("transport error %q echoes the target URL, want the class only", err)
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("transport error = %q, want it to name the class", err)
	}
	if got := health.snapshot().LastErrorClass; got != "unavailable" {
		t.Errorf("LastErrorClass = %q, want unavailable", got)
	}
}

// TestClassifyProfileUploadStatus checks the status->class mapping and that it
// never escapes the closed set.
func TestClassifyProfileUploadStatus(t *testing.T) {
	closedSet := map[string]bool{}
	for _, c := range appcatalog.ProfilingUploadErrorClasses() {
		closedSet[c] = true
	}
	cases := map[int]string{
		http.StatusUnauthorized:          "unauthenticated",
		http.StatusForbidden:             "unauthenticated",
		http.StatusTooManyRequests:       "rate_limited",
		http.StatusBadRequest:            "invalid",
		http.StatusNotFound:              "invalid",
		http.StatusRequestEntityTooLarge: "invalid",
		http.StatusUnprocessableEntity:   "invalid",
		http.StatusInternalServerError:   "unavailable",
		http.StatusBadGateway:            "unavailable",
		http.StatusServiceUnavailable:    "unavailable",
		http.StatusGatewayTimeout:        "timeout",
		http.StatusTeapot:                "other",
		http.StatusFound:                 "other",
	}
	for code, want := range cases {
		if got := classifyProfileUploadStatus(code); got != want {
			t.Errorf("classifyProfileUploadStatus(%d) = %q, want %q", code, got, want)
		}
	}
	// Exhaustive sweep: nothing in the whole status space escapes the closed set.
	for code := 100; code < 600; code++ {
		if got := classifyProfileUploadStatus(code); !closedSet[got] {
			t.Fatalf("classifyProfileUploadStatus(%d) = %q, outside the closed set", code, got)
		}
	}
}

// TestClassifyProfileUploadError checks the error->class mapping, including the
// TLS class #375's custom-CA/mTLS support makes reachable.
func TestClassifyProfileUploadError(t *testing.T) {
	closedSet := map[string]bool{}
	for _, c := range appcatalog.ProfilingUploadErrorClasses() {
		closedSet[c] = true
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"deadline sentinel", context.DeadlineExceeded, "timeout"},
		{"canceled sentinel", context.Canceled, "canceled"},
		{"wrapped deadline", errors.New("Post \"x\": context deadline exceeded"), "timeout"},
		{"client timeout", errors.New("Client.Timeout exceeded while awaiting headers"), "timeout"},
		{"refused", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), "unavailable"},
		{"dns", errors.New("dial tcp: lookup nope.invalid: no such host"), "unavailable"},
		{"x509 unknown authority", &tls.CertificateVerificationError{
			Err: x509.UnknownAuthorityError{},
		}, "tls"},
		{"tls text", errors.New("remote error: tls: bad certificate"), "tls"},
		{"unknown", errors.New("something entirely unexpected"), "other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyProfileUploadError(c.err)
			if got != c.want {
				t.Errorf("classifyProfileUploadError(%v) = %q, want %q", c.err, got, c.want)
			}
			if got != "" && !closedSet[got] {
				t.Errorf("class %q is outside the closed set", got)
			}
		})
	}
}

// TestPyroscopeConfig_ReservedIdentityTags proves both reserved identity tags are
// present and that a user tag cannot displace either of them. service_version
// was already protected; service_instance_id is the gap #374 closes — without it
// a profile could not be attributed to one process in a multi-replica deployment.
func TestPyroscopeConfig_ReservedIdentityTags(t *testing.T) {
	cfg := config.Default()
	cfg.SelfObservability.InstanceID = "inst-42"
	cfg.Profiling.Pyroscope.Tags = map[string]string{
		"service_version":     "hijacked",
		"service_instance_id": "hijacked",
		"env":                 "lab",
	}
	pc := pyroscopeConfig(cfg, "v9.9.9")
	if pc.Tags["service_version"] != "v9.9.9" {
		t.Errorf("service_version = %q, want v9.9.9 (not user-overridable)", pc.Tags["service_version"])
	}
	if pc.Tags["service_instance_id"] != "inst-42" {
		t.Errorf("service_instance_id = %q, want inst-42 (not user-overridable)", pc.Tags["service_instance_id"])
	}
	if pc.Tags["env"] != "lab" {
		t.Errorf("env = %q, want lab (non-reserved tags pass through)", pc.Tags["env"])
	}
}

// TestPyroscopeConfig_InstanceIDRespectsPIIPolicy checks the instance tag reuses
// the SAME derivation as service.instance.id on the OTLP resource, so the
// hostname PII policy applies to profiles too rather than being bypassed.
func TestPyroscopeConfig_InstanceIDRespectsPIIPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.SelfObservability.InstanceID = ""
	cfg.PIIFilter.Hostnames = false
	pc := pyroscopeConfig(cfg, "v1")
	want := instanceID(cfg)
	if got := pc.Tags["service_instance_id"]; got != want {
		t.Errorf("service_instance_id = %q, want %q (instanceID's PII-aware derivation)", got, want)
	}
}

// TestPyroscopeConfig_HealthClientAttached proves the mapping installs the health
// client, so upload health is recorded by construction rather than by a caller
// remembering to opt in.
func TestPyroscopeConfig_HealthClientAttached(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles.example"
	pc := mustPyroscopeConfigWithUploadClient(t, cfg, "v1")
	if pc.HTTPClient == nil {
		t.Fatal("pyroscope.Config.HTTPClient is nil — upload health would never be recorded")
	}
	if _, ok := pc.HTTPClient.(*profilingUploadClient); !ok {
		t.Fatalf("HTTPClient is %T, want *profilingUploadClient", pc.HTTPClient)
	}
}

// TestReadinessIndependentOfProfilingHealth is the acceptance check that a
// Pyroscope outage never fails the readiness probe. Profiling is a diagnostic
// side-channel; a Kubernetes rollout must not stall because a profiles backend
// is down.
func TestReadinessIndependentOfProfilingHealth(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles.example"
	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}

	// Drive the process-wide tracker deep into an outage.
	prev := profilingHealthState
	profilingHealthState = newProfilingHealth()
	defer func() { profilingHealthState = prev }()
	for range 50 {
		profilingHealthState.observe("unavailable", 0.01)
	}
	if snap := profilingHealthState.snapshot(); snap.Healthy {
		t.Fatal("precondition failed: the tracker should be unhealthy after 50 failures")
	}

	w := httptest.NewRecorder()
	a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/readyz = %d during a total Pyroscope outage, want 200 — profiling must never gate readiness (body %q)",
			w.Code, w.Body.String())
	}

	// And the same must hold for the pure verdict function, so a future caller
	// cannot reintroduce the coupling behind the handler.
	if ready, reason := readinessVerdict(nil, nil); !ready || reason != "" {
		t.Errorf("readinessVerdict = %v/%q during a profiling outage, want ready", ready, reason)
	}
}

// TestProfilingStatusRecoversVisibly checks the status DTO the admin page reads
// tells the recovery story, and carries no credential material.
func TestProfilingStatusRecoversVisibly(t *testing.T) {
	h := newProfilingHealth()
	h.observe("", 0.5)
	h.observe("unauthenticated", 0.2)
	h.observe("unauthenticated", 0.2)
	h.observe("unauthenticated", 0.2)
	down := h.snapshot()
	if down.Healthy || down.ConsecutiveFailures != 3 {
		t.Fatalf("down snapshot = %+v, want unhealthy with streak 3", down)
	}
	h.observe("", 0.3)
	up := h.snapshot()
	if !up.Healthy || up.ConsecutiveFailures != 0 {
		t.Fatalf("recovered snapshot = %+v, want healthy with streak 0", up)
	}
	if up.LastDurationSeconds != 0.3 {
		t.Errorf("LastDurationSeconds = %v, want 0.3", up.LastDurationSeconds)
	}
	if _, err := time.Parse(time.RFC3339, up.LastSuccessAt); err != nil {
		t.Errorf("LastSuccessAt = %q, not RFC3339: %v", up.LastSuccessAt, err)
	}
}

// TestProfilingHealthZeroValueSnapshot pins the pre-first-upload state: no
// timestamps, no error class, and HEALTHY — "has not uploaded yet" must not read
// as a failure on the status page.
func TestProfilingHealthZeroValueSnapshot(t *testing.T) {
	snap := newProfilingHealth().snapshot()
	if snap.Attempts != 0 || snap.Failures != 0 || snap.ConsecutiveFailures != 0 {
		t.Errorf("fresh snapshot counters = %+v, want all zero", snap)
	}
	if snap.LastSuccessAt != "" || snap.LastFailureAt != "" || snap.LastErrorClass != "" {
		t.Errorf("fresh snapshot = %+v, want empty timestamps and class", snap)
	}
	if !snap.Healthy {
		t.Error("fresh snapshot Healthy = false, want true (nothing has failed yet)")
	}
}

// TestStartProfiling_EmitterOptionWiresUploadMetrics exercises the
// withProfilingEmitter option end to end: pyroscopeConfig must hand the supplied
// emitter to the upload client, so the one line app.New adds is the only thing
// standing between the tracker and the five upload metrics.
//
// It drives a real upload through the client the mapping produced, rather than
// inspecting fields, because "the option was stored" and "the metrics are emitted"
// are different claims and only the second one matters.
func TestStartProfiling_EmitterOptionWiresUploadMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prev := profilingHealthState
	profilingHealthState = newProfilingHealth()
	defer func() { profilingHealthState = prev }()

	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = srv.URL

	rec := telemetrytest.New()
	pc := mustPyroscopeConfigWithUploadClient(t, cfg, "v1", withProfilingEmitter(rec.Emitter()))
	client, ok := pc.HTTPClient.(*profilingUploadClient)
	if !ok {
		t.Fatalf("HTTPClient is %T, want *profilingUploadClient", pc.HTTPClient)
	}

	resp, err := client.Do(postTo(t, srv.URL))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if pts := rec.MetricPoints(appcatalog.MetricProfilingUploadAttempts); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("attempts = %+v, want one point valued 1 — the emitter option did not reach the client", pts)
	}
	if pts := rec.MetricPoints(appcatalog.MetricProfilingUploadLastSuccess); len(pts) != 1 || pts[0].Value <= 0 {
		t.Errorf("last_success = %+v, want one positive point", pts)
	}
	// And the process-wide tracker the status page reads saw the same upload.
	if snap := profilingHealthState.snapshot(); snap.Attempts != 1 || snap.Failures != 0 {
		t.Errorf("tracker snapshot = %+v, want one clean attempt", snap)
	}
}

// TestPyroscopeConfig_NoEmitterStillTracks pins the pre-wiring behavior: with no
// emitter the tracker (and therefore the admin page) still works, and only the
// metrics are withheld. Otherwise the un-wired state would look like a total
// regression rather than a partial one.
func TestPyroscopeConfig_NoEmitterStillTracks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	prev := profilingHealthState
	profilingHealthState = newProfilingHealth()
	defer func() { profilingHealthState = prev }()

	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = srv.URL

	pc := mustPyroscopeConfigWithUploadClient(t, cfg, "v1") // no withProfilingEmitter
	client, ok := pc.HTTPClient.(*profilingUploadClient)
	if !ok {
		t.Fatalf("HTTPClient is %T, want *profilingUploadClient", pc.HTTPClient)
	}
	if client.emitter != nil {
		t.Error("emitter is non-nil with no option supplied")
	}
	resp, err := client.Do(postTo(t, srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	snap := profilingHealthState.snapshot()
	if snap.Attempts != 1 || snap.Failures != 1 || snap.LastErrorClass != "unavailable" {
		t.Errorf("tracker snapshot = %+v, want one failed attempt classified unavailable", snap)
	}

	// The status page reads the same tracker and must show it.
	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}
	info := a.profilingInfo()
	if info.PyroscopeUpload == nil {
		t.Fatal("ProfilingInfo.PyroscopeUpload is nil with Pyroscope enabled")
	}
	if info.PyroscopeUpload.Failures != 1 {
		t.Errorf("status upload health = %+v, want Failures=1", info.PyroscopeUpload)
	}
	if info.PyroscopeUpload.LastErrorClass != "unavailable" {
		t.Errorf("status LastErrorClass = %q, want unavailable", info.PyroscopeUpload.LastErrorClass)
	}
}

// TestProfilingInfo_DisabledHasNoUploadSection checks a disabled push agent
// reports no upload health at all, so "not configured" and "configured but never
// uploaded" stay distinguishable on the page.
func TestProfilingInfo_DisabledHasNoUploadSection(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = false
	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}
	if got := a.profilingInfo(); got.PyroscopeUpload != nil {
		t.Errorf("PyroscopeUpload = %+v with Pyroscope disabled, want nil", got.PyroscopeUpload)
	}
}
