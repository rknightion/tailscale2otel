package users_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/users"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
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

func userLogsNamed(rec *telemetrytest.Recorder, name string) []telemetrytest.LogRecord {
	var out []telemetrytest.LogRecord
	for _, lr := range rec.LogRecords() {
		if lr.EventName == name {
			out = append(out, lr)
		}
	}
	return out
}

// TestCollect_UserInviteLifecycle is deliberately based on the open-invite
// snapshot contract. A first observation is not treated as proof of creation,
// and disappearance is not treated as proof of acceptance, revocation, or
// cancellation: the API exposes no terminal reason for an invite leaving this
// endpoint.
func TestCollect_UserInviteLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	api := &fakeLister{
		users: sampleUsers(),
		invites: []tsapi.UserInvite{{
			ID:        "invite-1",
			Role:      "admin",
			InviterID: "inviter-1",
			Email:     "invitee@example.com",
			InviteURL: "https://example.invalid/bearer-token",
		}},
	}
	c := users.New(api, 0, users.WithClock(func() time.Time { return now }))

	collect := func() *telemetrytest.Recorder {
		t.Helper()
		rec := telemetrytest.New()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		return rec
	}

	first := collect()
	observed := userLogsNamed(first, users.EventUserInviteObserved)
	if len(observed) != 1 {
		t.Fatalf("observed invite logs = %d, want 1 (%+v)", len(observed), first.LogRecords())
	}
	if !observed[0].Timestamp.Equal(now) {
		t.Errorf("observed timestamp = %v, want collection time %v", observed[0].Timestamp, now)
	}
	for key, want := range map[string]string{
		"tailscale.user_invite.id":       "invite-1",
		"tailscale.user_invite.role":     "admin",
		"tailscale.lifecycle.transition": "observed",
		"user.id":                        "inviter-1",
		"user.name":                      "invitee@example.com",
	} {
		if got := observed[0].Attrs[key]; got != want {
			t.Errorf("observed attr %s = %q, want %q", key, got, want)
		}
	}
	if strings.Contains(observed[0].Body, "bearer-token") {
		t.Errorf("observed invite body leaked invite URL: %q", observed[0].Body)
	}

	now = now.Add(time.Minute)
	second := collect()
	if got := len(userLogsNamed(second, users.EventUserInviteObserved)); got != 0 {
		t.Errorf("repeated observed invite logs = %d, want 0", got)
	}
	if got := len(userLogsNamed(second, users.EventUserInviteNoLongerOpen)); got != 0 {
		t.Errorf("still-open terminal logs = %d, want 0", got)
	}

	api.invites = nil
	now = now.Add(time.Minute)
	third := collect()
	closed := userLogsNamed(third, users.EventUserInviteNoLongerOpen)
	if len(closed) != 1 {
		t.Fatalf("no-longer-open invite logs = %d, want 1 (%+v)", len(closed), third.LogRecords())
	}
	if closed[0].Attrs["tailscale.lifecycle.transition"] != "no_longer_open" {
		t.Errorf("terminal transition = %q, want no_longer_open", closed[0].Attrs["tailscale.lifecycle.transition"])
	}
	if !strings.Contains(closed[0].Body, "terminal reason") {
		t.Errorf("terminal body = %q, want API terminal-reason limitation", closed[0].Body)
	}

	fourth := collect()
	if got := len(userLogsNamed(fourth, users.EventUserInviteNoLongerOpen)); got != 0 {
		t.Errorf("repeated no-longer-open invite logs = %d, want 0", got)
	}
}

// TestCollect_UserInviteLifecycleRedactsIdentity verifies that the normalized
// event keeps its bounded transition/id fields while the existing user-id and
// email PII controls remove inviter and invitee identity from the log attrs.
func TestCollect_UserInviteLifecycleRedactsIdentity(t *testing.T) {
	api := &fakeLister{
		users: sampleUsers(),
		invites: []tsapi.UserInvite{{
			ID:        "invite-1",
			Role:      "member",
			InviterID: "inviter-1",
			Email:     "invitee@example.com",
		}},
	}
	rec := telemetrytest.NewWithPII(pii.Categories{
		pii.CatEmails:  false,
		pii.CatUserIDs: false,
	})
	c := users.New(api, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	logs := userLogsNamed(rec, users.EventUserInviteObserved)
	if len(logs) != 1 {
		t.Fatalf("observed invite logs = %d, want 1 (%+v)", len(logs), rec.LogRecords())
	}
	for _, key := range []string{"user.id", "user.name"} {
		if _, ok := logs[0].Attrs[key]; ok {
			t.Errorf("PII-disabled invite log retained %s: %+v", key, logs[0].Attrs)
		}
	}
	if got := logs[0].Attrs["tailscale.lifecycle.transition"]; got != "observed" {
		t.Errorf("transition after PII filtering = %q, want observed", got)
	}
	if got := logs[0].Attrs["tailscale.user_invite.id"]; got != "invite-1" {
		t.Errorf("invite id after PII filtering = %q, want invite-1", got)
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

// availabilityStates returns, per operation, the single state whose gauge is
// 1. Copied from postureintegrations_test.go so this package's availability
// assertions don't depend on another package's test-only helper.
func availabilityStates(t *testing.T, rec *telemetrytest.Recorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		op := p.Attrs["tailscale.api.operation"]
		st := p.Attrs["tailscale.api.state"]
		switch p.Value {
		case 1:
			if prev, dup := out[op]; dup {
				t.Fatalf("operation %q has two states at 1: %q and %q", op, prev, st)
			}
			out[op] = st
		case 0:
		default:
			t.Fatalf("availability gauge for %q/%q = %v, want 0 or 1", op, st, p.Value)
		}
	}
	return out
}

// TestCollect_AvailabilityStates_ListUsers drives the primary Users() call
// through every classified state (#524) and asserts the recorded
// availability under the "listUsers" operation name. Disposition is the zero
// value: an ambiguous 403 must read as scope_denied, never disabled — there
// is no upstream-documented feature gate for user listing.
func TestCollect_AvailabilityStates_ListUsers(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState string
	}{
		{"success", nil, "supported"},
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, "credential_rejected"},
		{"403 scope denied", &tsapi.StatusError{Code: 403}, "scope_denied"},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, "request_rejected"},
		{"transient transport error", context.DeadlineExceeded, "transient_failure"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites(), err: tc.err}, 0)
			err := c.Collect(context.Background(), rec.Emitter())
			if tc.err == nil && err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if tc.err != nil && err == nil {
				t.Fatalf("Collect: want error, got nil")
			}
			if got := availabilityStates(t, rec)["listUsers"]; got != tc.wantState {
				t.Errorf("listUsers availability = %q, want %q", got, tc.wantState)
			}
		})
	}
}

// TestCollect_AvailabilityStates_UserInvites drives the UserInvites()
// subrequest through every classified state and asserts it independently of
// Users(), which always succeeds in this table. Disposition is the zero
// value for the same reason as above.
func TestCollect_AvailabilityStates_UserInvites(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState string
	}{
		{"success", nil, "supported"},
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, "credential_rejected"},
		{"403 scope denied", &tsapi.StatusError{Code: 403}, "scope_denied"},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, "request_rejected"},
		{"transient transport error", context.DeadlineExceeded, "transient_failure"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites(), invitesErr: tc.err}, 0)
			err := c.Collect(context.Background(), rec.Emitter())
			if tc.err == nil && err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if tc.err != nil && err == nil {
				t.Fatalf("Collect: want error, got nil")
			}
			states := availabilityStates(t, rec)
			if got := states["user_invites"]; got != tc.wantState {
				t.Errorf("user_invites availability = %q, want %q", got, tc.wantState)
			}
			if got := states["listUsers"]; got != "supported" {
				t.Errorf("listUsers availability = %q, want supported (independent of UserInvites)", got)
			}
		})
	}
}

// TestCollect_AvailabilityIndependence pins #524's core requirement: the two
// operations are tracked independently in the same tick. Users() succeeding
// must not mask a UserInvites() 403.
func TestCollect_AvailabilityIndependence(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{
		users:      sampleUsers(),
		invitesErr: &tsapi.StatusError{Code: 403},
	}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect: want error (invites 403), got nil")
	}
	states := availabilityStates(t, rec)
	if states["listUsers"] != "supported" {
		t.Errorf("listUsers = %q, want supported", states["listUsers"])
	}
	if states["user_invites"] != "scope_denied" {
		t.Errorf("user_invites = %q, want scope_denied", states["user_invites"])
	}
}

// TestUserInvitesRecordedUnderSubrequestNameNotOperationID pins #524: the
// UserInvites() subrequest MUST be recorded under the bounded subrequest name
// "user_invites" (internal/app.SubrequestUserInvites), NOT the upstream
// operationId "listUserInvites". internal/app/capability.go's
// filterOperations joins a capability-matrix row to tracker entries by exact
// operation-string match against the subrequest name; recording under the
// operationId would leave that row permanently "unknown". This collector
// cannot import internal/app (import cycle), so the join key is duplicated
// here as a literal rather than a shared constant — this test is the guard
// against that literal drifting.
func TestUserInvitesRecordedUnderSubrequestNameNotOperationID(t *testing.T) {
	rec := telemetrytest.New()
	c := users.New(&fakeLister{users: sampleUsers(), invites: sampleInvites()}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	states := availabilityStates(t, rec)
	if got, ok := states["listUserInvites"]; ok {
		t.Errorf("UserInvites recorded under operationId %q instead of subrequest name %q", got, "user_invites")
	}
	if got, ok := states["user_invites"]; !ok || got != "supported" {
		t.Errorf("user_invites availability = %q (present=%v), want supported", got, ok)
	}
}
