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

	"github.com/rknightion/tailscale2otel/v4/internal/release"
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

// TestFetcherSnapshot_NeverAttempted asserts a fresh Fetcher's Snapshot reports
// no value, a zero CheckedAt, and no error class — the admin page's "checking"
// state (#330), distinct from a fetch that has actually failed.
func TestFetcherSnapshot_NeverAttempted(t *testing.T) {
	f := newTestFetcher(t, "http://127.0.0.1:0/never-called")
	snap := f.Snapshot()
	if snap.OK || snap.Latest != "" {
		t.Fatalf("Snapshot before any fetch = %+v, want OK=false Latest=\"\"", snap)
	}
	if !snap.CheckedAt.IsZero() {
		t.Fatalf("CheckedAt before any fetch = %v, want zero", snap.CheckedAt)
	}
	if snap.ErrClass != "" {
		t.Fatalf("ErrClass before any fetch = %q, want empty", snap.ErrClass)
	}
}

// TestFetcherSnapshot_SuccessClearsErrClassAndSetsCheckedAt asserts a
// successful Refresh records CheckedAt and leaves ErrClass empty.
func TestFetcherSnapshot_SuccessClearsErrClassAndSetsCheckedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Version":"1.98.4"}`)
	}))
	defer srv.Close()
	f := newTestFetcher(t, srv.URL)

	before := time.Now()
	f.Refresh(context.Background())
	snap := f.Snapshot()
	if !snap.OK || snap.Latest != "1.98.4" {
		t.Fatalf("Snapshot after success = %+v, want OK=true Latest=1.98.4", snap)
	}
	if snap.ErrClass != "" {
		t.Fatalf("ErrClass after success = %q, want empty", snap.ErrClass)
	}
	if snap.CheckedAt.Before(before) {
		t.Fatalf("CheckedAt = %v, want >= %v", snap.CheckedAt, before)
	}
}

// TestFetcherSnapshot_ClassifiesNetworkError asserts a transport-level failure
// (nothing listening) classifies as "network".
func TestFetcherSnapshot_ClassifiesNetworkError(t *testing.T) {
	f := newTestFetcher(t, "http://127.0.0.1:1/unreachable") // port 1: connection refused
	f.Refresh(context.Background())
	snap := f.Snapshot()
	if snap.OK {
		t.Fatalf("Snapshot after network failure: OK=true, want false")
	}
	if snap.ErrClass != "network" {
		t.Fatalf("ErrClass = %q, want %q", snap.ErrClass, "network")
	}
	if snap.CheckedAt.IsZero() {
		t.Fatal("CheckedAt not set after a failed Refresh")
	}
}

// TestFetcherSnapshot_ClassifiesHTTPError asserts a non-2xx response
// classifies as "http_error".
func TestFetcherSnapshot_ClassifiesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	f := newTestFetcher(t, srv.URL)
	f.Refresh(context.Background())
	if got := f.Snapshot().ErrClass; got != "http_error" {
		t.Fatalf("ErrClass = %q, want %q", got, "http_error")
	}
}

// TestFetcherSnapshot_ClassifiesParseError asserts a 2xx response whose body
// the Parser rejects (e.g. missing Version field) classifies as "parse_error".
func TestFetcherSnapshot_ClassifiesParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`) // valid JSON, but ParseTailscalePkgs rejects empty Version
	}))
	defer srv.Close()
	f := newTestFetcher(t, srv.URL)
	f.Refresh(context.Background())
	if got := f.Snapshot().ErrClass; got != "parse_error" {
		t.Fatalf("ErrClass = %q, want %q", got, "parse_error")
	}
}

// TestFetcherSnapshot_FailOpenKeepsGoodDataButRecordsError asserts the
// fail-open contract at the Snapshot level: a failing Refresh after a
// successful one must NOT clear OK/Latest, but must still surface the new
// ErrClass so the admin page can show "using cached data, last check failed".
func TestFetcherSnapshot_FailOpenKeepsGoodDataButRecordsError(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"Version":"1.98.4"}`)
	}))
	defer srv.Close()
	f := newTestFetcher(t, srv.URL)

	f.Refresh(context.Background())
	fail.Store(true)
	f.Refresh(context.Background())

	snap := f.Snapshot()
	if !snap.OK || snap.Latest != "1.98.4" {
		t.Fatalf("Snapshot after fail-open = %+v, want OK=true Latest=1.98.4 preserved", snap)
	}
	if snap.ErrClass != "http_error" {
		t.Fatalf("ErrClass after failing refresh = %q, want %q", snap.ErrClass, "http_error")
	}
}
