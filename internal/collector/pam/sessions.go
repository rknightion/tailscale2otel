package pam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

const (
	defaultSessionsInterval        = time.Minute
	defaultSessionsPageSize        = 100
	defaultSessionEvidenceCapacity = 1024
	sessionsCursorKey              = "pam/sessions/cursor"
	sessionEvidenceKeyPrefix       = "pam/sessions/evidence/"
	opSessions                     = "border0GetSessions"
	metricSessions                 = "tailscale.pam.sessions"
	metricSessionDuration          = "tailscale.pam.session.duration"
	metricSessionsKilled           = "tailscale.pam.sessions.killed"
	metricSessionsActive           = "tailscale.pam.sessions.active"
	metricSessionEvents            = "tailscale.pam.session.events"
	attrSessionType                = "tailscale.pam.session.type"
	attrAuthorizationResult        = "tailscale.pam.session.authorization_result"
	attrSessionEventType           = "tailscale.pam.session.event.type"
	attrSessionEventStatus         = "tailscale.pam.session.event.status"
)

var sessionDurationBucketsSeconds = []float64{1, 10, 30, 60, 300, 900, 3600, 14400, 43200}

type sessionAPI interface {
	Sessions(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error)
}

var (
	_ collector.SnapshotCollector = (*SessionsCollector)(nil)
	_ sessionAPI                  = (*b0api.Client)(nil)
)

// SessionsCollector polls Border0's newest-first session history. Counter and
// histogram families are emitted only for session IDs not present in durable
// evidence and newer than the independent poll cursor.
type SessionsCollector struct {
	api                    sessionAPI
	interval               time.Duration
	cursorStore            collector.CheckpointStore
	evidenceStore          collector.CheckpointStore
	tracker                *apistate.Tracker
	now                    func() time.Time
	pageSize               int
	capacity               int
	cursor                 time.Time
	seen                   map[string]time.Time
	pendingEvidenceDeletes []string
}

// SessionsOption configures the independently scheduled session collector.
type SessionsOption func(*SessionsCollector)

// WithSessionsAPIState wires the per-operation API availability tracker.
func WithSessionsAPIState(tracker *apistate.Tracker) SessionsOption {
	return func(c *SessionsCollector) { c.tracker = tracker }
}

// WithSessionsClock overrides the observation clock for deterministic tests.
func WithSessionsClock(now func() time.Time) SessionsOption {
	return func(c *SessionsCollector) {
		if now != nil {
			c.now = now
		}
	}
}

// WithSessionsPageSize overrides the bounded page size. Non-positive values
// retain the default.
func WithSessionsPageSize(pageSize int) SessionsOption {
	return func(c *SessionsCollector) {
		if pageSize > 0 {
			c.pageSize = pageSize
		}
	}
}

// WithSessionEvidenceCapacity bounds the number of digest-only identities
// retained across restarts. Non-positive values retain the default.
func WithSessionEvidenceCapacity(capacity int) SessionsOption {
	return func(c *SessionsCollector) {
		if capacity > 0 {
			c.capacity = capacity
		}
	}
}

// NewSessions returns the independently scheduled PAM session collector.
// cursorStore and evidenceStore are deliberately separate durability classes;
// nil stores fall back to separate process-local stores.
func NewSessions(a sessionAPI, interval time.Duration, cursorStore, evidenceStore collector.CheckpointStore, opts ...SessionsOption) *SessionsCollector {
	if cursorStore == nil {
		cursorStore = collector.NewMemoryStore()
	}
	if evidenceStore == nil {
		evidenceStore = collector.NewMemoryStore()
	}
	c := &SessionsCollector{
		api:           a,
		interval:      interval,
		cursorStore:   cursorStore,
		evidenceStore: evidenceStore,
		now:           time.Now,
		pageSize:      defaultSessionsPageSize,
		capacity:      defaultSessionEvidenceCapacity,
		seen:          make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.loadState()
	return c
}

// Name returns the stable independently scheduled collector identifier.
func (*SessionsCollector) Name() string { return "pam_sessions" }

// DefaultInterval returns the configured interval, or one minute when unset.
func (c *SessionsCollector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultSessionsInterval
}

// Collect polls from page one and stops issuing requests once the returned
// prefix reaches a durable session ID or the persisted start-time cursor.
func (c *SessionsCollector) Collect(ctx context.Context, e telemetry.Emitter) error {
	if err := c.retryEvidenceCleanup(); err != nil {
		return err
	}
	if c.api == nil {
		return errors.New("pam sessions: API client is nil")
	}

	pageNumber := 1
	requestedPages := make(map[int]struct{})
	additions := make(map[string]time.Time)
	active := make(map[sessionSeries]map[string]struct{})
	newCursor := c.cursor
	for {
		if _, duplicate := requestedPages[pageNumber]; duplicate {
			return fmt.Errorf("pam sessions: pagination repeated page %d", pageNumber)
		}
		requestedPages[pageNumber] = struct{}{}
		page, err := c.api.Sessions(ctx, b0api.PageOptions{Page: pageNumber, PageSize: c.pageSize})
		apistate.Observe(e, c.tracker, c.Name(), opSessions, apistate.Disposition{}, err, c.now())
		if err != nil {
			return err
		}

		stop := false
		for i := range page.SessionLogs {
			s := &page.SessionLogs[i]
			if s.EndTime == nil {
				series := sessionSeries{service: s.SocketName, sessionType: boundedSessionType(s.SessionType)}
				ids := active[series]
				if ids == nil {
					ids = make(map[string]struct{})
					active[series] = ids
				}
				ids[sessionDigestFor(*s)] = struct{}{}
			}
			if stop {
				continue
			}
			digest := sessionDigestFor(*s)
			if _, exists := c.seen[digest]; exists || (!c.cursor.IsZero() && !s.StartTime.After(c.cursor)) {
				stop = true
				continue
			}
			c.emitSession(e, *s)
			additions[digest] = s.StartTime
			if s.StartTime.After(newCursor) {
				newCursor = s.StartTime
			}
		}

		if stop || page.Pagination == nil || page.Pagination.NextPage <= 0 {
			break
		}
		pageNumber = page.Pagination.NextPage
	}

	c.emitActive(e, active)
	return c.persistState(additions, newCursor)
}

type sessionSeries struct {
	service     string
	sessionType string
}

func (c *SessionsCollector) emitSession(e telemetry.Emitter, s b0api.Session) {
	sessionType := boundedSessionType(s.SessionType)
	e.Counter(docPAMSessions.Name, docPAMSessions.Unit, docPAMSessions.Description, 1, telemetry.Attrs{
		attrServiceName:         s.SocketName,
		attrSessionType:         sessionType,
		attrAuthorizationResult: boundedAuthorizationResult(s.Result),
	})
	if s.Killed {
		e.Counter(docPAMSessionsKilled.Name, docPAMSessionsKilled.Unit, docPAMSessionsKilled.Description, 1, telemetry.Attrs{
			attrServiceName: s.SocketName,
			attrSessionType: sessionType,
		})
	}
	if s.EndTime != nil && !s.StartTime.IsZero() && !s.EndTime.Before(s.StartTime) {
		e.Histogram(docPAMSessionDuration.Name, docPAMSessionDuration.Unit, docPAMSessionDuration.Description,
			s.EndTime.Sub(s.StartTime).Seconds(), sessionDurationBucketsSeconds,
			telemetry.Attrs{attrSessionType: sessionType})
	}
	for i := range s.Events {
		e.Counter(docPAMSessionEvents.Name, docPAMSessionEvents.Unit, docPAMSessionEvents.Description, 1, telemetry.Attrs{
			attrSessionEventType:   boundedSessionEventType(s.Events[i].Type),
			attrSessionEventStatus: boundedSessionEventStatus(s.Events[i].Status),
		})
	}
}

func boundedSessionType(value string) string {
	switch value {
	case "ssh", "http", "database", "tls", "vnc", "rdp", "subnet_router", "exit_node", "snowflake", "elasticsearch", "kubernetes", "aws_s3":
		return value
	default:
		return "other"
	}
}

func boundedAuthorizationResult(value string) string {
	switch value {
	case "success", "failed", "failure", "denied", "error":
		return value
	default:
		return "other"
	}
}

func boundedSessionEventType(value string) string {
	switch value {
	case "ssh_session", "ssh_exec":
		return value
	default:
		return "other"
	}
}

func boundedSessionEventStatus(value string) string {
	switch value {
	case "success", "error", "failed", "failure":
		return value
	default:
		return "other"
	}
}

func (c *SessionsCollector) emitActive(e telemetry.Emitter, active map[sessionSeries]map[string]struct{}) {
	series := make([]sessionSeries, 0, len(active))
	for key := range active {
		series = append(series, key)
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i].service != series[j].service {
			return series[i].service < series[j].service
		}
		return series[i].sessionType < series[j].sessionType
	})
	points := make([]telemetry.GaugePoint, 0, len(series))
	for _, key := range series {
		points = append(points, telemetry.GaugePoint{
			Value: float64(len(active[key])),
			Attrs: telemetry.Attrs{attrServiceName: key.service, attrSessionType: key.sessionType},
		})
	}
	e.GaugeSnapshot(docPAMSessionsActive.Name, docPAMSessionsActive.Unit, docPAMSessionsActive.Description, points)
}

func (c *SessionsCollector) loadState() {
	if cursor, ok := c.cursorStore.Get(sessionsCursorKey); ok {
		c.cursor = cursor
	}
	deletes := make([]string, 0)
	for _, key := range c.evidenceStore.Keys() {
		digest, ok := strings.CutPrefix(key, sessionEvidenceKeyPrefix)
		if !ok {
			continue
		}
		observedAt, exists := c.evidenceStore.Get(key)
		if !exists || !validSessionDigest(digest) {
			deletes = append(deletes, key)
			continue
		}
		c.seen[digest] = observedAt
	}
	for _, digest := range c.trimEvidence() {
		deletes = append(deletes, sessionEvidenceKeyPrefix+digest)
	}
	c.pendingEvidenceDeletes = sortedUnique(deletes)
}

func (c *SessionsCollector) retryEvidenceCleanup() error {
	if len(c.pendingEvidenceDeletes) == 0 {
		return nil
	}
	if err := collector.UpdateCheckpointBatch(c.evidenceStore, nil, c.pendingEvidenceDeletes); err != nil {
		return fmt.Errorf("pam sessions: clean startup evidence: %w", err)
	}
	c.pendingEvidenceDeletes = nil
	return nil
}

func (c *SessionsCollector) persistState(additions map[string]time.Time, cursor time.Time) error {
	staged := make(map[string]time.Time, len(c.seen)+len(additions))
	for digest, start := range c.seen {
		staged[digest] = start
	}
	for digest, start := range additions {
		staged[digest] = start
	}
	dropped := trimSessionEvidence(staged, c.capacity)
	updates := make(map[string]time.Time, len(additions))
	for digest := range additions {
		if retained, ok := staged[digest]; ok {
			updates[sessionEvidenceKeyPrefix+digest] = retained
		}
	}
	deletes := make([]string, 0, len(dropped))
	for _, digest := range dropped {
		deletes = append(deletes, sessionEvidenceKeyPrefix+digest)
	}
	// Evidence is persisted before the independent cursor. A crash between the
	// two operations can repeat a bounded request but cannot double-count it.
	if err := collector.UpdateCheckpointBatch(c.evidenceStore, updates, sortedUnique(deletes)); err != nil {
		return err
	}
	c.seen = staged
	if cursor.After(c.cursor) {
		if err := c.cursorStore.Set(sessionsCursorKey, cursor); err != nil {
			return err
		}
		c.cursor = cursor
	}
	return nil
}

func (c *SessionsCollector) trimEvidence() []string {
	return trimSessionEvidence(c.seen, c.capacity)
}

func trimSessionEvidence(seen map[string]time.Time, capacity int) []string {
	if len(seen) <= capacity {
		return nil
	}
	type entry struct {
		digest string
		start  time.Time
	}
	entries := make([]entry, 0, len(seen))
	for digest, start := range seen {
		entries = append(entries, entry{digest: digest, start: start})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].start.Equal(entries[j].start) {
			return entries[i].digest < entries[j].digest
		}
		return entries[i].start.After(entries[j].start)
	})
	dropped := make([]string, 0, len(entries)-capacity)
	for _, entry := range entries[capacity:] {
		delete(seen, entry.digest)
		dropped = append(dropped, entry.digest)
	}
	return dropped
}

func sessionDigest(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func sessionDigestFor(session b0api.Session) string {
	if session.SessionID != "" {
		return sessionDigest(session.SessionID)
	}
	body, err := json.Marshal(session)
	if err != nil {
		body = []byte(fmt.Sprintf("%#v", session))
	}
	return sessionDigest("missing:" + string(body))
}

func validSessionDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func sortedUnique(keys []string) []string {
	if len(keys) < 2 {
		return keys
	}
	sort.Strings(keys)
	out := keys[:0]
	for _, key := range keys {
		if len(out) == 0 || key != out[len(out)-1] {
			out = append(out, key)
		}
	}
	return out
}
