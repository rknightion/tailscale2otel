package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v4/internal/collector/users"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// fakeLister returns a canned slice of users and user invites (or an error).
type fakeLister struct {
	users       []tsclient.User
	invites     []tsapi.UserInvite
	err         error
	invitesErr  error
	calls       int
	inviteCalls int
}

func (f *fakeLister) Users(context.Context) ([]tsclient.User, error) {
	f.calls++
	return f.users, f.err
}

func (f *fakeLister) UserInvites(context.Context) ([]tsapi.UserInvite, error) {
	f.inviteCalls++
	return f.invites, f.invitesErr
}

// findPoint returns the first MetricPoint whose attrs match every key/value in
// want, or fails the test.
func findPoint(t *testing.T, pts []telemetrytest.MetricPoint, want map[string]string) telemetrytest.MetricPoint {
	t.Helper()
outer:
	for _, p := range pts {
		for k, v := range want {
			if p.Attrs[k] != v {
				continue outer
			}
		}
		return p
	}
	t.Fatalf("no metric point matching %v in %+v", want, pts)
	return telemetrytest.MetricPoint{}
}

// fixedNow is the deterministic clock used by age-histogram tests
// (users.WithClock). Fixed rather than time.Now() so bucket-boundary math is
// reproducible.
var fixedNow = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

func sampleUsers() []tsclient.User {
	return []tsclient.User{
		{
			ID:                 "u1",
			LoginName:          "alice@example.com",
			Role:               tsclient.UserRoleOwner,
			Status:             tsclient.UserStatusActive,
			Type:               tsclient.UserTypeMember,
			DeviceCount:        3,
			CurrentlyConnected: true,
			LastSeen:           time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
			Created:            fixedNow.Add(-400 * 24 * time.Hour), // ~400 days old
		},
		{
			ID:                 "u2",
			LoginName:          "bob@example.com",
			Role:               tsclient.UserRoleMember,
			Status:             tsclient.UserStatusActive,
			Type:               tsclient.UserTypeMember,
			DeviceCount:        1,
			CurrentlyConnected: false,
			LastSeen:           time.Time{}, // zero -> skipped for last_seen
			Created:            time.Time{}, // zero -> skipped for users.age (unknown provider value)
		},
		{
			// Same role/status/type combo as bob => aggregated into the same count point.
			ID:                 "u3",
			LoginName:          "carol@example.com",
			Role:               tsclient.UserRoleMember,
			Status:             tsclient.UserStatusActive,
			Type:               tsclient.UserTypeMember,
			DeviceCount:        0,
			CurrentlyConnected: true,
			LastSeen:           time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
			Created:            fixedNow.Add(-5 * 24 * time.Hour), // 5 days old
		},
	}
}

func TestName(t *testing.T) {
	c := users.New(&fakeLister{}, 0)
	if c.Name() != "users" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "users")
	}
}

func TestDefaultInterval(t *testing.T) {
	if got := users.New(&fakeLister{}, 0).DefaultInterval(); got != 300*time.Second {
		t.Fatalf("DefaultInterval(0) = %v, want 300s", got)
	}
	if got := users.New(&fakeLister{}, 45*time.Second).DefaultInterval(); got != 45*time.Second {
		t.Fatalf("DefaultInterval(45s) = %v, want 45s", got)
	}
}

func TestCollect_PerEntityFalse(t *testing.T) {
	// WithPerEntity(false) suppresses the per-user gauges while keeping the
	// aggregate users.count and user_invites.count rollups.
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0, users.WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, name := range []string{
		"tailscale.user.devices",
		"tailscale.user.connected",
		"tailscale.user.last_seen",
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("per-user gauge %q emitted with WithPerEntity(false): %+v", name, pts)
		}
	}

	if pts := rec.MetricPoints("tailscale.users.count"); len(pts) == 0 {
		t.Error("aggregate tailscale.users.count not emitted with WithPerEntity(false)")
	}
	if pts := rec.MetricPoints("tailscale.user_invites.count"); len(pts) == 0 {
		t.Error("aggregate tailscale.user_invites.count not emitted with WithPerEntity(false)")
	}
}

// TestCollect_ActivityDataFalse guards issue #64 sub-item 3: when the
// control-plane doesn't report per-user device-count/connection-state (e.g.
// Headscale), WithActivityData(false) must suppress tailscale.user.devices and
// tailscale.user.connected entirely rather than reporting a fabricated 0/false.
// last_seen and the aggregate counts are unaffected.
func TestCollect_ActivityDataFalse(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0, users.WithActivityData(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, name := range []string{"tailscale.user.devices", "tailscale.user.connected"} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("gauge %q emitted with WithActivityData(false): %+v", name, pts)
		}
	}
	if pts := rec.MetricPoints("tailscale.user.last_seen"); len(pts) == 0 {
		t.Error("last_seen should still be emitted with WithActivityData(false)")
	}
	if pts := rec.MetricPoints("tailscale.users.count"); len(pts) == 0 {
		t.Error("users.count should still be emitted with WithActivityData(false)")
	}
}

// TestCollect_ActivityDataDefaultTrue is the control: with no option supplied,
// behavior must be unchanged from before issue #64 (both gauges emitted).
func TestCollect_ActivityDataDefaultTrue(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range []string{"tailscale.user.devices", "tailscale.user.connected"} {
		if pts := rec.MetricPoints(name); len(pts) == 0 {
			t.Errorf("gauge %q should be emitted by default (WithActivityData unset)", name)
		}
	}
}

func TestCollect_CountByCombo(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.users.count")
	// Two distinct combos: (owner/active/member)=1 and (member/active/member)=2.
	if len(pts) != 2 {
		t.Fatalf("count points = %d, want 2 (%+v)", len(pts), pts)
	}
	for _, p := range pts {
		if p.Kind != "gauge" {
			t.Fatalf("count kind = %q, want gauge", p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("count unit = %q, want 1", p.Unit)
		}
	}
	owner := findPoint(t, pts, map[string]string{
		"tailscale.user.role":   "owner",
		"tailscale.user.status": "active",
		"tailscale.user.type":   "member",
	})
	if owner.Value != 1 {
		t.Fatalf("owner combo count = %v, want 1", owner.Value)
	}
	member := findPoint(t, pts, map[string]string{
		"tailscale.user.role":   "member",
		"tailscale.user.status": "active",
		"tailscale.user.type":   "member",
	})
	if member.Value != 2 {
		t.Fatalf("member combo count = %v, want 2", member.Value)
	}
}

func TestCollect_PerUserDevices(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.user.devices")
	if len(pts) != 3 {
		t.Fatalf("devices points = %d, want 3 (%+v)", len(pts), pts)
	}
	alice := findPoint(t, pts, map[string]string{
		"user.id":   "u1",
		"user.name": "alice@example.com",
	})
	if alice.Value != 3 {
		t.Fatalf("alice devices = %v, want 3", alice.Value)
	}
	if alice.Kind != "gauge" || alice.Unit != "1" {
		t.Fatalf("devices kind/unit = %q/%q, want gauge/1", alice.Kind, alice.Unit)
	}
}

func TestCollect_PerUserConnected(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.user.connected")
	if len(pts) != 3 {
		t.Fatalf("connected points = %d, want 3 (%+v)", len(pts), pts)
	}
	alice := findPoint(t, pts, map[string]string{"user.id": "u1"})
	if alice.Value != 1 {
		t.Fatalf("alice connected = %v, want 1", alice.Value)
	}
	bob := findPoint(t, pts, map[string]string{"user.id": "u2"})
	if bob.Value != 0 {
		t.Fatalf("bob connected = %v, want 0", bob.Value)
	}
	if alice.Unit != "1" {
		t.Fatalf("connected unit = %q, want 1", alice.Unit)
	}
}

func TestCollect_PerUserLastSeen(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.user.last_seen")
	// bob has zero LastSeen => skipped, so only alice + carol.
	if len(pts) != 2 {
		t.Fatalf("last_seen points = %d, want 2 (%+v)", len(pts), pts)
	}
	alice := findPoint(t, pts, map[string]string{"user.id": "u1"})
	want := float64(time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC).Unix())
	if alice.Value != want {
		t.Fatalf("alice last_seen = %v, want %v", alice.Value, want)
	}
	if alice.Unit != "s" {
		t.Fatalf("last_seen unit = %q, want s", alice.Unit)
	}
	for _, p := range pts {
		if p.Attrs["user.id"] == "u2" {
			t.Fatalf("bob (zero LastSeen) should be skipped, got %+v", p)
		}
	}
}

// TestCollect_UsersAge covers #426: a bounded fleet-level age distribution
// from tsclient.User.Created. alice (~400d old) and carol (5d old) both have
// a Created time and merge into the one nil-attr histogram series; bob has a
// zero Created (the provider didn't report it) and must be entirely omitted
// rather than recorded as age zero.
func TestCollect_UsersAge(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers()}, 0,
		users.WithClock(func() time.Time { return fixedNow }))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.users.age")
	if len(pts) != 1 {
		t.Fatalf("users.age points = %d, want 1 (%+v)", len(pts), pts)
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Fatalf("users.age kind = %q, want histogram", p.Kind)
	}
	if p.Unit != "s" {
		t.Fatalf("users.age unit = %q, want s", p.Unit)
	}
	if p.Count != 2 {
		t.Fatalf("users.age count = %d, want 2 (bob's zero Created must be omitted)", p.Count)
	}
	wantSum := float64(400*24*time.Hour/time.Second) + float64(5*24*time.Hour/time.Second)
	if p.Value != wantSum {
		t.Fatalf("users.age sum = %v, want %v", p.Value, wantSum)
	}
}

// TestCollect_UsersAge_AllZeroCreatedEmitsNoSeries is the extreme case of the
// above: when every user has a zero Created (e.g. a control plane that
// doesn't report it), the histogram must not appear at all.
func TestCollect_UsersAge_AllZeroCreatedEmitsNoSeries(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: []tsclient.User{
		{ID: "u1", LoginName: "alice@example.com", Role: tsclient.UserRoleMember},
	}}, 0, users.WithClock(func() time.Time { return fixedNow }))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if pts := rec.MetricPoints("tailscale.users.age"); len(pts) != 0 {
		t.Fatalf("users.age points = %d, want 0 (%+v)", len(pts), pts)
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	rec := telemetrytest.New()
	wantErr := errors.New("boom")
	c := users.New(&fakeLister{err: wantErr}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); !errors.Is(err, wantErr) {
		t.Fatalf("Collect err = %v, want %v", err, wantErr)
	}
}

// sampleInvites has three manual-link invites (no lastEmailSentAt) and one
// emailed invite (i3, sent 3 days before fixedNow) — mirroring the shape of
// the old accepted-based fixture (2/1/1 split) but grouped by delivery
// method instead of the fabricated accepted flag (#411/#412).
func sampleInvites() []tsapi.UserInvite {
	return []tsapi.UserInvite{
		{ID: "i1", Role: "member"},
		{ID: "i2", Role: "member"},
		{ID: "i3", Role: "member", LastEmailSentAt: fixedNow.Add(-3 * 24 * time.Hour)},
		{ID: "i4", Role: "admin"},
	}
}

func TestCollect_UserInvitesGroupedCounts(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.user_invites.count")
	// Three distinct (role, delivery) combos:
	//   (member,manual_link)=2, (member,emailed)=1, (admin,manual_link)=1.
	if len(pts) != 3 {
		t.Fatalf("invite count points = %d, want 3 (%+v)", len(pts), pts)
	}
	for _, p := range pts {
		if p.Kind != "gauge" {
			t.Fatalf("invite count kind = %q, want gauge", p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("invite count unit = %q, want 1", p.Unit)
		}
	}

	memberManual := findPoint(t, pts, map[string]string{
		"tailscale.user_invite.role":     "member",
		"tailscale.user_invite.delivery": "manual_link",
	})
	if memberManual.Value != 2 {
		t.Fatalf("member/manual_link invite count = %v, want 2", memberManual.Value)
	}
	memberEmailed := findPoint(t, pts, map[string]string{
		"tailscale.user_invite.role":     "member",
		"tailscale.user_invite.delivery": "emailed",
	})
	if memberEmailed.Value != 1 {
		t.Fatalf("member/emailed invite count = %v, want 1", memberEmailed.Value)
	}
	adminManual := findPoint(t, pts, map[string]string{
		"tailscale.user_invite.role":     "admin",
		"tailscale.user_invite.delivery": "manual_link",
	})
	if adminManual.Value != 1 {
		t.Fatalf("admin/manual_link invite count = %v, want 1", adminManual.Value)
	}
}

// TestCollect_UserInvitesNeverEmitAcceptedAttr is the #411 regression guard:
// the fabricated tailscale.user_invite.accepted label (the API cannot supply
// accepted-invite history) must never appear on any invite metric point.
func TestCollect_UserInvitesNeverEmitAcceptedAttr(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0,
		users.WithClock(func() time.Time { return fixedNow }))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, name := range rec.MetricNames() {
		for _, p := range rec.MetricPoints(name) {
			if _, ok := p.Attrs["tailscale.user_invite.accepted"]; ok {
				t.Errorf("metric %q emitted fabricated tailscale.user_invite.accepted attr: %+v", name, p.Attrs)
			}
		}
	}
}

// TestCollect_UserInvitePendingAge verifies #412's pending-age histogram: only
// the emailed invite (i3, sent 3 days before fixedNow) contributes a data
// point; the three manual-link invites have no delivery timestamp to measure
// age from and must be omitted, not recorded as age zero.
func TestCollect_UserInvitePendingAge(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0,
		users.WithClock(func() time.Time { return fixedNow }))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.user_invites.pending_age")
	if len(pts) != 1 {
		t.Fatalf("pending_age points = %d, want 1 (%+v)", len(pts), pts)
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Fatalf("pending_age kind = %q, want histogram", p.Kind)
	}
	if p.Unit != "s" {
		t.Fatalf("pending_age unit = %q, want s", p.Unit)
	}
	if p.Attrs["tailscale.user_invite.role"] != "member" {
		t.Fatalf("pending_age role attr = %q, want member", p.Attrs["tailscale.user_invite.role"])
	}
	wantSeconds := float64(3 * 24 * time.Hour / time.Second)
	if p.Value != wantSeconds {
		t.Fatalf("pending_age sum = %v, want %v", p.Value, wantSeconds)
	}
	if p.Count != 1 {
		t.Fatalf("pending_age count = %d, want 1", p.Count)
	}
}

func TestCollect_NullInvitesNoSeriesNoError(t *testing.T) {
	rec := telemetrytest.New()
	// A null/empty invite list (the real tailnet returns null) must emit no
	// invite series and must not error.
	c := users.New(&fakeLister{users: sampleUsers(), invites: nil}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if pts := rec.MetricPoints("tailscale.user_invites.count"); len(pts) != 0 {
		t.Fatalf("invite count points = %d, want 0 (%+v)", len(pts), pts)
	}
	// Existing user metrics must still be emitted unchanged.
	if pts := rec.MetricPoints("tailscale.users.count"); len(pts) != 2 {
		t.Fatalf("users.count points = %d, want 2 (%+v)", len(pts), pts)
	}
}

func TestCollect_PropagatesInviteError(t *testing.T) {
	rec := telemetrytest.New()
	wantErr := errors.New("invite boom")
	c := users.New(&fakeLister{users: sampleUsers(), invitesErr: wantErr}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); !errors.Is(err, wantErr) {
		t.Fatalf("Collect err = %v, want %v", err, wantErr)
	}
}
