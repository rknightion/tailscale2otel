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

	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
	// LiveSkip, when true, excludes this op from the live-contract lane (Lane 3).
	// Typically set for ops whose Invoke carries a placeholder path parameter that
	// would 404 against the real API.
	LiveSkip bool
	// FuzzSkip, when true, excludes this op from schema-driven fuzz (Lane 4).
	// Typically set for ops whose response is not JSON (e.g. HuJSON policy file).
	FuzzSkip bool
	// Invoke runs the real Client method against c, discarding the return value
	// and returning only the decode error (if any).
	Invoke func(ctx context.Context, c *tsapi.Client) error
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(rawJSON)
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
