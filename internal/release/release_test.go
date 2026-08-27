package release_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	got, err := release.ParseGitHubLatest(bytes.NewReader(body))
	if err != nil || got != "v0.2.0" {
		t.Fatalf("ParseGitHubLatest = %q,%v want v0.2.0,nil", got, err)
	}
	if _, err := release.ParseGitHubLatest(strings.NewReader(`{"tag_name":""}`)); err == nil {
		t.Error("ParseGitHubLatest empty tag: want error")
	}
}

func TestParseTailscalePkgs(t *testing.T) {
	body := []byte(`{"Version":"1.98.4","TarballsVersion":"1.98.4","MacZipsVersion":"1.98.5"}`)
	got, err := release.ParseTailscalePkgs(bytes.NewReader(body))
	if err != nil || got != "1.98.4" {
		t.Fatalf("ParseTailscalePkgs = %q,%v want 1.98.4,nil", got, err)
	}
	if _, err := release.ParseTailscalePkgs(strings.NewReader(`{}`)); err == nil {
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

// bigGitHubRelease renders a GitHub /releases/latest response whose body field
// is large enough to push the whole payload past any fixed read cap. The real
// v4.0.0 response is 74 KiB because release-please puts the entire changelog in
// "body"; tag_name still arrives in the first few hundred bytes.
func bigGitHubRelease(tag string, bodyBytes int) string {
	return `{"url":"https://api.github.com/repos/o/r/releases/1","tag_name":"` + tag +
		`","name":"` + tag + `","draft":false,"prerelease":false,"body":"` +
		strings.Repeat("changelog line. ", bodyBytes/16) + `"}`
}

// countingReader records how far a parser actually read.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestParseGitHubLatestOversizedBody(t *testing.T) {
	body := bigGitHubRelease("v4.0.0", 6<<20)
	if len(body) <= 4<<20 {
		t.Fatalf("fixture is %d bytes: must exceed the 4 MiB read ceiling, so this pins that a body "+
			"larger than the retained limit still resolves — not merely one larger than the old 64 KiB cap", len(body))
	}
	cr := &countingReader{r: strings.NewReader(body)}
	got, err := release.ParseGitHubLatest(cr)
	if err != nil || got != "v4.0.0" {
		t.Fatalf("ParseGitHubLatest(oversized) = %q,%v want v4.0.0,nil", got, err)
	}
	// The point of the fix: tag_name arrives early, so the parser must stop
	// there rather than consuming the changelog behind it. Whole-document
	// unmarshalling read all %d of these bytes and was at the mercy of the cap.
	if cr.n >= len(body)/2 {
		t.Errorf("parser read %d of %d bytes: it should stop at tag_name, not consume the body", cr.n, len(body))
	}
	t.Logf("read %d of %d bytes to resolve tag_name", cr.n, len(body))
}

func TestParseTailscalePkgsOversizedBody(t *testing.T) {
	body := `{"Version":"1.98.4","Notes":"` + strings.Repeat("x", 6<<20) + `"}`
	got, err := release.ParseTailscalePkgs(strings.NewReader(body))
	if err != nil || got != "1.98.4" {
		t.Fatalf("ParseTailscalePkgs(oversized) = %q,%v want 1.98.4,nil", got, err)
	}
}

// A body that never carries the key, and runs past the hard ceiling, must be
// distinguishable from a malformed one: the operator needs to know the endpoint
// answered with something too big rather than something broken.
func TestFetcherTruncatedBodyIsItsOwnErrorClass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Padding":"`)
		for range 600 {
			_, _ = io.WriteString(w, strings.Repeat("x", 1<<16))
		}
		_, _ = io.WriteString(w, `","Version":"1.98.4"}`)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv.URL)
	f.Refresh(t.Context())
	snap := f.Snapshot()
	if snap.ErrClass != "truncated" {
		t.Fatalf("ErrClass = %q want truncated", snap.ErrClass)
	}
	if _, ok := f.Latest(); ok {
		t.Error("Latest: want no cached version after a truncated fetch")
	}
}

func TestFetcherMalformedBodyIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Version":`)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv.URL)
	f.Refresh(t.Context())
	if got := f.Snapshot().ErrClass; got != "parse_error" {
		t.Fatalf("ErrClass = %q want parse_error", got)
	}
}

// The regression at the level it actually bit: a whole fetch against a
// GitHub-shaped response bigger than the old 64 KiB read cap. Before the
// streaming parser this came back parse_error and no version, permanently.
func TestFetcherResolvesOversizedGitHubRelease(t *testing.T) {
	body := bigGitHubRelease("v4.0.0", 6<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	f := release.NewFetcher("self", srv.URL, "ua/1",
		release.ParseGitHubLatest, &http.Client{}, time.Hour, slog.New(slog.DiscardHandler))
	f.Refresh(t.Context())

	snap := f.Snapshot()
	if snap.ErrClass != "" {
		t.Fatalf("ErrClass = %q want no error (body was %d bytes)", snap.ErrClass, len(body))
	}
	got, ok := f.Latest()
	if !ok || got != "v4.0.0" {
		t.Fatalf("Latest = %q,%v want v4.0.0,true", got, ok)
	}
}

// A key nested inside another object must not be mistaken for the top-level
// one. A GitHub release embeds an "author" object and an "assets" array, each
// with its own fields, so the scan has to consume unwanted values whole rather
// than matching any occurrence of the name.
func TestParseGitHubLatestIgnoresNestedKeys(t *testing.T) {
	body := `{"author":{"login":"o","tag_name":"WRONG"},` +
		`"assets":[{"tag_name":"ALSO-WRONG"}],` +
		`"reactions":{"nested":{"tag_name":"DEEPER-WRONG"}},` +
		`"tag_name":"v4.0.0"}`
	got, err := release.ParseGitHubLatest(strings.NewReader(body))
	if err != nil || got != "v4.0.0" {
		t.Fatalf("ParseGitHubLatest = %q,%v want v4.0.0,nil", got, err)
	}
}

func TestParseGitHubLatestMissingKey(t *testing.T) {
	if _, err := release.ParseGitHubLatest(strings.NewReader(`{"name":"v4.0.0"}`)); err == nil {
		t.Error("want an error when tag_name is absent")
	}
	if _, err := release.ParseGitHubLatest(strings.NewReader(`["v4.0.0"]`)); err == nil {
		t.Error("want an error when the response is not a JSON object")
	}
}
