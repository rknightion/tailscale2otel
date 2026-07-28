package geoip

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// DefaultEndpoint is MaxMind's database download API. Verified live against
// download.maxmind.com on 2026-07-28:
//
//   - GET {endpoint}/{edition}/download?suffix=tar.gz with HTTP Basic auth
//     (username = account ID, password = license key) returns the gzipped tar.
//   - ?suffix=tar.gz.sha256 returns "<hex>  <filename>".
//   - If-Modified-Since against the previous Last-Modified returns 304, which
//     MaxMind documents as not counting against the daily download limit.
//   - Bad credentials are 401; an unknown edition is 404.
//   - The download 302s to Cloudflare R2 with a presigned URL. net/http follows
//     it and deliberately does NOT forward the Authorization header across
//     hosts, which is both required and correct — re-sending Basic auth would
//     hand the license key to a third party.
const DefaultEndpoint = "https://download.maxmind.com/geoip/databases"

// defaultMaxBytes caps the decompressed size of an archive member. The endpoint
// is configurable, so a hostile or corrupt archive must not be able to fill the
// disk; this sits far above any real MaxMind database.
const defaultMaxBytes = 512 << 20

// Downloader fetches MaxMind databases over the official download API and
// installs them, one file per edition, into Dir.
//
// Every failure mode leaves whatever is already installed untouched: the archive
// is verified against its published SHA-256 before anything is extracted, the
// database is written to a temporary file and renamed into place, and a failed
// fetch simply returns an error. The caller (see updater.go) logs it and tries
// again on the next tick.
type Downloader struct {
	AccountID  string
	LicenseKey string
	// Endpoint is the API base; empty selects DefaultEndpoint.
	Endpoint string
	// Dir is where installed databases live, as <Dir>/<Edition>.mmdb.
	Dir string
	// Timeout bounds one edition's fetch end to end. Zero means no timeout
	// beyond the caller's context.
	Timeout time.Duration
	// MaxBytes caps a decompressed archive member; zero selects defaultMaxBytes.
	MaxBytes int64
	// Client is the HTTP client to use; nil builds one. It MUST follow
	// redirects (the default client does) — the download 302s to R2.
	Client *http.Client
}

// DownloadResult reports what one Fetch did.
type DownloadResult struct {
	Edition string
	// Path is where the database is installed, whether or not this run updated
	// it (a 304 leaves the previously installed file in place).
	Path string
	// Updated is false when the server answered 304 Not Modified.
	Updated bool
	// BuildTime is the archive's Last-Modified, i.e. MaxMind's build date. Zero
	// on a 304 (the server sends no Last-Modified with one).
	BuildTime time.Time
}

// DatabasePath is where an edition's database is installed. The config layer
// uses it to default enrichment.geoip.country_database / asn_database when only
// the downloader is configured, so it must agree with Fetch.
func DatabasePath(dir, edition string) string {
	return filepath.Join(dir, edition+".mmdb")
}

// ValidateEdition rejects any edition name that could escape a URL path or a
// filename. The name is interpolated into both, so this runs before a request is
// made rather than sanitizing afterwards into something the operator did not
// ask for.
func ValidateEdition(edition string) error {
	if edition == "" {
		return errors.New("edition is empty")
	}
	for _, r := range edition {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("edition %q contains %q; only letters, digits, '-' and '_' are allowed", edition, r)
		}
	}
	return nil
}

// Fetch downloads, verifies and installs one edition, returning what it did. A
// 304 Not Modified is a success with Updated=false.
func (d *Downloader) Fetch(ctx context.Context, edition string) (DownloadResult, error) {
	if err := ValidateEdition(edition); err != nil {
		return DownloadResult{}, err
	}
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}
	dest := DatabasePath(d.Dir, edition)
	res := DownloadResult{Edition: edition, Path: dest}

	// 0o750, not 0o755: the databases are this process's state, and nothing else
	// on the host needs to read them.
	if err := os.MkdirAll(d.Dir, 0o750); err != nil {
		return res, fmt.Errorf("create geoip database directory %s: %w", d.Dir, err)
	}

	// The installed file's mtime IS the conditional-request state: Fetch sets it
	// to the server's Last-Modified after a successful install, so the next run
	// can ask "anything newer than this?" exactly, with no sidecar metadata file
	// to keep in sync (or to go stale against a hand-replaced database).
	var since time.Time
	if fi, err := os.Stat(dest); err == nil {
		since = fi.ModTime()
	}

	body, lastModified, notModified, err := d.get(ctx, edition, "tar.gz", since)
	if err != nil {
		return res, err
	}
	if notModified {
		return res, nil
	}
	defer body.Close()

	want, err := d.checksum(ctx, edition)
	if err != nil {
		return res, err
	}

	tmp, sum, err := d.spool(body)
	if err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(tmp) }()

	if sum != want {
		return res, fmt.Errorf("geoip database %s failed its checksum: archive is %s, MaxMind published %s", edition, sum, want)
	}

	if err := d.install(tmp, dest, edition, lastModified); err != nil {
		return res, err
	}
	res.Updated = true
	res.BuildTime = lastModified
	return res, nil
}

// get issues one authenticated request for an edition's given suffix. A non-zero
// since adds the conditional header; a 304 returns notModified with no body.
func (d *Downloader) get(ctx context.Context, edition, suffix string, since time.Time) (body io.ReadCloser, lastModified time.Time, notModified bool, err error) {
	endpoint := d.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	url := strings.TrimSuffix(endpoint, "/") + "/" + path.Join(edition, "download") + "?suffix=" + suffix

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("build geoip download request: %w", err)
	}
	// Basic auth, never a query parameter: a credential in the URL lands in
	// every proxy and server access log along the way.
	req.SetBasicAuth(d.AccountID, d.LicenseKey)
	if !since.IsZero() {
		req.Header.Set("If-Modified-Since", since.UTC().Format(http.TimeFormat))
	}

	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The URL carries no credential, so wrapping the transport error is safe.
		return nil, time.Time{}, false, fmt.Errorf("fetch geoip database %s: %w", edition, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		lm, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
		return resp.Body, lm, false, nil
	case http.StatusNotModified:
		_ = resp.Body.Close()
		return nil, time.Time{}, true, nil
	default:
		_ = resp.Body.Close()
		// Deliberately does not include the response body: MaxMind's own error
		// bodies are harmless, but a misconfigured endpoint could echo the
		// request back, and this error is logged.
		return nil, time.Time{}, false, fmt.Errorf("fetch geoip database %s: HTTP %d %s",
			edition, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
}

// checksum fetches the archive's published SHA-256. The response is
// "<hex>  <filename>"; only the first field is used.
func (d *Downloader) checksum(ctx context.Context, edition string) (string, error) {
	body, _, _, err := d.get(ctx, edition, "tar.gz.sha256", time.Time{})
	if err != nil {
		return "", err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return "", fmt.Errorf("read geoip checksum for %s: %w", edition, err)
	}
	sum, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	if len(sum) != 64 {
		return "", fmt.Errorf("geoip checksum for %s is malformed", edition)
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", fmt.Errorf("geoip checksum for %s is not hexadecimal", edition)
	}
	return sum, nil
}

// spool streams the archive to a temporary file, hashing as it goes, and returns
// the temp path and the hex digest. Hashing the bytes on the way to disk means
// the archive is read once and never held in memory.
func (d *Downloader) spool(body io.Reader) (string, string, error) {
	f, err := os.CreateTemp(d.Dir, ".geoip-*.tar.gz")
	if err != nil {
		return "", "", fmt.Errorf("create temporary file for geoip download: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, d.maxBytes())); err != nil {
		// Best-effort cleanup: the download already failed, and a leftover temp
		// file in the state directory is not worth masking that error with.
		_ = os.Remove(f.Name())
		return "", "", fmt.Errorf("download geoip archive: %w", err)
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// install extracts the archive's single .mmdb member and renames it over dest,
// then stamps dest with the server's Last-Modified so the next Fetch can make an
// exact conditional request.
func (d *Downloader) install(archive, dest, edition string, lastModified time.Time) error {
	tmp, err := d.extract(archive, dest, edition)
	if err != nil {
		return err
	}
	// Rename, never write in place: the reader loads the file whole, and a
	// half-written database would be a decode error at best. Rename within one
	// directory is atomic on every filesystem this runs on.
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install geoip database %s: %w", edition, err)
	}
	if !lastModified.IsZero() {
		if err := os.Chtimes(dest, lastModified, lastModified); err != nil {
			// Not fatal: the database is installed and usable. The only loss is
			// that the next conditional request re-downloads once.
			return nil //nolint:nilerr // see comment
		}
	}
	return nil
}

// extract writes the archive's single .mmdb member to a temporary file beside
// dest and returns its path.
//
// The member's NAME is never used to build a path — the destination is always
// <Dir>/<Edition>.mmdb, chosen by us — so archive traversal ("../../etc/...")
// is structurally impossible rather than filtered. What is still checked is the
// member TYPE (only regular files; a symlink or device member is refused
// outright) and its decompressed SIZE.
func (d *Downloader) extract(archive, dest, edition string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", fmt.Errorf("open geoip archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("geoip archive for %s is not gzip: %w", edition, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read geoip archive for %s: %w", edition, err)
		}
		if !strings.HasSuffix(h.Name, ".mmdb") {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("geoip archive for %s holds a non-regular .mmdb member (type %q)", edition, h.Typeflag)
		}
		if h.Size > d.maxBytes() {
			return "", fmt.Errorf("geoip database in the %s archive is %d bytes, above the %d-byte limit", edition, h.Size, d.maxBytes())
		}
		return d.spillMember(tr, dest, edition)
	}
	return "", fmt.Errorf("geoip archive for %s holds no .mmdb file", edition)
}

// spillMember copies the current tar member to a temporary file beside dest.
func (d *Downloader) spillMember(r io.Reader, dest, edition string) (string, error) {
	out, err := os.CreateTemp(filepath.Dir(dest), ".geoip-*.mmdb")
	if err != nil {
		return "", fmt.Errorf("create temporary file for geoip database: %w", err)
	}
	defer out.Close()

	// LimitReader as well as the header check above: a tar header's Size is
	// attacker-supplied and need not match the bytes that follow it.
	n, err := io.Copy(out, io.LimitReader(r, d.maxBytes()+1))
	if err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("extract geoip database for %s: %w", edition, err)
	}
	if n > d.maxBytes() {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("geoip database in the %s archive exceeds the %d-byte limit", edition, d.maxBytes())
	}
	// The database is read by every lookup and written only by this process.
	// 0o600: written and read only by this process.
	if err := out.Chmod(0o600); err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("set permissions on geoip database for %s: %w", edition, err)
	}
	return out.Name(), nil
}

func (d *Downloader) maxBytes() int64 {
	if d.MaxBytes > 0 {
		return d.MaxBytes
	}
	return defaultMaxBytes
}
