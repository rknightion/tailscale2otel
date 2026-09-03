package acl_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestPolicySnapshotAndDiffAreChangeDrivenAndPersisted(t *testing.T) {
	clock := time.Unix(1_000_000, 0).UTC()
	now := func() time.Time { return clock }
	state := aclpolicy.NewMemorySnapshotStateStore()
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: "{\"acls\":[{\"action\":\"accept\",\"src\":[\"group:alpha\"],\"dst\":[\"tag:app:443\"]}]}", ETag: "rev-one"}}

	first := acl.New(api, 0, now, acl.WithPolicySnapshots(24*time.Hour, 512, state))
	rec1 := telemetrytest.New()
	if err := first.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if got := logsNamed(rec1, acl.EventPolicySnapshot); len(got) != 1 {
		t.Fatalf("first snapshot logs = %d, want 1", len(got))
	} else if got[0].Attrs["tailscale.snapshot.revision"] != "rev-one" {
		t.Errorf("first snapshot revision = %q, want rev-one", got[0].Attrs["tailscale.snapshot.revision"])
	}
	if got := logsNamed(rec1, acl.EventPolicyDiff); len(got) != 0 {
		t.Fatalf("first diff logs = %d, want 0 (no prior policy)", len(got))
	}

	clock = clock.Add(time.Hour)
	rec2 := telemetrytest.New()
	if err := first.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("unchanged Collect: %v", err)
	}
	if got := logsNamed(rec2, acl.EventPolicySnapshot); len(got) != 0 {
		t.Fatalf("unchanged snapshot logs = %d, want 0", len(got))
	}
	if got := logsNamed(rec2, acl.EventPolicyDiff); len(got) != 0 {
		t.Fatalf("unchanged diff logs = %d, want 0", len(got))
	}

	// A new collector models a process restart. Its persisted baseline must
	// suppress an unchanged snapshot and retain the old body for the next diff.
	restarted := acl.New(api, 0, now, acl.WithPolicySnapshots(24*time.Hour, 512, state))
	api.acl = &tsclient.RawACL{HuJSON: "{\"acls\":[{\"action\":\"accept\",\"src\":[\"group:alpha\"],\"dst\":[\"tag:app:8443\"]}]}", ETag: "rev-two"}
	clock = clock.Add(time.Hour)
	rec3 := telemetrytest.New()
	if err := restarted.Collect(context.Background(), rec3.Emitter()); err != nil {
		t.Fatalf("changed Collect after restart: %v", err)
	}
	if got := logsNamed(rec3, acl.EventPolicySnapshot); len(got) != 1 {
		t.Fatalf("changed snapshot logs = %d, want 1", len(got))
	}
	diffs := logsNamed(rec3, acl.EventPolicyDiff)
	if len(diffs) != 1 {
		t.Fatalf("changed diff logs = %d, want 1", len(diffs))
	}
	if body := diffs[0].Body; !strings.Contains(body, "--- previous policy") || !strings.Contains(body, "+{\"acls\"") || !strings.Contains(body, "-{\"acls\"") || !strings.Contains(body, "tag:app:8443") || !strings.Contains(body, "tag:app:443") {
		t.Errorf("diff body = %q, want unified old/new policy lines", body)
	}
}

func TestFileSnapshotStateStoreSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy-state.json")
	first := aclpolicy.NewFileSnapshotStateStore(path)
	want := aclpolicy.SnapshotState{Revision: "rev-one", Emitted: time.Unix(1_000_000, 0).UTC(), Body: "{\"acls\":[]}"}
	if err := first.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := aclpolicy.NewFileSnapshotStateStore(path).Load()
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	if got != want {
		t.Fatalf("persisted state = %#v, want %#v", got, want)
	}
}

func TestPolicySnapshotsChunkUnderOneEmissionID(t *testing.T) {
	clock := time.Unix(1_000_000, 0).UTC()
	body := "{\"acls\":[\"" + strings.Repeat("é", 100) + "\"]}"
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: body, ETag: "rev-one"}}
	c := acl.New(api, 0, func() time.Time { return clock }, acl.WithPolicySnapshots(24*time.Hour, 64, aclpolicy.NewMemorySnapshotStateStore()))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	logs := logsNamed(rec, acl.EventPolicySnapshot)
	if len(logs) < 2 {
		t.Fatalf("snapshot chunks = %d, want >1", len(logs))
	}
	id := logs[0].Attrs["tailscale.snapshot.emission_id"]
	for index, log := range logs {
		if log.Attrs["tailscale.snapshot.emission_id"] != id {
			t.Fatalf("chunk %d emission id = %q, want %q", index, log.Attrs["tailscale.snapshot.emission_id"], id)
		}
		if log.Attrs["tailscale.snapshot.revision"] != "rev-one" {
			t.Fatalf("chunk %d revision = %q, want rev-one", index, log.Attrs["tailscale.snapshot.revision"])
		}
	}
}

func TestPolicyDiffsChunkWithinConfiguredLogLimit(t *testing.T) {
	clock := time.Unix(1_000_000, 0).UTC()
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":["old"]}`, ETag: "rev-one"}}
	c := acl.New(api, 0, func() time.Time { return clock }, acl.WithPolicySnapshots(24*time.Hour, 64, aclpolicy.NewMemorySnapshotStateStore()))
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("initial Collect: %v", err)
	}

	api.acl = &tsclient.RawACL{HuJSON: `{"acls":["` + strings.Repeat("é", 100) + `"]}`, ETag: "rev-two"}
	clock = clock.Add(time.Hour)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("changed Collect: %v", err)
	}
	logs := logsNamed(rec, acl.EventPolicyDiff)
	if len(logs) < 2 {
		t.Fatalf("diff chunks = %d, want >1", len(logs))
	}
	id := logs[0].Attrs["tailscale.snapshot.emission_id"]
	for index, log := range logs {
		if got, max := len(log.Body), 64; got > max {
			t.Fatalf("diff chunk %d bytes = %d, want <= configured limit %d", index+1, got, max)
		}
		if log.Attrs["tailscale.snapshot.emission_id"] != id {
			t.Fatalf("diff chunk %d emission id = %q, want %q", index+1, log.Attrs["tailscale.snapshot.emission_id"], id)
		}
		if log.Attrs["tailscale.snapshot.kind"] != "policy" {
			t.Fatalf("diff chunk %d snapshot kind = %q, want policy", index+1, log.Attrs["tailscale.snapshot.kind"])
		}
	}
}

func TestPolicySnapshotsAreOffWithoutTheExplicitOption(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "rev-one"}}
	c := acl.New(api, 0, time.Now)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := logsNamed(rec, acl.EventPolicySnapshot); len(got) != 0 {
		t.Fatalf("default snapshot logs = %d, want 0", len(got))
	}
	if got := logsNamed(rec, acl.EventPolicyDiff); len(got) != 0 {
		t.Fatalf("default diff logs = %d, want 0", len(got))
	}
}

func logsNamed(rec *telemetrytest.Recorder, name string) []telemetrytest.LogRecord {
	var logs []telemetrytest.LogRecord
	for _, log := range rec.LogRecords() {
		if log.EventName == name {
			logs = append(logs, log)
		}
	}
	return logs
}
