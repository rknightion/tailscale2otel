package s3

import (
	"strings"
	"testing"
)

// requestPath is the single place that computes the full URL path for a
// request: the endpoint's own base path (e.g. a reverse-proxy prefix such as
// "/storage/s3"), the bucket when addressing path-style (virtual-host
// addressing puts the bucket in the host instead), and the caller-supplied
// path (always "/" for a list, or "/"+key for a get). These pure cases pin
// the join's exact shape without a full HTTP round trip; a root endpoint's
// result must stay byte-identical to what request() produced before this
// method existed.
func TestClient_RequestPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoint  string
		pathStyle bool
		path      string
		want      string
	}{
		{"root endpoint, path-style, list root", "https://s3.example.com", true, "/", "/flows/"},
		{"root endpoint, path-style, get key", "https://s3.example.com", true, "/flow/a.ndjson", "/flows/flow/a.ndjson"},
		{"root endpoint, virtual-host, list root", "https://s3.example.com", false, "/", "/"},
		{"root endpoint, virtual-host, get key", "https://s3.example.com", false, "/flow/a.ndjson", "/flow/a.ndjson"},
		{"explicit root slash endpoint, path-style", "https://s3.example.com/", true, "/", "/flows/"},
		{"non-root endpoint, path-style, list root", "https://gw.example.net/storage/s3", true, "/", "/storage/s3/flows/"},
		{"non-root endpoint, trailing slash, path-style, get key", "https://gw.example.net/storage/s3/", true, "/flow/a.ndjson", "/storage/s3/flows/flow/a.ndjson"},
		{"non-root endpoint, virtual-host, get key", "https://gw.example.net/storage/s3", false, "/flow/a.ndjson", "/storage/s3/flow/a.ndjson"},
		{"deeply nested base path, path-style, list root", "https://gw.example.net/a/b/c", true, "/", "/a/b/c/flows/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(Config{
				Endpoint: tc.endpoint, Region: "eu-west-2", Bucket: "flows",
				PathStyle: tc.pathStyle, Credentials: staticCreds(),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.requestPath(tc.path); got != tc.want {
				t.Errorf("requestPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// The join must never produce a doubled "/" or drop the leading "/",
// regardless of how the configured base path is spelled.
func TestClient_RequestPath_NeverDoublesSlashOrDropsLeadingSlash(t *testing.T) {
	bases := []string{"", "/", "/storage/s3", "/storage/s3/", "/a/b/c/"}
	paths := []string{"/", "/key", "/dir/key"}
	for _, base := range bases {
		for _, pathStyle := range []bool{true, false} {
			for _, path := range paths {
				c, err := New(Config{
					Endpoint: "https://gw.example.net" + base, Region: "eu-west-2", Bucket: "flows",
					PathStyle: pathStyle, Credentials: staticCreds(),
				})
				if err != nil {
					t.Fatalf("New(base=%q): %v", base, err)
				}
				got := c.requestPath(path)
				if !strings.HasPrefix(got, "/") {
					t.Errorf("base=%q pathStyle=%v path=%q: requestPath = %q, want a leading /", base, pathStyle, path, got)
				}
				if strings.Contains(got, "//") {
					t.Errorf("base=%q pathStyle=%v path=%q: requestPath = %q, contains a doubled /", base, pathStyle, path, got)
				}
			}
		}
	}
}
