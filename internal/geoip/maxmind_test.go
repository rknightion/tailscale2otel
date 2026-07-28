package geoip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tarball builds a MaxMind-shaped archive: a single directory holding the .mmdb
// plus the copyright/license files the real ones ship. members lets a test add
// or replace entries to exercise the hostile cases.
func tarball(t *testing.T, edition string, mmdb []byte, extra ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dir := edition + "_20260728"

	write := func(h tar.Header, body []byte) {
		h.Size = int64(len(body))
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	write(tar.Header{Name: dir + "/COPYRIGHT.txt", Typeflag: tar.TypeReg}, []byte("(c) MaxMind"))
	if mmdb != nil {
		write(tar.Header{Name: dir + "/" + edition + ".mmdb", Typeflag: tar.TypeReg}, mmdb)
	}
	for _, h := range extra {
		body := bytes.Repeat([]byte("x"), int(h.Size))
		h.Size = 0
		write(h, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fakeMaxMind serves the two suffixes the real API serves, with the same Basic
// auth, Last-Modified and 304 behavior verified against download.maxmind.com.
type fakeMaxMind struct {
	archive      []byte
	sum          string // overrides the real checksum when non-empty
	lastModified time.Time
	requests     []string
	authSeen     string
	status       int // overrides a 200 when non-zero
}

func (f *fakeMaxMind) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
		user, pass, ok := r.BasicAuth()
		if !ok || user == "" || pass == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.authSeen = user + ":" + pass
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`Database edition not found`))
			return
		}
		switch r.URL.Query().Get("suffix") {
		case "tar.gz.sha256":
			sum := f.sum
			if sum == "" {
				sum = sha256Hex(f.archive)
			}
			_, _ = fmt.Fprintf(w, "%s  GeoLite2-Country_20260728.tar.gz\n", sum)
		case "tar.gz":
			if ims := r.Header.Get("If-Modified-Since"); ims != "" {
				if since, err := http.ParseTime(ims); err == nil && !f.lastModified.After(since) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
			w.Header().Set("Last-Modified", f.lastModified.UTC().Format(http.TimeFormat))
			_, _ = w.Write(f.archive)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
}

func newDownloader(t *testing.T, f *fakeMaxMind, dir string) *Downloader {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return &Downloader{
		AccountID:  "359153",
		LicenseKey: "test-license-key",
		Endpoint:   srv.URL,
		Dir:        dir,
		Timeout:    10 * time.Second,
	}
}

func readMMDB(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDownloader_FetchInstallsDatabase(t *testing.T) {
	mmdb := readMMDB(t, countryDB)
	lm := time.Date(2026, 7, 28, 18, 45, 34, 0, time.UTC)
	f := &fakeMaxMind{archive: tarball(t, "GeoLite2-Country", mmdb), lastModified: lm}
	dir := t.TempDir()
	d := newDownloader(t, f, dir)

	res, err := d.Fetch(context.Background(), "GeoLite2-Country")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.Updated {
		t.Error("Updated = false, want true on a first download")
	}
	want := filepath.Join(dir, "GeoLite2-Country.mmdb")
	if res.Path != want {
		t.Errorf("Path = %q, want %q", res.Path, want)
	}
	if got := readMMDB(t, want); !bytes.Equal(got, mmdb) {
		t.Error("installed database does not match the archived one")
	}

	// Basic auth carries the account ID and license key, and the license key is
	// never put in the URL (where it would land in every proxy access log).
	if f.authSeen != "359153:test-license-key" {
		t.Errorf("basic auth = %q, want 359153:test-license-key", f.authSeen)
	}
	for _, req := range f.requests {
		if strings.Contains(req, "test-license-key") {
			t.Errorf("license key leaked into the request URL: %s", req)
		}
	}

	// The installed file's mtime is set to the server's Last-Modified, which is
	// what makes the next run's conditional request exact without a sidecar
	// metadata file.
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().UTC().Equal(lm) {
		t.Errorf("installed mtime = %v, want the Last-Modified %v", fi.ModTime().UTC(), lm)
	}

	// And the result is loadable by the reader half of this package.
	db := mustOpen(t, Options{CountryPath: want})
	if got, ok := db.Lookup(netip.MustParseAddr("81.2.69.142")); !ok || got.CountryISO != "GB" {
		t.Fatalf("downloaded database Lookup = %+v, ok = %v; want GB", got, ok)
	}
}

// A second run must issue a conditional request and do nothing on 304. This is
// the behavior that keeps a daily updater inside MaxMind's download quota.
func TestDownloader_NotModified(t *testing.T) {
	lm := time.Date(2026, 7, 28, 18, 45, 34, 0, time.UTC)
	f := &fakeMaxMind{archive: tarball(t, "GeoLite2-Country", readMMDB(t, countryDB)), lastModified: lm}
	dir := t.TempDir()
	d := newDownloader(t, f, dir)

	if _, err := d.Fetch(context.Background(), "GeoLite2-Country"); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	res, err := d.Fetch(context.Background(), "GeoLite2-Country")
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if res.Updated {
		t.Error("Updated = true on an unchanged database, want false")
	}
	if res.Path == "" {
		t.Error("Path is empty on a 304; want the existing installed path")
	}
}

// A checksum mismatch must abandon the download and leave whatever was already
// installed untouched -- a corrupted or tampered archive is exactly the case the
// checksum exists for, and half-installing it would be worse than not updating.
func TestDownloader_ChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "GeoLite2-Country.mmdb")
	if err := os.WriteFile(installed, []byte("previous database"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Age the installed file so the conditional request does not short-circuit
	// to 304 before the checksum is ever fetched.
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(installed, old, old); err != nil {
		t.Fatal(err)
	}
	f := &fakeMaxMind{
		archive:      tarball(t, "GeoLite2-Country", readMMDB(t, countryDB)),
		sum:          strings.Repeat("0", 64),
		lastModified: time.Now().UTC().Truncate(time.Second),
	}
	d := newDownloader(t, f, dir)

	if _, err := d.Fetch(context.Background(), "GeoLite2-Country"); err == nil {
		t.Fatal("Fetch with a bad checksum returned nil error")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %v, want it to name the checksum", err)
	}
	if got := string(readMMDB(t, installed)); got != "previous database" {
		t.Errorf("installed database = %q; the failed download overwrote it", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestDownloader_HTTPErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   string
	}{
		// The two error shapes the live API actually returns, verified against
		// download.maxmind.com: 401 for bad credentials, 404 for a bad edition.
		{"unauthorized", http.StatusUnauthorized, "401"},
		{"unknown edition", http.StatusNotFound, "404"},
		{"server error", http.StatusInternalServerError, "500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeMaxMind{status: c.status, lastModified: time.Now().UTC()}
			d := newDownloader(t, f, t.TempDir())
			_, err := d.Fetch(context.Background(), "GeoLite2-Country")
			if err == nil {
				t.Fatal("Fetch returned nil error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %s", err, c.want)
			}
			// Whatever the server said, the credential must not be echoed back
			// through the error into the logs.
			if strings.Contains(err.Error(), "test-license-key") {
				t.Errorf("license key leaked into the error: %v", err)
			}
		})
	}
}

// The archive is attacker-shaped input as far as this code is concerned: the
// endpoint is configurable, so "it comes from MaxMind" is not a guarantee.
func TestDownloader_HostileArchives(t *testing.T) {
	t.Run("no mmdb member", func(t *testing.T) {
		f := &fakeMaxMind{archive: tarball(t, "GeoLite2-Country", nil), lastModified: time.Now().UTC()}
		d := newDownloader(t, f, t.TempDir())
		if _, err := d.Fetch(context.Background(), "GeoLite2-Country"); err == nil {
			t.Fatal("Fetch on an archive with no .mmdb returned nil error")
		}
	})

	t.Run("traversal member name is never used as a path", func(t *testing.T) {
		dir := t.TempDir()
		outside := filepath.Join(dir, "escaped.mmdb")
		f := &fakeMaxMind{
			archive: tarball(t, "GeoLite2-Country", readMMDB(t, countryDB),
				tar.Header{Name: "../../escaped.mmdb", Typeflag: tar.TypeReg, Size: 4}),
			lastModified: time.Now().UTC(),
		}
		d := newDownloader(t, f, filepath.Join(dir, "db"))
		if _, err := d.Fetch(context.Background(), "GeoLite2-Country"); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if _, err := os.Stat(outside); err == nil {
			t.Fatal("a tar member name escaped the destination directory")
		}
	})

	t.Run("symlink and device members are refused", func(t *testing.T) {
		for _, typ := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar} {
			f := &fakeMaxMind{
				archive: tarball(t, "GeoLite2-Country", nil,
					tar.Header{Name: "GeoLite2-Country_20260728/GeoLite2-Country.mmdb", Typeflag: typ, Linkname: "/etc/passwd"}),
				lastModified: time.Now().UTC(),
			}
			d := newDownloader(t, f, t.TempDir())
			if _, err := d.Fetch(context.Background(), "GeoLite2-Country"); err == nil {
				t.Errorf("Fetch accepted a non-regular member of type %q", typ)
			}
		}
	})

	t.Run("oversized member is refused", func(t *testing.T) {
		f := &fakeMaxMind{
			archive:      tarball(t, "GeoLite2-Country", bytes.Repeat([]byte("A"), 4096)),
			lastModified: time.Now().UTC(),
		}
		d := newDownloader(t, f, t.TempDir())
		d.MaxBytes = 1024
		if _, err := d.Fetch(context.Background(), "GeoLite2-Country"); err == nil {
			t.Fatal("Fetch accepted a member above MaxBytes")
		}
	})
}

// The edition name goes into a URL path and into a filename. Anything that could
// escape either must be refused before a request is made, not sanitized into
// something surprising.
func TestValidateEdition(t *testing.T) {
	for _, ok := range []string{"GeoLite2-Country", "GeoLite2-ASN", "GeoIP2-City", "GeoIP2-Enterprise"} {
		if err := ValidateEdition(ok); err != nil {
			t.Errorf("ValidateEdition(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "../etc/passwd", "GeoLite2 Country", "GeoLite2/Country", "Geo?Lite", "a/../b", ".."} {
		if err := ValidateEdition(bad); err == nil {
			t.Errorf("ValidateEdition(%q) = nil, want an error", bad)
		}
	}
}

// DatabasePath is what the config layer uses to default country_database /
// asn_database when only download.enabled is set, so it has to agree with where
// Fetch actually installs the file.
func TestDatabasePathMatchesFetch(t *testing.T) {
	dir := t.TempDir()
	f := &fakeMaxMind{archive: tarball(t, "GeoLite2-ASN", readMMDB(t, asnDB)), lastModified: time.Now().UTC()}
	d := newDownloader(t, f, dir)
	res, err := d.Fetch(context.Background(), "GeoLite2-ASN")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := DatabasePath(dir, "GeoLite2-ASN"); got != res.Path {
		t.Errorf("DatabasePath = %q, Fetch installed at %q", got, res.Path)
	}
}

// A canceled context must abort promptly rather than hanging a shutdown.
func TestDownloader_ContextCancel(t *testing.T) {
	f := &fakeMaxMind{archive: tarball(t, "GeoLite2-Country", readMMDB(t, countryDB)), lastModified: time.Now().UTC()}
	d := newDownloader(t, f, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Fetch(ctx, "GeoLite2-Country"); err == nil {
		t.Fatal("Fetch with a canceled context returned nil error")
	}
}
