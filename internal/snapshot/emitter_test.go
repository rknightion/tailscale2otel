package snapshot_test

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rknightion/tailscale2otel/v5/internal/snapshot"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestEmitterEmitsOnChangeAndHeartbeatOnly(t *testing.T) {
	rec := telemetrytest.New()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	emitter, err := snapshot.New(snapshot.Config{
		Emitter:      rec.Emitter(),
		EventName:    "tailscale.config.snapshot",
		Kind:         snapshot.KindPolicy,
		Heartbeat:    24 * time.Hour,
		MaxBodyBytes: 32 * 1024,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !emitter.Observe(now, "v1", "policy v1", telemetry.Attrs{"source": "acl"}) {
		t.Fatal("first observation did not emit the change snapshot")
	}
	if emitter.Observe(now.Add(time.Minute), "v1", "policy v1", nil) {
		t.Fatal("unchanged observation emitted before the heartbeat interval")
	}
	if !emitter.Observe(now.Add(24*time.Hour), "v1", "policy v1", nil) {
		t.Fatal("unchanged observation did not emit at the heartbeat interval")
	}

	logs := rec.LogRecords()
	if len(logs) != 2 {
		t.Fatalf("log records = %d, want 2", len(logs))
	}
	assertSnapshotAttrs(t, logs[0].Attrs, "policy", "change", "v1", len("policy v1"), 1, 1)
	assertSnapshotAttrs(t, logs[1].Attrs, "policy", "heartbeat", "v1", len("policy v1"), 1, 1)
	if logs[0].Attrs["tailscale.snapshot.emission_id"] == logs[1].Attrs["tailscale.snapshot.emission_id"] {
		t.Fatal("change and heartbeat records reused one emission identifier")
	}
	if got := logs[0].Attrs["source"]; got != "acl" {
		t.Fatalf("source attribute = %q, want acl", got)
	}
}

func TestEmitterHonoursSeededState(t *testing.T) {
	rec := telemetrytest.New()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	emitter, err := snapshot.New(snapshot.Config{
		Emitter:         rec.Emitter(),
		EventName:       "tailscale.config.snapshot",
		Kind:            snapshot.KindDNS,
		Heartbeat:       time.Hour,
		MaxBodyBytes:    32 * 1024,
		InitialRevision: "persisted",
		InitialEmission: now,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if emitter.Observe(now.Add(time.Minute), "persisted", `{}`, nil) {
		t.Fatal("seeded current revision emitted a restart snapshot before heartbeat")
	}
	if !emitter.Observe(now.Add(2*time.Minute), "changed", `{"magicDNS":true}`, nil) {
		t.Fatal("changed revision did not emit")
	}
}

func TestEmitterChunksWithinConfiguredLimitWithoutSplittingUTF8(t *testing.T) {
	rec := telemetrytest.New()
	body := strings.Repeat("é", 70)
	emitter, err := snapshot.New(snapshot.Config{
		Emitter:      rec.Emitter(),
		EventName:    "tailscale.config.snapshot",
		Kind:         snapshot.KindSettings,
		Heartbeat:    time.Hour,
		MaxBodyBytes: 64,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !emitter.Observe(time.Unix(1, 0), "", body, telemetry.Attrs{
		"tailscale.snapshot.kind": "caller-cannot-override",
	}) {
		t.Fatal("observation did not emit")
	}
	logs := rec.LogRecords()
	if len(logs) < 2 {
		t.Fatalf("log records = %d, want chunked output", len(logs))
	}

	var rebuilt strings.Builder
	emissionID := logs[0].Attrs["tailscale.snapshot.emission_id"]
	for i, log := range logs {
		if !utf8.ValidString(log.Body) {
			t.Fatalf("chunk %d is not valid UTF-8", i+1)
		}
		if got, max := len(log.Body), snapshot.SafeBodyBytes(64); got > max {
			t.Fatalf("chunk %d bytes = %d, want <= %d", i+1, got, max)
		}
		assertSnapshotAttrs(t, log.Attrs, "settings", "change", log.Attrs["tailscale.snapshot.revision"], len(body), i+1, len(logs))
		if got := log.Attrs["tailscale.snapshot.emission_id"]; got != emissionID {
			t.Fatalf("chunk %d emission identifier = %q, want %q", i+1, got, emissionID)
		}
		rebuilt.WriteString(log.Body)
	}
	if got := rebuilt.String(); got != body {
		t.Fatalf("rebuilt body differs: got %d bytes, want %d", len(got), len(body))
	}
	if revision := logs[0].Attrs["tailscale.snapshot.revision"]; len(revision) != 64 {
		t.Fatalf("content-hash revision length = %d, want 64", len(revision))
	}
}

func TestEmitterRejectsChunkBudgetsSmallerThanUTF8Rune(t *testing.T) {
	for _, limit := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			_, err := snapshot.New(snapshot.Config{
				Emitter:      telemetrytest.New().Emitter(),
				EventName:    "tailscale.config.snapshot",
				Kind:         snapshot.KindPolicy,
				MaxBodyBytes: limit,
			})
			if err == nil {
				t.Fatalf("New(MaxBodyBytes=%d) error = nil, want rejection before a four-byte rune can overrun the limit", limit)
			}
		})
	}
}

func assertSnapshotAttrs(t *testing.T, attrs map[string]string, kind, reason, revision string, bytes, seq, total int) {
	t.Helper()
	want := map[string]string{
		"tailscale.snapshot.kind":     kind,
		"tailscale.snapshot.reason":   reason,
		"tailscale.snapshot.revision": revision,
		"tailscale.snapshot.bytes":    strconv.Itoa(bytes),
		"tailscale.snapshot.seq":      strconv.Itoa(seq),
		"tailscale.snapshot.total":    strconv.Itoa(total),
	}
	for key, value := range want {
		if got := attrs[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if attrs["tailscale.snapshot.emission_id"] == "" {
		t.Error("tailscale.snapshot.emission_id is empty")
	}
}
