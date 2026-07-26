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
//     identity or an instance profile, both of which ARE supported.
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

	"github.com/rknightion/tailscale2otel/v3/internal/objectstore"
)

// Object is retained as a source-compatible alias for objectstore.Object.
type Object = objectstore.Object

// Config describes one bucket and how to reach it.
type Config struct {
	// Endpoint is the service URL, e.g. https://s3.eu-west-2.amazonaws.com or a
	// MinIO/Ceph address. Required: there is no region-to-endpoint guessing here,
	// because the non-AWS implementations this is meant to support would all be
	// guessed wrong.
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
		return nil, fmt.Errorf("s3: parse endpoint: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("s3: endpoint scheme %q is not http or https", base.Scheme)
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
	return &Client{cfg: cfg, base: base, signer: signer{region: cfg.Region}, hc: hc, now: now}, nil
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

		body, err := c.do(ctx, "/", q.Encode())
		if err != nil {
			return ListResult{}, err
		}
		var page listResponse
		if err := xml.Unmarshal(body, &page); err != nil {
			return ListResult{}, fmt.Errorf("s3: decode list response: %w", err)
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
		body, _ := readAllClose(resp)
		return nil, fmt.Errorf("s3: get %s: %s: %s", key, resp.Status, snippet(body))
	}
	return resp.Body, nil
}

// do issues a signed GET and returns the whole body. Used for listings, which
// are bounded by max-keys and small.
func (c *Client) do(ctx context.Context, path, rawQuery string) ([]byte, error) {
	req, err := c.request(ctx, path, rawQuery)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3: list: %w", err)
	}
	body, err := readAllClose(resp)
	if err != nil {
		return nil, fmt.Errorf("s3: list: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("s3: list: %s: %s", resp.Status, snippet(body))
	}
	return body, nil
}

// request builds and signs one GET.
func (c *Client) request(ctx context.Context, path, rawQuery string) (*http.Request, error) {
	u := *c.base
	if c.cfg.PathStyle {
		u.Path = "/" + c.cfg.Bucket + path
	} else {
		u.Host = c.cfg.Bucket + "." + u.Host
		u.Path = path
	}
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

// snippet trims an error body to something loggable. S3 returns XML error
// documents that are useful but occasionally long.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
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
