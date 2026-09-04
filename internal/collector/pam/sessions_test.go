package pam

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type sessionAPIFunc func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error)

func (f sessionAPIFunc) Sessions(ctx context.Context, opts ...b0api.PageOptions) (b0api.SessionPage, error) {
	return f(ctx, opts...)
}

func TestSessionsCollectorPagesNewestFirstAndStopsAtSeenID(t *testing.T) {
	base := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	store := collector.NewMemoryStore()
	seedCalls := 0
	seed := NewSessions(sessionAPIFunc(func(_ context.Context, opts ...b0api.PageOptions) (b0api.SessionPage, error) {
		seedCalls++
		assertPageOption(t, opts, 1, defaultSessionsPageSize)
		return sessionPage(0, session("known", base, "ssh")), nil
	}), time.Minute, store, store)
	if err := seed.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("seed Collect() error = %v", err)
	}
	if seedCalls != 1 {
		t.Fatalf("seed request count = %d, want 1", seedCalls)
	}

	calls := 0
	api := sessionAPIFunc(func(_ context.Context, opts ...b0api.PageOptions) (b0api.SessionPage, error) {
		calls++
		page := opts[0].Page
		switch page {
		case 1:
			return sessionPage(2,
				session("new-2", base.Add(2*time.Minute), "ssh"),
				session("new-1", base.Add(time.Minute), "database"),
			), nil
		case 2:
			return sessionPage(3,
				session("known", base, "ssh"),
				session("must-not-emit", base.Add(-time.Minute), "ssh"),
			), nil
		default:
			t.Fatalf("unexpected request for page %d", page)
			return b0api.SessionPage{}, nil
		}
	})
	rec := telemetrytest.New()
	restarted := NewSessions(api, time.Minute, store, store)
	if err := restarted.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("request count = %d, want exactly 2 (stop on first seen ID)", calls)
	}
	if got := sumMetric(rec, metricSessions); got != 2 {
		t.Fatalf("sessions delta = %v, want 2", got)
	}
}

func TestSessionsCollectorReplayAcrossRestartEmitsNoSecondDelta(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	page := sessionPage(0, session("stable-session", start, "ssh"))
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return page, nil
	})
	cursorPath := filepath.Join(t.TempDir(), "cursors.json")
	evidencePath := filepath.Join(t.TempDir(), "evidence.json")
	cursors, err := collector.NewFileStore(cursorPath)
	if err != nil {
		t.Fatalf("open cursor store: %v", err)
	}
	evidence, err := collector.NewFileStore(evidencePath)
	if err != nil {
		t.Fatalf("open evidence store: %v", err)
	}

	firstRec := telemetrytest.New()
	if err := NewSessions(api, 0, cursors, evidence).Collect(context.Background(), firstRec.Emitter()); err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}
	if got := sumMetric(firstRec, metricSessions); got != 1 {
		t.Fatalf("first sessions delta = %v, want 1", got)
	}
	evidenceKeys := evidence.Keys()
	if len(evidenceKeys) != 1 || !strings.HasPrefix(evidenceKeys[0], sessionEvidenceKeyPrefix) || strings.Contains(evidenceKeys[0], "stable-session") {
		t.Fatalf("evidence keys = %v, want one digest-only key", evidenceKeys)
	}

	// Reopen both files to model a process restart rather than retaining the
	// first collector's memory.
	cursors, err = collector.NewFileStore(cursorPath)
	if err != nil {
		t.Fatalf("reopen cursor store: %v", err)
	}
	evidence, err = collector.NewFileStore(evidencePath)
	if err != nil {
		t.Fatalf("reopen evidence store: %v", err)
	}

	secondRec := telemetrytest.New()
	if err := NewSessions(api, 0, cursors, evidence).Collect(context.Background(), secondRec.Emitter()); err != nil {
		t.Fatalf("restart Collect() error = %v", err)
	}
	if got := sumMetric(secondRec, metricSessions); got != 0 {
		t.Fatalf("restart sessions delta = %v, want 0", got)
	}
	if got := len(secondRec.MetricPoints(metricSessionDuration)); got != 0 {
		t.Fatalf("restart duration observations = %d, want 0", got)
	}
}

func TestSessionsCollectorStopsAtCursorWhenEvidenceIsUnavailable(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	cursors := collector.NewMemoryStore()
	if err := cursors.Set(sessionsCursorKey, start); err != nil {
		t.Fatal(err)
	}
	calls := 0
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		calls++
		return sessionPage(2,
			session("new", start.Add(time.Second), "ssh"),
			session("at-cursor", start, "ssh"),
		), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, cursors, collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("request count = %d, want 1", calls)
	}
	if got := sumMetric(rec, metricSessions); got != 1 {
		t.Fatalf("sessions delta = %v, want 1", got)
	}
}

func TestSessionsCollectorEmitsSessionMetricFamilies(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	ended := start.Add(90 * time.Second)
	completed := session("completed", start, "ssh")
	completed.EndTime = &ended
	completed.Killed = true
	completed.Events = []b0api.SessionEvent{
		{Type: "ssh_exec", Status: "success"},
		{Type: "ssh_session", Status: "error"},
	}
	active := session("active", start.Add(time.Minute), "database")
	active.EndTime = nil
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, active, completed), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if got := sumMetric(rec, metricSessions); got != 2 {
		t.Errorf("sessions = %v, want 2", got)
	}
	if got := sumMetric(rec, metricSessionsKilled); got != 1 {
		t.Errorf("killed sessions = %v, want 1", got)
	}
	if got := sumMetric(rec, metricSessionEvents); got != 2 {
		t.Errorf("events = %v, want 2", got)
	}
	duration := rec.MetricPoints(metricSessionDuration)
	if len(duration) != 1 || duration[0].Count != 1 || duration[0].Value != 90 {
		t.Errorf("duration points = %+v, want one 90-second observation", duration)
	}
	activePoints := rec.MetricPoints(metricSessionsActive)
	if len(activePoints) != 1 || activePoints[0].Value != 1 || activePoints[0].Attrs[attrSessionType] != "database" {
		t.Errorf("active points = %+v, want one active database session", activePoints)
	}
	if pts := rec.MetricPoints("tailscale.pam.session.recordings"); len(pts) != 0 {
		t.Fatalf("recording metric emitted %d points; asynchronous recording state must not be counted", len(pts))
	}
}

func TestSessionsCollectorTreatsEveryWireFieldAsOptional(t *testing.T) {
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, b0api.Session{}), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() with zero-value session error = %v", err)
	}
	if got := sumMetric(rec, metricSessions); got != 1 {
		t.Fatalf("zero-value session count = %v, want 1", got)
	}
	if got := len(rec.MetricPoints(metricSessionDuration)); got != 0 {
		t.Fatalf("zero-value duration points = %d, want 0", got)
	}
}

func TestSessionsCollectorProcessesTrackedRedactedFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "b0api", "testdata", "pam_sessions.json"))
	if err != nil {
		t.Fatalf("read tracked PAM session fixture: %v", err)
	}
	var page b0api.SessionPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode tracked PAM session fixture: %v", err)
	}
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return page, nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() tracked fixture error = %v", err)
	}
	if got := sumMetric(rec, metricSessions); got != float64(len(page.SessionLogs)) || got == 0 {
		t.Fatalf("tracked fixture sessions = %v, want %d", got, len(page.SessionLogs))
	}
	if got := len(rec.MetricPoints(metricSessionDuration)); got == 0 {
		t.Fatal("tracked fixture emitted no completed-session durations")
	}
	if got := sumMetric(rec, metricSessionEvents); got == 0 {
		t.Fatal("tracked fixture emitted no bounded session events")
	}
}

func TestSessionsCollectorBoundsWireEnums(t *testing.T) {
	s := session("unknown-enums", time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC), "future-session-type")
	s.Result = "future-result"
	s.Events = []b0api.SessionEvent{{Type: "future-event", Status: "future-status"}}
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, s), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	points := rec.MetricPoints(metricSessions)
	if len(points) != 1 || points[0].Attrs[attrSessionType] != "other" || points[0].Attrs[attrAuthorizationResult] != "other" {
		t.Fatalf("bounded session attrs = %+v, want type/result other", points)
	}
	events := rec.MetricPoints(metricSessionEvents)
	if len(events) != 1 || events[0].Attrs[attrSessionEventType] != "other" || events[0].Attrs[attrSessionEventStatus] != "other" {
		t.Fatalf("bounded event attrs = %+v, want type/status other", events)
	}
}

func TestSessionsCollectorClassifies403AsScopeDenied(t *testing.T) {
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return b0api.SessionPage{}, &b0api.StatusError{Code: 403}
	})
	rec := telemetrytest.New()
	tracker := apistate.NewTracker()
	err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionsAPIState(tracker)).Collect(context.Background(), rec.Emitter())
	if err == nil {
		t.Fatal("Collect() error = nil, want typed 403")
	}
	found := false
	for _, point := range rec.MetricPoints(apistate.MetricAvailability) {
		if point.Attrs[semconv.AttrAPIState] == string(apistate.StateScopeDenied) && point.Value == 1 {
			found = true
		}
		if point.Attrs[semconv.AttrAPIState] == string(apistate.StateDisabled) && point.Value == 1 {
			t.Fatalf("403 classified as disabled: %+v", point)
		}
	}
	if !found {
		t.Fatal("403 did not emit scope_denied availability")
	}
}

func TestSessionsCollectorDoesNotAdvanceStateOnFetchFailure(t *testing.T) {
	want := errors.New("unavailable")
	cursors := collector.NewMemoryStore()
	evidence := collector.NewMemoryStore()
	c := NewSessions(sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return b0api.SessionPage{}, want
	}), 0, cursors, evidence)
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); !errors.Is(err, want) {
		t.Fatalf("Collect() error = %v, want %v", err, want)
	}
	if _, ok := cursors.Get(sessionsCursorKey); ok {
		t.Fatal("cursor advanced after fetch failure")
	}
	if got := evidence.Keys(); len(got) != 0 {
		t.Fatalf("evidence keys after fetch failure = %v, want none", got)
	}
}

type failOnceSetStore struct {
	collector.CheckpointStore
	fail bool
}

func (s *failOnceSetStore) Set(key string, value time.Time) error {
	if s.fail {
		s.fail = false
		return errors.New("transient set failure")
	}
	return s.CheckpointStore.Set(key, value)
}

func TestSessionsCollectorRetriesEvidenceAfterPersistenceFailure(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	base := collector.NewMemoryStore()
	evidence := &failOnceSetStore{CheckpointStore: base, fail: true}
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, session("retry-me", start, "ssh")), nil
	})
	c := NewSessions(api, 0, collector.NewMemoryStore(), evidence)

	first := telemetrytest.New()
	if err := c.Collect(context.Background(), first.Emitter()); err == nil {
		t.Fatal("first Collect() error = nil, want evidence persistence failure")
	}
	second := telemetrytest.New()
	if err := c.Collect(context.Background(), second.Emitter()); err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}
	if got := sumMetric(second, metricSessions); got != 1 {
		t.Fatalf("retry sessions delta = %v, want 1", got)
	}
}

func TestSessionsCollectorUsesDistinctEvidenceForMissingIDs(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	first := session("", start, "ssh")
	first.SocketName = "first-service"
	second := session("", start.Add(time.Second), "database")
	second.SocketName = "second-service"
	evidence := collector.NewMemoryStore()
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, first, second), nil
	})
	if err := NewSessions(api, 0, collector.NewMemoryStore(), evidence).Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatal(err)
	}
	if got := len(evidence.Keys()); got != 2 {
		t.Fatalf("evidence key count = %d, want 2 for distinct sessions without IDs", got)
	}
}

type failOnceDeleteStore struct {
	collector.CheckpointStore
	fail bool
}

func (s *failOnceDeleteStore) Delete(key string) error {
	if s.fail {
		s.fail = false
		return errors.New("transient delete failure")
	}
	return s.CheckpointStore.Delete(key)
}

func TestSessionsCollectorRetriesStartupEvidenceCleanup(t *testing.T) {
	base := collector.NewMemoryStore()
	if err := base.Set(sessionEvidenceKeyPrefix+"malformed", time.Now()); err != nil {
		t.Fatal(err)
	}
	store := &failOnceDeleteStore{CheckpointStore: base, fail: true}
	calls := 0
	c := NewSessions(sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		calls++
		return sessionPage(0), nil
	}), 0, collector.NewMemoryStore(), store)
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err == nil {
		t.Fatal("first Collect returned nil after transient cleanup failure")
	}
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("second Collect did not retry cleanup: %v", err)
	}
	if calls != 1 {
		t.Fatalf("API calls = %d, want 1 after cleanup retry succeeded", calls)
	}
}

func TestSessionsCollectorContract(t *testing.T) {
	c := NewSessions(nil, 0, nil, nil)
	if got := c.Name(); got != "pam_sessions" {
		t.Fatalf("Name() = %q, want pam_sessions", got)
	}
	if got := c.DefaultInterval(); got != time.Minute {
		t.Fatalf("DefaultInterval() = %v, want 1m", got)
	}
}

func session(id string, start time.Time, sessionType string) b0api.Session {
	return b0api.Session{
		SessionID:   id,
		SocketName:  "bounded-service",
		StartTime:   start,
		SessionType: sessionType,
		Result:      "success",
	}
}

func sessionPage(next int, sessions ...b0api.Session) b0api.SessionPage {
	total := len(sessions)
	return b0api.SessionPage{
		Pagination:  &b0api.Pagination{CurrentPage: 1, NextPage: next, TotalRecords: &total},
		SessionLogs: sessions,
	}
}

func assertPageOption(t *testing.T, opts []b0api.PageOptions, page, pageSize int) {
	t.Helper()
	if len(opts) != 1 || opts[0].Page != page || opts[0].PageSize != pageSize {
		t.Fatalf("page options = %+v, want page=%d page_size=%d", opts, page, pageSize)
	}
}

func sumMetric(rec *telemetrytest.Recorder, name string) float64 {
	var total float64
	for _, point := range rec.MetricPoints(name) {
		total += point.Value
	}
	return total
}
