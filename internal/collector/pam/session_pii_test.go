package pam

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestSessionMetricAttributesUseStrictPIIAllowlist(t *testing.T) {
	start := time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	s := session("private-session-id", start, "ssh")
	s.EndTime = &end
	s.UserEmail = "person@example.invalid"
	s.Name = "Private Person"
	s.Picture = "https://example.invalid/private.png"
	s.Subject = "private-subject"
	s.Nickname = "private-nickname"
	s.ClientIP = "192.0.2.99"
	s.ClientPort = json.RawMessage(`54321`)
	sshUser := "private-user"
	s.SSHUser = &sshUser
	s.ServerName = "private-host"
	s.ServerPort = "22"
	s.CountryCode = "private-country"
	s.CountryFlag = "private-flag"
	s.AuthInfo = `{"allowed":["private authorization detail"]}`
	s.Metadata.Device.IP = "192.0.2.100"
	s.Metadata.Device.Name = "private-device"
	s.Events = []b0api.SessionEvent{{
		Type: "ssh_exec", Status: "success",
		Metadata: `{"command":"private command","username":"private-user"}`,
	}}
	api := sessionAPIFunc(func(context.Context, ...b0api.PageOptions) (b0api.SessionPage, error) {
		return sessionPage(0, s), nil
	})
	rec := telemetrytest.New()
	if err := NewSessions(api, 0, collector.NewMemoryStore(), collector.NewMemoryStore()).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]struct{}{
		attrServiceName:         {},
		attrSessionType:         {},
		attrAuthorizationResult: {},
		attrSessionEventType:    {},
		attrSessionEventStatus:  {},
	}
	for _, name := range rec.MetricNames() {
		if !strings.HasPrefix(name, "tailscale.pam.") {
			continue
		}
		for _, point := range rec.MetricPoints(name) {
			for key, value := range point.Attrs {
				if _, ok := allowed[key]; !ok {
					t.Errorf("metric %q emitted non-allowlisted attribute %q", name, key)
				}
				for _, forbidden := range []string{
					"person@example.invalid", "Private Person", "private.png", "private-subject",
					"private-nickname", "192.0.2.99", "54321", "private-user", "private-host",
					"22", "private-country", "private-flag", "private authorization detail",
					"192.0.2.100", "private-device", "private command", "private-session-id",
				} {
					if strings.Contains(value, forbidden) {
						t.Errorf("metric %q attribute %q leaks forbidden value %q", name, key, forbidden)
					}
				}
			}
		}
	}
	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("session collector emitted %d opt-in logs without an explicit log option", len(logs))
	}
}
