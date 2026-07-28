package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Version pins that describe "the current release" rot silently, because
// nothing fails when they fall behind — the chart still installs, Compose still
// starts, they just run something older than the release they claim to track.
// The chart's appVersion has already done this twice: a comment in Chart.yaml
// records correcting it from a long-stale 0.1.0 that "tracked nothing", and it
// had drifted again to 2.0.2 against a 3.0.0 release when #335 was picked up.
//
// Both pins are now maintained by release-please `extra-files`, and this test is
// the backstop for that wiring. The timing is the same one that makes
// TestModulePathMatchesReleaseVersion catchable: .release-please-manifest.json
// is bumped BY THE RELEASE PR, so if extra-files ever stops updating a pin, the
// mismatch appears on that branch — the one PR that must not merge unnoticed.
func releaseVersion(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".release-please-manifest.json"))
	if err != nil {
		t.Fatalf("read release-please manifest: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse release-please manifest: %v", err)
	}
	v, ok := manifest["."]
	if !ok || v == "" {
		t.Fatalf("release-please manifest has no root (\".\") entry: %v", manifest)
	}
	return v
}

var composeImageRe = regexp.MustCompile(`(?m)^\s*image:\s*ghcr\.io/rknightion/tailscale2otel:\$\{TS2OTEL_VERSION:-([^}]+)\}`)

// TestComposeImageTagMatchesRelease pins the Compose default image tag.
//
// The base compose file used to carry BOTH a `build:` section and
// `image: ...:latest`, so `up` and `up --build` ran different things and the
// shipped header disagreed with the docs about which to use (#335). `latest` is
// also not a predictable default: it moves under a running deployment.
func TestComposeImageTagMatchesRelease(t *testing.T) {
	root := filepath.Join("..", "..")
	want := releaseVersion(t, root)

	raw, err := os.ReadFile(filepath.Join(root, "deploy", "docker-compose.yaml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	m := composeImageRe.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("deploy/docker-compose.yaml has no " +
			"`image: ghcr.io/rknightion/tailscale2otel:${TS2OTEL_VERSION:-<version>}` line. " +
			"The default tag must be a real released version (not `latest`, which moves under a " +
			"running deployment) and must stay overridable via TS2OTEL_VERSION.")
	}
	if got := string(m[1]); got != want {
		t.Errorf("compose default image tag is %q, but the current release is %q.\n"+
			"release-please `extra-files` should be updating this line — check the "+
			"x-release-please-version annotation on it in deploy/docker-compose.yaml.", got, want)
	}

	// The whole point of the split: no build: in the base file. With both
	// present, `up` pulls a published image and `up --build` silently tags a
	// working tree as that same image, and nothing tells the two apart later.
	if regexp.MustCompile(`(?m)^\s{4}build:`).Match(raw) {
		t.Error("deploy/docker-compose.yaml still has a `build:` section; " +
			"local builds belong in deploy/docker-compose.dev.yaml so `up` is unambiguously " +
			"the published image")
	}
}

var chartAppVersionRe = regexp.MustCompile(`(?m)^appVersion:\s*"([^"]+)"`)

// TestChartAppVersionMatchesRelease pins the chart's appVersion, which is what
// the chart installs by default when image.tag is empty.
func TestChartAppVersionMatchesRelease(t *testing.T) {
	root := filepath.Join("..", "..")
	want := releaseVersion(t, root)

	raw, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "tailscale2otel", "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	m := chartAppVersionRe.FindSubmatch(raw)
	if m == nil {
		t.Fatal("Chart.yaml has no quoted appVersion line")
	}
	if got := string(m[1]); got != want {
		t.Errorf("chart appVersion is %q, but the current release is %q.\n"+
			"appVersion is the image tag the chart installs when image.tag is empty, so a stale "+
			"value silently deploys an old build. release-please `extra-files` should be updating "+
			"it — check the x-release-please-version annotation in Chart.yaml.", got, want)
	}
}
