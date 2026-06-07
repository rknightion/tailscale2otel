// Package release provides a cached, fail-open fetcher for an external
// "latest version" string plus version parse/compare helpers, shared by the
// self update-available check (C4) and per-device version-skew metrics (B6).
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// versionRe captures the leading MAJOR.MINOR.PATCH, tolerating a leading "v"
// and any suffix (-t<hash>, -dev<date>, "v0.2.0"). Three components required.
var versionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// Version is a parsed MAJOR.MINOR.PATCH triple.
type Version struct{ Major, Minor, Patch int }

// Parse reduces a raw version string to a Version. Returns ok=false for empty
// or unparseable input (e.g. the "dev" placeholder build).
func Parse(raw string) (Version, bool) {
	m := versionRe.FindStringSubmatch(raw)
	if m == nil {
		return Version{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{Major: maj, Minor: min, Patch: pat}, true
}

// Normalize returns the MAJOR.MINOR.PATCH prefix, or "unknown" if unparseable.
func Normalize(raw string) string {
	v, ok := Parse(raw)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Less reports whether v is an older release than o.
func (v Version) Less(o Version) bool {
	switch {
	case v.Major != o.Major:
		return v.Major < o.Major
	case v.Minor != o.Minor:
		return v.Minor < o.Minor
	default:
		return v.Patch < o.Patch
	}
}

// MinorsBehind reports how many minor releases dev is behind latest, within the
// same major, clamped to >= 0. Cross-major comparison returns 0 (Tailscale uses
// a single-major 1.x scheme, so cross-major skew is not meaningful here).
func MinorsBehind(dev, latest Version) int {
	if dev.Major != latest.Major || latest.Minor <= dev.Minor {
		return 0
	}
	return latest.Minor - dev.Minor
}

// Upstream "latest release" endpoints. Both are public, unauthenticated JSON.
const (
	// GitHubLatestURL is this project's own latest GitHub release (C4).
	GitHubLatestURL = "https://api.github.com/repos/rknightion/tailscale2otel/releases/latest"
	// TailscalePkgsURL is Tailscale's canonical latest-stable manifest (B6).
	TailscalePkgsURL = "https://pkgs.tailscale.com/stable/?mode=json"
)

// Parser extracts a version string from a response body.
type Parser func(body []byte) (string, error)

// ParseGitHubLatest pulls tag_name from a GitHub /releases/latest response.
func ParseGitHubLatest(body []byte) (string, error) {
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", errors.New("github release: empty tag_name")
	}
	return r.TagName, nil
}

// ParseTailscalePkgs pulls Version from pkgs.tailscale.com/stable/?mode=json.
func ParseTailscalePkgs(body []byte) (string, error) {
	var r struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.Version == "" {
		return "", errors.New("tailscale pkgs: empty Version")
	}
	return r.Version, nil
}

// Fetcher periodically fetches a "latest version" from a remote endpoint and
// serves the last successfully fetched value. It is fail-open: a fetch error
// keeps the previous value (or none) and is logged at debug, never returned.
// Safe for concurrent use.
type Fetcher struct {
	name      string
	url       string
	userAgent string
	parse     Parser
	client    *http.Client
	ttl       time.Duration
	logger    *slog.Logger

	mu     sync.RWMutex
	latest string
	ok     bool
}

// NewFetcher builds a Fetcher. logger may be nil (falls back to slog.Default).
func NewFetcher(name, url, userAgent string, parse Parser, client *http.Client, ttl time.Duration, logger *slog.Logger) *Fetcher {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Fetcher{name: name, url: url, userAgent: userAgent, parse: parse, client: client, ttl: ttl, logger: logger}
}

// Latest returns the last successfully fetched version and whether one exists.
func (f *Fetcher) Latest() (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.latest, f.ok
}

// Refresh performs one fetch now, updating the cached value on success only.
func (f *Fetcher) Refresh(ctx context.Context) {
	v, err := f.fetch(ctx)
	if err != nil {
		f.logger.Debug("release check failed (fail-open)", "source", f.name, "url", f.url, "error", err)
		return
	}
	f.mu.Lock()
	f.latest, f.ok = v, true
	f.mu.Unlock()
}

func (f *Fetcher) fetch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: status %d", f.url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	return f.parse(body)
}

// Run refreshes immediately, then every ttl until ctx is canceled. Intended to
// be started as a goroutine.
func (f *Fetcher) Run(ctx context.Context) {
	f.Refresh(ctx)
	t := time.NewTicker(f.ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.Refresh(ctx)
		}
	}
}
