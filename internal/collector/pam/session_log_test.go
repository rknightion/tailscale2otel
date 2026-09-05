package pam

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestSessionLogEmitsNewSessionsOnceAcrossReplayAndRestart(t *testing.T) {
	var fixture b0api.SessionPage
	body, err := os.ReadFile(filepath.Join("..", "..", "b0api", "testdata", "pam_sessions.json"))
	if err != nil {
		t.Fatalf("read tracked PAM session fixture: %v", err)
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatalf("decode tracked PAM session fixture: %v", err)
	}
	if len(fixture.SessionLogs) < 2 {
		t.Fatal("tracked PAM session fixture must contain multiple sessions")
	}
	pages := sessionLogFixtureAPI(t, fixture.SessionLogs)
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

	first := telemetrytest.New()
	c := NewSessions(pages, 0, cursors, evidence, WithSessionLog(true, allSessionLogCategories(), enrich.DefaultAddrSet()))
	if err := c.Collect(context.Background(), first.Emitter()); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if got, want := len(sessionLogs(first)), len(fixture.SessionLogs); got != want {
		t.Fatalf("first session logs = %d, want %d", got, want)
	}

	replay := telemetrytest.New()
	if err := c.Collect(context.Background(), replay.Emitter()); err != nil {
		t.Fatalf("replay Collect: %v", err)
	}
	if got := len(sessionLogs(replay)); got != 0 {
		t.Fatalf("replay session logs = %d, want 0", got)
	}

	cursors, err = collector.NewFileStore(cursorPath)
	if err != nil {
		t.Fatalf("reopen cursor store: %v", err)
	}
	evidence, err = collector.NewFileStore(evidencePath)
	if err != nil {
		t.Fatalf("reopen evidence store: %v", err)
	}
	restarted := telemetrytest.New()
	if err := NewSessions(pages, 0, cursors, evidence, WithSessionLog(true, allSessionLogCategories(), enrich.DefaultAddrSet())).Collect(context.Background(), restarted.Emitter()); err != nil {
		t.Fatalf("restart Collect: %v", err)
	}
	if got := len(sessionLogs(restarted)); got != 0 {
		t.Fatalf("restart session logs = %d, want 0", got)
	}
}

func TestSessionLogPIICategoriesRemoveMappedFields(t *testing.T) {
	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		category pii.Category
		ip       string
		absent   []string
	}{
		{name: "email", category: pii.CatEmails, absent: []string{"user.name"}},
		{name: "display name", category: pii.CatUserDisplayNames, absent: []string{"user.full_name"}},
		{name: "SSH user", category: pii.CatUserIDs, absent: []string{"user.id"}},
		{name: "device name", category: pii.CatHostnames, absent: []string{"host.name"}},
		{name: "command", category: pii.CatCommandText, absent: []string{attrSessionCommand}},
		{name: "external address", category: pii.CatExternalIPs, ip: "192.0.2.10", absent: []string{attrSessionExternalIP, attrSessionClientPort}},
		{name: "tailnet address", category: pii.CatTailscaleIPs, ip: "100.100.0.10", absent: []string{attrSessionTailnetIP, attrSessionClientPort}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := completeSessionLogFixture(start)
			if tc.ip != "" {
				s.ClientIP = tc.ip
			}
			cats := allSessionLogCategories()
			cats[tc.category] = false
			rec := telemetrytest.New()
			if err := NewSessions(sessionLogSingleAPI(s), 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, cats, enrich.DefaultAddrSet())).Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			logs := sessionLogs(rec)
			if len(logs) != 1 {
				t.Fatalf("session logs = %d, want 1", len(logs))
			}
			for _, key := range tc.absent {
				if _, ok := logs[0].Attrs[key]; ok {
					t.Errorf("%s remained with %s disabled: %+v", key, tc.category, logs[0].Attrs)
				}
			}
		})
	}
}

func TestSessionLogUsesRuntimeAddrSetBeforeGenericIPClassification(t *testing.T) {
	s := completeSessionLogFixture(time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	s.ClientIP = "10.42.0.9"
	cats := allSessionLogCategories()
	cats[pii.CatInternalIPs] = false
	rec := telemetrytest.NewWithPII(cats)
	addrs := enrich.NewAddrSet(netip.MustParsePrefix("10.42.0.0/16"))
	if err := NewSessions(sessionLogSingleAPI(s), 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, cats, addrs)).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	logs := sessionLogs(rec)
	if len(logs) != 1 {
		t.Fatalf("session logs = %d, want 1", len(logs))
	}
	if got := logs[0].Attrs[attrSessionTailnetIP]; got != "10.42.0.9" {
		t.Errorf("tailnet address = %q, want custom AddrSet member", got)
	}
	if got := logs[0].Attrs[attrSessionClientPort]; got != "54321" {
		t.Errorf("client port = %q, want port retained with emitted tailnet address", got)
	}
}

func TestSessionLogSocketNameSurvivesAllPIICategoriesOff(t *testing.T) {
	s := completeSessionLogFixture(time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	cats := allSessionLogCategories()
	for category := range cats {
		cats[category] = false
	}
	rec := telemetrytest.NewWithPII(cats)
	if err := NewSessions(sessionLogSingleAPI(s), 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, cats, enrich.DefaultAddrSet())).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	logs := sessionLogs(rec)
	if len(logs) != 1 {
		t.Fatalf("session logs = %d, want 1", len(logs))
	}
	if got := logs[0].Attrs[attrSessionSocketName]; got != "bounded-service" {
		t.Errorf("socket name = %q, want bounded socket name retained", got)
	}
}

func TestSessionLogFailureBodyDoesNotClaimAuthorizationSucceeded(t *testing.T) {
	s := completeSessionLogFixture(time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	s.Result = "failed"
	rec := telemetrytest.New()
	if err := NewSessions(sessionLogSingleAPI(s), 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, allSessionLogCategories(), enrich.DefaultAddrSet())).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	logs := sessionLogs(rec)
	if len(logs) != 1 {
		t.Fatalf("session logs = %d, want 1", len(logs))
	}
	if !strings.Contains(logs[0].Body, "authorization") || strings.Contains(logs[0].Body, "authorized") || strings.Contains(logs[0].Body, "connected") {
		t.Errorf("failed session body = %q, want authorization result wording without a success or connection claim", logs[0].Body)
	}
}

func TestSessionLogIncompleteSessionUsesUnknownDuration(t *testing.T) {
	s := completeSessionLogFixture(time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	s.EndTime = nil
	rec := telemetrytest.New()
	if err := NewSessions(sessionLogSingleAPI(s), 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, allSessionLogCategories(), enrich.DefaultAddrSet())).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	logs := sessionLogs(rec)
	if len(logs) != 1 {
		t.Fatalf("session logs = %d, want 1", len(logs))
	}
	if got := logs[0].Attrs[attrSessionDuration]; got != "unknown" {
		t.Errorf("incomplete-session duration = %q, want unknown", got)
	}
	if !strings.Contains(logs[0].Body, "duration=unknown") {
		t.Errorf("incomplete-session body = %q, want unknown duration", logs[0].Body)
	}
}

func TestSessionLogUsesStrictAttributeAllowlist(t *testing.T) {
	s := completeSessionLogFixture(time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	s.AuthInfo = `{"allowed":["private authorization detail"]}`
	s.Events[0].Metadata = `{"command":"private command","username":"private-user","token":"private-token"}`
	rec := telemetrytest.New()
	if err := NewSessions(sessionLogSingleAPI(s), 0, collector.NewMemoryStore(), collector.NewMemoryStore(), WithSessionLog(true, allSessionLogCategories(), enrich.DefaultAddrSet())).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	logs := sessionLogs(rec)
	if len(logs) != 1 {
		t.Fatalf("session logs = %d, want 1", len(logs))
	}
	for key, want := range map[string]string{
		attrSessionSocketName:    "bounded-service",
		attrSessionType:          "ssh",
		attrAuthorizationResult:  "success",
		attrSessionKilled:        "true",
		attrSessionRecordingType: "asciinema",
		attrSessionDuration:      "90",
		"user.name":              "person@example.invalid",
		"user.full_name":         "Private Person",
		"user.id":                "private-ssh-user",
		attrSessionExternalIP:    "192.0.2.10",
		attrSessionClientPort:    "54321",
		"host.name":              "private-device",
		attrSessionCommand:       "private command",
	} {
		if got := logs[0].Attrs[key]; got != want {
			t.Errorf("log attribute %q = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(logs[0].Body, "authorized") || strings.Contains(logs[0].Body, "connected") {
		t.Errorf("session log body = %q, want authorization wording without connection-health claim", logs[0].Body)
	}
	allowed := map[string]struct{}{
		attrSessionSocketName: {}, attrSessionType: {}, attrAuthorizationResult: {}, attrSessionKilled: {},
		attrSessionRecordingType: {}, attrSessionDuration: {}, "user.name": {}, "user.full_name": {},
		"user.id": {}, attrSessionTailnetIP: {}, attrSessionExternalIP: {}, attrSessionClientPort: {}, "host.name": {}, attrSessionCommand: {},
	}
	for key, value := range logs[0].Attrs {
		if _, ok := allowed[key]; !ok {
			t.Errorf("log emitted non-allowlisted attribute %q", key)
		}
		for _, forbidden := range []string{"private authorization detail", "private-user", "private-token"} {
			if value == forbidden {
				t.Errorf("log attribute %q leaked %q", key, forbidden)
			}
		}
	}
	for _, forbidden := range []string{"private authorization detail", "private command", "private-user", "private-token"} {
		if strings.Contains(logs[0].Body, forbidden) {
			t.Errorf("log body leaked %q: %q", forbidden, logs[0].Body)
		}
	}
}

func allSessionLogCategories() pii.Categories {
	cats := pii.Categories{}
	for _, category := range pii.AllCategories {
		cats[category] = true
	}
	return cats
}

func sessionLogs(rec *telemetrytest.Recorder) []telemetrytest.LogRecord {
	var logs []telemetrytest.LogRecord
	for _, log := range rec.LogRecords() {
		if log.EventName == EventSession {
			logs = append(logs, log)
		}
	}
	return logs
}

func sessionLogFixtureAPI(t *testing.T, sessions []b0api.Session) sessionAPIFunc {
	t.Helper()
	split := len(sessions) / 2
	return func(_ context.Context, opts ...b0api.PageOptions) (b0api.SessionPage, error) {
		if len(opts) != 1 {
			t.Fatalf("page options = %+v, want one", opts)
		}
		switch opts[0].Page {
		case 1:
			return sessionPage(2, sessions[:split]...), nil
		case 2:
			return sessionPage(0, sessions[split:]...), nil
		default:
			t.Fatalf("unexpected page %d", opts[0].Page)
			return b0api.SessionPage{}, nil
		}
	}
}

func sessionLogSingleAPI(s b0api.Session) sessionAPIFunc {
	return func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, s), nil
	}
}

func completeSessionLogFixture(start time.Time) b0api.Session {
	end := start.Add(90 * time.Second)
	sshUser := "private-ssh-user"
	return b0api.Session{
		SessionID: "private-session", SocketName: "bounded-service", StartTime: start, EndTime: &end,
		SessionType: "ssh", Result: "success", Killed: true, UserEmail: "person@example.invalid",
		Name: "Private Person", SSHUser: &sshUser, ClientIP: "192.0.2.10", ClientPort: json.RawMessage(`54321`),
		Metadata:   b0api.SessionMetadata{Device: b0api.SessionDevice{Name: "private-device"}},
		Recordings: []b0api.Recording{{RecordingType: "asciinema"}},
		Events:     []b0api.SessionEvent{{Type: "ssh_exec", Status: "success", Metadata: `{"command":"private command"}`}},
	}
}
