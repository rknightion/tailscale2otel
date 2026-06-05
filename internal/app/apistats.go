package app

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/internal/ringbuf"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// apiLatencyHistoryLen is the number of recent request latencies retained per
// endpoint for the API panel's latency sparkline.
const apiLatencyHistoryLen = 60

// endpointStat accumulates the outcomes of requests to one low-cardinality API
// endpoint label.
type endpointStat struct {
	requests    int64
	errors      int64 // final status >= 400 or a transport error (status 0)
	retries     int64 // sum of (attempts-1) across requests
	rateLimited int64 // requests whose final status was 429
	lastStatus  int
	lastErr     string
	lastAt      time.Time
	last429At   time.Time
	durMs       *ringbuf.Ring[int64]
}

// APIStats aggregates per-endpoint Tailscale API request outcomes for the admin
// status page. It is fed by the tsapi OnRequest hook (one call per completed
// logical request, after retries) and read by buildStatus; a mutex makes the
// cross-goroutine access race-free. A nil *APIStats is a safe no-op.
type APIStats struct {
	mu sync.Mutex
	by map[string]*endpointStat
}

// NewAPIStats returns an empty aggregator.
func NewAPIStats() *APIStats {
	return &APIStats{by: make(map[string]*endpointStat)}
}

// Record folds one completed request into its endpoint's aggregates. Because the
// hook reports only the final outcome, rateLimited counts requests that ended in
// 429 (sustained limiting); transient 429s that recovered show up as retries.
func (s *APIStats) Record(i tsapi.RequestInfo) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.by[i.Endpoint]
	if st == nil {
		st = &endpointStat{durMs: ringbuf.New[int64](apiLatencyHistoryLen)}
		s.by[i.Endpoint] = st
	}
	now := time.Now()
	st.requests++
	if r := i.Attempts - 1; r > 0 {
		st.retries += int64(r)
	}
	st.lastStatus = i.Status
	st.lastErr = i.Err
	st.lastAt = now
	if i.Status == 0 || i.Status >= 400 {
		st.errors++
	}
	if i.Status == http.StatusTooManyRequests {
		st.rateLimited++
		st.last429At = now
	}
	if i.Duration > 0 {
		st.durMs.Add(i.Duration.Milliseconds())
	}
}

// APIEndpointSnapshot is an independent copy of one endpoint's aggregates.
type APIEndpointSnapshot struct {
	Endpoint    string
	Requests    int64
	Errors      int64
	Retries     int64
	RateLimited int64
	LastStatus  int
	LastErr     string
	LastAt      time.Time
	Last429At   time.Time
	DurMs       []int64
}

// Snapshot returns per-endpoint copies sorted by endpoint, independent of the
// aggregator's internal state. On a nil receiver it returns nil.
func (s *APIStats) Snapshot() []APIEndpointSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]APIEndpointSnapshot, 0, len(s.by))
	for ep, st := range s.by {
		out = append(out, APIEndpointSnapshot{
			Endpoint:    ep,
			Requests:    st.requests,
			Errors:      st.errors,
			Retries:     st.retries,
			RateLimited: st.rateLimited,
			LastStatus:  st.lastStatus,
			LastErr:     st.lastErr,
			LastAt:      st.lastAt,
			Last429At:   st.last429At,
			DurMs:       st.durMs.Values(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Endpoint < out[j].Endpoint })
	return out
}
