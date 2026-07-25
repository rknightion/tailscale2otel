package hsapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// allocDelta reports the total bytes allocated while fn ran, plus the live heap
// at the instant fn returned (before the next GC). TotalAlloc is cumulative and
// therefore an upper bound on the peak; HeapAlloc sampled immediately after fn
// is the closest in-process proxy for the peak RSS the container would see.
func allocDelta(t *testing.T, fn func()) (total, liveHeap uint64) {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	runtime.GC()
	return after.TotalAlloc - before.TotalAlloc, after.HeapAlloc
}

// repeatReader endlessly repeats s without allocating.
type repeatReader struct {
	s string
	i int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		c := copy(p[n:], r.s[r.i:])
		n += c
		r.i = (r.i + c) % len(r.s)
	}
	return n, nil
}

// TestHSDecodeBudget_OversizedBodyBoundsAllocation is the headline regression for
// #488: an endless Headscale body must be rejected without the decoder ever
// materializing it. Measured on this exact input with the pre-fix
// io.LimitedReader guard (a flat 32 MiB cap checked only AFTER encoding/json had
// built the value): totalAlloc 128.0 MiB, live heap 96.6 MiB — ~38% of the
// 256Mi memory limit the Helm chart ships by default, for a body being rejected.
func TestHSDecodeBudget_OversizedBodyBoundsAllocation(t *testing.T) {
	var err error
	total, live := allocDelta(t, func() {
		body := io.MultiReader(strings.NewReader(`"`), infiniteByteReader{'a'})
		var out string
		err = decodeJSONLimited(body, defaultMaxResponseBytes, &out)
	})
	if err == nil {
		t.Fatal("expected a budget error")
	}
	t.Logf("oversized decode: totalAlloc=%.1f MiB (%d B) liveHeap=%.1f MiB (%d B) — pre-fix: 128.0 MiB / 96.6 MiB",
		float64(total)/(1<<20), total, float64(live)/(1<<20), live)
	// Ceiling well inside the 256 MiB container limit; the pre-fix numbers on
	// this exact input were 128.0 MiB allocated / 96.6 MiB live. The residual
	// cost is encoding/json's own read buffer doubling toward the 4 MiB byte
	// ceiling (~16 MiB cumulative, ~13 MiB live), not the body being buffered.
	const ceiling = 24 << 20
	if total > ceiling {
		t.Fatalf("totalAlloc = %d MiB, want <= %d MiB", total>>20, ceiling>>20)
	}
	if live > ceiling {
		t.Fatalf("liveHeap = %d MiB, want <= %d MiB", live>>20, ceiling>>20)
	}
}

// TestHSDecodeBudget_ByteBudgetExceeded: a body of many small valid tokens, so
// no structural control can trip and only the byte budget can stop it.
func TestHSDecodeBudget_ByteBudgetExceeded(t *testing.T) {
	var err error
	total, live := allocDelta(t, func() {
		body := io.MultiReader(strings.NewReader(`{"nodes":[`), &repeatReader{s: `{"id":"1","name":"n"},`})
		var out map[string]any
		err = decodeJSONLimited(body, defaultMaxResponseBytes, &out)
	})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitBytes {
		t.Fatalf("err = %v, want a bytes *BudgetError", err)
	}
	if be.ConfigKey != cfgKeyMaxResponseBytes {
		t.Fatalf("ConfigKey = %q, want %q — a byte-budget violation must name the key to raise", be.ConfigKey, cfgKeyMaxResponseBytes)
	}
	t.Logf("full-byte-budget stream: totalAlloc=%.1f MiB liveHeap=%.1f MiB", float64(total)/(1<<20), float64(live)/(1<<20))
	// Worst SURVIVABLE case: nothing structural trips, so the 4 MiB byte ceiling
	// is the only control. Must stay far inside the 256 MiB container limit.
	const ceiling = 64 << 20
	if live > ceiling {
		t.Fatalf("liveHeap = %d MiB, want <= %d MiB", live>>20, ceiling>>20)
	}
}

func TestHSDecodeBudget_DeepNestingRejected(t *testing.T) {
	depth := jsonBudgetDepth() + 8
	deep := strings.Repeat("[", depth) + strings.Repeat("]", depth)
	var out any
	err := decodeJSONLimited(strings.NewReader(deep), defaultMaxResponseBytes, &out)
	if !errors.Is(err, ErrResponseTooComplex) {
		t.Fatalf("err = %v, want ErrResponseTooComplex", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitDepth {
		t.Fatalf("err = %v, want a depth *BudgetError", err)
	}
	if errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("a structural violation must not masquerade as ErrResponseTooLarge: %v", err)
	}
}

// TestHSDecodeBudget_HugeStringRejected covers Headscale's one legitimately
// string-shaped payload: GET /api/v1/policy returns the whole ACL document as a
// single JSON string. An endless one must trip the structural string budget.
func TestHSDecodeBudget_HugeStringRejected(t *testing.T) {
	var err error
	total, live := allocDelta(t, func() {
		// A byte ceiling far above the 4 MiB string budget, so the STRUCTURAL
		// control is provably what stops it rather than the byte ceiling.
		body := io.MultiReader(strings.NewReader(`{"policy":"`), infiniteByteReader{'x'})
		var out Policy
		err = decodeJSONLimited(body, 64<<20, &out)
	})
	if !errors.Is(err, ErrResponseTooComplex) {
		t.Fatalf("err = %v, want ErrResponseTooComplex", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitString {
		t.Fatalf("err = %v, want a string *BudgetError", err)
	}
	t.Logf("huge-string decode: totalAlloc=%.1f MiB liveHeap=%.1f MiB", float64(total)/(1<<20), float64(live)/(1<<20))
	const ceiling = 32 << 20
	if live > ceiling {
		t.Fatalf("liveHeap = %d MiB, want <= %d MiB", live>>20, ceiling>>20)
	}
}

// TestHSDecodeBudget_HugeArrayRejected covers the cheapest attack per byte:
// `[0,0,0,…]` costs 2 bytes on the wire and ~16 decoded, so the element budget
// has to stop it well before the byte ceiling would.
func TestHSDecodeBudget_HugeArrayRejected(t *testing.T) {
	var err error
	total, live := allocDelta(t, func() {
		// 64 MiB byte ceiling: at 2 B/element the element budget (500,000) trips
		// after ~1 MiB, proving the structural control is the one that fires.
		body := io.MultiReader(strings.NewReader(`[`), &repeatReader{s: `0,`})
		var out []int
		err = decodeJSONLimited(body, 64<<20, &out)
	})
	if !errors.Is(err, ErrResponseTooComplex) {
		t.Fatalf("err = %v, want ErrResponseTooComplex", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitArrayElements {
		t.Fatalf("err = %v, want an array-elements *BudgetError", err)
	}
	t.Logf("huge-array decode: totalAlloc=%.1f MiB liveHeap=%.1f MiB", float64(total)/(1<<20), float64(live)/(1<<20))
	const ceiling = 32 << 20
	if live > ceiling {
		t.Fatalf("liveHeap = %d MiB, want <= %d MiB", live>>20, ceiling>>20)
	}
}

// representativeNodeList builds a fully-populated GET /api/v1/node response
// shaped like Headscale's grpc-gateway JSON (types.go): every node carries dual
// -stack addresses, an embedded user, three route lists and tags.
func representativeNodeList(nodes int) []byte {
	r := nodesResponse{Nodes: make([]Node, nodes)}
	for i := range r.Nodes {
		r.Nodes[i] = Node{
			ID:          fmt.Sprintf("%d", i+1),
			Name:        fmt.Sprintf("host-%04d.headscale.example.org", i),
			GivenName:   fmt.Sprintf("host-%04d", i),
			IPAddresses: []string{fmt.Sprintf("100.64.%d.%d", i%256, (i/256)%256), fmt.Sprintf("fd7a:115c:a1e0::%x", i)},
			User: User{
				ID: fmt.Sprintf("%d", i%40), Name: fmt.Sprintf("user%d", i%40),
				DisplayName: fmt.Sprintf("User Number %d", i%40),
				Email:       fmt.Sprintf("user%d@example.org", i%40),
				ProviderID:  fmt.Sprintf("oidc-%016d", i%40), Provider: "oidc",
				ProfilePicURL: fmt.Sprintf("https://example.org/avatars/user%d.png", i%40),
				CreatedAt:     "2026-01-01T00:00:00Z",
			},
			LastSeen: "2026-07-25T10:00:00Z", Expiry: "2026-10-25T10:00:00Z",
			CreatedAt: "2026-01-01T00:00:00Z", RegisterMethod: "REGISTER_METHOD_AUTH_KEY", Online: true,
			ApprovedRoutes:  []string{"10.0.0.0/24", "192.168.1.0/24"},
			AvailableRoutes: []string{"10.0.0.0/24", "192.168.1.0/24", "0.0.0.0/0", "::/0"},
			SubnetRoutes:    []string{"10.0.0.0/24"},
			Tags:            []string{"tag:server", "tag:prod"},
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	return b
}

// TestHSDecodeBudget_RepresentativeNodeListAccepted is the "don't reject real
// traffic" guard, and the evidence behind the 4 MiB default: a regression that
// rejects legitimate Headscale responses is worse than the bug being fixed.
func TestHSDecodeBudget_RepresentativeNodeListAccepted(t *testing.T) {
	for _, nodes := range []int{1, 100, 2000} {
		body := representativeNodeList(nodes)
		var out nodesResponse
		var err error
		total, _ := allocDelta(t, func() {
			err = decodeJSONLimited(strings.NewReader(string(body)), defaultMaxResponseBytes, &out)
		})
		if err != nil {
			t.Fatalf("nodes=%d: decode: %v", nodes, err)
		}
		if len(out.Nodes) != nodes {
			t.Fatalf("nodes=%d: decoded %d", nodes, len(out.Nodes))
		}
		perNode := float64(len(body)) / float64(nodes)
		pct := 100 * float64(len(body)) / float64(defaultMaxResponseBytes)
		t.Logf("node list: nodes=%d wire=%d bytes (%.0f B/node, %.2f%% of the %d MiB budget, %.0fx headroom) totalAlloc=%d KiB",
			nodes, len(body), perNode, pct, defaultMaxResponseBytes>>20, 100/pct, total>>10)
	}
}

// TestHSDecodeBudget_PolicyDocumentAccepted: a 512 KiB ACL policy carried as one
// JSON string — far past any hand-written Headscale policy — is still inside
// both the byte budget and the 4 MiB structural string budget.
func TestHSDecodeBudget_PolicyDocumentAccepted(t *testing.T) {
	body, err := json.Marshal(Policy{Policy: strings.Repeat("a", 512<<10), UpdatedAt: "2026-07-25T10:00:00Z"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Policy
	if err := decodeJSONLimited(strings.NewReader(string(body)), defaultMaxResponseBytes, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Policy) != 512<<10 {
		t.Fatalf("policy len = %d", len(out.Policy))
	}
	t.Logf("policy doc: wire=%d bytes (%.1f%% of the %d MiB byte budget), single string 512 KiB (12.5%% of the 4 MiB string budget)",
		len(body), 100*float64(len(body))/float64(defaultMaxResponseBytes), defaultMaxResponseBytes>>20)
}

func TestHSDecodeBudget_MalformedUnderBudgetIsSyntaxError(t *testing.T) {
	var out map[string]any
	err := decodeJSONLimited(strings.NewReader(`{not valid json`), defaultMaxResponseBytes, &out)
	if err == nil {
		t.Fatal("expected a decode error")
	}
	var be *BudgetError
	if errors.As(err, &be) {
		t.Fatalf("malformed-but-small body must not report a budget error, got %v", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("err = %T: %v, want *json.SyntaxError", err, err)
	}
}

// TestHSGetJSON_NoContentLengthOversizedIsBounded covers the chunked-transfer
// case: with no Content-Length there is nothing to reject up front, so the
// streaming budget is the only control.
func TestHSGetJSON_NoContentLengthOversizedIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No Content-Length and an incrementally-written body, so Go falls back
		// to chunked transfer encoding.
		_, _ = io.WriteString(w, `{"nodes":[{"name":"`)
		w.(http.Flusher).Flush()
		_, _ = io.Copy(w, infiniteByteReader{'a'})
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "k", Timeout: 30 * time.Second})

	var gotLen int64
	var reqErr error
	start := time.Now()
	total, live := allocDelta(t, func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/probe", nil)
		resp, e := c.http.Do(req)
		if e != nil {
			reqErr = e
			return
		}
		defer resp.Body.Close()
		gotLen = resp.ContentLength
		_, reqErr = c.Nodes(t.Context())
	})
	if gotLen != -1 {
		t.Fatalf("ContentLength = %d, want -1 (chunked, no Content-Length)", gotLen)
	}
	if !errors.Is(reqErr, ErrResponseTooLarge) && !errors.Is(reqErr, ErrResponseTooComplex) {
		t.Fatalf("err = %v, want a budget error", reqErr)
	}
	elapsed := time.Since(start)
	t.Logf("chunked oversized: err=%v elapsed=%s totalAlloc=%.1f MiB liveHeap=%.1f MiB",
		reqErr, elapsed.Round(time.Millisecond), float64(total)/(1<<20), float64(live)/(1<<20))
	if total > 64<<20 {
		t.Fatalf("totalAlloc = %d MiB, want <= 64 MiB", total>>20)
	}
	// The post-decode drain that keeps the connection reusable must be BOUNDED.
	// Against an endless body an unbounded drain runs until the 30s client
	// timeout, turning a rejected response into 30s of wasted bandwidth per poll
	// — the tight loop #488's acceptance criteria forbid.
	if elapsed > 10*time.Second {
		t.Fatalf("rejecting an endless body took %s, want well under the 30s client timeout "+
			"(the post-decode body drain is unbounded)", elapsed)
	}
}

// TestHSGetJSON_DeclaredContentLengthRejectedBeforeReading covers the cheap
// pre-check: an upstream that DECLARES an over-budget Content-Length is refused
// without the body being decoded at all.
func TestHSGetJSON_DeclaredContentLengthRejectedBeforeReading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "999999999")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.LimitReader(infiniteByteReader{'a'}, 1<<16))
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "k", Timeout: 30 * time.Second})
	_, err := c.Nodes(t.Context())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitBytes || be.ConfigKey != cfgKeyMaxResponseBytes {
		t.Fatalf("err = %v, want a bytes *BudgetError naming %s", err, cfgKeyMaxResponseBytes)
	}
}

// TestHSClientBudget_DefaultAndOverride pins the operator knob: zero means the
// built-in default, an explicit value is used as given.
func TestHSClientBudget_DefaultAndOverride(t *testing.T) {
	if got := NewClient(Options{URL: "https://example.invalid"}).budget().MaxBytes; got != defaultMaxResponseBytes {
		t.Fatalf("default budget = %d, want %d", got, int64(defaultMaxResponseBytes))
	}
	if got := NewClient(Options{URL: "https://example.invalid", MaxResponseBytes: 7 << 20}).budget().MaxBytes; got != 7<<20 {
		t.Fatalf("override budget = %d, want %d", got, 7<<20)
	}
	// A negative value can only come from a config path that Validate rejects;
	// the client must still fall back rather than refuse every body.
	if got := NewClient(Options{URL: "https://example.invalid", MaxResponseBytes: -1}).budget().MaxBytes; got != defaultMaxResponseBytes {
		t.Fatalf("negative budget = %d, want the built-in default", got)
	}
}

// TestHSGetJSON_SmallerBudgetIsWired proves the configured value really reaches
// the decode path: a body over a tightened budget is rejected.
func TestHSGetJSON_SmallerBudgetIsWired(t *testing.T) {
	body := representativeNodeList(2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	// Comfortably accepted under the default budget...
	if _, err := NewClient(Options{URL: srv.URL, APIKey: "k", Timeout: 30 * time.Second}).Nodes(t.Context()); err != nil {
		t.Fatalf("default budget rejected a %d-byte real response: %v", len(body), err)
	}
	// ...and rejected once the operator tightens the knob below the wire size.
	tight := int64(len(body) / 2)
	_, err := NewClient(Options{URL: srv.URL, APIKey: "k", Timeout: 30 * time.Second, MaxResponseBytes: tight}).Nodes(t.Context())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge at max_response_bytes=%d", err, tight)
	}
}

// jsonBudgetDepth returns the shared structural nesting limit, discovered
// through the package's own budget so the test cannot drift from it.
func jsonBudgetDepth() int { return budgetOf(defaultMaxResponseBytes).MaxDepth }
