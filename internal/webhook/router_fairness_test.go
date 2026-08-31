package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// TestRouterAutomaticFairShareKeepsOneRouteFromExhaustingGlobalBudget pins
// the default route share. A four-slot listener with two routes gives each
// route two slots, so a third request waiting on the noisy route must not take
// one of the two global slots still available to the other route.
func TestRouterAutomaticFairShareKeepsOneRouteFromExhaustingGlobalBudget(t *testing.T) {
	const globalMax = 4

	noisy := New(Options{Path: "/webhook", Secret: "noisy", MaxConcurrentRequests: globalMax}, nil, nil)
	quiet := New(Options{Path: "/webhook", Secret: "quiet", MaxConcurrentRequests: globalMax}, nil, nil)
	router := NewRouter([]Route{
		{Tailnet: "noisy.example", Server: noisy},
		{Tailnet: "quiet.example", Server: quiet},
	})

	if got := cap(noisy.admit); got != globalMax/2 {
		t.Fatalf("noisy route budget = %d, want %d", got, globalMax/2)
	}
	if got := cap(quiet.admit); got != globalMax/2 {
		t.Fatalf("quiet route budget = %d, want %d", got, globalMax/2)
	}
	if got := cap(router.admit); got != globalMax {
		t.Fatalf("global budget = %d, want %d", got, globalMax)
	}

	// Occupy every slot available to the noisy route. These requests also hold
	// two of the four global slots, leaving room for another route.
	releases := make([]func(), 0, globalMax/2)
	for range globalMax / 2 {
		release, ok := noisy.acquire(context.Background())
		if !ok {
			t.Fatal("noisy route could not acquire its fair-share slot")
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	// A further noisy request must stop at its route budget. A canceled context
	// makes this assertion immediate instead of waiting for admissionWait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, ok := noisy.acquire(ctx); ok {
		release()
		t.Fatal("noisy route acquired beyond its fair-share budget")
	}

	// The quiet route still has a route slot and a global slot available while
	// the noisy route is full: this is the cross-tailnet fairness guarantee.
	releaseQuiet, ok := quiet.acquire(context.Background())
	if !ok {
		t.Fatal("quiet route was starved by noisy route admission")
	}
	releaseQuiet()
}

func fairnessWebhookBody(tailnet, message string) string {
	return `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"` + tailnet + `","message":"` + message + `"}]`
}

func serveFairnessWebhook(h http.Handler, body, signature string) *http.Response {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Host = "127.0.0.1:9099"
	req.Header.Set(signatureHeader, signature)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw.Result()
}

// TestRouterFairShareAllowsQuietRequestWhileNoisyRouteIsBlocked exercises the
// live HTTP router. Both fair-share slots for the noisy tailnet are held after
// authentication and delivery processing starts; the quiet tailnet must still
// acquire its own route slot and a global slot and return success.
func TestRouterFairShareAllowsQuietRequestWhileNoisyRouteIsBlocked(t *testing.T) {
	const globalMax = 4
	const noisySecret = "noisy-secret"
	const quietSecret = "quiet-secret"

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	onAccepted := func(ingest.AcceptedEvent) {
		entered <- struct{}{}
		<-release
	}

	noisyRec := telemetrytest.New()
	noisy := New(Options{
		Path:                  "/webhook",
		Listen:                "127.0.0.1:0",
		Secret:                noisySecret,
		MaxConcurrentRequests: globalMax,
		OnAccepted:            onAccepted,
	}, noisyRec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	quietRec := telemetrytest.New()
	quiet := New(Options{
		Path:                  "/webhook",
		Listen:                "127.0.0.1:0",
		Secret:                quietSecret,
		MaxConcurrentRequests: globalMax,
	}, quietRec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := NewRouter([]Route{
		{Tailnet: "noisy.example", Server: noisy},
		{Tailnet: "quiet.example", Server: quiet},
	})
	h := router.Handler()
	now := time.Now().UTC().Truncate(time.Second)
	noisyBodies := []string{
		fairnessWebhookBody("noisy.example", "first"),
		fairnessWebhookBody("noisy.example", "second"),
	}
	done := make(chan *http.Response, len(noisyBodies))
	for _, body := range noisyBodies {
		body, signature := body, signBody(noisySecret, now, body)
		go func() {
			done <- serveFairnessWebhook(h, body, signature)
		}()
	}
	for range noisyBodies {
		<-entered
	}

	quietBody := fairnessWebhookBody("quiet.example", "quiet")
	quietResponse := serveFairnessWebhook(h, quietBody, signBody(quietSecret, now, quietBody))
	defer quietResponse.Body.Close()
	if quietResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(quietResponse.Body)
		t.Fatalf("quiet route status = %d, want 200; body=%q", quietResponse.StatusCode, body)
	}

	releaseAll()
	for range noisyBodies {
		response := <-done
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("noisy route status = %d, want 200", response.StatusCode)
		}
	}
}
