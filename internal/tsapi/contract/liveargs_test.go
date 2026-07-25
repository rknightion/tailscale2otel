package contract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi/contract"
)

// newFakeAPI stands up a server that routes on the request path, so the
// path-parameter behaviour under test is actually observable (contract.Decode's
// echo-everything server deliberately is not).
func newFakeAPI(t *testing.T, routes map[string]string) (*tsapi.Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		for suffix, body := range routes {
			if strings.HasSuffix(r.URL.Path, suffix) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := tsapi.NewClient(tsapi.Options{
		Tailnet:    "example.com",
		BaseURL:    srv.URL,
		APIKey:     "contract-harness",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, &seen
}

func TestResolveLiveArgs_PicksDeterministicIDsFromListCalls(t *testing.T) {
	c, _ := newFakeAPI(t, map[string]string{
		"/devices": `{"devices":[{"id":"zzz-9","nodeId":"n9CNTRL"},{"id":"aaa-1","nodeId":"n1CNTRL"}]}`,
		"/services": `{"vipServices":[{"name":"svc:zebra"},{"name":"svc:alpha"}]}`,
	})
	args, unavailable := contract.ResolveLiveArgs(context.Background(), c)
	if len(unavailable) != 0 {
		t.Fatalf("unavailable = %v, want none", unavailable)
	}
	// Lowest-sorting id/name, so a rerun against the same tailnet picks the same
	// resource and a failure is reproducible.
	if args.DeviceID != "aaa-1" {
		t.Errorf("DeviceID = %q, want %q", args.DeviceID, "aaa-1")
	}
	if args.ServiceName != "svc:alpha" {
		t.Errorf("ServiceName = %q, want %q", args.ServiceName, "svc:alpha")
	}
}

func TestResolveLiveArgs_EmptyTailnetGivesExplicitReasons(t *testing.T) {
	c, _ := newFakeAPI(t, map[string]string{
		"/devices":  `{"devices":[]}`,
		"/services": `{"vipServices":[]}`,
	})
	args, unavailable := contract.ResolveLiveArgs(context.Background(), c)
	if args.DeviceID != "" || args.ServiceName != "" {
		t.Fatalf("args = %+v, want zero", args)
	}
	for _, res := range []contract.LiveResource{contract.LiveDeviceID, contract.LiveServiceName} {
		reason, ok := unavailable[res]
		if !ok {
			t.Errorf("no reason recorded for %s", res)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("empty reason for %s", res)
		}
	}
}

// TestResolveLiveArgs_ResolvesOnlyViaReads is the "never manufacture a fixture"
// guard: resolution must issue GETs against list endpoints and nothing else.
func TestResolveLiveArgs_ResolvesOnlyViaReads(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte(`{"devices":[{"id":"d1"}],"vipServices":[{"name":"svc:a"}]}`))
	}))
	defer srv.Close()
	c, err := tsapi.NewClient(tsapi.Options{
		Tailnet: "example.com", BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	contract.ResolveLiveArgs(context.Background(), c)
	if len(methods) == 0 {
		t.Fatal("resolution issued no requests")
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("resolution issued a %s request — it must never create or mutate anything", m)
		}
	}
}

// TestManifest_ParameterizedOpsUseRealIDs is the #424 regression: the three
// path-parameterized ops must no longer be excluded from the live lane, and must
// substitute the resolved id rather than a hardcoded placeholder.
func TestManifest_ParameterizedOpsUseRealIDs(t *testing.T) {
	want := map[string]contract.LiveResource{
		"listDeviceInvites":          contract.LiveDeviceID,
		"getDevicePostureAttributes": contract.LiveDeviceID,
		"listServiceHosts":           contract.LiveServiceName,
	}
	for id, res := range want {
		op, ok := contract.ByID(id)
		if !ok {
			t.Fatalf("%s missing from manifest", id)
		}
		if op.LiveSkip {
			t.Errorf("%s is still LiveSkip — it should resolve a real %s instead", id, res)
		}
		if op.LiveInvoke == nil {
			t.Errorf("%s has no LiveInvoke", id)
		}
		if len(op.LiveRequires) != 1 || op.LiveRequires[0] != res {
			t.Errorf("%s LiveRequires = %v, want [%s]", id, op.LiveRequires, res)
		}
	}
}

// TestManifest_LiveInvokeSubstitutesTheResolvedValue proves the resolved id
// actually reaches the request URL — the bug this issue exists to fix is that a
// hardcoded placeholder 404s against the real API.
func TestManifest_LiveInvokeSubstitutesTheResolvedValue(t *testing.T) {
	c, seen := newFakeAPI(t, map[string]string{
		"/device-invites": `[]`,
		"/attributes":     `{"attributes":{}}`,
		"/devices":        `{"hosts":[]}`,
	})
	args := contract.LiveArgs{DeviceID: "real-device-42", ServiceName: "svc:real"}
	for _, id := range []string{"listDeviceInvites", "getDevicePostureAttributes", "listServiceHosts"} {
		op, _ := contract.ByID(id)
		if err := op.LiveInvoke(context.Background(), c, args); err != nil {
			t.Errorf("%s LiveInvoke: %v", id, err)
		}
	}
	joined := strings.Join(*seen, "\n")
	for _, want := range []string{"real-device-42", "svc:real"} {
		if !strings.Contains(joined, want) {
			t.Errorf("resolved value %q never reached a request path:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "placeholder") {
		t.Errorf("a hardcoded placeholder is still being sent:\n%s", joined)
	}
}

// TestLiveArgs_Has covers the gate the live lane uses to decide not-applicable.
func TestLiveArgs_Has(t *testing.T) {
	full := contract.LiveArgs{DeviceID: "d", ServiceName: "s"}
	if !full.Has(contract.LiveDeviceID) || !full.Has(contract.LiveServiceName) {
		t.Error("full args reported missing")
	}
	var empty contract.LiveArgs
	if empty.Has(contract.LiveDeviceID) || empty.Has(contract.LiveServiceName) {
		t.Error("empty args reported present")
	}
	if full.Has(contract.LiveResource("nonsense")) {
		t.Error("unknown resource reported present — must fail closed")
	}
}

// TestOp_LiveRun routes through LiveInvoke when set and Invoke otherwise, so the
// live lane has exactly one call path.
func TestOp_LiveRun(t *testing.T) {
	// listUsers is an object response ({"users":[…]}, per its KnownTopLevelKeys),
	// not a bare array — tsclient decodes it into a map[string][]User.
	c, seen := newFakeAPI(t, map[string]string{"/users": `{"users":[]}`})
	op, _ := contract.ByID("listUsers")
	if err := op.LiveRun(context.Background(), c, contract.LiveArgs{}); err != nil {
		t.Fatalf("LiveRun on a non-parameterized op: %v", err)
	}
	if len(*seen) == 0 {
		t.Fatal("LiveRun issued no request")
	}
}
