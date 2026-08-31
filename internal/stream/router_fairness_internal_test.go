package stream

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// TestRouterAutomaticFairShareKeepsOneRouteFromExhaustingGlobalBudget pins
// the default route share. A four-slot listener with two routes gives each
// route two slots, so a third request waiting on the noisy route must not take
// one of the two global slots still available to the other route.
func TestRouterAutomaticFairShareKeepsOneRouteFromExhaustingGlobalBudget(t *testing.T) {
	const globalMax = 4

	noisy := New(Options{Path: "/noisy", MaxConcurrentRequests: globalMax}, nil, nil, nil, nil)
	quiet := New(Options{Path: "/quiet", MaxConcurrentRequests: globalMax}, nil, nil, nil, nil)
	router := NewRouter([]Route{
		{Path: "/noisy", Server: noisy},
		{Path: "/quiet", Server: quiet},
	})

	if got := cap(noisy.admit); got != globalMax/2 {
		t.Fatalf("noisy route budget = %d, want %d", got, globalMax/2)
	}
	if got := cap(quiet.admit); got != globalMax/2 {
		t.Fatalf("quiet route budget = %d, want %d", got, globalMax/2)
	}
	if got := cap(noisy.globalAdmit); got != globalMax {
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
	_ = router // retain the composed router as the subject under test
}

// fairnessStreamBody is a small valid HEC flow used to keep a request inside
// the receiver while the custom body reader holds its admission slot.
const fairnessStreamBody = `{"event":{"logged":"2024-06-06T15:27:26Z","nodeId":"n1","start":"2024-06-06T15:25:26Z","end":"2024-06-06T15:26:26Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:443","dst":"100.64.0.2:51820","txPkts":1,"txBytes":100,"rxPkts":1,"rxBytes":80}]}}`

type fairnessStreamBodyReader struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
	reader  *strings.Reader
}

func newFairnessStreamBodyReader(release <-chan struct{}) *fairnessStreamBodyReader {
	return &fairnessStreamBodyReader{
		entered: make(chan struct{}),
		release: release,
		reader:  strings.NewReader(fairnessStreamBody),
	}
}

func (b *fairnessStreamBodyReader) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return b.reader.Read(p)
}

func fairnessStreamServer(opts Options) *Server {
	rec := telemetrytest.New()
	cache := enrich.NewDeviceCache()
	flowProc := flowlog.NewProcessor(cache, flowlog.Options{NodeDims: true})
	auditProc := audit.NewProcessor()
	return New(opts, flowProc, auditProc, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func serveFairnessStream(h http.Handler, path string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Host = "127.0.0.1:9099"
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw
}

// TestRouterFairShareAllowsQuietRequestWhileNoisyRouteIsBlocked exercises the
// live HTTP router. Both fair-share slots for the noisy route are held in body
// reads; the quiet route must still acquire its own route slot and a global
// slot, parse a real flow, and return success.
func TestRouterFairShareAllowsQuietRequestWhileNoisyRouteIsBlocked(t *testing.T) {
	const globalMax = 4

	noisy := fairnessStreamServer(Options{
		Path:                  "/noisy",
		Listen:                "127.0.0.1:0",
		MaxConcurrentRequests: globalMax,
	})
	quiet := fairnessStreamServer(Options{
		Path:                  "/quiet",
		Listen:                "127.0.0.1:0",
		MaxConcurrentRequests: globalMax,
	})
	router := NewRouter([]Route{
		{Path: "/noisy", Server: noisy},
		{Path: "/quiet", Server: quiet},
	})
	h := router.Handler()

	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	bodies := []*fairnessStreamBodyReader{
		newFairnessStreamBodyReader(release),
		newFairnessStreamBodyReader(release),
	}
	done := make(chan *httptest.ResponseRecorder, len(bodies))
	for _, body := range bodies {
		go func(body *fairnessStreamBodyReader) {
			done <- serveFairnessStream(h, "/noisy", body)
		}(body)
	}
	for _, body := range bodies {
		<-body.entered
	}

	quietResponse := serveFairnessStream(h, "/quiet", strings.NewReader(fairnessStreamBody))
	if quietResponse.Code != http.StatusOK {
		t.Fatalf("quiet route status = %d, want 200; body=%q", quietResponse.Code, quietResponse.Body.String())
	}

	releaseAll()
	for range bodies {
		if response := <-done; response.Code != http.StatusOK {
			t.Fatalf("noisy route status = %d, want 200; body=%q", response.Code, response.Body.String())
		}
	}
}
