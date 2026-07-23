package hsapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// infiniteByteReader yields an endless stream of the same byte without
// pre-allocating a large buffer, so tests can prove boundedness against the
// real (32 MiB) cap cheaply.
type infiniteByteReader struct{ b byte }

func (r infiniteByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.b
	for i := 1; i < len(p); i *= 2 {
		copy(p[i:], p[:i])
	}
	return len(p), nil
}

// countingReader records how many bytes were pulled through it, so a test
// can assert an upper bound on what decodeJSONLimited actually consumed.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestDecodeJSONLimited_NormalResponseUnchanged(t *testing.T) {
	var out struct {
		Nodes []string `json:"nodes"`
	}
	err := decodeJSONLimited(strings.NewReader(`{"nodes":["a","b"]}`), maxResponseBytes, &out)
	if err != nil {
		t.Fatalf("decodeJSONLimited: %v", err)
	}
	if len(out.Nodes) != 2 || out.Nodes[0] != "a" || out.Nodes[1] != "b" {
		t.Fatalf("out = %+v", out)
	}
}

func TestDecodeJSONLimited_MalformedJSONUnderCapIsNotTooLarge(t *testing.T) {
	var out map[string]any
	err := decodeJSONLimited(strings.NewReader(`{not valid json`), maxResponseBytes, &out)
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("malformed-but-small body must not report ErrResponseTooLarge, got: %v", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("expected a *json.SyntaxError, got %T: %v", err, err)
	}
}

func TestDecodeJSONLimited_OversizedFailsWithoutBufferingWholeBody(t *testing.T) {
	// Body: an opening quote for a JSON string value that never closes,
	// followed by an endless stream. A correct decoder never finds the end
	// of the string and keeps asking for more bytes until the cap trips.
	// A small custom limit exercises the general algorithm quickly, without
	// materializing dozens of MB under the race detector; the production
	// maxResponseBytes constant is exercised end-to-end (real network,
	// getJSON, and the wired constant) by
	// TestClientNodes_OversizedBodyFailsWithErrResponseTooLarge below.
	const limit = 4096
	cr := &countingReader{r: infiniteByteReader{'a'}}
	body := io.MultiReader(strings.NewReader(`"`), cr)

	var out string
	err := decodeJSONLimited(body, limit, &out)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if cr.n > limit {
		t.Fatalf("read %d bytes past the opening quote, want <= %d (cap)", cr.n, limit)
	}
}

func TestGetJSON_Non200StillTruncatesAt512Bytes(t *testing.T) {
	huge := strings.Repeat("x", 4096) // well over the 512-byte cap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, huge, http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "x", Timeout: 5 * time.Second})
	_, err := c.Nodes(t.Context())
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	msg := err.Error()
	// The formatted error embeds the (trimmed) truncated body; it must not
	// contain the whole 4096-byte payload.
	if strings.Count(msg, "x") > 512 {
		t.Fatalf("error message embeds more than 512 bytes of body: len=%d", len(msg))
	}
}

func TestClientNodes_OversizedBodyFailsWithErrResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `"`)
		// Stream a bounded-but-larger-than-cap body and then let the handler
		// return (closing the response), so the client's post-decode drain
		// defer has a small, finite remainder to discard instead of
		// draining an endless stream.
		_, _ = io.CopyN(w, infiniteByteReader{'a'}, maxResponseBytes+(1<<20))
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "secret", Timeout: 30 * time.Second})
	_, err := c.Nodes(t.Context())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
}
