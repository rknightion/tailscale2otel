// Package webhook implements an HTTP receiver for Tailscale webhook events.
//
// Tailscale posts a JSON array of events to a configured endpoint and signs the
// request with an HMAC-SHA256 signature derived from a per-webhook secret. The
// Server verifies that signature (using the scheme documented at
// https://tailscale.com/kb/1213/webhooks), then emits one OTEL log record and
// one counter increment per event via the telemetry.Emitter facade.
//
// Signature scheme (verified against Tailscale's official example consumer):
//
//	Header: Tailscale-Webhook-Signature: t=<unixSeconds>,v1=<hex>
//	  - The parser accepts multiple v1=<hex> entries for forward-compatible
//	    header tolerance; a match against any one is sufficient.
//	Signed string: <unixSeconds> + "." + <raw request body>
//	Signature:     hex(HMAC-SHA256(secret, signedString))
//	Comparison:    constant time (subtle.ConstantTimeCompare) over each v1 value.
//
// When Options.Tolerance > 0, requests are rejected as possible replays if
// their timestamp falls outside [now-Tolerance, now+Tolerance] — too old
// ("stale_timestamp") OR too far in the future ("future_timestamp"). The
// future-side check matters because a correctly-signed request timestamped
// arbitrarily far ahead would otherwise be accepted immediately and remain
// replayable until (its future timestamp + Tolerance), turning a short
// clock-skew allowance into a much longer replay window. A tolerance of 0
// disables both checks, which keeps tests using fixed timestamps
// deterministic.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v4/internal/certreload"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/eventstore"
	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/listenaddr"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry/pii"
)

// requestDurationBucketsSeconds are the explicit histogram bucket boundaries
// for tailscale.webhook.request.duration (in seconds).
var requestDurationBucketsSeconds = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// receiverPropagator extracts W3C TraceContext from incoming request headers.
// Tailscale's sender won't send a traceparent header, so extraction yields an
// empty parent and the span becomes a root — that's correct.
var receiverPropagator = propagation.TraceContext{}

// noopWebhookTracer is a package-level cached noop tracer used when no tracer
// is configured, avoiding per-request allocations.
var noopWebhookTracer = tracenoop.NewTracerProvider().Tracer("")

const (
	// signatureHeader is the request header carrying the signed timestamp and
	// one or more HMAC signatures.
	signatureHeader = "Tailscale-Webhook-Signature"
	// signatureVersion is the only signature scheme Tailscale currently emits.
	signatureVersion = "v1"

	// MetricEvents counts received webhook events, keyed only by event type
	// (low cardinality).
	MetricEvents = "tailscale.webhook.events"
	// MetricRejected counts rejected webhook requests, keyed by rejection reason.
	MetricRejected = "tailscale.webhook.rejected"
	// MetricDuplicates counts webhook events suppressed by per-receiver delivery deduplication.
	MetricDuplicates = "tailscale.webhook.duplicates"
	// MetricSchemaDrift records the known/unknown state of the webhook wire version.
	MetricSchemaDrift = "tailscale.webhook.schema_drift"

	// defaultMaxBodyBytes caps webhook request bodies when Options.MaxBodyBytes is
	// 0. Real Tailscale webhook payloads are KB-scale (a handful of JSON events
	// per delivery, see https://tailscale.com/kb/1213/webhooks), so 1 MiB gives
	// generous headroom without buffering pre-auth requests at the 64 MiB size
	// sized for the streaming receiver's batch flow/audit logs.
	defaultMaxBodyBytes = 1 << 20 // 1 MiB

	// defaultMaxConcurrentRequests is the aggregate admission budget used when
	// Options.MaxConcurrentRequests is 0 (GHSA-9547-8jpc-48h6, mirroring the
	// streaming receiver's #209 fix): at most this many handlers may be buffering
	// a body — and thus be pending HMAC verification — at once, so worst-case
	// unauthenticated memory use is bounded by MaxConcurrentRequests *
	// MaxBodyBytes rather than by how many senders show up.
	defaultMaxConcurrentRequests = 4
	// maxEventsPerBatch bounds per-event allocation/canonicalization work after
	// the byte cap. Tailscale deliveries are normally only a handful of events;
	// 1,000 leaves ample compatibility headroom while preventing arrays of tiny
	// objects/nulls from amplifying one accepted body into unbounded work.
	maxEventsPerBatch = 1000

	defaultDeliveryDedupTTL      = 25 * time.Hour
	defaultDeliveryDedupCapacity = 65536

	// eventNamePrefix is prepended to the Tailscale event type to form the OTEL
	// LogRecord EventName, e.g. "tailscale.webhook.nodeCreated".
	eventNamePrefix = "tailscale.webhook."

	// attrType is the low-cardinality event-type attribute.
	attrType = "tailscale.webhook.type"
	// attrReason labels a rejection by cause.
	attrReason       = "reason"
	attrSchemaField  = "field"
	attrSchemaStatus = "status"

	// AttrNodeID through AttrNewRoles are the bounded typed allowlist for
	// version-1 webhook event data. The PII registry classifies these keys at the
	// application boundary; webhook never emits arbitrary data fields.
	AttrNodeID        = "tailscale.webhook.node.id"
	AttrDeviceName    = "tailscale.webhook.node.device.name"
	AttrManagedBy     = "tailscale.webhook.node.managed_by"
	AttrActor         = "tailscale.webhook.actor"
	AttrURL           = "tailscale.webhook.url"
	AttrKeyExpiration = "tailscale.webhook.key.expiration"
	AttrUser          = "tailscale.webhook.user"
	AttrOldRoles      = "tailscale.webhook.old_roles"
	AttrNewRoles      = "tailscale.webhook.new_roles"

	// maxDistinctEventTypes caps how many distinct event-type values are used as a
	// metric attribute / log EventName before further new types collapse into
	// overflowType. The event type is attacker-chosen on the wire, so when the
	// receiver runs without a secret (verification skipped) an unauthenticated
	// flood of unique types would otherwise explode the events metric's series and
	// the log EventName cardinality. The cap sits well above Tailscale's documented
	// event set (~25 types, see severityByType) so real traffic — and headroom for
	// new types — passes through verbatim; only an abnormal flood overflows.
	maxDistinctEventTypes = 64
	// overflowType is the single bucket attacker/novel types collapse into once the
	// distinct-type cap is reached.
	overflowType = "other"
)

var (
	errTooManyEvents     = errors.New("webhook batch exceeds event limit")
	errInvalidRouteBatch = errors.New("webhook batch cannot be routed")
)

// admissionWait is how long a request will wait for an admission slot before
// giving up when the aggregate budget is momentarily full — long enough to
// smooth a brief burst, short enough that a genuinely overloaded receiver
// still fails fast (mirrors the streaming receiver's #209 constant).
const admissionWait = 250 * time.Millisecond

// severityByType is the explicit, per-type log-severity classification for
// webhook events. It replaces an earlier substring heuristic ({Expir, Suspend,
// NeedsApproval, Deleted}) that MISSED nodeNeedsSignature and the deprecated
// nodeNeedsAuthorization (neither contains a matched substring), emitting both
// at INFO when they warrant WARN. Only types whose severity is NOT the default
// INFO are listed; severityForType returns INFO for everything else. The
// authoritative event catalog is https://tailscale.com/kb/1213/webhooks#events
// (see todos.txt S4-11(a)).
//
// Deliberately INFO (not listed): the client-misconfiguration health events
// exitNodeIPForwardingNotEnabled and subnetIPForwardingNotEnabled — they are
// surfaced via the events counter and a dedicated Prometheus alert (see
// deploy/alerts/), not by elevating log severity.
var severityByType = map[string]telemetry.Severity{
	// Node key expiry — the device drops off the tailnet when the key expires.
	"nodeKeyExpired":          telemetry.SeverityWarn,
	"nodeKeyExpiringInOneDay": telemetry.SeverityWarn,
	// Pending approvals — a node/user is blocked until an admin acts.
	"nodeNeedsApproval": telemetry.SeverityWarn,
	"userNeedsApproval": telemetry.SeverityWarn,
	// Deprecated alias of nodeNeedsApproval (still delivered until disabled).
	"nodeNeedsAuthorization": telemetry.SeverityWarn,
	// Tailnet Lock — a node is blocked from the tailnet until a trusted node signs it.
	"nodeNeedsSignature": telemetry.SeverityWarn,
	// Deletions are notable, irreversible config changes.
	"nodeDeleted":    telemetry.SeverityWarn,
	"webhookDeleted": telemetry.SeverityWarn,
	// Undocumented in the catalog above but historically observed; kept at WARN
	// (matching prior substring behavior) pending live verification — remove if
	// invalid (todos.txt S4-11(c), gated on the S4-10 capture).
	"userSuspended": telemetry.SeverityWarn,
	"userDeleted":   telemetry.SeverityWarn,
}

// Options configures a Server.
type Options struct {
	// Listen is the TCP address ListenAndServe binds (e.g. ":9099"). Only used
	// by Run; tests drive Handler directly.
	Listen string
	// Path is the single route that accepts webhook POSTs (e.g. "/webhook").
	Path string
	// Secret is the per-webhook signing secret. When empty, signature
	// verification is skipped (useful for local testing behind a trusted proxy).
	Secret string
	// SecretProvider, when non-nil, supplies the signing secret for each request.
	// It is called once per request; a request therefore observes one value even
	// if the backing file rotates while the body is being read. The app uses this
	// for file-backed secret rotation and keeps the last-known-good value in the
	// provider itself.
	SecretProvider func() string
	// TLSCertFile and TLSKeyFile, when both set, make Run serve HTTPS.
	TLSCertFile string
	TLSKeyFile  string
	// Tolerance is the maximum age of a request timestamp before it is rejected
	// as a replay. Zero disables the check.
	Tolerance time.Duration
	// MaxBodyBytes caps the raw request body size before signature verification,
	// bounding unauthenticated memory use. 0 selects a 1 MiB default (real
	// Tailscale webhook payloads are KB-scale); a negative value disables the cap.
	MaxBodyBytes int64
	// MaxConcurrentRequests bounds handlers buffering a body simultaneously
	// (GHSA-9547-8jpc-48h6): MaxBodyBytes caps ONE request's body, not the sum of
	// every body in flight, and that buffering happens before HMAC verification
	// — no credential is required to reach it. 0 selects
	// defaultMaxConcurrentRequests; negative disables the limit.
	MaxConcurrentRequests int
	// PerRouteMaxConcurrentRequests bounds any one route when this server is
	// installed in a Router. Zero selects an automatic fair share of the global
	// admission budget; a positive value is an explicit per-route cap and a
	// negative value disables the per-route cap.
	PerRouteMaxConcurrentRequests int
	// OnIngest, when non-nil, is called once after a successful parse with
	// (IngestSourceWebhook, IngestSignalWebhook, len(events), len(body)).
	// Supplied by the app, gated on self-observability.
	OnIngest func(source, signal string, records, bytes int)
	// OnAccepted, when non-nil, observes each authenticated, delivery-dedup
	// surviving event after it is handed to the webhook processor.
	OnAccepted ingest.AcceptedObserver
	// EventStore optionally feeds a bounded, admin-facing event view
	// (internal/eventstore, #300) after telemetry has already been emitted for
	// an accepted event. Nil disables it with no change in behavior — the OTLP
	// emit path never blocks or fails on this (see Server.emit).
	EventStore *eventstore.Memory
}

// Server receives and verifies Tailscale webhook POSTs and emits telemetry.
type Server struct {
	opts           Options
	e              telemetry.Emitter
	logger         *slog.Logger
	now            func() time.Time // injectable clock; defaults to time.Now
	secretProvider func() string
	dedup          *dedup.Set // optional cross-source de-dup set (see WithDedup)
	durableAppend  DurableAppend
	onIngest       func(source, signal string, records, bytes int)
	onAccepted     ingest.AcceptedObserver
	tracer         trace.Tracer
	// remoteParent is the inbound-traceparent trust policy (#373); empty means
	// trust, which is the pre-#373 behavior.
	remoteParent string
	eventStore   *eventstore.Memory

	// tlsReloader backs tls.Config.GetCertificate when both TLS files are set,
	// so a rotated certificate is picked up without restarting the listener
	// (#316). One instance is shared by Run and by the Router's Run, since
	// every route serves one TLS configuration.
	tlsReloader *certreload.Reloader

	// maxConcurrentRequests retains the process-wide setting used to size a
	// Router's shared listener budget. admit is the aggregate budget for a
	// standalone server and becomes this route's sub-budget in a Router.
	maxConcurrentRequests int
	// admit is the admission semaphore for this server. On a standalone server
	// it is the aggregate budget; in a Router it is this route's sub-budget. nil
	// when that layer is disabled.
	admit chan struct{}
	// globalAdmit is the shared listener budget used by a Router. It is acquired
	// after the route budget, so a request queued behind one noisy route never
	// consumes a slot another route could use.
	globalAdmit                   chan struct{}
	perRouteMaxConcurrentRequests int
	delivery                      *deliveryDeduper

	// typesMu guards seenTypes, the bounded set of distinct event types already
	// admitted as a telemetry dimension. handle (and thus emit) runs concurrently
	// per request, so access is mutex-guarded. See boundType / maxDistinctEventTypes.
	typesMu   sync.Mutex
	seenTypes map[string]struct{}
}

// Route binds one configured tailnet to its isolated signing-secret receiver.
// Routing is deliberately by the signed payload's tailnet identity, never by a
// shared secret or first configured runtime.
type Route struct {
	Tailnet string
	Server  *Server
}

// Router selects a webhook receiver only after every event in a bounded body
// proves the same non-empty tailnet. Unknown, missing and mixed batches are
// refused before an individual receiver's metrics, dedup state or processors
// are touched.
type Router struct {
	routes map[string]*Server
	base   *Server
	admit  chan struct{}
	// tokenless is true when any route relies on loopback reachability rather
	// than a signing secret. Routing requires reading the body, so the shared
	// listener must apply the browser/Host gate before that read.
	tokenless bool
	// invalidAuthMix prevents a shared listener from weakening a tokenless
	// route's pre-body browser gate or applying that gate to a signed route.
	// Config validation rejects this shape; the Router repeats the check for
	// programmatic callers.
	invalidAuthMix bool
}

// NewRouter composes per-tailnet webhook servers sharing one listener/path.
func NewRouter(routes []Route) *Router {
	r := &Router{routes: make(map[string]*Server, len(routes))}
	hasSigned := false
	for _, route := range routes {
		if route.Tailnet == "" || route.Server == nil {
			continue
		}
		r.routes[route.Tailnet] = route.Server
		if route.Server.currentSecret() == "" {
			r.tokenless = true
		} else {
			hasSigned = true
		}
		if r.base == nil {
			r.base = route.Server
		}
	}
	r.invalidAuthMix = r.tokenless && hasSigned
	if r.base == nil {
		return r
	}

	globalMax := r.base.maxConcurrentRequests
	if globalMax == 0 && r.base.admit != nil {
		// Keep programmatically-constructed Servers (rather than New) useful too.
		globalMax = cap(r.base.admit)
	}
	if globalMax > 0 {
		r.admit = make(chan struct{}, globalMax)
	}
	for _, server := range r.routes {
		routeMax := server.perRouteMaxConcurrentRequests
		if routeMax == 0 && globalMax > 0 {
			routeMax = globalMax / len(r.routes)
			if routeMax < 1 {
				routeMax = 1
			}
		}
		server.admit = nil
		if routeMax > 0 {
			server.admit = make(chan struct{}, routeMax)
		}
		server.globalAdmit = r.admit
	}
	return r
}

// Handler reads no more than the existing receiver cap, inspects only each
// event's tailnet field, then hands the untouched bytes to the selected Server
// for normal HMAC verification and emission.
func (r *Router) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.base != nil && req.URL.Path != r.base.opts.Path {
			http.NotFound(w, req)
			return
		}
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.base == nil {
			http.Error(w, "receiver unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.invalidAuthMix {
			http.Error(w, "webhook router cannot mix tokenless and signed routes", http.StatusServiceUnavailable)
			return
		}
		if r.tokenless {
			if reason := httpguard.TokenlessReceiverReason(req); reason != "" {
				r.base.rejectStatus(w, http.StatusForbidden, "cross_site",
					"webhook receiver rejected a browser-shaped tokenless request", errors.New(reason))
				return
			}
		}
		defer req.Body.Close()
		// Routing needs the body to identify its tailnet. Hold only the shared
		// pre-body slot while reading that untrusted body; after the identity is
		// known, release it while waiting for the route budget so a noisy route
		// cannot pin global slots for other routes.
		releasePreBody, admitted := acquireAdmission(req.Context(), r.admit)
		if !admitted {
			w.Header().Set("Retry-After", "1")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		preBodyReleased := false
		defer func() {
			if !preBodyReleased {
				releasePreBody()
			}
		}()
		limit := r.base.opts.MaxBodyBytes
		if limit == 0 {
			limit = defaultMaxBodyBytes
		}
		reader := io.Reader(req.Body)
		if limit >= 0 {
			reader = http.MaxBytesReader(w, req.Body, limit)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		// body is now bounded and route selection can proceed without occupying
		// the listener-wide pre-body slot.
		releasePreBody()
		preBodyReleased = true
		tailnet, err := webhookTailnet(body)
		if errors.Is(err, errTooManyEvents) {
			r.base.rejectStatus(w, http.StatusRequestEntityTooLarge, "too_many_events",
				"webhook batch exceeds event limit", err)
			return
		}
		s, found := r.routes[tailnet]
		if err != nil || !found {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		releaseRoute, routeAdmitted := acquireAdmission(req.Context(), s.admit)
		if !routeAdmitted {
			w.Header().Set("Retry-After", "1")
			s.rejectStatus(w, http.StatusServiceUnavailable, "overloaded",
				"webhook route at capacity, retry shortly", nil)
			return
		}
		releaseGlobal, globallyAdmitted := acquireAdmission(req.Context(), r.admit)
		if !globallyAdmitted {
			releaseRoute()
			w.Header().Set("Retry-After", "1")
			s.rejectStatus(w, http.StatusServiceUnavailable, "overloaded",
				"webhook receiver at capacity, retry shortly", nil)
			return
		}
		defer releaseGlobal()
		defer releaseRoute()
		req.Body = io.NopCloser(bytes.NewReader(body))
		s.handleWithAdmission(w, req, true)
	})
}

// acquireAdmission takes one admission channel slot, returning a release func.
// A nil channel disables that layer of the budget.
func acquireAdmission(ctx context.Context, admit chan struct{}) (func(), bool) {
	if admit == nil {
		return func() {}, true
	}
	select {
	case admit <- struct{}{}:
		return func() { <-admit }, true
	default:
	}
	timer := time.NewTimer(admissionWait)
	defer timer.Stop()
	select {
	case admit <- struct{}{}:
		return func() { <-admit }, true
	case <-ctx.Done():
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

// acquire takes the route budget first and the shared listener budget second.
// If the global layer is full, the route slot is returned before the request is
// refused so a blocked route cannot leak capacity.
func (s *Server) acquire(ctx context.Context) (func(), bool) {
	releaseRoute, ok := acquireAdmission(ctx, s.admit)
	if !ok {
		return nil, false
	}
	releaseGlobal, ok := acquireAdmission(ctx, s.globalAdmit)
	if !ok {
		releaseRoute()
		return nil, false
	}
	return func() {
		releaseGlobal()
		releaseRoute()
	}, true
}

// currentSecret returns the signing secret for a new request. A provider is
// expected to expose an atomic last-known-good snapshot; the request handler
// stores the returned value locally so a concurrent rotation cannot change the
// HMAC key halfway through verification.
func (s *Server) currentSecret() string {
	if s.secretProvider != nil {
		return s.secretProvider()
	}
	return s.opts.Secret
}

// secretlessUnsafe reports whether a secretless receiver is exposed on a
// network-reachable bind. It is deliberately evaluated per request so a
// provider rotation to/from an empty value cannot leave a stale startup-time
// decision in force.
func (s *Server) secretlessUnsafe(secret string) bool {
	return secret == "" && !listenaddr.IsLoopback(s.opts.Listen)
}

// ShutdownTimeout bounds the receiver's graceful HTTP shutdown: once the
// operator's context is canceled, already-ACKed in-flight requests get this
// long to finish emitting before the server is torn down. Exported because it
// is one stage of the process-wide staged drain that deployment shutdown
// budgets must cover (#332); internal/app asserts it at compile time.
const ShutdownTimeout = 10 * time.Second

// Run binds the shared listener; all per-tailnet Servers have identical listen
// settings because the config exposes one webhook listener.
func (r *Router) Run(ctx context.Context) error {
	if r.base == nil {
		return errors.New("webhook: router has no routes")
	}
	if r.invalidAuthMix {
		return errors.New("webhook: router cannot mix tokenless and signed routes on one listener")
	}
	srv := &http.Server{
		Addr:              r.base.opts.Listen,
		Handler:           r.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		var err error
		if r.base.tlsReloader != nil {
			srv.TLSConfig = &tls.Config{GetCertificate: r.base.tlsReloader.GetCertificate, MinVersion: tls.VersionTLS12}
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// webhookTailnet performs the minimum parsing required for safe attribution.
func webhookTailnet(body []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return "", errInvalidRouteBatch
	}
	var tailnet string
	count := 0
	for dec.More() {
		if count >= maxEventsPerBatch {
			return "", errTooManyEvents
		}
		count++
		var event json.RawMessage
		if err := dec.Decode(&event); err != nil || bytes.Equal(bytes.TrimSpace(event), []byte("null")) {
			return "", errInvalidRouteBatch
		}
		var identity struct {
			Tailnet string `json:"tailnet"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(event, &identity); err != nil || strings.TrimSpace(identity.Tailnet) == "" || strings.TrimSpace(identity.Type) == "" {
			return "", errInvalidRouteBatch
		}
		if tailnet == "" {
			tailnet = identity.Tailnet
			continue
		}
		if tailnet != identity.Tailnet {
			return "", errInvalidRouteBatch
		}
	}
	if count == 0 {
		return "", errInvalidRouteBatch
	}
	if tok, err = dec.Token(); err != nil || tok != json.Delim(']') {
		return "", errInvalidRouteBatch
	}
	var extra any
	if !errors.Is(dec.Decode(&extra), io.EOF) {
		return "", errInvalidRouteBatch
	}
	return tailnet, nil
}

// Option configures a Server at construction time.
type Option func(*Server)

// DurableAppend persists one already-authenticated, completely validated raw
// request body before the receiver acknowledges it. The app binds the function
// to the configured route; transport headers and route selection stay outside
// this seam.
type DurableAppend func(context.Context, []byte, time.Time) error

// WithDurableAppend enables durable acknowledgement for this route. A nil
// appender preserves the synchronous stateless receiver path.
func WithDurableAppend(appendBody DurableAppend) Option {
	return func(s *Server) { s.durableAppend = appendBody }
}

// WithDedup attaches a cross-SOURCE de-duplication set shared with the audit
// Processor (see audit.WithCrossDedup). When set is non-nil, a webhook event
// that maps to a change already recorded in set — by the audit poller/stream or
// a prior webhook — is suppressed (no log record, no counter increment) so
// enabling both webhooks and audit-log polling does not double-count. This is
// BEST-EFFORT (see crossKey); a nil set is a no-op.
func WithDedup(set *dedup.Set) Option {
	return func(s *Server) { s.dedup = set }
}

// WithTracer sets the tracer for one span per received webhook request. A nil
// tracer disables span emission (the server falls back to the noop tracer).
func WithTracer(tr trace.Tracer) Option { return func(s *Server) { s.tracer = tr } }

// WithRemoteParentPolicy sets how an inbound W3C traceparent's sampled bit is
// treated: telemetry.RemoteParentTrust (default), RemoteParentIgnore, or
// RemoteParentLink. An empty value keeps the trusting default.
func WithRemoteParentPolicy(p string) Option { return func(s *Server) { s.remoteParent = p } }

// WithClock overrides the receiver clock. It is used by deterministic replay
// and signature-tolerance tests; production callers should use the default.
func WithClock(now func() time.Time) Option {
	return func(s *Server) {
		if now != nil {
			s.now = now
		}
	}
}

// withDeliveryDedup overrides the delivery dedup settings for package tests.
// The production receiver deliberately exposes no user-configurable replay
// window: 25 hours covers the documented webhook retry horizon.
func withDeliveryDedup(ttl time.Duration, capacity int) Option {
	return func(s *Server) { s.delivery = newDeliveryDeduper(ttl, capacity, s.now) }
}

// event mirrors a single Tailscale webhook event. Field names and types match
// Tailscale's documented payload and official example consumer.
//
// Data values are kept as raw JSON because they are NOT uniformly flat strings:
// userRoleUpdated carries array-valued oldRoles/newRoles, and policyUpdate carries
// large oldPolicy/newPolicy strings (kb/1213). A map[string]string here would make
// json.Unmarshal fail on the array values and reject the WHOLE delivery (S4-11e).
type event struct {
	Timestamp string                     `json:"timestamp"` // RFC3339
	Version   int                        `json:"version"`
	Type      string                     `json:"type"`
	Tailnet   string                     `json:"tailnet"`
	Message   string                     `json:"message"`
	Data      map[string]json.RawMessage `json:"data"`
}

type acceptedBatch struct {
	events  []event
	digests []string
}

// New returns a Server that verifies against opts.Secret and emits via e.
// A nil logger is replaced with a no-op logger. Optional Options (e.g. WithDedup)
// are applied after construction.
func New(opts Options, e telemetry.Emitter, logger *slog.Logger, options ...Option) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Path == "" {
		opts.Path = "/webhook"
	}
	maxConcurrent := opts.MaxConcurrentRequests
	if maxConcurrent == 0 {
		maxConcurrent = defaultMaxConcurrentRequests
	}
	admitMax := maxConcurrent
	if opts.PerRouteMaxConcurrentRequests > 0 &&
		(admitMax <= 0 || opts.PerRouteMaxConcurrentRequests < admitMax) {
		admitMax = opts.PerRouteMaxConcurrentRequests
	}
	var admit chan struct{}
	if admitMax > 0 {
		admit = make(chan struct{}, admitMax)
	}
	s := &Server{
		opts:                          opts,
		e:                             e,
		logger:                        logger,
		now:                           time.Now,
		secretProvider:                opts.SecretProvider,
		onIngest:                      opts.OnIngest,
		onAccepted:                    opts.OnAccepted,
		eventStore:                    opts.EventStore,
		maxConcurrentRequests:         maxConcurrent,
		admit:                         admit,
		perRouteMaxConcurrentRequests: opts.PerRouteMaxConcurrentRequests,
	}
	for _, o := range options {
		o(s)
	}
	if s.delivery == nil {
		s.delivery = newDeliveryDeduper(defaultDeliveryDedupTTL, defaultDeliveryDedupCapacity, s.now)
	}
	if opts.TLSCertFile != "" && opts.TLSKeyFile != "" {
		s.tlsReloader = certreload.New(opts.TLSCertFile, opts.TLSKeyFile, "webhook", logger, e)
	}
	return s
}

// Handler returns the http.Handler serving the configured Path. It is the unit
// of behavior exercised by tests via httptest.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handle)
	return mux
}

// Run binds opts.Listen, serves Handler at opts.Path, and shuts down gracefully
// when ctx is canceled. It returns nil on a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		var err error
		if s.tlsReloader != nil {
			srv.TLSConfig = &tls.Config{GetCertificate: s.tlsReloader.GetCertificate, MinVersion: tls.VersionTLS12}
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// handle is the core request handler: it accepts only POST, verifies the
// signature, parses the event array, and emits telemetry.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.handleWithAdmission(w, r, false)
}

// handleWithAdmission is the same request path as handle, with an escape hatch
// for Router after it has taken both the route and global admission slots. The
// Router must read the body once before it can identify the tailnet, so it takes
// the global slot for that pre-routing read, releases it while waiting for the
// selected route, then reacquires it before calling here. Skipping the second
// acquisition prevents a route from deadlocking itself on its own slot.
func (s *Server) handleWithAdmission(w http.ResponseWriter, r *http.Request, admissionHeld bool) {
	start := time.Now()

	// Start a server span for this request BEFORE the request-duration defer
	// below, so that defer can close over ctx (#367: HistogramCtx needs the
	// span-carrying ctx in scope when it is DECLARED, not just when it runs —
	// a Go closure can only reference a variable already declared at the point
	// of the "defer" statement). W3C trace-context is extracted from headers;
	// Tailscale's sender won't send a traceparent, so the span becomes a root
	// — that's correct. The span ends via defer regardless of exit path.
	tr := s.tracer
	if tr == nil {
		tr = noopWebhookTracer
	}
	ctx := receiverPropagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	// See internal/stream for why the policy and the class must both be applied
	// at Start rather than after (#372, #373).
	ctx, parentOpts := telemetry.RemoteParentContext(ctx, s.remoteParent)
	startOpts := append([]trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(telemetry.SpanClassKey.String(telemetry.SpanClassReceiver)),
	}, parentOpts...)
	ctx, span := tr.Start(ctx, "webhook.receive", startOpts...)
	defer span.End()
	r = r.WithContext(ctx)

	// Record in-flight count and request duration unconditionally, balanced even
	// on panic or early return. +1 immediately, -1 in defer (balanced pair).
	s.e.UpDownCounter(docWebhookInflight.Name, docWebhookInflight.Unit, docWebhookInflight.Description, 1, nil)
	defer func() {
		s.e.UpDownCounter(docWebhookInflight.Name, docWebhookInflight.Unit, docWebhookInflight.Description, -1, nil)
		// HistogramCtx (not Histogram): ctx carries the "webhook.receive" span
		// started above, so a sampled request attaches an exemplar to this
		// duration histogram (#367).
		s.e.HistogramCtx(ctx, docWebhookRequestDuration.Name, docWebhookRequestDuration.Unit, docWebhookRequestDuration.Description,
			time.Since(start).Seconds(), requestDurationBucketsSeconds, nil)
	}()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		span.SetStatus(codes.Error, "method not allowed")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Capture the credential once for this request. The provider itself keeps a
	// last-known-good value, and the local copy makes a concurrent rotation unable
	// to change the verification key halfway through one request.
	secret := s.currentSecret()
	if s.secretlessUnsafe(secret) {
		span.SetStatus(codes.Error, "auth required")
		s.rejectStatus(w, http.StatusForbidden, "auth_required",
			"webhook receiver refuses unauthenticated requests on a network-reachable bind", nil)
		return
	}
	if secret == "" {
		if reason := httpguard.TokenlessReceiverReason(r); reason != "" {
			span.SetStatus(codes.Error, "cross-site request")
			s.rejectStatus(w, http.StatusForbidden, "cross_site",
				"webhook receiver rejected a browser-shaped tokenless request", errors.New(reason))
			return
		}
	}
	defer r.Body.Close()

	if !admissionHeld {
		// Admission control (GHSA-9547-8jpc-48h6): MaxBodyBytes caps ONE body,
		// and buffering happens BEFORE HMAC verification, so the route/global
		// budgets must be taken before reading any body bytes.
		release, admitted := s.acquire(r.Context())
		if !admitted {
			span.SetStatus(codes.Error, "overloaded")
			s.logger.Warn("webhook: refusing request, receiver at capacity", "max_concurrent_requests", s.maxConcurrentRequests)
			w.Header().Set("Retry-After", "1")
			s.rejectStatus(w, http.StatusServiceUnavailable, "overloaded", "receiver at capacity, retry shortly", nil)
			return
		}
		defer release()
	}

	body, err := s.readBody(w, r)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			span.SetStatus(codes.Error, "request body exceeds max size")
			s.rejectStatus(w, http.StatusRequestEntityTooLarge, "too_large", "request body exceeds max size", err)
			return
		}
		span.SetStatus(codes.Error, "failed to read request body")
		s.reject(w, "read_error", "failed to read request body", err)
		return
	}

	if secret != "" {
		if reason, err := s.verifyWithSecret(r.Header.Get(signatureHeader), body, secret); err != nil {
			span.SetStatus(codes.Error, reason)
			s.reject(w, reason, "signature verification failed", err)
			return
		}
	}

	batch, err := decodeAcceptedBatch(body)
	if err != nil {
		if errors.Is(err, errTooManyEvents) {
			span.SetStatus(codes.Error, "webhook batch exceeds event limit")
			s.rejectStatus(w, http.StatusRequestEntityTooLarge, "too_many_events",
				"webhook batch exceeds event limit", err)
			return
		}
		span.SetStatus(codes.Error, "failed to parse webhook body")
		s.reject(w, "invalid_body", "failed to parse webhook body", err)
		return
	}

	if s.durableAppend != nil {
		acceptedAt := s.now()
		if err := r.Context().Err(); err != nil {
			span.SetStatus(codes.Error, "durability unavailable")
			w.Header().Set("Retry-After", "1")
			s.rejectStatus(w, http.StatusServiceUnavailable, "wal_unavailable",
				"webhook durability unavailable, retry shortly", nil)
			return
		}
		if err := s.durableAppend(r.Context(), body, acceptedAt); err != nil {
			span.SetStatus(codes.Error, "durability unavailable")
			w.Header().Set("Retry-After", "1")
			s.rejectStatus(w, http.StatusServiceUnavailable, "wal_unavailable",
				"webhook durability unavailable, retry shortly", nil)
			return
		}
	} else {
		s.applyAcceptedBatch(ctx, batch, len(body), s.now)
	}

	// Record aggregate counts and body size on the span before the success response.
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Int("tailscale.webhook.events", len(batch.events)),
			attribute.Int("http.request.body.size", len(body)),
		)
	}

	w.WriteHeader(http.StatusOK)
}

// ApplyDurable applies one trusted body previously admitted and persisted by
// this route. It deliberately does not verify transport authentication,
// timestamp tolerance, or route identity: those are request-time concerns.
func (s *Server) ApplyDurable(ctx context.Context, body []byte, acceptedAt time.Time) error {
	batch, err := decodeAcceptedBatchContext(ctx, body)
	if err != nil {
		return fmt.Errorf("webhook apply durable body: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.applyAcceptedBatch(ctx, batch, len(body), func() time.Time { return acceptedAt })
	return nil
}

func decodeAcceptedBatch(body []byte) (acceptedBatch, error) {
	return decodeAcceptedBatchContext(context.Background(), body)
}

func decodeAcceptedBatchContext(ctx context.Context, body []byte) (acceptedBatch, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return acceptedBatch{}, err
	}
	if tok != json.Delim('[') {
		return acceptedBatch{}, errors.New("webhook body must be a JSON array")
	}
	batch := acceptedBatch{
		events:  make([]event, 0, min(maxEventsPerBatch, 16)),
		digests: make([]string, 0, min(maxEventsPerBatch, 16)),
	}
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return acceptedBatch{}, err
		}
		if len(batch.events) >= maxEventsPerBatch {
			return acceptedBatch{}, errTooManyEvents
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return acceptedBatch{}, err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return acceptedBatch{}, errors.New("webhook event must be an object")
		}
		ev, err := decodeEvent(raw)
		if err != nil {
			return acceptedBatch{}, err
		}
		if strings.TrimSpace(ev.Type) == "" || strings.TrimSpace(ev.Tailnet) == "" {
			return acceptedBatch{}, errors.New("webhook event requires non-empty type and tailnet")
		}
		digest, err := canonicalDigest(raw)
		if err != nil {
			return acceptedBatch{}, err
		}
		batch.events = append(batch.events, ev)
		batch.digests = append(batch.digests, digest)
	}
	if len(batch.events) == 0 {
		return acceptedBatch{}, errors.New("webhook batch must contain at least one event")
	}
	if tok, err = dec.Token(); err != nil || tok != json.Delim(']') {
		if err != nil {
			return acceptedBatch{}, err
		}
		return acceptedBatch{}, errors.New("webhook body has invalid array terminator")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return acceptedBatch{}, errors.New("webhook body contains trailing JSON")
		}
		return acceptedBatch{}, err
	}
	return batch, nil
}

// ctx carries the receiver's server span (or the replay caller's span) so each
// emitted log record is a child of it. Without it the webhook path emits
// orphaned records while the poll path emits correlated ones (#367).
func (s *Server) applyAcceptedBatch(ctx context.Context, batch acceptedBatch, bodyBytes int, acceptedAt func() time.Time) {
	if s.onIngest != nil {
		s.onIngest(semconv.IngestSourceWebhook, semconv.IngestSignalWebhook, len(batch.events), bodyBytes)
	}
	for i, ev := range batch.events {
		if !s.delivery.Add(batch.digests[i]) {
			s.e.Counter(docWebhookDuplicates.Name, docWebhookDuplicates.Unit, docWebhookDuplicates.Description, 1, nil)
			continue
		}
		s.emit(ctx, ev)
		if s.onAccepted != nil {
			s.onAccepted(ingest.AcceptedEvent{
				Source:     semconv.IngestSourceWebhook,
				Signal:     semconv.IngestSignalWebhook,
				EventTime:  parseTimestamp(ev.Timestamp),
				AcceptedAt: acceptedAt(),
			})
		}
	}
}

func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	limit := s.opts.MaxBodyBytes
	if limit == 0 {
		limit = defaultMaxBodyBytes
	}
	reader := r.Body
	if limit >= 0 {
		reader = http.MaxBytesReader(w, r.Body, limit)
	}
	return io.ReadAll(reader)
}

// reject records the rejection counter, logs at Warn, and writes a 401. A
// "read_error" or "invalid_body" reason still uses 401 here for simplicity:
// the body could not be authenticated as a well-formed signed payload.
func (s *Server) reject(w http.ResponseWriter, reason, msg string, err error) {
	s.rejectStatus(w, http.StatusUnauthorized, reason, msg, err)
}

func (s *Server) rejectStatus(w http.ResponseWriter, status int, reason, msg string, err error) {
	s.logger.Warn(msg, "reason", reason, "error", err)
	s.e.Counter(docWebhookRejected.Name, docWebhookRejected.Unit, docWebhookRejected.Description, 1, telemetry.Attrs{
		attrReason: reason,
	})
	http.Error(w, http.StatusText(status), status)
}

// verifyWithSecret checks a signature against one request-local secret. The
// caller must skip this method entirely when secret is empty, preserving the
// tokenless loopback mode.
func (s *Server) verifyWithSecret(header string, body []byte, secret string) (string, error) {
	if header == "" {
		return "missing_signature", errors.New("missing signature header")
	}

	ts, sigs, err := parseSignatureHeader(header)
	if err != nil {
		return "malformed_signature", err
	}

	if s.opts.Tolerance > 0 {
		now := s.now()
		if ts.Before(now.Add(-s.opts.Tolerance)) {
			return "stale_timestamp", errors.New("timestamp older than tolerance")
		}
		if ts.After(now.Add(s.opts.Tolerance)) {
			return "future_timestamp", errors.New("timestamp newer than tolerance")
		}
	}

	want := expectedSignature(secret, ts, body)
	for _, got := range sigs {
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return "", nil
		}
	}
	return "bad_signature", errors.New("no matching signature")
}

func expectedSignature(secret string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// emit converts one event into an OTEL log record plus a counter increment.
// The counter carries only the low-cardinality event type.
func (s *Server) emit(ctx context.Context, ev event) {
	if s.dedup != nil {
		if key, ok := crossKey(ev); ok && !s.dedup.Add(key) {
			// Same change already emitted via the audit logs (or a prior webhook):
			// suppress to avoid double-counting.
			return
		}
	}

	// The event type is attacker-chosen on the wire; bound its distinct values
	// before using it as a metric attribute or log EventName. Severity is still
	// derived from the ORIGINAL type (its lookup table is itself bounded), so a
	// legitimately new WARN type is classified correctly even if it overflows.
	dim := s.boundType(ev.Type)
	attrs := telemetry.Attrs{
		attrType:            dim,
		semconv.AttrTailnet: ev.Tailnet,
	}
	if ev.Version == 1 {
		for key, value := range typedDataAttrs(ev) {
			attrs[key] = value
		}
	}

	s.e.LogEventCtx(ctx, telemetry.Event{
		Name:      eventNamePrefix + dim,
		Body:      ev.Message,
		Severity:  severityForType(ev.Type),
		Timestamp: parseTimestamp(ev.Timestamp),
		// The body is the attacker/upstream-supplied free-text message; classify it
		// so a disabled free_text_details drops it from the body, not just attrs (#197).
		BodyPII: []pii.Category{pii.CatFreeTextDetails},
		Attrs:   attrs,
	})

	s.e.Counter(docWebhookEvents.Name, docWebhookEvents.Unit, docWebhookEvents.Description, 1, telemetry.Attrs{
		attrType: dim,
	})
	status := "known"
	if ev.Version != 1 {
		status = "unknown"
	}
	s.e.Counter(docWebhookSchemaDrift.Name, docWebhookSchemaDrift.Unit, docWebhookSchemaDrift.Description, 1, telemetry.Attrs{
		attrSchemaField: "version", attrSchemaStatus: status,
	})

	// Feed the bounded event explorer view AFTER every OTLP emission above, so
	// a full ring (or any bug in this path) can never affect what was already
	// exported (#300). A nil store makes this a no-op.
	if s.eventStore != nil {
		s.eventStore.Record(storeEvent(ev, dim))
	}
}

// storeEvent converts a webhook event into the eventstore's leaf Event shape.
//
// Details carries the free-text message body UNFILTERED by pii_filter — the
// same deliberate choice /flows makes for flow endpoint identity (#241) and
// internal/audit's storeEvent makes for its old/new diff: this view is local,
// bounded, and admin-authenticated, and pii_filter governs what this process
// EXPORTS over OTLP, not what an already-authenticated operator can see
// locally. eventstore.Memory.Record still truncates Details to
// eventstore.MaxDetailBytes — a policyUpdate event's message can carry an
// entire ACL document, which would make this one field unbounded even though
// the ring's event COUNT is bounded.
func storeEvent(ev event, dim string) eventstore.Event {
	out := eventstore.Event{
		Time:     parseTimestamp(ev.Timestamp),
		Source:   eventstore.SourceWebhook,
		Tailnet:  ev.Tailnet,
		Action:   dim,
		Type:     dim,
		Severity: eventstore.SeverityInfo,
		Summary:  ev.Message,
		Details:  ev.Message,
	}
	if severityForType(ev.Type) != telemetry.SeverityInfo {
		out.Severity = eventstore.SeverityWarn
	}
	if actor, ok := eventDataString(ev, "actor"); ok {
		out.ActorID = actor
	}
	if nodeID, ok := eventDataString(ev, "nodeID"); ok {
		out.TargetID = nodeID
	} else if user, ok := eventDataString(ev, "user"); ok {
		out.TargetID = user
	}
	if name, ok := eventDataString(ev, "deviceName"); ok {
		out.TargetName = name
	}
	return out
}

func decodeEvent(raw json.RawMessage) (event, error) {
	var wire struct {
		Timestamp string          `json:"timestamp"`
		Version   int             `json:"version"`
		Type      string          `json:"type"`
		Tailnet   string          `json:"tailnet"`
		Message   string          `json:"message"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return event{}, err
	}
	ev := event{
		Timestamp: wire.Timestamp,
		Version:   wire.Version,
		Type:      wire.Type,
		Tailnet:   wire.Tailnet,
		Message:   wire.Message,
	}
	if len(wire.Data) != 0 && !bytes.Equal(bytes.TrimSpace(wire.Data), []byte("null")) {
		_ = json.Unmarshal(wire.Data, &ev.Data) // wrong-shaped data is deliberately omitted.
	}
	return ev, nil
}

func typedDataAttrs(ev event) telemetry.Attrs {
	attrs := telemetry.Attrs{}
	addString := func(dataKey, attrKey string) {
		if value, ok := eventDataString(ev, dataKey); ok {
			attrs[attrKey] = value
		}
	}
	switch ev.Type {
	case "nodeCreated", "nodeNeedsApproval", "nodeApproved", "nodeKeyExpiringInOneDay", "nodeKeyExpired", "nodeDeleted", "nodeSigned", "nodeNeedsSignature":
		addString("nodeID", AttrNodeID)
		addString("deviceName", AttrDeviceName)
		addString("managedBy", AttrManagedBy)
		addString("actor", AttrActor)
		addString("url", AttrURL)
		if ev.Type == "nodeKeyExpiringInOneDay" || ev.Type == "nodeKeyExpired" {
			addString("expiration", AttrKeyExpiration)
		}
	case "policyUpdate":
		addString("actor", AttrActor)
		addString("url", AttrURL)
	case "userRoleUpdated":
		addString("user", AttrUser)
		addString("actor", AttrActor)
		addString("url", AttrURL)
		if roles, ok := eventDataStrings(ev, "oldRoles"); ok {
			attrs[AttrOldRoles] = roles
		}
		if roles, ok := eventDataStrings(ev, "newRoles"); ok {
			attrs[AttrNewRoles] = roles
		}
	}
	return attrs
}

func eventDataString(ev event, key string) (string, bool) {
	raw, ok := ev.Data[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func eventDataStrings(ev event, key string) ([]string, bool) {
	raw, ok := ev.Data[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false
	}
	return values, true
}

func canonicalDigest(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", errors.New("trailing JSON value")
		}
		return "", err
	}
	var out bytes.Buffer
	if err := writeCanonicalJSON(&out, value); err != nil {
		return "", err
	}
	sum := sha256.Sum256(out.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

func writeCanonicalJSON(out *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case nil, bool, json.Number:
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Write(b)
	case string:
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Write(b)
	case []any:
		out.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			b, err := json.Marshal(key)
			if err != nil {
				return err
			}
			out.Write(b)
			out.WriteByte(':')
			if err := writeCanonicalJSON(out, value[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return errors.New("unsupported JSON value")
	}
	return nil
}

// boundType maps an event type to the value used as a telemetry dimension,
// collapsing types beyond maxDistinctEventTypes distinct values into overflowType.
// Already-admitted types (and overflowType itself) always pass through, so the
// dimension's cardinality is capped at maxDistinctEventTypes+1 for the process
// lifetime. Safe for concurrent use.
func (s *Server) boundType(t string) string {
	s.typesMu.Lock()
	defer s.typesMu.Unlock()
	if _, ok := s.seenTypes[t]; ok {
		return t
	}
	if len(s.seenTypes) >= maxDistinctEventTypes {
		return overflowType
	}
	if s.seenTypes == nil {
		s.seenTypes = make(map[string]struct{}, maxDistinctEventTypes)
	}
	s.seenTypes[t] = struct{}{}
	return t
}

// parseSignatureHeader splits the header into its timestamp and the list of v1
// signatures. Unknown keys are ignored. An empty or malformed header is an error.
func parseSignatureHeader(header string) (time.Time, []string, error) {
	var (
		ts     time.Time
		haveTS bool
		sigs   []string
	)
	for pair := range strings.SplitSeq(header, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return time.Time{}, nil, errors.New("malformed signature element")
		}
		switch strings.TrimSpace(k) {
		case "t":
			secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, nil, errors.New("invalid timestamp in signature header")
			}
			ts = time.Unix(secs, 0)
			haveTS = true
		case signatureVersion:
			sigs = append(sigs, strings.TrimSpace(v))
		default:
			// Ignore unknown elements for forward compatibility.
		}
	}
	if !haveTS || len(sigs) == 0 {
		return time.Time{}, nil, errors.New("signature header missing timestamp or signature")
	}
	return ts, sigs, nil
}

// severityForType returns the log severity for a webhook event type, defaulting
// to INFO for any type not enumerated in severityByType.
func severityForType(eventType string) telemetry.Severity {
	if sev, ok := severityByType[eventType]; ok {
		return sev
	}
	return telemetry.SeverityInfo
}

// parseTimestamp parses an RFC3339 event timestamp, returning the zero time
// (which the emitter treats as "now") when the value is absent or unparseable.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// TLSStatus reports this receiver's certificate health, or false when it is not
// serving TLS (#316).
func (s *Server) TLSStatus() (certreload.Status, bool) {
	if s == nil || s.tlsReloader == nil {
		return certreload.Status{}, false
	}
	return s.tlsReloader.Status(), true
}

// TLSStatus delegates to the Router's base Server: every route shares one TLS
// configuration.
func (r *Router) TLSStatus() (certreload.Status, bool) {
	if r == nil || r.base == nil {
		return certreload.Status{}, false
	}
	return r.base.TLSStatus()
}
