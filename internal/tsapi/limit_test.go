package tsapi

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
// real (256 MiB) cap without actually materializing hundreds of megabytes.
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
		Logs []string `json:"logs"`
	}
	err := decodeJSONLimited(strings.NewReader(`{"logs":["a","b"]}`), maxResponseBytes, &out)
	if err != nil {
		t.Fatalf("decodeJSONLimited: %v", err)
	}
	if len(out.Logs) != 2 || out.Logs[0] != "a" || out.Logs[1] != "b" {
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
	// materializing hundreds of MB under the race detector; the production
	// maxResponseBytes constant is exercised end-to-end (real network,
	// getJSON, and the wired constant) by
	// TestNetworkFlowLogs_OversizedBodyFailsWithErrResponseTooLarge below.
	const limit = 4096
	cr := &countingReader{r: infiniteByteReader{'a'}}
	body := io.MultiReader(strings.NewReader(`"`), cr)

	var out string
	err := decodeJSONLimited(body, limit, &out)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	// decodeJSONLimited must never read more than cap+1 bytes total; the
	// leading `"` accounts for one of them.
	if cr.n > limit {
		t.Fatalf("read %d bytes past the opening quote, want <= %d (cap)", cr.n, limit)
	}
}

func TestGetJSON_NonSuccessStatusStillTruncatesAt16KiB(t *testing.T) {
	huge := strings.Repeat("x", 1<<16) // 64 KiB, well over the 16 KiB cap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, huge)
	}))
	defer srv.Close()

	c, err := NewClient(Options{Tailnet: "example.com", BaseURL: srv.URL, APIKey: "k", MaxAttempts: 1})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out struct{}
	err = c.getJSON(t.Context(), srv.URL+"/x", &out)
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want *StatusError", err)
	}
	if statusErr.Code != http.StatusInternalServerError {
		t.Fatalf("Code = %d, want 500", statusErr.Code)
	}
	if len(statusErr.Body) != 1<<14 {
		t.Fatalf("Body len = %d, want exactly %d (16 KiB truncation preserved)", len(statusErr.Body), 1<<14)
	}
}

func TestNetworkFlowLogs_OversizedBodyFailsWithErrResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `"`)
		// Stream well beyond maxResponseBytes; the client must stop reading
		// once the cap is exceeded rather than read this to completion.
		_, _ = io.Copy(w, infiniteByteReader{'a'})
	}))
	defer srv.Close()

	c, err := NewClient(Options{Tailnet: "example.com", BaseURL: srv.URL, APIKey: "k", MaxAttempts: 1, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.NetworkFlowLogs(t.Context(), time.Unix(0, 0), time.Unix(60, 0))
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
}
