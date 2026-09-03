package s3

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/objectstore"
)

func staticCreds() Provider {
	return StaticProvider{Credentials: Credentials{AccessKeyID: "AK", SecretAccessKey: "SK"}}
}

// capture records what the client actually put on the wire, which is the only
// thing a bucket ever sees. requestURI is the raw request line — the bytes the
// signature has to agree with — as opposed to the decoded URL fields.
type capture struct {
	method, path, requestURI, query, host, auth string
}

// serve stands up a fake S3 returning body for every request, and records the
// last one.
func serve(t *testing.T, body string, status int) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.requestURI = r.Method, r.URL.Path, r.RequestURI
		got.query, got.host = r.URL.RawQuery, r.Host
		got.auth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// dialTo returns an HTTP client that sends every request to addr regardless of
// the hostname it was addressed to. Virtual-host addressing puts the bucket in
// the hostname, which does not resolve against a loopback test server, so this
// is what lets the addressing itself be tested.
func dialTo(addr string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

func newClient(t *testing.T, endpoint string, pathStyle bool) *Client {
	t.Helper()
	c, err := New(Config{
		Endpoint: endpoint, Region: "eu-west-2", Bucket: "flows",
		PathStyle: pathStyle, Credentials: staticCreds(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

const listOnePage = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>flow/2026/07/24/a.ndjson</Key><Size>120</Size><LastModified>2026-07-24T09:00:00.000Z</LastModified></Contents>
  <Contents><Key>flow/2026/07/24/b.ndjson.zst</Key><Size>340</Size><LastModified>2026-07-24T09:05:00.000Z</LastModified></Contents>
</ListBucketResult>`

func TestList_DecodesObjects(t *testing.T) {
	srv, got := serve(t, listOnePage, http.StatusOK)
	result, err := newClient(t, srv.URL, true).List(context.Background(), "flow/2026/07/24/", "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	objs := result.Objects
	if len(objs) != 2 {
		t.Fatalf("objects = %+v, want 2", objs)
	}
	for _, obj := range objs {
		if obj.Identity != obj.Key {
			t.Errorf("object identity = %q, want key %q", obj.Identity, obj.Key)
		}
	}
	if objs[0].Key != "flow/2026/07/24/a.ndjson" || objs[0].Size != 120 {
		t.Errorf("first object = %+v", objs[0])
	}
	if want := time.Date(2026, 7, 24, 9, 5, 0, 0, time.UTC); !objs[1].LastModified.Equal(want) {
		t.Errorf("LastModified = %v, want %v", objs[1].LastModified, want)
	}
	// list-type=2 is what distinguishes ListObjectsV2 from the deprecated V1
	// listing, which returns a different marker field entirely.
	q, _ := url.ParseQuery(got.query)
	if q.Get("list-type") != "2" {
		t.Errorf("query = %q, want a ListObjectsV2 request", got.query)
	}
	if q.Get("prefix") != "flow/2026/07/24/" {
		t.Errorf("prefix = %q", q.Get("prefix"))
	}
}

func TestClient_ImplementsObjectStoreBackend(t *testing.T) {
	var _ objectstore.Backend = (*Client)(nil)
}

// Most non-AWS implementations only support path-style addressing, and getting
// this backwards produces a DNS failure rather than an HTTP error.
func TestAddressing_PathStyleAndVirtualHost(t *testing.T) {
	srv, got := serve(t, listOnePage, http.StatusOK)

	if _, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0); err != nil {
		t.Fatalf("path style: %v", err)
	}
	if got.path != "/flows/" {
		t.Errorf("path-style path = %q, want the bucket in the path", got.path)
	}

	// Virtual-host style puts the bucket in the hostname, which does not resolve
	// against a loopback listener — so the request is dialed to the test server
	// regardless of host, and the Host header shows what was addressed.
	vh, err := New(Config{
		Endpoint: srv.URL, Region: "eu-west-2", Bucket: "flows",
		Credentials: staticCreds(), HTTPClient: dialTo(srv.Listener.Addr().String()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vh.List(context.Background(), "", "", 0); err != nil {
		t.Fatalf("virtual host: %v", err)
	}
	if !strings.HasPrefix(got.host, "flows.") {
		t.Errorf("virtual-host Host = %q, want the bucket in the hostname", got.host)
	}
	if got.path != "/" {
		t.Errorf("virtual-host path = %q, want the bucket out of the path", got.path)
	}
}

func TestNew_RejectsPlaintextRemoteEndpointByDefault(t *testing.T) {
	_, err := New(Config{
		Endpoint:    "http://storage.example.com:9000",
		Region:      "eu-west-2",
		Bucket:      "flows",
		Credentials: staticCreds(),
	})
	if err == nil {
		t.Fatal("New accepted a plaintext remote endpoint without explicit opt-in")
	}
	for _, want := range []string{"plaintext", "AllowInsecureHTTP"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want %q", err, want)
		}
	}
}

func TestNew_AllowsPlaintextLoopbackOrExplicitOverride(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		override bool
	}{
		{"localhost", "http://localhost:9000", false},
		{"IPv4 loopback", "http://127.0.0.1:9000", false},
		{"IPv6 loopback", "http://[::1]:9000", false},
		{"explicit remote override", "http://storage.example.com:9000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{
				Endpoint:          tc.endpoint,
				Region:            "eu-west-2",
				Bucket:            "flows",
				AllowInsecureHTTP: tc.override,
				Credentials:       staticCreds(),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
		})
	}
}

// A listing longer than one page must be followed, or ingestion silently stops
// at the first 1000 objects — which on a busy tailnet is a few hours of flows.
func TestList_FollowsPagination(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("continuation-token")
		seen = append(seen, token)
		if token == "" {
			_, _ = io.WriteString(w, `<ListBucketResult>
			  <IsTruncated>true</IsTruncated><NextContinuationToken>page2</NextContinuationToken>
			  <Contents><Key>a</Key><Size>1</Size></Contents></ListBucketResult>`)
			return
		}
		_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated>
		  <Contents><Key>b</Key><Size>1</Size></Contents></ListBucketResult>`)
	}))
	defer srv.Close()

	result, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	objs := result.Objects
	if len(objs) != 2 || objs[0].Key != "a" || objs[1].Key != "b" {
		t.Errorf("objects = %+v, want both pages in order", objs)
	}
	if len(seen) != 2 || seen[1] != "page2" {
		t.Errorf("continuation tokens = %v, want the second page requested with the token", seen)
	}
}

// The per-cycle cap is what keeps a first run against a bucket holding months of
// history from trying to ingest all of it at once.
func TestList_HonoursTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always claims more, so only the limit can stop the loop.
		_, _ = io.WriteString(w, `<ListBucketResult>
		  <IsTruncated>true</IsTruncated><NextContinuationToken>more</NextContinuationToken>
		  <Contents><Key>a</Key></Contents><Contents><Key>b</Key></Contents>
		  <Contents><Key>c</Key></Contents></ListBucketResult>`)
	}))
	defer srv.Close()

	got, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Objects) != 2 || !got.Truncated {
		t.Errorf("List = %+v, want two objects and Truncated=true", got)
	}
}

func TestList_ExactFinalPageIsNotTruncated(t *testing.T) {
	srv, _ := serve(t, listOnePage, http.StatusOK)

	got, err := newClient(t, srv.URL, true).List(
		context.Background(),
		"flow/2026/07/24/",
		"",
		2,
	)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Objects) != 2 || got.Truncated {
		t.Errorf("List = %+v, want two objects and Truncated=false", got)
	}
}

// start-after is how a cursor resumes without re-listing what is done. It
// applies to the first page only; after that the continuation token carries the
// position, and sending both is an error on some implementations.
func TestList_StartAfterOnlyOnTheFirstPage(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		if len(queries) == 1 {
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated>
			  <NextContinuationToken>p2</NextContinuationToken><Contents><Key>a</Key></Contents></ListBucketResult>`)
			return
		}
		_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	defer srv.Close()

	if _, err := newClient(t, srv.URL, true).List(context.Background(), "", "flow/2026/07/24/a.ndjson", 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	if queries[0].Get("start-after") == "" {
		t.Error("start-after missing from the first page")
	}
	if queries[1].Get("start-after") != "" {
		t.Error("start-after repeated alongside the continuation token")
	}
}

func TestGet_ReturnsTheBody(t *testing.T) {
	srv, got := serve(t, `{"nodeId":"n1"}`, http.StatusOK)
	rc, err := newClient(t, srv.URL, true).Get(context.Background(), "flow/2026/07/24/a.ndjson")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != `{"nodeId":"n1"}` {
		t.Errorf("body = %q", body)
	}
	if got.path != "/flows/flow/2026/07/24/a.ndjson" {
		t.Errorf("path = %q", got.path)
	}
	if got.auth == "" {
		t.Error("the request was not signed")
	}
}

// A 403 is the shape a wrong key, a wrong region or a clock skew takes. The
// status is safe to surface, but the peer-controlled body can reflect request
// credentials and must stay out of the returned error.
func TestGet_SurfacesTheServerError(t *testing.T) {
	srv, _ := serve(t, `<Error><Code>SignatureDoesNotMatch</Code></Error>`, http.StatusForbidden)
	_, err := newClient(t, srv.URL, true).Get(context.Background(), "k")
	if err == nil {
		t.Fatal("Get succeeded against a 403")
	}
	if !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("error = %v, want status without peer-controlled response text", err)
	}
}

// A key with characters needing escaping must be sent exactly as it was signed,
// or the request 403s on precisely the objects whose names are awkward.
func TestGet_EncodedKeyIsSentAsSigned(t *testing.T) {
	srv, got := serve(t, "", http.StatusOK)
	rc, err := newClient(t, srv.URL, true).Get(context.Background(), "flow/a b+c.ndjson")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	rc.Close()
	if got.path != "/flows/flow/a b+c.ndjson" {
		t.Errorf("decoded path = %q, want the key intact", got.path)
	}
	// The signature covers the escaped form, so it is the escaped form that has
	// to reach the server.
	if !strings.Contains(got.requestURI, "%20") {
		t.Errorf("request line = %q, want the space escaped on the wire", got.requestURI)
	}
	if strings.Contains(got.requestURI, "%2520") {
		t.Errorf("request line = %q; the path was encoded twice", got.requestURI)
	}
}

// A reverse-proxied endpoint (e.g. https://gw.example.net/storage/s3) has a
// base path of its own; overwriting it, as request() once did, sends and
// signs the wrong path and every request 404s/403s against everything
// upstream of the proxy prefix.
func TestAddressing_NonRootEndpointPathStyle(t *testing.T) {
	srv, got := serve(t, listOnePage, http.StatusOK)
	c, err := New(Config{
		Endpoint: srv.URL + "/storage/s3", Region: "eu-west-2", Bucket: "flows",
		PathStyle: true, Credentials: staticCreds(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.List(context.Background(), "", "", 0); err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.path != "/storage/s3/flows/" {
		t.Errorf("list path = %q, want the endpoint's base path preserved ahead of the bucket", got.path)
	}

	if _, err := c.Get(context.Background(), "flow/2026/07/24/a.ndjson"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.path != "/storage/s3/flows/flow/2026/07/24/a.ndjson" {
		t.Errorf("get path = %q, want the base path preserved ahead of bucket and key", got.path)
	}
}

// Virtual-host addressing puts the bucket in the hostname, but the endpoint's
// base path must still be preserved in front of the key.
func TestAddressing_NonRootEndpointVirtualHost(t *testing.T) {
	srv, got := serve(t, listOnePage, http.StatusOK)
	c, err := New(Config{
		Endpoint: srv.URL + "/storage/s3", Region: "eu-west-2", Bucket: "flows",
		Credentials: staticCreds(), HTTPClient: dialTo(srv.Listener.Addr().String()),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Get(context.Background(), "flow/2026/07/24/a.ndjson"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(got.host, "flows.") {
		t.Errorf("Host = %q, want the bucket in the hostname", got.host)
	}
	if got.path != "/storage/s3/flow/2026/07/24/a.ndjson" {
		t.Errorf("path = %q, want the base path preserved with the bucket kept out of the path", got.path)
	}
}

// Combines escaping with a non-root, trailing-slash-terminated base path: a
// trailing slash on the configured endpoint must not produce a doubled "/"
// once the bucket and key are appended, unicode/percent/plus/space must all
// be escaped, and "/" separators inside the key must stay unescaped.
func TestGet_EncodedKeyIsSentAsSigned_NonRootEndpoint(t *testing.T) {
	srv, got := serve(t, "", http.StatusOK)
	c, err := New(Config{
		Endpoint: srv.URL + "/storage/s3/", Region: "eu-west-2", Bucket: "flows",
		PathStyle: true, Credentials: staticCreds(),
	})
	if err != nil {
		t.Fatal(err)
	}

	key := "flow/2026/07/24/100% ünïcödé+file.ndjson"
	rc, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	rc.Close()

	want := "/storage/s3/flows/" + key
	if got.path != want {
		t.Errorf("decoded path = %q, want %q", got.path, want)
	}
	if strings.Contains(got.requestURI, "//") {
		t.Errorf("request line = %q, contains a doubled slash", got.requestURI)
	}
	if strings.Contains(got.requestURI, "%2520") || strings.Contains(got.requestURI, "%2525") {
		t.Errorf("request line = %q; the path was encoded twice", got.requestURI)
	}
	// "/" separators must never be escaped, so the escaped request line has
	// exactly as many literal "/" characters as the decoded path.
	if wantSlashes, gotSlashes := strings.Count(want, "/"), strings.Count(got.requestURI, "/"); gotSlashes != wantSlashes {
		t.Errorf("request line = %q has %d '/' separators, want %d matching the decoded path", got.requestURI, gotSlashes, wantSlashes)
	}
}

func TestNew_RejectsIncompleteConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"no endpoint", Config{Bucket: "b", Region: "r", Credentials: staticCreds()}, "endpoint"},
		{"no bucket", Config{Endpoint: "https://s3.example.com", Region: "r", Credentials: staticCreds()}, "bucket"},
		{"no region", Config{Endpoint: "https://s3.example.com", Bucket: "b", Credentials: staticCreds()}, "region"},
		{"no credentials", Config{Endpoint: "https://s3.example.com", Bucket: "b", Region: "r"}, "credentials"},
		{"bad scheme", Config{Endpoint: "s3://b", Bucket: "b", Region: "r", Credentials: staticCreds()}, "scheme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// Every request signs with freshly retrieved credentials, so a provider that has
// rotated them mid-run is picked up without restarting.
func TestRequest_RetrievesCredentialsPerRequest(t *testing.T) {
	srv, got := serve(t, listOnePage, http.StatusOK)
	n := 0
	c, err := New(Config{
		Endpoint: srv.URL, Region: "eu-west-2", Bucket: "flows", PathStyle: true,
		Credentials: providerFunc(func(context.Context) (Credentials, error) {
			n++
			return Credentials{AccessKeyID: fmt.Sprintf("AK%d", n), SecretAccessKey: "SK"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background(), "", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background(), "", "", 0); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("credentials retrieved %d times, want once per request", n)
	}
	if !strings.Contains(got.auth, "Credential=AK2/") {
		t.Errorf("second request signed with %q, want the rotated key", got.auth)
	}
}

type providerFunc func(context.Context) (Credentials, error)

func (f providerFunc) Retrieve(ctx context.Context) (Credentials, error) { return f(ctx) }
