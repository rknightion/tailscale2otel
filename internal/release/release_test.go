package release_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/release"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want release.Version
		ok   bool
	}{
		{"v0.2.0", release.Version{Major: 0, Minor: 2, Patch: 0}, true},
		{"1.98.4", release.Version{Major: 1, Minor: 98, Patch: 4}, true},
		{"1.98.4-t01c6b9661", release.Version{Major: 1, Minor: 98, Patch: 4}, true},
		{"dev", release.Version{}, false},
		{"", release.Version{}, false},
		{"1.2", release.Version{}, false},
	}
	for _, c := range cases {
		got, ok := release.Parse(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v,%v want %+v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMinorsBehind(t *testing.T) {
	latest := release.Version{Major: 1, Minor: 98, Patch: 4}
	cases := []struct {
		dev  release.Version
		want int
	}{
		{release.Version{Major: 1, Minor: 98, Patch: 4}, 0}, // current
		{release.Version{Major: 1, Minor: 98, Patch: 2}, 0}, // patch-only drift not counted
		{release.Version{Major: 1, Minor: 95, Patch: 0}, 3}, // 3 minors behind
		{release.Version{Major: 1, Minor: 99, Patch: 0}, 0}, // ahead -> 0
		{release.Version{Major: 0, Minor: 1, Patch: 0}, 0},  // cross-major -> 0
	}
	for _, c := range cases {
		if got := release.MinorsBehind(c.dev, latest); got != c.want {
			t.Errorf("MinorsBehind(%+v) = %d want %d", c.dev, got, c.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	if got := release.Normalize("v1.98.4-t01"); got != "1.98.4" {
		t.Errorf("Normalize = %q want 1.98.4", got)
	}
	if got := release.Normalize("dev"); got != "unknown" {
		t.Errorf("Normalize(dev) = %q want unknown", got)
	}
}

func TestParseGitHubLatest(t *testing.T) {
	body := []byte(`{"tag_name":"v0.2.0","name":"v0.2.0","draft":false,"prerelease":false}`)
	got, err := release.ParseGitHubLatest(body)
	if err != nil || got != "v0.2.0" {
		t.Fatalf("ParseGitHubLatest = %q,%v want v0.2.0,nil", got, err)
	}
	if _, err := release.ParseGitHubLatest([]byte(`{"tag_name":""}`)); err == nil {
		t.Error("ParseGitHubLatest empty tag: want error")
	}
}

func TestParseTailscalePkgs(t *testing.T) {
	body := []byte(`{"Version":"1.98.4","TarballsVersion":"1.98.4","MacZipsVersion":"1.98.5"}`)
	got, err := release.ParseTailscalePkgs(body)
	if err != nil || got != "1.98.4" {
		t.Fatalf("ParseTailscalePkgs = %q,%v want 1.98.4,nil", got, err)
	}
	if _, err := release.ParseTailscalePkgs([]byte(`{}`)); err == nil {
		t.Error("ParseTailscalePkgs empty Version: want error")
	}
}

func newTestFetcher(t *testing.T, url string) *release.Fetcher {
	t.Helper()
	return release.NewFetcher("test", url, "ua/1",
		release.ParseTailscalePkgs, &http.Client{}, time.Hour, slog.New(slog.DiscardHandler))
}

func TestFetcherCachesLastGood(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"Version":"1.98.4"}`)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv.URL)

	// Before any fetch: no value.
	if v, ok := f.Latest(); ok || v != "" {
		t.Fatalf("Latest before fetch = %q,%v want \"\",false", v, ok)
	}

	f.Refresh(context.Background())
	if v, ok := f.Latest(); !ok || v != "1.98.4" {
		t.Fatalf("Latest after fetch = %q,%v want 1.98.4,true", v, ok)
	}

	// A failing fetch must NOT clobber the last-known-good value (fail-open).
	fail.Store(true)
	f.Refresh(context.Background())
	if v, ok := f.Latest(); !ok || v != "1.98.4" {
		t.Fatalf("Latest after failed refresh = %q,%v want 1.98.4,true", v, ok)
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls = %d want 2", calls.Load())
	}
}
