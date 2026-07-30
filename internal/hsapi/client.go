// Package hsapi is a minimal read-only HTTP/JSON client for the Headscale
// control-plane API (/api/v1/*), authenticated with a Bearer API key. It mirrors
// the internal/tsapi getJSON + URL-builder pattern but without retry (small
// tailnets; retry is a noted follow-up).
package hsapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/jsonbudget"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// maxDrainBytes caps the post-request body drain (see getJSON).
const maxDrainBytes = 64 << 10

// Options configures the Headscale client.
type Options struct {
	URL     string        // control-plane base URL, e.g. https://hs.example.org
	APIKey  string        // Bearer token
	Timeout time.Duration // per-request timeout (0 = no client timeout)

	// MaxResponseBytes bounds a single successful JSON response body before it
	// is decoded. Zero uses defaultMaxResponseBytes. See limit.go for the sizing
	// evidence and the tuning constraint.
	MaxResponseBytes int64

	// Tracer, when non-nil, emits one client span per getJSON call, mirroring
	// internal/tsapi/transport.go's retryTransport.startSpan. A nil Tracer is
	// replaced with a no-op at span-start (see noopAPITracer), so callers never
	// need a nil-guard.
	Tracer trace.Tracer

	// OnRequest, when non-nil, is called exactly once after each getJSON call
	// completes, with the span-carrying context (for trace-exemplar linkage)
	// and a RequestInfo describing the outcome. This mirrors
	// internal/tsapi.Options.OnRequest so the composition root can wire the
	// same self-observation (api.requests-style metrics, admin status page)
	// for Headscale that it already has for the Tailscale API client.
	OnRequest func(context.Context, RequestInfo)
}

// RequestInfo describes one completed Headscale API request, reported to
// Options.OnRequest. Mirrors tsapi.RequestInfo's shape (minus fields that make
// no sense here: hsapi's getJSON never retries, so there is no Attempts or
// WaitDuration). Err is the transport error string ("" on any HTTP response,
// including non-2xx — those surface via Status and the returned *StatusError,
// never via Err), and never contains response body or header data.
type RequestInfo struct {
	Endpoint string        // low-cardinality label (endpointLabel)
	Status   int           // final HTTP status, 0 on transport error
	Duration time.Duration // wall-clock of the request
	Err      string        // transport error text, "" when an HTTP response was received
}

// noopAPITracer is the shared fallback for a nil Options.Tracer, so the
// nil-tracer path allocates no tracer per getJSON call.
var noopAPITracer = tracenoop.NewTracerProvider().Tracer("")

// endpointLabel derives a stable, low-cardinality label from a Headscale API
// path, e.g. "/api/v1/node" -> "node". Every getJSON call site in this package
// passes one of a small fixed set of literal paths (node, user, preauthkey,
// apikey, policy), so — unlike tsapi's endpointLabel — no per-item elision is
// needed: the label space is bounded by construction, not by string surgery.
func endpointLabel(path string) string {
	return strings.TrimPrefix(strings.TrimPrefix(path, "/"), "api/v1/")
}

// Client talks to a Headscale server.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	// maxResponseBytes is the response decode byte budget, resolved once in
	// NewClient (#488).
	maxResponseBytes int64

	// tracer and onRequest mirror Options.Tracer/OnRequest; tracer is never nil
	// (resolved to noopAPITracer in NewClient) so getJSON needs no nil-guard.
	tracer    trace.Tracer
	onRequest func(context.Context, RequestInfo)
}

// NewClient builds a Headscale client from opts.
func NewClient(opts Options) *Client {
	maxBytes := opts.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	tracer := opts.Tracer
	if tracer == nil {
		tracer = noopAPITracer
	}
	return &Client{
		baseURL:          strings.TrimRight(opts.URL, "/"),
		apiKey:           opts.APIKey,
		http:             &http.Client{Timeout: opts.Timeout},
		maxResponseBytes: maxBytes,
		tracer:           tracer,
		onRequest:        opts.OnRequest,
	}
}

// budget is the decode budget applied to every endpoint. Headscale exposes only
// snapshot resources, so there is a single tier (unlike tsapi's snapshot/log
// split).
func (c *Client) budget() jsonbudget.Budget { return budgetOf(c.maxResponseBytes) }

// getJSON performs an authenticated GET of path and decodes JSON into out. It
// emits one client span per call (see Options.Tracer) and reports the outcome
// to Options.OnRequest, mirroring internal/tsapi/transport.go's retryTransport.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	start := time.Now()
	label := endpointLabel(path)
	spanCtx, span := c.tracer.Start(ctx, "headscale.api "+label, trace.WithSpanKind(trace.SpanKindClient))

	req, err := http.NewRequestWithContext(spanCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		c.observe(spanCtx, span, label, start, 0, err)
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.observe(spanCtx, span, label, start, 0, err)
		return err
	}
	// Drain a BOUNDED remainder before closing so the connection stays reusable.
	// The bound is the point: after a budget violation (or a truncated non-200
	// read) the rest of the body may be endless, and an unbounded drain would
	// keep pulling it until the client timeout — 30s of wasted bandwidth per poll
	// on a body already rejected. 64 KiB comfortably covers a real leftover.
	defer func() { _, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		statusErr := &StatusError{Path: path, Code: resp.StatusCode, Body: string(body)}
		c.observe(spanCtx, span, label, start, resp.StatusCode, statusErr)
		return statusErr
	}
	budget := c.budget()
	// Content-Length, when the upstream declares one, lets an over-budget body be
	// rejected before a single byte of it is read. It is advisory (absent on a
	// chunked response, where ContentLength is -1), so the streaming budget below
	// remains the real control.
	if resp.ContentLength > budget.MaxBytes {
		decodeErr := budget.ByteCeilingError()
		c.observe(spanCtx, span, label, start, resp.StatusCode, decodeErr)
		return decodeErr
	}
	decodeErr := jsonbudget.Decode(resp.Body, budget, out)
	c.observe(spanCtx, span, label, start, resp.StatusCode, decodeErr)
	return decodeErr
}

// observe finalizes the span for one getJSON call (status/error attributes,
// span status, End) and calls OnRequest with the span-carrying context so
// exemplars can link to the ended span, mirroring
// internal/tsapi/transport.go's retryTransport.observe. status is 0 when no
// HTTP response was received (transport error); err is the call's outcome —
// nil, a *StatusError (non-2xx, already carrying status separately), or a
// transport/decode error.
//
// §0.2 tier-2 useful identifiers: url.full carries the baseURL+path (no query
// string, no secret — the API key travels as a Bearer header, never the URL),
// so an operator can see WHICH control-plane call was slow/failed even though
// the span name (label) elides nothing here (the label space is already
// bounded, see endpointLabel).
func (c *Client) observe(spanCtx context.Context, span trace.Span, label string, start time.Time, status int, err error) {
	errStr := ""
	if err != nil {
		var se *StatusError
		if !errors.As(err, &se) {
			// Only a genuine transport/decode error goes into RequestInfo.Err; a
			// non-2xx HTTP status is already carried via Status, and StatusError's
			// Body is server-authored diagnostic text (see errors.go) — safe to
			// include, but kept out of Err to match tsapi's contract that Err is
			// transport-error text only, never derived from an HTTP response.
			errStr = err.Error()
		}
	}
	if span.IsRecording() {
		span.SetAttributes(
			attribute.String("headscale.endpoint", label),
			attribute.String("http.request.method", http.MethodGet),
			attribute.String("server.address", c.baseURL),
		)
		if status != 0 {
			span.SetAttributes(attribute.Int("http.response.status_code", status))
		}
		switch {
		case errStr != "":
			span.RecordError(errors.New(errStr))
			span.SetStatus(codes.Error, errStr)
		case status >= 400:
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
	span.End()
	if c.onRequest != nil {
		c.onRequest(spanCtx, RequestInfo{
			Endpoint: label,
			Status:   status,
			Duration: time.Since(start),
			Err:      errStr,
		})
	}
}

// Nodes lists all nodes (GET /api/v1/node).
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var r nodesResponse
	if err := c.getJSON(ctx, "/api/v1/node", &r); err != nil {
		return nil, err
	}
	return r.Nodes, nil
}

// HSUsers lists all users (GET /api/v1/user).
func (c *Client) HSUsers(ctx context.Context) ([]User, error) {
	var r usersResponse
	if err := c.getJSON(ctx, "/api/v1/user", &r); err != nil {
		return nil, err
	}
	return r.Users, nil
}

// PreAuthKeys lists all pre-auth keys (GET /api/v1/preauthkey).
func (c *Client) PreAuthKeys(ctx context.Context) ([]PreAuthKey, error) {
	var r preAuthKeysResponse
	if err := c.getJSON(ctx, "/api/v1/preauthkey", &r); err != nil {
		return nil, err
	}
	return r.PreAuthKeys, nil
}

// APIKeys lists all API keys (GET /api/v1/apikey).
func (c *Client) APIKeys(ctx context.Context) ([]APIKey, error) {
	var r apiKeysResponse
	if err := c.getJSON(ctx, "/api/v1/apikey", &r); err != nil {
		return nil, err
	}
	return r.APIKeys, nil
}

// PolicyDoc fetches the ACL policy document (GET /api/v1/policy).
func (c *Client) PolicyDoc(ctx context.Context) (*Policy, error) {
	var p Policy
	if err := c.getJSON(ctx, "/api/v1/policy", &p); err != nil {
		return nil, err
	}
	return &p, nil
}
