package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/credreload"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// #362's most common rotation is a Grafana Cloud token file. It cannot be passed
// through as a header value — it has to be combined with the instance ID into a
// Basic credential — so it is watched under an internal sentinel key and
// transformed on the way out. These tests pin both halves of that: the transform
// happens, and the sentinel never escapes as a real header.

func writeTokenFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOTLPCredSources_WatchesGrafanaCloudTokenFile(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.OTLP.GrafanaCloud.TokenFile = writeTokenFile(t, dir, "token", "tok1\n")

	src := otlpCredSources(cfg)
	if !src.WatchesAnything() {
		t.Fatal("a configured grafana_cloud.token_file must be watched; otherwise rotating " +
			"the credential every deployment actually rotates still requires a restart")
	}
	if got := src.HeaderFiles[gcTokenSentinel]; got != cfg.OTLP.GrafanaCloud.TokenFile {
		t.Errorf("token file watched as %q, want %q", got, cfg.OTLP.GrafanaCloud.TokenFile)
	}
}

// No token file and no TLS files means nothing to watch, so no reloader is built
// and the static path is kept — the "costs nothing when unused" guarantee.
func TestOTLPCredSources_NothingConfiguredWatchesNothing(t *testing.T) {
	if otlpCredSources(config.Default()).WatchesAnything() {
		t.Error("a default config watches a file; it should stay on the static path")
	}
}

func TestApplyDynamicOTLPCredentials_RotatesGrafanaCloudBasicAuth(t *testing.T) {
	dir := t.TempDir()
	tokenPath := writeTokenFile(t, dir, "token", "tok1\n")

	r, err := credreload.New(credreload.Options{
		Sources: credreload.Sources{HeaderFiles: map[string]string{gcTokenSentinel: tokenPath}},
	})
	if err != nil {
		t.Fatalf("credreload.New: %v", err)
	}

	var opts telemetry.Options
	applyDynamicOTLPCredentials(&opts, r, map[string]string{"X-Scope-OrgID": "1"}, "12345")
	if opts.DynamicHeaders == nil {
		t.Fatal("DynamicHeaders not installed for a watched token file")
	}

	want := func(tok string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte("12345:"+tok))
	}

	h := opts.DynamicHeaders()
	if got := h["Authorization"]; got != want("tok1") {
		t.Errorf("Authorization = %q, want %q", got, want("tok1"))
	}
	// The static header must survive alongside the file-backed one: they carry
	// different things, and replacing rather than layering would silently drop a
	// configured tenant header.
	if got := h["X-Scope-OrgID"]; got != "1" {
		t.Errorf("static header X-Scope-OrgID = %q, want %q", got, "1")
	}
	// The sentinel is an internal carrier, not a header. Leaking it would put the
	// raw token on the wire under a second, unauthenticated header name.
	if _, ok := h[gcTokenSentinel]; ok {
		t.Errorf("internal sentinel %q leaked into the outbound headers: %v", gcTokenSentinel, h)
	}
	for k, v := range h {
		if k != "Authorization" && strings.Contains(v, "tok1") {
			t.Errorf("raw token appears in header %q = %q", k, v)
		}
	}

	// Rotate the file and reload: the next call must produce the NEW credential
	// without anything being reconstructed.
	if err := os.WriteFile(tokenPath, []byte("tok2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload after rotation: %v", err)
	}
	if got := opts.DynamicHeaders()["Authorization"]; got != want("tok2") {
		t.Errorf("after rotation Authorization = %q, want %q", got, want("tok2"))
	}
}

// A nil reloader must leave Options untouched, so a deployment with no watched
// file keeps the exact static construction.
func TestApplyDynamicOTLPCredentials_NilReloaderIsNoOp(t *testing.T) {
	var opts telemetry.Options
	applyDynamicOTLPCredentials(&opts, nil, map[string]string{"Authorization": "Basic x"}, "1")
	if opts.DynamicHeaders != nil || opts.DynamicTLSConfig != nil {
		t.Error("a nil reloader installed dynamic credential funcs")
	}
}

// An enabled poller uses the configured interval; a disabled one returns 0, which
// credreload documents as "no poller" — Reload() can still be driven explicitly.
func TestPollInterval(t *testing.T) {
	cfg := config.Default()
	if got := pollInterval(cfg.OTLP.CredentialReload); got != 0 {
		t.Errorf("disabled poller interval = %v, want 0", got)
	}
	cfg.OTLP.CredentialReload.Enabled = true
	if got := pollInterval(cfg.OTLP.CredentialReload); got != cfg.OTLP.CredentialReload.Interval.D() {
		t.Errorf("enabled poller interval = %v, want %v", got, cfg.OTLP.CredentialReload.Interval.D())
	}
}

func TestReceiverPollIntervalUsesDefaultCadence(t *testing.T) {
	cfg := config.Default()
	if got, want := receiverPollInterval(cfg.OTLP.CredentialReload), 30*time.Second; got != want {
		t.Fatalf("receiver poll interval = %v, want default cadence %v", got, want)
	}
}

func TestReceiverCredentialReloadersRotateBothSecretFiles(t *testing.T) {
	dir := t.TempDir()
	streamPath := writeTokenFile(t, dir, "stream-token", "stream-one\n")
	webhookPath := writeTokenFile(t, dir, "webhook-secret", "webhook-one\n")
	cfg := config.Default()
	cfg.Streaming.TokenFile = streamPath
	cfg.Webhook.SecretFile = webhookPath

	reloaders, err := newCredReloaders(cfg, nil)
	if err != nil {
		t.Fatalf("newCredReloaders: %v", err)
	}
	if reloaders.streaming == nil || reloaders.webhook == nil {
		t.Fatalf("receiver reloaders = %#v, want both streaming and webhook watchers", reloaders)
	}
	streamToken := reloaders.streamTokenProvider(streamPath)
	webhookSecret := reloaders.webhookSecretProvider(webhookPath)
	if streamToken == nil || webhookSecret == nil {
		t.Fatal("receiver file providers are nil")
	}
	if got := streamToken(); got != "stream-one" {
		t.Errorf("initial stream token = %q, want stream-one", got)
	}
	if got := webhookSecret(); got != "webhook-one" {
		t.Errorf("initial webhook secret = %q, want webhook-one", got)
	}

	if err := os.WriteFile(streamPath, []byte("stream-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(webhookPath, []byte("webhook-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloaders.streaming.reloader.Reload(); err != nil {
		t.Fatalf("streaming Reload: %v", err)
	}
	if err := reloaders.webhook.reloader.Reload(); err != nil {
		t.Fatalf("webhook Reload: %v", err)
	}
	if got := streamToken(); got != "stream-two" {
		t.Errorf("rotated stream token = %q, want stream-two", got)
	}
	if got := webhookSecret(); got != "webhook-two" {
		t.Errorf("rotated webhook secret = %q, want webhook-two", got)
	}
}
