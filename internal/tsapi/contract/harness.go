// Package contract holds the consumed-surface manifest — the authoritative list
// of Tailscale API GET operations that tailscale2otel decodes — and a decoder
// harness that exercises the real tsapi.Client methods against an httptest
// server. Later CI lanes (schema-driven fuzz, OpenAPI drift, live contract)
// consume this package; internal/tsapi must NEVER import it (one-way dep).
package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"

	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// Op describes one GET operation from the Tailscale API that tailscale2otel
// consumes. The ID matches the OAS operationId exactly.
type Op struct {
	// ID is the OAS operationId — e.g. "listTailnetDevices".
	ID string
	// Method is always "GET".
	Method string
	// KnownTopLevelKeys is best-effort/informational: the top-level JSON keys our
	// decoder reads. []string{""} is a sentinel meaning the response is a bare
	// array (not an object). Used by Decode to flag unexpected wrapper fields.
	KnownTopLevelKeys []string
	// LiveSkip, when true, permanently excludes this op from the live-contract
	// lane (Lane 3). This is for ops that can never be exercised live, NOT for
	// ops that merely need a path parameter — those declare LiveRequires and
	// LiveInvoke instead, and are excluded per-run only when the tailnet has no
	// suitable resource (#424).
	LiveSkip bool
	// FuzzSkip, when true, excludes this op from schema-driven fuzz (Lane 4).
	// Typically set for ops whose response is not JSON (e.g. HuJSON policy file).
	FuzzSkip bool
	// Invoke runs the real Client method against c, discarding the return value
	// and returning only the decode error (if any). Path-parameterized ops pass a
	// placeholder here, which is correct for Decode and the fuzz lane because that
	// harness serves the same body on every path.
	Invoke func(ctx context.Context, c *tsapi.Client) error
	// LiveRequires lists the resources LiveInvoke needs resolved before it can
	// run. Empty for ops addressed by the tailnet alone.
	LiveRequires []LiveResource
	// LiveInvoke is Invoke's live-lane counterpart for path-parameterized ops: it
	// substitutes the real values in args instead of a placeholder that would 404
	// against the real API. nil when Invoke is already live-safe.
	LiveInvoke func(ctx context.Context, c *tsapi.Client, args LiveArgs) error
}

// LiveRun is the live lane's single call path: it routes through LiveInvoke when
// the op declares one and falls back to Invoke otherwise. Callers must check
// args.Missing(op.LiveRequires) first — LiveRun does not gate itself, so that a
// caller cannot accidentally treat "resource absent" as "op passed".
func (op Op) LiveRun(ctx context.Context, c *tsapi.Client, args LiveArgs) error {
	if op.LiveInvoke != nil {
		return op.LiveInvoke(ctx, c, args)
	}
	return op.Invoke(ctx, c)
}

// ByID returns the Op with the given operationId, or false if not found.
func ByID(id string) (Op, bool) {
	for _, op := range Manifest {
		if op.ID == id {
			return op, true
		}
	}
	return Op{}, false
}

// ConsumedOpIDs returns every operationId in Manifest, for use as the default
// -ops filter in tools/apidrift.
func ConsumedOpIDs() []string {
	ids := make([]string, len(Manifest))
	for i, op := range Manifest {
		ids[i] = op.ID
	}
	return ids
}

// DecodeReport is the result of a Decode call.
type DecodeReport struct {
	// Err is the decode error returned by the real Client method, or nil.
	Err error
	// UnknownTopLevelKeys lists top-level keys present in rawJSON but absent from
	// op.KnownTopLevelKeys. Empty when the response is a bare array or the body
	// has no unexpected keys.
	UnknownTopLevelKeys []string
}

// Decode stands up an httptest server that serves rawJSON, points a real
// tsapi.Client at it, calls op.Invoke, and reports any decode error plus any
// unexpected top-level JSON keys. The httptest server accepts any path so
// path-parameter ops work without a real device ID.
func Decode(op Op, rawJSON []byte) DecodeReport {
	request := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := rawJSON
		if request > 0 {
			// A paginated decoder must receive a terminal second page from this
			// single-body harness, otherwise replaying the same cursor tests the
			// harness rather than the response decoder.
			var obj map[string]json.RawMessage
			if json.Unmarshal(rawJSON, &obj) == nil {
				if _, ok := obj["cursor"]; ok {
					obj["cursor"] = json.RawMessage(`""`)
					body, _ = json.Marshal(obj)
				}
			}
		}
		request++
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, err := tsapi.NewClient(tsapi.Options{
		Tailnet:    "example.com",
		BaseURL:    srv.URL,
		APIKey:     "contract-harness",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		return DecodeReport{Err: err}
	}

	rep := DecodeReport{Err: op.Invoke(context.Background(), c)}
	rep.UnknownTopLevelKeys = unknownTopLevelKeys(op, rawJSON)
	return rep
}

// unknownTopLevelKeys returns any top-level object keys in rawJSON that are not
// listed in op.KnownTopLevelKeys. Returns nil for bare-array responses or
// non-object bodies (which are not a drift signal for top-level keys).
func unknownTopLevelKeys(op Op, rawJSON []byte) []string {
	// Sentinel: bare-array response — skip top-level key check.
	if len(op.KnownTopLevelKeys) == 1 && op.KnownTopLevelKeys[0] == "" {
		return nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &obj); err != nil {
		return nil // bare array / non-object body — not an error here
	}

	known := make(map[string]bool, len(op.KnownTopLevelKeys))
	for _, k := range op.KnownTopLevelKeys {
		known[k] = true
	}

	var out []string
	for k := range obj {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
