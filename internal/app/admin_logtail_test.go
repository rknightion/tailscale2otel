package app

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestSupportBundleLogTailIsBoundedAndOldestFirst(t *testing.T) {
	var live bytes.Buffer
	logger := withSupportBundleLogTail(slog.New(slog.NewTextHandler(&live, nil)), 2, 32<<10)
	logger.Info("first")
	logger.With("component", "admin").Info("second")
	logger.Info("third")

	lines := supportBundleLogTail(logger)
	if len(lines) != 2 {
		t.Fatalf("tail length = %d, want 2", len(lines))
	}
	if strings.Contains(strings.Join(lines, "\n"), "first") {
		t.Errorf("bounded tail retained evicted first record: %q", lines)
	}
	if !strings.Contains(lines[0], `"msg":"second"`) || !strings.Contains(lines[0], `"component":"admin"`) {
		t.Errorf("first retained JSON log = %q, want second record with inherited attr", lines[0])
	}
	if !strings.Contains(lines[1], `"msg":"third"`) {
		t.Errorf("second retained JSON log = %q, want third record", lines[1])
	}
	if !strings.Contains(live.String(), "first") || !strings.Contains(live.String(), "third") {
		t.Errorf("live logger output was disturbed: %q", live.String())
	}
}

func TestSupportBundleLogTailUsesSlogSecretRedaction(t *testing.T) {
	const secret = "tail-secret-canary"
	logger := withSupportBundleLogTail(slog.New(slog.NewTextHandler(io.Discard, nil)), 4, 32<<10)
	logger.Info("credential state", "token", config.Secret(secret))

	joined := strings.Join(supportBundleLogTail(logger), "\n")
	if strings.Contains(joined, secret) {
		t.Fatalf("captured log tail leaked config.Secret: %s", joined)
	}
	if !strings.Contains(joined, `"token":"REDACTED"`) {
		t.Fatalf("captured log tail lost slog.LogValuer redaction: %s", joined)
	}
}

func TestSupportBundleLogTailZeroDisablesCapture(t *testing.T) {
	logger := withSupportBundleLogTail(slog.New(slog.NewTextHandler(io.Discard, nil)), 0, 32<<10)
	logger.Info("must not be retained")
	if got := supportBundleLogTail(logger); got != nil {
		t.Fatalf("disabled capture = %q, want nil", got)
	}
}

func TestSupportBundleLogTailBoundsEachJSONLEntry(t *testing.T) {
	const limit = 128
	var live bytes.Buffer
	logger := withSupportBundleLogTail(slog.New(slog.NewTextHandler(&live, nil)), 2, limit)
	logger.Info(strings.Repeat("x", 1024))

	lines := supportBundleLogTail(logger)
	if len(lines) != 1 {
		t.Fatalf("tail length = %d, want 1", len(lines))
	}
	if len(lines[0]) > limit {
		t.Fatalf("captured entry bytes = %d, want <= %d", len(lines[0]), limit)
	}
	if !strings.Contains(lines[0], `"truncated":true`) {
		t.Fatalf("bounded entry = %q, want explicit truncation marker", lines[0])
	}
	if !strings.Contains(live.String(), strings.Repeat("x", 128)) {
		t.Fatal("support-bundle bound disturbed the live logger")
	}
}
