package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/webhook"
)

const catTestSecret = "tskey-webhook-catalog-test-secret"

// catTwoEventBody is a JSON array of two events: a benign nodeCreated (INFO) and
// a nodeKeyExpiringInOneDay (WARN). Both drive the events counter plus one
// per-event log record (computed name eventNamePrefix+type).
const catTwoEventBody = `[` +
	`{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"Node foo created","data":{"nodeID":"n1"}},` +
	`{"timestamp":"2026-06-02T10:05:00Z","version":1,"type":"nodeKeyExpiringInOneDay","tailnet":"example.com","message":"Key for bar expiring","data":{"nodeID":"n2"}}` +
	`]`

// catSignBody computes the Tailscale-Webhook-Signature header value for body at
// timestamp ts, using the verified scheme: hex(HMAC-SHA256(secret, <ts>.<body>)).
func catSignBody(secret string, ts time.Time, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(fmt.Append(nil, ts.Unix()))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), sig)
}

// catDoPost posts body to path on h with an optional signature header and
// returns the response.
func catDoPost(t *testing.T, h http.Handler, path, body, sig string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if sig != "" {
		req.Header.Set("Tailscale-Webhook-Signature", sig)
	}
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw.Result()
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the receiver actually emits (with a tailscale.webhook. prefix) must be
// declared in Catalog() with a matching unit, instrument, and description
// (docs/metrics.md is generated from Catalog(), so this keeps the generated docs
// honest), and every emitted log record's EventName must follow the documented
// tailscale.webhook.<type> pattern declared via LogCatalog().
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	s := webhook.New(webhook.Options{
		Path:      "/webhook",
		Secret:    catTestSecret,
		Tolerance: 0,
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1) A validly-signed delivery emits tailscale.webhook.events plus two
	//    tailscale.webhook.<type> log records.
	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := catSignBody(catTestSecret, ts, catTwoEventBody)
	if resp := catDoPost(t, s.Handler(), "/webhook", catTwoEventBody, sig); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid POST status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// 2) A request with a missing signature emits tailscale.webhook.rejected.
	if resp := catDoPost(t, s.Handler(), "/webhook", catTwoEventBody, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned POST status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range webhook.Catalog() {
		declared[m.Name] = m
	}

	const prefix = "tailscale.webhook."
	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		// Only this package's owned-prefix metrics are subject to the catalog.
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in webhook.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}

	// Assert the two owned metrics were actually exercised and declared.
	for _, name := range []string{webhook.MetricEvents, webhook.MetricRejected} {
		if len(rec.MetricPoints(name)) == 0 {
			t.Errorf("expected metric %q to be emitted by the drive recipe", name)
		}
		if _, ok := declared[name]; !ok {
			t.Errorf("metric %q is not declared in webhook.Catalog()", name)
		}
	}

	// LogCatalog declares the computed per-event pattern (eventNamePrefix+<type>),
	// which cannot be matched by exact membership; instead require every emitted
	// log record's EventName to follow that prefix.
	logRecords := rec.LogRecords()
	if len(logRecords) == 0 {
		t.Fatalf("expected log records from the valid delivery, got none")
	}
	if len(webhook.LogCatalog()) == 0 {
		t.Errorf("webhook.LogCatalog() is empty; the per-event log pattern is undocumented")
	}
	for _, lr := range logRecords {
		if lr.EventName == "" {
			continue
		}
		if !strings.HasPrefix(lr.EventName, prefix) {
			t.Errorf("emitted log event %q does not follow the %q pattern declared in webhook.LogCatalog()", lr.EventName, prefix)
		}
	}

	// Attribute-drift guard (#126): every emitted metric attribute must be declared.
	// (Log events use a dynamic tailscale.webhook.<type> EventName pattern, so the
	// per-name guard covers the metrics here.)
	telemetrytest.AssertCatalogAttrs(t, rec, webhook.Catalog(), webhook.LogCatalog())
}
