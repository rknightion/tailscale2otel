package acl_test

import (
	"context"
	"os"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeAPI implements the narrow acl api interface for tests. It is the single
// shared fake for the acl_test package (used by acl_test.go, risk_test.go, and
// catalog_test.go).
type fakeAPI struct {
	acl   *tsclient.RawACL
	err   error
	calls int
}

func (f *fakeAPI) PolicyFileRaw(_ context.Context) (*tsclient.RawACL, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.acl, nil
}

// SnapshotCollector compile-time check.
var _ collector.SnapshotCollector = (*acl.Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := acl.New(&fakeAPI{}, 0, nil)
	if c.Name() != "acl" {
		t.Fatalf("Name() = %q, want acl", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
	c2 := acl.New(&fakeAPI{}, 5*time.Minute, nil)
	if got := c2.DefaultInterval(); got != 5*time.Minute {
		t.Fatalf("DefaultInterval() = %v, want 5m", got)
	}
}

// lastChanged returns the single tailscale.acl.last_changed gauge value.
func lastChanged(t *testing.T, rec *telemetrytest.Recorder) float64 {
	t.Helper()
	pts := rec.MetricPoints("tailscale.acl.last_changed")
	if len(pts) != 1 {
		t.Fatalf("last_changed points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("last_changed kind = %q, want gauge", p.Kind)
	}
	if p.Unit != "s" {
		t.Fatalf("last_changed unit = %q, want s", p.Unit)
	}
	return p.Value
}

func TestLastChangedTracksETag(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "etag-1"}}

	// Deterministic, advancing clock.
	clock := time.Unix(1_000_000, 0).UTC()
	now := func() time.Time { return clock }

	c := acl.New(api, 0, now)

	// First observation: record etag, last_changed = now().
	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("Collect#1: %v", err)
	}
	first := lastChanged(t, rec1)
	if first != float64(clock.Unix()) {
		t.Fatalf("first last_changed = %v, want %v", first, clock.Unix())
	}

	// Second poll, SAME etag, later wall clock: value must NOT change.
	clock = clock.Add(time.Hour)
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect#2: %v", err)
	}
	if got := lastChanged(t, rec2); got != first {
		t.Fatalf("unchanged etag changed last_changed: got %v, want %v", got, first)
	}

	// Third poll, CHANGED etag at a new wall clock: value updates to now().
	clock = clock.Add(time.Hour)
	api.acl = &tsclient.RawACL{HuJSON: `{"acls":[{"action":"accept"}]}`, ETag: "etag-2"}
	rec3 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec3.Emitter()); err != nil {
		t.Fatalf("Collect#3: %v", err)
	}
	if got := lastChanged(t, rec3); got != float64(clock.Unix()) {
		t.Fatalf("changed etag last_changed = %v, want %v", got, clock.Unix())
	}
}

func TestDefaultNowWhenNil(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: "{}", ETag: "e"}}
	c := acl.New(api, 0, nil)
	before := time.Now().Add(-time.Second).Unix()
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	after := time.Now().Add(time.Second).Unix()
	got := lastChanged(t, rec)
	if got < float64(before) || got > float64(after) {
		t.Fatalf("last_changed = %v, want within [%d,%d]", got, before, after)
	}
}

func TestCollectPropagatesError(t *testing.T) {
	api := &fakeAPI{err: context.DeadlineExceeded}
	rec := telemetrytest.New()
	if err := acl.New(api, 0, time.Now).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect: expected error, got nil")
	}
}

// ruleCounts indexes tailscale.acl.rules points by their section attribute.
func ruleCounts(t *testing.T, rec *telemetrytest.Recorder) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.acl.rules") {
		if p.Kind != "gauge" {
			t.Fatalf("rules kind = %q, want gauge", p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("rules unit = %q, want 1", p.Unit)
		}
		sec := p.Attrs["tailscale.acl.section"]
		if sec == "" {
			t.Fatalf("rules point missing tailscale.acl.section attr: %+v", p.Attrs)
		}
		if _, dup := out[sec]; dup {
			t.Fatalf("duplicate rules point for section %q", sec)
		}
		out[sec] = p.Value
	}
	return out
}

// size returns the single tailscale.acl.size gauge value.
func size(t *testing.T, rec *telemetrytest.Recorder) float64 {
	t.Helper()
	pts := rec.MetricPoints("tailscale.acl.size")
	if len(pts) != 1 {
		t.Fatalf("size points = %d, want 1", len(pts))
	}
	if pts[0].Unit != "By" {
		t.Fatalf("size unit = %q, want By", pts[0].Unit)
	}
	return pts[0].Value
}

func TestRuleCountsFromRealACL(t *testing.T) {
	huJSON, err := os.ReadFile("../../../.capture/acl.json")
	if err != nil {
		t.Skipf("real ACL fixture unavailable: %v", err)
	}

	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: string(huJSON), ETag: "etag-real"}}
	c := acl.New(api, 0, func() time.Time { return time.Unix(1_000_000, 0).UTC() })

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// size + last_changed must still be present.
	if got := size(t, rec); got != float64(len(huJSON)) {
		t.Fatalf("size = %v, want %d", got, len(huJSON))
	}
	if got := lastChanged(t, rec); got != float64(time.Unix(1_000_000, 0).Unix()) {
		t.Fatalf("last_changed = %v, want %v", got, 1_000_000)
	}

	got := ruleCounts(t, rec)
	// Counts taken directly from .capture/acl.json: array sections use the
	// element count, object sections use the key count.
	want := map[string]float64{
		"acls":          4,
		"grants":        14,
		"ssh":           1,
		"postures":      0,
		"autoApprovers": 3,
		"tagOwners":     11,
		"hosts":         1,
		"groups":        0,
		"nodeAttrs":     2,
	}
	for sec, w := range want {
		g, ok := got[sec]
		if !ok {
			t.Fatalf("missing rules point for section %q", sec)
		}
		if g != w {
			t.Fatalf("section %q count = %v, want %v", sec, g, w)
		}
	}

	// "tests" is absent from the fixture, so no point should be emitted for it.
	if _, ok := got["tests"]; ok {
		t.Fatalf("unexpected rules point for absent section %q", "tests")
	}
	// "ipsets" / "randomizeClientPort" are not recognized sections.
	if _, ok := got["ipsets"]; ok {
		t.Fatal("unexpected rules point for non-recognized section ipsets")
	}
	if _, ok := got["randomizeClientPort"]; ok {
		t.Fatal("unexpected rules point for scalar randomizeClientPort")
	}

	// --- B1 risk scores from the real fixture ---
	// grants: src wildcards on grants #2,#8 = 2; dst wildcards on #4,#6,#7,#9,#10 = 5.
	wild := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.acl.wildcard_rules") {
		wild[p.Attrs["tailscale.acl.section"]+"/"+p.Attrs["tailscale.acl.position"]] = p.Value
	}
	if wild["grants/src"] != 2 || wild["grants/dst"] != 5 {
		t.Fatalf("grants wildcard src/dst = %v/%v, want 2/5", wild["grants/src"], wild["grants/dst"])
	}
	if wild["acls/src"] != 0 || wild["acls/dst"] != 0 {
		t.Fatalf("acls wildcard src/dst = %v/%v, want 0/0", wild["acls/src"], wild["acls/dst"])
	}

	unr := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.acl.unrestricted_rules") {
		unr[p.Attrs["tailscale.acl.section"]] = p.Value
	}
	if unr["acls"] != 0 || unr["grants"] != 0 {
		t.Fatalf("unrestricted acls/grants = %v/%v, want 0/0", unr["acls"], unr["grants"])
	}

	aa := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.acl.autoapprovers") {
		aa[p.Attrs["tailscale.acl.autoapprover_kind"]] = p.Value
	}
	if aa["routes"] != 7 || aa["exit_node"] != 2 || aa["services"] != 1 {
		t.Fatalf("autoapprovers routes/exit_node/services = %v/%v/%v, want 7/2/1", aa["routes"], aa["exit_node"], aa["services"])
	}

	sshPts := rec.MetricPoints("tailscale.acl.ssh_wildcard")
	if len(sshPts) != 1 || sshPts[0].Value != 0 {
		t.Fatalf("ssh_wildcard = %v (n=%d), want 0 (n=1)", sshPts, len(sshPts))
	}
}

func TestMalformedHuJSONStillEmitsSizeAndLastChanged(t *testing.T) {
	// Unterminated object: not valid HuJSON, so Standardize must fail.
	bad := `{"acls": [`
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: bad, ETag: "etag-bad"}}
	c := acl.New(api, 0, func() time.Time { return time.Unix(2_000_000, 0).UTC() })

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect must not error on malformed HuJSON: %v", err)
	}

	if got := size(t, rec); got != float64(len(bad)) {
		t.Fatalf("size = %v, want %d", got, len(bad))
	}
	if got := lastChanged(t, rec); got != float64(time.Unix(2_000_000, 0).Unix()) {
		t.Fatalf("last_changed = %v, want %v", got, 2_000_000)
	}
	if pts := rec.MetricPoints("tailscale.acl.rules"); len(pts) != 0 {
		t.Fatalf("rules points on malformed HuJSON = %d, want 0", len(pts))
	}
}
