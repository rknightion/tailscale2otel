// Package release provides a cached, fail-open fetcher for an external
// "latest version" string plus version parse/compare helpers, shared by the
// self update-available check (C4) and per-device version-skew metrics (B6).
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// versionRe captures the leading MAJOR.MINOR.PATCH, tolerating a leading "v"
// and any suffix (-t<hash>, -dev<date>, "v0.2.0"). Three components required.
var versionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// Version is a parsed MAJOR.MINOR.PATCH triple.
type Version struct{ Major, Minor, Patch int }

// Parse reduces a raw version string to a Version. Returns ok=false for empty
// or unparseable input (e.g. the "dev" placeholder build).
func Parse(raw string) (Version, bool) {
	m := versionRe.FindStringSubmatch(raw)
	if m == nil {
		return Version{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{Major: maj, Minor: min, Patch: pat}, true
}

// Normalize returns the MAJOR.MINOR.PATCH prefix, or "unknown" if unparseable.
func Normalize(raw string) string {
	v, ok := Parse(raw)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Less reports whether v is an older release than o.
func (v Version) Less(o Version) bool {
	switch {
	case v.Major != o.Major:
		return v.Major < o.Major
	case v.Minor != o.Minor:
		return v.Minor < o.Minor
	default:
		return v.Patch < o.Patch
	}
}

// MinorsBehind reports how many minor releases dev is behind latest, within the
// same major, clamped to >= 0. Cross-major comparison returns 0 (Tailscale uses
// a single-major 1.x scheme, so cross-major skew is not meaningful here).
func MinorsBehind(dev, latest Version) int {
	if dev.Major != latest.Major || latest.Minor <= dev.Minor {
		return 0
	}
	return latest.Minor - dev.Minor
}

// Upstream "latest release" endpoints. Both are public, unauthenticated JSON.
const (
	// GitHubLatestURL is this project's own latest GitHub release (C4).
	GitHubLatestURL = "https://api.github.com/repos/rknightion/tailscale2otel/releases/latest"
	// TailscalePkgsURL is Tailscale's canonical latest-stable manifest (B6).
	TailscalePkgsURL = "https://pkgs.tailscale.com/stable/?mode=json"
	// SelfReleaseURL is the human-facing release page for this project, used as
	// the admin status page's update link (#330) — the same release the
	// GitHubLatestURL API call above resolves, just the page a human clicks.
	SelfReleaseURL = "https://github.com/rknightion/tailscale2otel/releases/latest"
)

// Parser extracts a version string from a response body.
type Parser func(body []byte) (string, error)

// ParseGitHubLatest pulls tag_name from a GitHub /releases/latest response.
func ParseGitHubLatest(body []byte) (string, error) {
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", errors.New("github release: empty tag_name")
	}
	return r.TagName, nil
}

// ParseTailscalePkgs pulls Version from pkgs.tailscale.com/stable/?mode=json.
func ParseTailscalePkgs(body []byte) (string, error) {
	var r struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.Version == "" {
		return "", errors.New("tailscale pkgs: empty Version")
	}
	return r.Version, nil
}

// Fetcher periodically fetches a "latest version" from a remote endpoint and
// serves the last successfully fetched value. It is fail-open: a fetch error
// keeps the previous value (or none) and is logged at debug, never returned.
// Safe for concurrent use.
type Fetcher struct {
	name      string
	url       string
	userAgent string
	parse     Parser
	client    *http.Client
	ttl       time.Duration
	logger    *slog.Logger

	// tracer emits one client span per fetch() call, mirroring
	// internal/tsapi/transport.go's retryTransport.startSpan. Resolved to
	// noopAPITracer in NewFetcher when no WithTracer option is given, so fetch
	// never needs a nil-guard.
	tracer trace.Tracer

	mu        sync.RWMutex
	latest    string
	ok        bool
	checkedAt time.Time
	errClass  string
}

// FetcherOption configures optional Fetcher behavior not carried by
// NewFetcher's required positional parameters. This package's constructor
// convention is positional (see NewFetcher), so a new optional dependency is
// added as a trailing variadic option rather than growing the positional list
// or breaking existing call sites.
type FetcherOption func(*Fetcher)

// WithTracer sets the tracer used to emit one client span per fetch. A nil
// tracer (the default, or explicitly passed) falls back to a no-op tracer.
func WithTracer(tracer trace.Tracer) FetcherOption {
	return func(f *Fetcher) { f.tracer = tracer }
}

// noopAPITracer is the shared fallback for a Fetcher with no WithTracer
// option, so the nil-tracer path allocates no tracer per fetch() call.
var noopAPITracer = tracenoop.NewTracerProvider().Tracer("")

// Snapshot is a point-in-time view of a Fetcher's state, for a consumer (the
// admin status page, #330) that needs to explain WHY no value is available —
// "never checked yet" is a different story than "checking is failing" is a
// different story than "succeeded, but stale since the last failure" (the
// fail-open case: OK/Latest stay populated from the last success while
// ErrClass reports the most recent failure).
type Snapshot struct {
	// Latest and OK mirror Fetcher.Latest(): the last successfully fetched
	// version and whether one has ever been fetched.
	Latest string
	OK     bool
	// CheckedAt is the time of the last Refresh ATTEMPT (success or failure);
	// zero if Refresh has never run.
	CheckedAt time.Time
	// ErrClass classifies the most recent failed attempt: "network",
	// "http_error", "parse_error", or "" if the last attempt succeeded (or none
	// has run yet). Deliberately a class, not the raw error text — see the
	// delivery-signal precedent in internal/app/status.go for why raw error
	// text does not belong on an operator-facing surface.
	ErrClass string
}

// Snapshot returns the Fetcher's current state.
func (f *Fetcher) Snapshot() Snapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return Snapshot{Latest: f.latest, OK: f.ok, CheckedAt: f.checkedAt, ErrClass: f.errClass}
}

// NewFetcher builds a Fetcher. logger may be nil (falls back to slog.Default).
// opts are applied after defaults (currently just WithTracer); a fully
// backward-compatible extension point (see FetcherOption) so this constructor's
// existing positional call sites need no changes.
func NewFetcher(name, url, userAgent string, parse Parser, client *http.Client, ttl time.Duration, logger *slog.Logger, opts ...FetcherOption) *Fetcher {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	f := &Fetcher{name: name, url: url, userAgent: userAgent, parse: parse, client: client, ttl: ttl, logger: logger, tracer: noopAPITracer}
	for _, opt := range opts {
		opt(f)
	}
	if f.tracer == nil {
		f.tracer = noopAPITracer
	}
	return f
}

// Latest returns the last successfully fetched version and whether one exists.
func (f *Fetcher) Latest() (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.latest, f.ok
}

// Errors fetch() wraps its failures in, purely so Refresh can classify them
// without string-matching. Never returned to a caller directly.
var (
	errNetwork = errors.New("network")
	errHTTP    = errors.New("http_error")
	errParse   = errors.New("parse_error")
)

// classify maps a fetch() error to its Snapshot.ErrClass string.
func classify(err error) string {
	switch {
	case errors.Is(err, errNetwork):
		return "network"
	case errors.Is(err, errHTTP):
		return "http_error"
	case errors.Is(err, errParse):
		return "parse_error"
	default:
		return "unknown"
	}
}

// Refresh performs one fetch now, updating the cached value on success only
// (fail-open). CheckedAt and ErrClass are recorded on every attempt, success
// or failure, so a Snapshot consumer can always tell when the last attempt
// happened and whether it succeeded.
func (f *Fetcher) Refresh(ctx context.Context) {
	v, err := f.fetch(ctx)
	f.mu.Lock()
	f.checkedAt = time.Now()
	if err != nil {
		f.errClass = classify(err)
		f.mu.Unlock()
		f.logger.Debug("release check failed (fail-open)", "source", f.name, "url", f.url, "error", err)
		return
	}
	f.latest, f.ok = v, true
	f.errClass = ""
	f.mu.Unlock()
}

// fetch performs one GET of f.url, emitting a single client span for the
// call (see WithTracer), mirroring internal/tsapi/transport.go's
// retryTransport pattern. The span name is "release.check " + f.name: name is
// always one of a small fixed set of literal strings chosen by the caller
// (e.g. "self", "tailscale"), so — like hsapi's endpointLabel — the label
// space is bounded by construction, needing no further elision.
func (f *Fetcher) fetch(ctx context.Context) (string, error) {
	// Classified as background so a periodic update check can be sampled
	// independently of scrape and receiver traffic (#372).
	spanCtx, span := f.tracer.Start(ctx, "release.check "+f.name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(telemetry.SpanClassKey.String(telemetry.SpanClassBackground)))
	v, status, err := f.doFetch(spanCtx)
	if span.IsRecording() {
		span.SetAttributes(
			attribute.String("release.source", f.name),
			attribute.String("http.request.method", http.MethodGet),
			attribute.String("url.full", f.url),
		)
		if status != 0 {
			span.SetAttributes(attribute.Int("http.response.status_code", status))
		}
		switch {
		case err != nil:
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		case status >= 400:
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
	span.End()
	return v, err
}

// doFetch is fetch's transport body, split out so fetch can wrap it in a span
// without duplicating the status bookkeeping. status is 0 when no HTTP
// response was received.
func (f *Fetcher) doFetch(ctx context.Context) (v string, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %w", errNetwork, err)
	}
	req.Header.Set("Accept", "application/json")
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %w", errNetwork, err)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", status, fmt.Errorf("%w: %s: status %d", errHTTP, f.url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", status, fmt.Errorf("%w: %w", errNetwork, err)
	}
	v, err = f.parse(body)
	if err != nil {
		return "", status, fmt.Errorf("%w: %w", errParse, err)
	}
	return v, status, nil
}

// Run refreshes immediately, then every ttl until ctx is canceled. Intended to
// be started as a goroutine.
func (f *Fetcher) Run(ctx context.Context) {
	f.Refresh(ctx)
	t := time.NewTicker(f.ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.Refresh(ctx)
		}
	}
}
