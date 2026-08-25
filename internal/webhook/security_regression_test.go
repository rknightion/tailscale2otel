package webhook

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestDeliveryDeduperRetainsOnlyFixedSizeDigest(t *testing.T) {
	d := newDeliveryDeduper(time.Hour, 1, time.Now)
	d.Add(strings.Repeat("attacker-controlled-event", 1<<18))
	for key := range d.seen {
		if len(key) != sha256.Size {
			t.Fatalf("retained delivery key size = %d, want %d", len(key), sha256.Size)
		}
	}
	if got := len(d.order[0].key); got != sha256.Size {
		t.Fatalf("retained delivery FIFO key size = %d, want %d", got, sha256.Size)
	}
}

type readProbe struct {
	read bool
	r    io.Reader
}

func (p *readProbe) Read(dst []byte) (int, error) {
	p.read = true
	return p.r.Read(dst)
}

func tokenlessRequest(h http.Handler, host, contentType string, headers map[string]string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", body)
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestTokenlessLoopbackRejectsBrowserRequestsBeforeReadingBody(t *testing.T) {
	valid := `[{"version":1,"type":"nodeCreated","tailnet":"example.test","message":"ok"}]`
	tests := []struct {
		name        string
		host        string
		contentType string
		headers     map[string]string
	}{
		{name: "dns rebinding host", host: "attacker.example", contentType: "application/json"},
		{name: "foreign origin", host: "127.0.0.1:9099", contentType: "application/json", headers: map[string]string{"Origin": "https://attacker.example"}},
		{name: "cross site fetch", host: "127.0.0.1:9099", contentType: "application/json", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "navigation", host: "127.0.0.1:9099", contentType: "application/json", headers: map[string]string{"Sec-Fetch-Mode": "navigate"}},
		{name: "safelisted content type", host: "127.0.0.1:9099", contentType: "text/plain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTrustworthyTestServer(t, Options{})
			body := &readProbe{r: strings.NewReader(valid)}
			w := tokenlessRequest(s.Handler(), tc.host, tc.contentType, tc.headers, body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%q", w.Code, w.Body.String())
			}
			if body.read {
				t.Fatal("browser-shaped tokenless request body was read before the request gate")
			}
		})
	}
}

func TestTokenlessLoopbackAllowsNonBrowserJSONDelivery(t *testing.T) {
	valid := `[{"version":1,"type":"nodeCreated","tailnet":"example.test","message":"ok"}]`
	for _, host := range []string{"127.0.0.1:9099", "127.7.8.9", "[::1]:9099", "LOCALHOST.:9099"} {
		t.Run(host, func(t *testing.T) {
			s, _ := newTrustworthyTestServer(t, Options{})
			w := tokenlessRequest(s.Handler(), host, "application/json", nil, strings.NewReader(valid))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
			}
		})
	}
}

func TestRouterAppliesTokenlessBrowserGateBeforeBodyRead(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook"}, rec.Emitter(), slog.New(slog.DiscardHandler))
	router := NewRouter([]Route{{Tailnet: "example.test", Server: s}})
	body := &readProbe{r: strings.NewReader(`[{"version":1,"type":"nodeCreated","tailnet":"example.test"}]`)}
	w := tokenlessRequest(router.Handler(), "attacker.example", "application/json", nil, body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if body.read {
		t.Fatal("router read the request body before the tokenless browser gate")
	}
}

func TestRouterRefusesMixedTokenlessAndSignedRoutesBeforeBodyRead(t *testing.T) {
	rec := telemetrytest.New()
	logger := slog.New(slog.DiscardHandler)
	tokenless := New(Options{Listen: "127.0.0.1:0", Path: "/webhook"}, rec.Emitter(), logger)
	signed := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret}, rec.Emitter(), logger)
	router := NewRouter([]Route{
		{Tailnet: "tokenless.example", Server: tokenless},
		{Tailnet: "signed.example", Server: signed},
	})
	body := &readProbe{r: strings.NewReader(`[{"version":1,"type":"nodeCreated","tailnet":"signed.example"}]`)}
	w := tokenlessRequest(router.Handler(), "webhook.example.internal", "application/json", nil, body)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for unsupported mixed auth modes", w.Code)
	}
	if body.read {
		t.Fatal("mixed-auth router read the request body before refusing invalid setup")
	}
}

func TestRouterReturnsTooManyEventsForOversizedBatch(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook"}, rec.Emitter(), slog.New(slog.DiscardHandler))
	router := NewRouter([]Route{{Tailnet: "example.test", Server: s}})
	event := `{"version":1,"type":"nodeCreated","tailnet":"example.test"}`
	body := "[" + strings.Repeat(event+",", maxEventsPerBatch) + event + "]"
	w := tokenlessRequest(router.Handler(), "127.0.0.1:9099", "application/json", nil, strings.NewReader(body))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	points := rec.MetricPoints(docWebhookRejected.Name)
	if len(points) != 1 || points[0].Attrs[attrReason] != "too_many_events" {
		t.Fatalf("rejection points = %+v, want reason=too_many_events", points)
	}
}

func TestAuthenticatedWebhookDoesNotRequireLoopbackHost(t *testing.T) {
	s, _ := newTrustworthyTestServer(t, Options{Secret: testSecret})
	body := `[{"version":1,"type":"nodeCreated","tailnet":"example.test","message":"ok"}]`
	now := time.Now().UTC().Truncate(time.Second)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Host = "webhook.example.internal"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(signatureHeader, signBody(testSecret, now, body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
}

func TestWebhookBatchEventBudgetAndIdentity(t *testing.T) {
	event := `{"version":1,"type":"nodeCreated","tailnet":"example.test"}`
	batch := func(n int) []byte { return []byte("[" + strings.Repeat(event+",", n-1) + event + "]") }

	if _, err := decodeAcceptedBatch(batch(1000)); err != nil {
		t.Fatalf("1,000-event batch rejected: %v", err)
	}
	if _, err := decodeAcceptedBatch(batch(1001)); err == nil {
		t.Fatal("1,001-event batch accepted")
	}
	for _, body := range [][]byte{[]byte(`[null]`), []byte(`[{}]`), []byte(`[{"type":"nodeCreated"}]`), []byte(`[{"tailnet":"example.test"}]`)} {
		if _, err := decodeAcceptedBatch(body); err == nil {
			t.Fatalf("identity-free event accepted: %s", body)
		}
	}
}

func TestWebhookBatchBudgetAlsoAppliesToDurableReplay(t *testing.T) {
	s, _ := newTrustworthyTestServer(t, Options{})
	event := `{"version":1,"type":"nodeCreated","tailnet":"example.test"}`
	body := []byte("[" + strings.Repeat(event+",", 1000) + event + "]")
	if err := s.ApplyDurable(context.Background(), body, time.Now()); err == nil {
		t.Fatal("durable replay accepted a 1,001-event batch")
	}
}
