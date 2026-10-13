package app

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

// Exercise the real profiler and upload transport: config-map assertions alone
// cannot prove the labels that reach the profiles backend. These modes describe
// process ownership, not per-tailnet attribution of samples in a shared process.
func TestProfilingUploadsOwnership(t *testing.T) {
	const fixtureName = "fixture-alpha.example"
	const otherName = "fixture-beta.example"
	const version = "v-fixture"
	const instance = "fixture-process"
	for _, tc := range []struct {
		name    string
		mode    string
		multi   bool
		wantTag string
	}{
		{name: "off", mode: "off"},
		{name: "name", mode: "name", wantTag: fixtureName},
		// Independently computed SHA-256 prefix of the public fixture name.
		{name: "hashed", mode: "hashed", wantTag: "f32d8f952662"},
		{name: "multi-name", mode: "name", multi: true},
		{name: "multi-hashed", mode: "hashed", multi: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var uploads []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/ingest" {
					t.Errorf("unexpected upload route: %s %s", r.Method, r.URL.Path)
					http.Error(w, "wrong route", http.StatusBadRequest)
					return
				}
				reader, err := r.MultipartReader()
				if err != nil {
					t.Errorf("upload is not multipart: %v", err)
					return
				}
				var profileBytes int
				for {
					part, err := reader.NextPart()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Errorf("read upload part: %v", err)
						return
					}
					n, err := io.Copy(io.Discard, part)
					if err != nil {
						t.Errorf("read profile: %v", err)
						return
					}
					if part.FormName() == "profile" {
						profileBytes += int(n)
					}
				}
				if profileBytes == 0 {
					t.Error("upload contains no profile bytes")
				}
				mu.Lock()
				uploads = append(uploads, r.URL.Query().Get("name"))
				mu.Unlock()
			}))
			defer server.Close()
			// The SDK supports an external ad-hoc destination override; keep every
			// request on the fixture server even in an operator's configured shell.
			t.Setenv("PYROSCOPE_ADHOC_SERVER_ADDRESS", server.URL)
			cfg := config.Default()
			cfg.Tailscale.Tailnet = fixtureName
			if tc.multi {
				cfg.Tailnets = []config.TailnetConfig{{Name: fixtureName}, {Name: otherName}}
			}
			cfg.PIIFilter.TailnetName = false
			cfg.SelfObservability.InstanceID = instance
			cfg.Profiling.MutexProfileFraction = 0
			cfg.Profiling.BlockProfileRate = 0
			cfg.Profiling.Pyroscope.Enabled = true
			cfg.Profiling.Pyroscope.ServerAddress = server.URL
			cfg.Profiling.Pyroscope.UploadRate = config.Duration(time.Hour)
			cfg.Profiling.Pyroscope.TailnetLabel = tc.mode
			cfg.Profiling.Pyroscope.Tags = map[string]string{
				"service_version":     "spoofed-version",
				"service_instance_id": "spoofed-instance",
				"tailscale_tailnet":   "spoofed-tailnet",
				"fixture":             "ownership",
			}
			profiler, err := startProfiling(cfg, version, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = profiler.Stop() }()
			profiler.Flush(true)
			mu.Lock()
			captured := append([]string(nil), uploads...)
			mu.Unlock()
			if len(captured) == 0 {
				t.Fatal("profiler produced no HTTP uploads")
			}
			for _, name := range captured {
				prefix, labels, ok := strings.Cut(name, "{")
				if !ok || prefix != "tailscale2otel" || !strings.HasSuffix(labels, "}") {
					t.Fatalf("invalid uploaded application name: %q", name)
				}
				tags := make(map[string]string)
				for _, pair := range strings.Split(strings.TrimSuffix(labels, "}"), ",") {
					key, value, ok := strings.Cut(pair, "=")
					if !ok {
						t.Fatalf("invalid uploaded label: %q", pair)
					}
					tags[key] = value
				}
				if tags["service_version"] != version || tags["service_instance_id"] != instance || tags["fixture"] != "ownership" {
					t.Fatalf("upload lost or spoofed process identity: %q", name)
				}
				label, exists := tags["tailscale_tailnet"]
				if label != tc.wantTag || exists != (tc.wantTag != "") {
					t.Fatalf("uploaded tailnet label = %q (present %v), want %q", label, exists, tc.wantTag)
				}
				if tc.wantTag != fixtureName && strings.Contains(name, fixtureName) || strings.Contains(name, otherName) || strings.Contains(name, "spoofed-") {
					t.Fatalf("upload leaked raw or spoofed ownership: %q", name)
				}
			}
			t.Logf("HTTP uploads=%d; service=%s; service_version=%s; service_instance_id=%s; tailscale_tailnet=%q; pii_filter.tailnet_name=false", len(captured), serviceName, version, instance, tc.wantTag)
		})
	}
}
