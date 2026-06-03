package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/users"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
		"enduser.id":           "u1",
		"tailscale.user.login": "alice@example.com",
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
	alice := findPoint(t, pts, map[string]string{"enduser.id": "u1"})
	if alice.Value != 1 {
		t.Fatalf("alice connected = %v, want 1", alice.Value)
	}
	bob := findPoint(t, pts, map[string]string{"enduser.id": "u2"})
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
	alice := findPoint(t, pts, map[string]string{"enduser.id": "u1"})
	want := float64(time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC).Unix())
	if alice.Value != want {
		t.Fatalf("alice last_seen = %v, want %v", alice.Value, want)
	}
	if alice.Unit != "s" {
		t.Fatalf("last_seen unit = %q, want s", alice.Unit)
	}
	for _, p := range pts {
		if p.Attrs["enduser.id"] == "u2" {
			t.Fatalf("bob (zero LastSeen) should be skipped, got %+v", p)
		}
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

func sampleInvites() []tsapi.UserInvite {
	return []tsapi.UserInvite{
		{ID: "i1", Role: "member", Accepted: false},
		{ID: "i2", Role: "member", Accepted: false},
		{ID: "i3", Role: "member", Accepted: true},
		{ID: "i4", Role: "admin", Accepted: false},
	}
}

func TestCollect_UserInvitesGroupedCounts(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.user_invites.count")
	// Three distinct (role, accepted) combos:
	//   (member,false)=2, (member,true)=1, (admin,false)=1.
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

	memberPending := findPoint(t, pts, map[string]string{
		"tailscale.user_invite.role":     "member",
		"tailscale.user_invite.accepted": "false",
	})
	if memberPending.Value != 2 {
		t.Fatalf("member/false invite count = %v, want 2", memberPending.Value)
	}
	memberAccepted := findPoint(t, pts, map[string]string{
		"tailscale.user_invite.role":     "member",
		"tailscale.user_invite.accepted": "true",
	})
	if memberAccepted.Value != 1 {
		t.Fatalf("member/true invite count = %v, want 1", memberAccepted.Value)
	}
	adminPending := findPoint(t, pts, map[string]string{
		"tailscale.user_invite.role":     "admin",
		"tailscale.user_invite.accepted": "false",
	})
	if adminPending.Value != 1 {
		t.Fatalf("admin/false invite count = %v, want 1", adminPending.Value)
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
