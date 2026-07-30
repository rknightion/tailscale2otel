package tsapi

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

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
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

// TestDecodeBudget_OversizedBodyBoundsAllocation is the headline regression for
// #474: an endless body must be rejected without the decoder ever materializing
// a multi-hundred-megabyte buffer. Before the fix this allocated ~1 GiB (live
// heap ~896 MiB) against the old 256 MiB byte cap, well past the 256 MiB Helm
// memory limit. The structural string budget now trips first, so the ceiling is
// a small multiple of maxDecodeStringBytes regardless of the byte budget.
func TestDecodeBudget_OversizedBodyBoundsAllocation(t *testing.T) {
	var err error
	total, live := allocDelta(t, func() {
		body := io.MultiReader(strings.NewReader(`"`), infiniteByteReader{'a'})
		var out string
		err = decodeJSONBudgeted(body, budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out)
	})
	if err == nil {
		t.Fatal("expected a budget error")
	}
	// The body is one endless string literal, so the structural string budget is
	// what stops it — long before the 32 MiB byte ceiling is reached. That is the
	// whole point: the byte ceiling is no longer the first line of defense.
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitString {
		t.Fatalf("err = %v, want a string *BudgetError", err)
	}
	t.Logf("oversized decode: totalAlloc=%d MiB liveHeap=%d MiB (pre-fix: 1024 MiB / 896 MiB)", total>>20, live>>20)
	// Generous ceiling relative to the 256 MiB container limit; the pre-fix
	// numbers on this exact input were 1024 MiB allocated / 896 MiB live.
	const ceiling = 32 << 20
	if total > ceiling {
		t.Fatalf("totalAlloc = %d MiB, want <= %d MiB", total>>20, ceiling>>20)
	}
	if live > ceiling {
		t.Fatalf("liveHeap = %d MiB, want <= %d MiB", live>>20, ceiling>>20)
	}
}

// TestDecodeBudget_FullLogBudgetStreamStaysInsideContainer is the worst
// SURVIVABLE case: an endless stream of small, individually-legal records, so
// nothing structural trips and the 32 MiB byte ceiling is the only control. The
// decode must still finish well inside the 256 MiB Helm memory limit.
func TestDecodeBudget_FullLogBudgetStreamStaysInsideContainer(t *testing.T) {
	// ~250 B per element — realistic record size, so the per-array element budget
	// (500,000) is not what stops it; 32 MiB / 250 B is ~134,000 elements.
	rec := `{"nodeId":"` + strings.Repeat("n", 200) + `","start":"2026-07-25T10:00:00Z"},`
	var err error
	total, live := allocDelta(t, func() {
		body := io.MultiReader(strings.NewReader(`{"logs":[`), &repeatReader{s: rec})
		var out map[string]any
		err = decodeJSONBudgeted(body, budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out)
	})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	t.Logf("full-byte-budget stream: totalAlloc=%d MiB liveHeap=%d MiB", total>>20, live>>20)
	// Half the 256 MiB container limit, leaving room for the rest of the process.
	const ceiling = 128 << 20
	if live > ceiling {
		t.Fatalf("liveHeap = %d MiB, want <= %d MiB", live>>20, ceiling>>20)
	}
}

func TestDecodeBudget_ByteBudgetExceeded(t *testing.T) {
	// A body of many small valid tokens: no single string, array or depth
	// violation, so only the byte budget can stop it.
	body := io.MultiReader(strings.NewReader(`[`), &repeatReader{s: `1,`})
	var out []int
	b := decodeBudget{MaxBytes: 8 << 10, MaxDepth: maxDecodeDepth, MaxStringBytes: maxDecodeStringBytes, MaxArrayElements: 1 << 30, ConfigKey: cfgKeyMaxResponseBytes}
	err := decodeJSONBudgeted(body, b, &out)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v (%T), want a *BudgetError", err, err)
	}
	if be.Limit != BudgetLimitBytes {
		t.Fatalf("Limit = %q, want %q", be.Limit, BudgetLimitBytes)
	}
	if be.ConfigKey == "" {
		t.Fatal("a byte-budget violation must name the config key to raise")
	}
}

func TestDecodeBudget_DeepNestingRejected(t *testing.T) {
	deep := strings.Repeat("[", maxDecodeDepth+8) + strings.Repeat("]", maxDecodeDepth+8)
	var out any
	err := decodeJSONBudgeted(strings.NewReader(deep), budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out)
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

func TestDecodeBudget_HugeStringRejected(t *testing.T) {
	var err error
	total, _ := allocDelta(t, func() {
		body := io.MultiReader(strings.NewReader(`{"logs":"`), infiniteByteReader{'x'})
		var out map[string]any
		err = decodeJSONBudgeted(body, budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out)
	})
	if !errors.Is(err, ErrResponseTooComplex) {
		t.Fatalf("err = %v, want ErrResponseTooComplex", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitString {
		t.Fatalf("err = %v, want a string *BudgetError", err)
	}
	t.Logf("huge-string decode: totalAlloc=%d MiB", total>>20)
}

func TestDecodeBudget_HugeArrayRejected(t *testing.T) {
	// Enough "0," elements to blow the element budget while staying under the
	// byte budget it is paired with here.
	b := decodeBudget{MaxBytes: 64 << 20, MaxDepth: maxDecodeDepth, MaxStringBytes: maxDecodeStringBytes, MaxArrayElements: 1000, ConfigKey: cfgKeyMaxResponseBytes}
	body := io.MultiReader(strings.NewReader(`[`), &repeatReader{s: `0,`})
	var out []int
	err := decodeJSONBudgeted(body, b, &out)
	if !errors.Is(err, ErrResponseTooComplex) {
		t.Fatalf("err = %v, want ErrResponseTooComplex", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != BudgetLimitArrayElements {
		t.Fatalf("err = %v, want an array-elements *BudgetError", err)
	}
}

// TestDecodeBudget_ElementBudgetIsPerContainer proves the element counter is
// per-array, not a running total: many sibling arrays, each small, must pass.
func TestDecodeBudget_ElementBudgetIsPerContainer(t *testing.T) {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := range 200 {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`[1,2,3,4,5]`)
	}
	sb.WriteByte(']')
	b := decodeBudget{MaxBytes: 1 << 20, MaxDepth: maxDecodeDepth, MaxStringBytes: maxDecodeStringBytes, MaxArrayElements: 300, ConfigKey: cfgKeyMaxResponseBytes}
	var out [][]int
	if err := decodeJSONBudgeted(strings.NewReader(sb.String()), b, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 200 || len(out[0]) != 5 {
		t.Fatalf("out = %d outer, %d inner", len(out), len(out[0]))
	}
}

// TestDecodeBudget_StringContentIsNotStructure guards the scanner's string
// state machine: braces, brackets, commas and escaped quotes INSIDE a JSON
// string must not be counted as structure, or valid traffic gets rejected.
func TestDecodeBudget_StringContentIsNotStructure(t *testing.T) {
	const doc = `{"a":"[[[{{{,,,\"}]\\","b":["x,y","{\"k\":1}"],"c":"\\"}`
	b := decodeBudget{MaxBytes: 1 << 20, MaxDepth: 4, MaxStringBytes: 1 << 10, MaxArrayElements: 4, ConfigKey: cfgKeyMaxResponseBytes}
	var out map[string]any
	if err := decodeJSONBudgeted(strings.NewReader(doc), b, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := out["a"]; got != `[[[{{{,,,"}]\` {
		t.Fatalf("a = %q", got)
	}
	if got := out["c"]; got != `\` {
		t.Fatalf("c = %q", got)
	}
}

func TestDecodeBudget_NormalResponseUnchanged(t *testing.T) {
	var out struct {
		Logs []string `json:"logs"`
	}
	if err := decodeJSONBudgeted(strings.NewReader(`{"logs":["a","b"]}`), budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Logs) != 2 {
		t.Fatalf("out = %+v", out)
	}
}

func TestDecodeBudget_MalformedUnderBudgetIsSyntaxError(t *testing.T) {
	var out map[string]any
	err := decodeJSONBudgeted(strings.NewReader(`{not valid json`), budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out)
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

// representativeFlowLogEnvelope builds a flow-log response shaped like the live
// API (see internal/flowlog/record.go and the field notes there): every record
// carries srcNode/dstNodes identities and four traffic slices.
func representativeFlowLogEnvelope(records int) []byte {
	base := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	resp := flowlog.NetworkResponse{Logs: make([]flowlog.FlowLog, records)}
	for i := range resp.Logs {
		node := func(n int) flowlog.NodeRef {
			return flowlog.NodeRef{
				NodeID:    fmt.Sprintf("nodeid-%016d", n),
				Name:      fmt.Sprintf("host-%04d.tail1a2b3c.ts.net", n),
				Addresses: []string{fmt.Sprintf("100.64.%d.%d", n%256, (n/256)%256), fmt.Sprintf("fd7a:115c:a1e0::%x", n)},
				Tags:      []string{"tag:server", "tag:prod"},
				User:      fmt.Sprintf("user%d@example.com", n%40),
				OS:        "linux",
			}
		}
		conns := make([]flowlog.ConnectionCounts, 6)
		for j := range conns {
			conns[j] = flowlog.ConnectionCounts{
				Proto:  6,
				Src:    fmt.Sprintf("100.64.1.%d:%d", j, 40000+j),
				Dst:    fmt.Sprintf("100.64.2.%d:443", j),
				TxPkts: int64(1000 + j), TxBytes: int64(100000 + j),
				RxPkts: int64(900 + j), RxBytes: int64(90000 + j),
			}
		}
		dst := make([]flowlog.NodeRef, 3)
		for j := range dst {
			dst[j] = node(i*3 + j + 1)
		}
		src := node(i)
		resp.Logs[i] = flowlog.FlowLog{
			Logged:          base.Add(time.Duration(i) * time.Second),
			NodeID:          src.NodeID,
			Start:           base.Add(time.Duration(i)*time.Second - time.Minute),
			End:             base.Add(time.Duration(i) * time.Second),
			VirtualTraffic:  conns,
			SubnetTraffic:   conns[:2],
			ExitTraffic:     conns[:1],
			PhysicalTraffic: conns[:3],
			SrcNode:         &src,
			DstNodes:        dst,
		}
	}
	b, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return b
}

// representativeAuditEnvelope builds an audit response whose events carry the
// polymorphic old/new payloads. The FIRST event embeds a whole tailnet policy
// file as a JSON string (aclBytes) — the worst realistic single-string case —
// while the rest carry ordinary small values.
func representativeAuditEnvelope(events int, aclBytes int) []byte {
	base := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	acl, _ := json.Marshal(strings.Repeat("a", aclBytes))
	small, _ := json.Marshal("tag:server")
	resp := audit.ConfigurationResponse{Version: "v1", TailnetID: "example.com", Logs: make([]audit.Event, events)}
	for i := range resp.Logs {
		newVal := small
		if i == 0 {
			newVal = acl
		}
		resp.Logs[i] = audit.Event{
			EventTime:    base.Add(time.Duration(i) * time.Second),
			Type:         "POLICY_FILE",
			EventGroupID: fmt.Sprintf("grp-%016d", i),
			Origin:       "admin-console",
			Actor: audit.Actor{
				ID: fmt.Sprintf("uid-%016d", i), Type: "USER",
				LoginName:   fmt.Sprintf("user%d@example.com", i%40),
				DisplayName: fmt.Sprintf("User Number %d", i%40), Tags: []string{"tag:admin"},
			},
			Target:        audit.Target{ID: fmt.Sprintf("tid-%016d", i), Name: "policy", Type: "TAILNET", Property: "acl"},
			Action:        "UPDATE",
			Old:           json.RawMessage(`null`),
			New:           json.RawMessage(newVal),
			ActionDetails: "updated the tailnet policy file",
		}
	}
	b, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDecodeBudget_RepresentativeFlowLogEnvelopeAccepted(t *testing.T) {
	for _, records := range []int{1, 100, 5000} {
		body := representativeFlowLogEnvelope(records)
		var out flowlog.NetworkResponse
		var err error
		total, _ := allocDelta(t, func() {
			err = decodeJSONBudgeted(strings.NewReader(string(body)), budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out)
		})
		if err != nil {
			t.Fatalf("records=%d: decode: %v", records, err)
		}
		if len(out.Logs) != records {
			t.Fatalf("records=%d: decoded %d", records, len(out.Logs))
		}
		t.Logf("flow-log envelope: records=%d wire=%d bytes (%.1f B/record, %.2f%% of the %d MiB budget) totalAlloc=%d KiB",
			records, len(body), float64(len(body))/float64(records),
			100*float64(len(body))/float64(defaultMaxLogResponseBytes), defaultMaxLogResponseBytes>>20, total>>10)
	}
}

func TestDecodeBudget_RepresentativeAuditEnvelopeAccepted(t *testing.T) {
	// 500 events, one of them carrying a 512 KiB tailnet policy file as a single
	// JSON string — far past any hand-written ACL — and still inside both budgets.
	body := representativeAuditEnvelope(500, 512<<10)
	var out audit.ConfigurationResponse
	if err := decodeJSONBudgeted(strings.NewReader(string(body)), budgetOf(defaultMaxLogResponseBytes, cfgKeyMaxLogResponseBytes), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Logs) != 500 {
		t.Fatalf("decoded %d events", len(out.Logs))
	}
	t.Logf("audit envelope: events=500 wire=%d bytes (%.1f%% of the %d MiB budget), max single string 512 KiB (%.1f%% of the %d MiB string budget)",
		len(body), 100*float64(len(body))/float64(defaultMaxLogResponseBytes), defaultMaxLogResponseBytes>>20,
		100*float64(512<<10)/float64(maxDecodeStringBytes), maxDecodeStringBytes>>20)
}

// TestGetJSON_NoContentLengthOversizedIsBounded covers the chunked-transfer
// case: with no Content-Length there is nothing to reject up front, so the
// streaming budget is the only control.
func TestGetJSON_NoContentLengthOversizedIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No Content-Length set and the body is written incrementally, so Go
		// falls back to chunked transfer encoding.
		_, _ = io.WriteString(w, `{"logs":[{"nodeId":"`)
		fl := w.(http.Flusher)
		fl.Flush()
		_, _ = io.Copy(w, infiniteByteReader{'a'})
	}))
	defer srv.Close()

	c, err := NewClient(Options{Tailnet: "example.com", BaseURL: srv.URL, APIKey: "k", MaxAttempts: 1, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var gotLen int64
	var reqErr error
	total, live := allocDelta(t, func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/probe", nil)
		resp, e := c.http.Do(req)
		if e != nil {
			reqErr = e
			return
		}
		defer resp.Body.Close()
		gotLen = resp.ContentLength
		_, reqErr = c.NetworkFlowLogs(t.Context(), time.Unix(0, 0), time.Unix(60, 0))
	})
	if gotLen != -1 {
		t.Fatalf("ContentLength = %d, want -1 (chunked, no Content-Length)", gotLen)
	}
	if !errors.Is(reqErr, ErrResponseTooComplex) && !errors.Is(reqErr, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want a budget error", reqErr)
	}
	t.Logf("chunked oversized: err=%v totalAlloc=%d MiB liveHeap=%d MiB", reqErr, total>>20, live>>20)
	if total > 64<<20 {
		t.Fatalf("totalAlloc = %d MiB, want <= 64 MiB", total>>20)
	}
}

// TestClientBudgets_TieredDefaults pins the tiering: the log endpoints get the
// bulk budget, every other endpoint the smaller one.
func TestClientBudgets_TieredDefaults(t *testing.T) {
	c, err := NewClient(Options{Tailnet: "example.com", BaseURL: "https://example.invalid", APIKey: "k"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.apiBudget().MaxBytes; got != defaultMaxResponseBytes {
		t.Fatalf("apiBudget = %d, want %d", got, int64(defaultMaxResponseBytes))
	}
	if got := c.logBudget().MaxBytes; got != defaultMaxLogResponseBytes {
		t.Fatalf("logBudget = %d, want %d", got, int64(defaultMaxLogResponseBytes))
	}
	c2, err := NewClient(Options{Tailnet: "example.com", BaseURL: "https://example.invalid", APIKey: "k",
		MaxResponseBytes: 1 << 20, MaxLogResponseBytes: 2 << 20})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c2.apiBudget().MaxBytes; got != 1<<20 {
		t.Fatalf("apiBudget = %d, want %d", got, 1<<20)
	}
	if got := c2.logBudget().MaxBytes; got != 2<<20 {
		t.Fatalf("logBudget = %d, want %d", got, 2<<20)
	}
}

// TestGetJSON_NonLogEndpointUsesSmallerBudget proves the non-log tier is really
// wired: a body over the small budget but under the log budget is rejected.
func TestGetJSON_NonLogEndpointUsesSmallerBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":[`)
		_, _ = io.CopyN(w, &repeatReader{s: `{"id":"x"},`}, 2<<20)
		_, _ = io.WriteString(w, `{"id":"y"}]}`)
	}))
	defer srv.Close()

	c, err := NewClient(Options{Tailnet: "example.com", BaseURL: srv.URL, APIKey: "k", MaxAttempts: 1,
		Timeout: 30 * time.Second, MaxResponseBytes: 1 << 20, MaxLogResponseBytes: 32 << 20})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out map[string]any
	err = c.getJSON(t.Context(), srv.URL+"/api/v2/tailnet/example.com/devices", &out)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
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
