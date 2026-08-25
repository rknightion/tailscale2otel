// Package s3 is a minimal read-only client for S3-compatible object storage:
// list a prefix, fetch an object. That is the entire surface the flow-log
// object-store ingestion path needs.
//
// It exists instead of aws-sdk-go-v2 because of what the SDK costs here. Linking
// its config + s3 packages grows the release binary by 8.3 MiB (+40%) and adds
// 18 module requirements, for two HTTP GETs; this package costs 112 KiB (+0.5%)
// and no dependencies at all — everything it needs (crypto/hmac, crypto/sha256,
// encoding/xml, net/http) is already linked. See issue #238 for the measurement.
//
// The trade is that SigV4 correctness is ours. It is pinned by test vectors
// generated with botocore, AWS's own signing implementation, so a signature this
// package produces is checked against the same authority S3 checks it against.
//
// # What it deliberately does not support
//
//   - Writing. Everything here is GET; there is no code path that can modify a
//     bucket, which is the right blast radius for an exporter.
//   - The shared config file (~/.aws/credentials, AWS_PROFILE, SSO login). That
//     is a developer-laptop convenience; static environment credentials cover
//     the same ground in one variable, and container deployments use web
//     identity, the ECS/EKS container credential endpoint, or an instance
//     profile, all of which ARE supported.
//   - Request retries beyond what the caller does. A failed list or get is
//     returned as an error; the collector retries on its next cycle, which is
//     the natural cadence for an ingestion source.
package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v4/internal/objectstore"
)

// Object is retained as a source-compatible alias for objectstore.Object.
type Object = objectstore.Object

// Config describes one bucket and how to reach it.
type Config struct {
	// Endpoint is the service URL, e.g. https://s3.eu-west-2.amazonaws.com or a
	// MinIO/Ceph address. Required: there is no region-to-endpoint guessing here,
	// because the non-AWS implementations this is meant to support would all be
	// guessed wrong. A base path on the endpoint (e.g.
	// https://gw.example.net/storage/s3, reaching S3-compatible storage through
	// a reverse-proxy prefix) is preserved ahead of the bucket/key on every
	// request — see requestPath.
	Endpoint string
	Region   string
	Bucket   string
	// PathStyle addresses the bucket as <endpoint>/<bucket>/<key> rather than
	// <bucket>.<endpoint>/<key>. Required by most non-AWS implementations.
	PathStyle bool
	// AllowInsecureHTTP permits a plaintext remote endpoint. Plain HTTP is
	// otherwise accepted only for loopback development endpoints.
	AllowInsecureHTTP bool
	// Credentials supplies the signing keys. Required.
	Credentials Provider
	// HTTPClient is optional; nil selects one with a sane timeout.
	HTTPClient *http.Client
	// now is injectable so signing is deterministic in tests.
	now func() time.Time
}

// Client is a read-only S3 client for one bucket.
type Client struct {
	cfg    Config
	base   *url.URL
	signer signer
	hc     *http.Client
	now    func() time.Time
	// basePath is base.Path with any trailing "/" trimmed — the endpoint's own
	// path prefix (e.g. "/storage/s3" for a reverse-proxied endpoint), normalized
	// once so requestPath never has to re-derive it. A root endpoint's base.Path
	// is "" or "/", both of which trim to "", so basePath contributes nothing
	// and requestPath's result is unchanged from before this field existed.
	basePath string
}

// New validates cfg and returns a client for one bucket.
func New(cfg Config) (*Client, error) {
	switch {
	case cfg.Endpoint == "":
		return nil, errors.New("s3: endpoint is required")
	case cfg.Bucket == "":
		return nil, errors.New("s3: bucket is required")
	case cfg.Region == "":
		return nil, errors.New("s3: region is required")
	case cfg.Credentials == nil:
		return nil, errors.New("s3: credentials are required")
	}
	base, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, errors.New("s3: endpoint is not a valid URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("s3: endpoint scheme %q is not http or https", base.Scheme)
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("s3: endpoint must not contain userinfo, query credentials, or a fragment")
	}
	if base.Scheme == "http" && !loopbackHost(base.Hostname()) && !cfg.AllowInsecureHTTP {
		return nil, errors.New("s3: plaintext remote endpoint requires Config.AllowInsecureHTTP; " +
			"credentials and session tokens would cross the network without TLS")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	return &Client{
		cfg:      cfg,
		base:     base,
		signer:   signer{region: cfg.Region},
		hc:       httpguard.NoRedirectClient(hc),
		now:      now,
		basePath: strings.TrimRight(base.Path, "/"),
	}, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// listResponse is the subset of ListObjectsV2's XML that matters.
type listResponse struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
}

// ListResult is retained as a source-compatible alias for objectstore.ListResult.
type ListResult = objectstore.ListResult

// maxListResponseBytes bounds one ListObjectsV2 response.
//
// The number is arithmetic, not a guess. A page carries at most 1000 keys (the
// ListObjectsV2 maximum), an S3 key may be 1024 bytes, and each key arrives
// wrapped in ~200 bytes of XML: <Contents>, <Key>, <LastModified>, <ETag>,
// <Size>, <StorageClass> and their closing tags. So a legitimate page of long
// keys is ~1.2 MiB — already past the 1 MiB metadata cap that used to apply
// here, which is exactly the #291 bug. The true ceiling is higher still, because
// XML escaping can turn one key byte into five ("&" -> "&amp;"): a page of 1000
// escape-heavy 1024-byte keys reaches ~5.1 MiB. 8 MiB clears that with room
// spare and is still a bound.
//
// Raising the number is cheap because the decode STREAMS (see
// decodeListResponse): the wire bytes are never held alongside the result, so on
// a real listing the peak footprint is the decoded keys (~1.2 MiB at the ceiling
// above, and unescaping only shrinks them) plus one token. A single-token body is
// the worst case the decoder can be made to buffer, and that is bounded by this
// constant — which is why it stays a constant rather than becoming an operator
// knob.
const maxListResponseBytes = 8 << 20

// errListResponseTooLarge is returned when a listing does not end within
// maxListResponseBytes.
//
// It is deliberately NOT a decode error. The bytes that arrived were discarded
// rather than parsed, and calling that malformed would blame the bucket for a cap
// of ours — which is how the truncation this replaces used to present.
var errListResponseTooLarge = errors.New("s3: list response exceeds this client's read limit")

// List returns objects under prefix, in the lexicographic order S3 lists them —
// which for a zero-padded date-partitioned layout is also chronological order.
//
// startAfter resumes past a key already handled, so a caller holding a cursor
// need not re-list what it has done. limit bounds the total returned across all
// pages; a non-positive limit means "everything under the prefix", which is only
// safe when the caller has bounded the prefix itself.
func (c *Client) List(ctx context.Context, prefix, startAfter string, limit int) (ListResult, error) {
	var result ListResult
	token := ""
	for {
		q := url.Values{"list-type": {"2"}}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		switch {
		case token != "":
			q.Set("continuation-token", token)
		case startAfter != "":
			// start-after applies only to the first page; afterwards the
			// continuation token carries the position.
			q.Set("start-after", startAfter)
		}
		if limit > 0 {
			remaining := limit - len(result.Objects)
			if remaining <= 0 {
				result.Truncated = true
				return result, nil
			}
			q.Set("max-keys", itoa(min(remaining, 1000)))
		}

		var page listResponse
		if err := c.listPage(ctx, q.Encode(), &page); err != nil {
			return ListResult{}, err
		}
		for _, o := range page.Contents {
			result.Objects = append(result.Objects, Object{
				Identity:     o.Key,
				Key:          o.Key,
				Size:         o.Size,
				LastModified: o.LastModified,
			})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			result.Truncated = false
			return result, nil
		}
		if limit > 0 && len(result.Objects) >= limit {
			result.Objects = result.Objects[:limit]
			result.Truncated = true
			return result, nil
		}
		token = page.NextContinuationToken
	}
}

// Get fetches one object. The caller owns the returned reader and must close it.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == "" {
		return nil, errors.New("s3: empty key")
	}
	req, err := c.request(ctx, "/"+key, "")
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3: get %s: %w", key, err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = readAllClose(resp)
		return nil, fmt.Errorf("s3: get %s: %s", key, resp.Status)
	}
	return resp.Body, nil
}

// listPage issues one signed ListObjectsV2 GET and decodes the response into
// page. It is the only caller of decodeListResponse; an error status is still
// read and reported the way it always has been.
func (c *Client) listPage(ctx context.Context, rawQuery string, page *listResponse) error {
	req, err := c.request(ctx, "/", rawQuery)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("s3: list: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Drain the bounded failure body for connection reuse, but never return it:
		// the peer can reflect request credentials into its response.
		_, readErr := readAllClose(resp)
		if readErr != nil {
			return fmt.Errorf("s3: list: %w", readErr)
		}
		return fmt.Errorf("s3: list: %s", resp.Status)
	}
	return decodeListResponse(resp, page)
}

// decodeListResponse stream decodes one ListObjectsV2 body into page and closes
// it. It is the streaming twin of readAllClose: the same drain-and-close
// discipline, re-capping the body through boundedBody rather than around it, but
// without ever materializing the response.
//
// Streaming is what makes maxListResponseBytes affordable — the wire bytes are
// never held alongside the decoded result — and the reason the bound has to be
// checked independently of the decoder is that the decoder is not a witness to
// truncation. Usually a cut document fails with an unexpected EOF, which is the
// wrong message (it blames the bucket); but a body cut at an element boundary
// parses cleanly as a complete listing that is silently missing objects, and
// nothing at all is reported. So the reader is allowed exactly ONE byte past the
// bound, which makes "the allowance ran out" distinguishable from "the document
// ended on the last permitted byte", and that signal — not the decoder's error —
// decides whether the response is refused.
func decodeListResponse(resp *http.Response, page *listResponse) error {
	limited := &io.LimitedReader{R: resp.Body, N: maxListResponseBytes + 1}
	resp.Body = boundedBody{Reader: limited, Closer: resp.Body}
	defer resp.Body.Close()

	err := xml.NewDecoder(resp.Body).Decode(page)
	if err == nil && limited.N > 0 {
		// The decoder stops on the root's closing tag and may never ask for the
		// byte after it, so ask here. Without this, whether an overrun is noticed
		// would depend on how the decoder happened to buffer, and a body whose
		// root closes exactly on the last permitted byte with more to follow would
		// slip through. A body that genuinely ends there answers EOF and is
		// unaffected: only bytes actually delivered count against the allowance.
		var probe [1]byte
		_, _ = resp.Body.Read(probe[:])
	}
	if limited.N <= 0 {
		return fmt.Errorf("%w of %d bytes: the response was discarded rather than parsed, because "+
			"parsing a truncated listing silently drops objects. This is a bound in this client, not a "+
			"malformed response from the bucket — a full page of 1000 maximum-length keys is ~1.2 MiB",
			errListResponseTooLarge, maxListResponseBytes)
	}
	if err != nil {
		return fmt.Errorf("s3: decode list response: %w", err)
	}
	return nil
}

// request builds and signs one GET.
func (c *Client) request(ctx context.Context, path, rawQuery string) (*http.Request, error) {
	u := *c.base
	if !c.cfg.PathStyle {
		u.Host = c.cfg.Bucket + "." + u.Host
	}
	u.Path = c.requestPath(path)
	// The signature covers the ENCODED path, and Go re-derives RawPath from Path
	// only when they differ, so setting both keeps what is signed identical to
	// what is sent for keys containing characters that need escaping.
	u.RawPath = u.EscapedPath()
	u.RawQuery = rawQuery

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	cred, err := c.cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("s3: credentials: %w", err)
	}
	c.signer.sign(req, cred, c.now())
	return req, nil
}

// requestPath is the ONLY place this package decides the full URL path for a
// request: the endpoint's own base path (basePath — e.g. "/storage/s3" for a
// reverse-proxied endpoint), the bucket when addressing path-style (virtual-host
// addressing puts the bucket in the host instead, via u.Host above), and the
// caller-supplied path (always "/" for a list, or "/"+key for a get). A root
// endpoint has an empty basePath, so its result is byte-identical to what
// request() computed before this method existed.
//
// This is also why the signed path and the sent path cannot diverge: request()
// assigns this result straight to u.Path (with u.RawPath kept in lockstep just
// below), and that same *url.URL becomes req.URL — which is exactly what
// sign() reads via req.URL.EscapedPath(). There is no second copy of this join
// anywhere for signing to disagree with.
func (c *Client) requestPath(path string) string {
	if !c.cfg.PathStyle {
		return c.basePath + path
	}
	return c.basePath + "/" + c.cfg.Bucket + path
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
