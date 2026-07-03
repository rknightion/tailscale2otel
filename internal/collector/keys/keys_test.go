package keys_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// fakeLister returns a canned slice of keys (or an error).
type fakeLister struct {
	keys  []tsapi.Key
	err   error
	calls int
}

func (f *fakeLister) KeysRich(context.Context) ([]tsapi.Key, error) {
	f.calls++
	return f.keys, f.err
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

func findLog(t *testing.T, recs []telemetrytest.LogRecord, eventName string) telemetrytest.LogRecord {
	t.Helper()
	for _, r := range recs {
		if r.EventName == eventName {
			return r
		}
	}
	t.Fatalf("no log record with event.name=%q in %+v", eventName, recs)
	return telemetrytest.LogRecord{}
}

// reusableKey builds a reusable machine auth key.
func reusableKey(id string, expires time.Time) tsapi.Key {
	return tsapi.Key{ID: id, Description: "ci runner", Type: "auth", Reusable: true, Expires: expires}
}

func TestName(t *testing.T) {
	c := keys.New(&fakeLister{}, 0, time.Hour, nil)
	if c.Name() != "keys" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "keys")
	}
}

func TestDefaultInterval(t *testing.T) {
	if got := keys.New(&fakeLister{}, 0, time.Hour, nil).DefaultInterval(); got != 300*time.Second {
		t.Fatalf("DefaultInterval(0) = %v, want 300s", got)
	}
	if got := keys.New(&fakeLister{}, 30*time.Second, time.Hour, nil).DefaultInterval(); got != 30*time.Second {
		t.Fatalf("DefaultInterval(30s) = %v, want 30s", got)
	}
}

func TestCollect_PerEntityFalse(t *testing.T) {
	// WithPerEntity(false) suppresses the per-key expiry gauge but keeps the
	// aggregate keys.count rollup AND the expiry-warning log event.
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	soon := now.Add(30 * time.Minute) // within the 1h expiryWarn window
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", soon),
	}}, 0, time.Hour, func() time.Time { return now }, keys.WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if pts := rec.MetricPoints("tailscale.key.expiry"); len(pts) != 0 {
		t.Errorf("per-key tailscale.key.expiry emitted with WithPerEntity(false): %+v", pts)
	}
	if pts := rec.MetricPoints("tailscale.keys.count"); len(pts) == 0 {
		t.Error("aggregate tailscale.keys.count not emitted with WithPerEntity(false)")
	}
	// The expiry-warning log must still fire regardless of perEntity.
	findLog(t, rec.LogRecords(), "tailscale.key.expiring")
}

func TestCollect_ExpiryGauge(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	exp := now.Add(48 * time.Hour)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", exp),
		{ID: "k2", Description: "no expiry", Type: "auth"}, // zero Expires -> skipped
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.key.expiry")
	if len(pts) != 1 {
		t.Fatalf("expiry points = %d, want 1 (k2 has zero expiry) (%+v)", len(pts), pts)
	}
	p := findPoint(t, pts, map[string]string{"tailscale.key.id": "k1"})
	if p.Value != float64(exp.Unix()) {
		t.Fatalf("expiry value = %v, want %v", p.Value, float64(exp.Unix()))
	}
	if p.Unit != "s" {
		t.Fatalf("expiry unit = %q, want s", p.Unit)
	}
	if p.Kind != "gauge" {
		t.Fatalf("expiry kind = %q, want gauge", p.Kind)
	}
	if p.Attrs["tailscale.key.description"] != "ci runner" {
		t.Fatalf("expiry description attr = %q, want 'ci runner'", p.Attrs["tailscale.key.description"])
	}
	if p.Attrs["tailscale.key.type"] == "" {
		t.Fatalf("expiry should carry a non-empty tailscale.key.type attr, got %+v", p.Attrs)
	}
}

func TestCollect_CountGauge(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", now.Add(48*time.Hour)),
		reusableKey("k2", now.Add(72*time.Hour)),
		{ID: "k3", Type: "auth"}, // distinct type (not reusable) -> auth/onetime
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.keys.count")
	if len(pts) == 0 {
		t.Fatalf("expected at least one count point")
	}
	for _, p := range pts {
		if p.Kind != "gauge" {
			t.Fatalf("count kind = %q, want gauge", p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("count unit = %q, want 1", p.Unit)
		}
		if _, ok := p.Attrs["tailscale.key.type"]; !ok {
			t.Fatalf("count point missing tailscale.key.type attr: %+v", p)
		}
	}
	// The two reusable keys must aggregate into one point with value 2.
	var total float64
	for _, p := range pts {
		total += p.Value
	}
	if total != 3 {
		t.Fatalf("sum of count points = %v, want 3 (total keys)", total)
	}
}

func TestCollect_WarnsOnExpiringKey(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	// Key expires in 1h; warn window 24h => should warn.
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", now.Add(time.Hour)),
	}}, 0, 24*time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	rs := rec.LogRecords()
	r := findLog(t, rs, "tailscale.key.expiring")
	if r.SeverityText != "WARN" {
		t.Fatalf("severity = %q, want WARN", r.SeverityText)
	}
	if r.Attrs["tailscale.key.id"] != "k1" {
		t.Fatalf("log key.id = %q, want k1", r.Attrs["tailscale.key.id"])
	}
	if r.Attrs["tailscale.key.type"] == "" {
		t.Fatalf("log should carry tailscale.key.type, got %+v", r.Attrs)
	}
	if r.Attrs["tailscale.key.description"] != "ci runner" {
		t.Fatalf("log key.description = %q, want 'ci runner'", r.Attrs["tailscale.key.description"])
	}
	gotSecs, err := strconv.Atoi(r.Attrs["tailscale.key.expires_in_seconds"])
	if err != nil {
		t.Fatalf("expires_in_seconds not an int: %q (%v)", r.Attrs["tailscale.key.expires_in_seconds"], err)
	}
	if want := int(time.Hour.Seconds()); gotSecs != want {
		t.Fatalf("expires_in_seconds = %d, want %d", gotSecs, want)
	}
	if r.Body == "" {
		t.Fatalf("warn log body should name the key, got empty")
	}
}

func TestCollect_NoWarnOutsideWindow(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	// Key expires in 1h; warn window 10m => no warn.
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", now.Add(time.Hour)),
	}}, 0, 10*time.Minute, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, r := range rec.LogRecords() {
		if r.EventName == "tailscale.key.expiring" {
			t.Fatalf("did not expect an expiring warning, got %+v", r)
		}
	}
}

func TestCollect_NoWarnForZeroExpiry(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "k1", Description: "never expires", Type: "auth"}, // zero Expires
	}}, 0, 24*time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, r := range rec.LogRecords() {
		if r.EventName == "tailscale.key.expiring" {
			t.Fatalf("zero-expiry key must not warn, got %+v", r)
		}
	}
}

// TestCollect_InvalidKeySuppressesExpiryAndWarn guards issue #64 sub-item 1: a
// key mapped Invalid (e.g. a spent Headscale one-time key, via
// hsapi.adaptPreAuthKey) must not report a live tailscale.key.expiry gauge or
// trigger the tailscale.key.expiring warning, even though Expires is set and
// within the warn window. The aggregate tailscale.keys.count rollup is
// unaffected (it already tracks the invalid dimension).
func TestCollect_InvalidKeySuppressesExpiryAndWarn(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	soon := now.Add(30 * time.Minute) // within the 1h expiryWarn window
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "spent", Description: "one-time", Type: "auth", Invalid: true, Expires: soon},
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if pts := rec.MetricPoints("tailscale.key.expiry"); len(pts) != 0 {
		t.Errorf("an invalid/spent key must not report a live expiry gauge, got %+v", pts)
	}
	for _, r := range rec.LogRecords() {
		if r.EventName == "tailscale.key.expiring" {
			t.Fatalf("an invalid/spent key must not trigger the expiring warning, got %+v", r)
		}
	}

	found := false
	for _, p := range rec.MetricPoints("tailscale.keys.count") {
		if p.Attrs["tailscale.key.invalid"] == "true" {
			found = true
		}
	}
	if !found {
		t.Error("aggregate tailscale.keys.count should still include the invalid key")
	}
}

// TestCollect_ValidKeyStillWarns is the control for the above: a key that is
// NOT Invalid must be unaffected by the new gating.
func TestCollect_ValidKeyStillWarns(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	soon := now.Add(30 * time.Minute)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "live", Description: "still good", Type: "auth", Invalid: false, Expires: soon},
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.key.expiry"); len(pts) != 1 {
		t.Errorf("a valid key must still report its expiry gauge, got %+v", pts)
	}
	findLog(t, rec.LogRecords(), "tailscale.key.expiring")
}

func TestCollect_NilNowDefaultsToTimeNow(t *testing.T) {
	// With nil now and a key already long expired, no panic and no false warn
	// behavior is asserted here beyond a successful Collect.
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", time.Now().Add(365*24*time.Hour)),
	}}, 0, time.Hour, nil)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect with nil now: %v", err)
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	rec := telemetrytest.New()
	wantErr := errors.New("boom")
	c := keys.New(&fakeLister{err: wantErr}, 0, time.Hour, nil)
	if err := c.Collect(context.Background(), rec.Emitter()); !errors.Is(err, wantErr) {
		t.Fatalf("Collect err = %v, want %v", err, wantErr)
	}
}

func TestCollect_ScopesGauge(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "oauth", Type: "client", Description: "tf", Scopes: []string{"all:read", "devices:core"}},
		{ID: "token", Type: "api", Scopes: []string{"all"}},
		{ID: "auth", Type: "auth", Reusable: true}, // no scopes -> no scopes point
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.key.scopes")
	if len(pts) != 2 {
		t.Fatalf("scopes points = %d, want 2 (auth key has no scopes) (%+v)", len(pts), pts)
	}
	oauth := findPoint(t, pts, map[string]string{"tailscale.key.id": "oauth"})
	if oauth.Value != 2 {
		t.Errorf("oauth scope count = %v, want 2", oauth.Value)
	}
	if oauth.Unit != "1" {
		t.Errorf("scopes unit = %q, want 1", oauth.Unit)
	}
	if oauth.Kind != "gauge" {
		t.Errorf("scopes kind = %q, want gauge", oauth.Kind)
	}
	if oauth.Attrs["tailscale.key.type"] != "client" {
		t.Errorf("scopes type attr = %q, want client", oauth.Attrs["tailscale.key.type"])
	}
	token := findPoint(t, pts, map[string]string{"tailscale.key.id": "token"})
	if token.Value != 1 {
		t.Errorf("token scope count = %v, want 1", token.Value)
	}
	if token.Attrs["tailscale.key.type"] != "api" {
		t.Errorf("token type attr = %q, want api", token.Attrs["tailscale.key.type"])
	}
}

func TestCollect_ScopesGauge_PerEntityFalse(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "oauth", Type: "client", Scopes: []string{"all:read"}},
	}}, 0, time.Hour, func() time.Time { return now }, keys.WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.key.scopes"); len(pts) != 0 {
		t.Errorf("scopes gauge emitted with WithPerEntity(false): %+v", pts)
	}
}

func TestCollect_PreauthorizedGauge(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "pa", Type: "auth", Reusable: true, Preauthorized: true},
		{ID: "npa", Type: "auth", Reusable: true, Preauthorized: false},
		{ID: "oauth", Type: "client", Scopes: []string{"all"}}, // not an auth key -> no point
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.key.preauthorized")
	if len(pts) != 2 {
		t.Fatalf("preauthorized points = %d, want 2 (auth keys only) (%+v)", len(pts), pts)
	}
	if p := findPoint(t, pts, map[string]string{"tailscale.key.id": "pa"}); p.Value != 1 {
		t.Errorf("pa preauthorized = %v, want 1", p.Value)
	}
	if p := findPoint(t, pts, map[string]string{"tailscale.key.id": "npa"}); p.Value != 0 {
		t.Errorf("npa preauthorized = %v, want 0", p.Value)
	}
	if p := findPoint(t, pts, map[string]string{"tailscale.key.id": "pa"}); p.Unit != "1" || p.Kind != "gauge" {
		t.Errorf("preauthorized unit/kind = %q/%q, want 1/gauge", p.Unit, p.Kind)
	}
	if p := findPoint(t, pts, map[string]string{"tailscale.key.id": "pa"}); p.Attrs["tailscale.key.type"] != "auth" {
		t.Errorf("preauthorized type attr = %q, want auth", p.Attrs["tailscale.key.type"])
	}
}

func TestCollect_PreauthorizedGauge_PerEntityFalse(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "pa", Type: "auth", Reusable: true, Preauthorized: true},
	}}, 0, time.Hour, func() time.Time { return now }, keys.WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.key.preauthorized"); len(pts) != 0 {
		t.Errorf("preauthorized gauge emitted with WithPerEntity(false): %+v", pts)
	}
}

func TestCollect_ScopesLog(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)

	t.Run("emits log with scope values when perEntity enabled and scopes present", func(t *testing.T) {
		rec := telemetrytest.New()
		c := keys.New(&fakeLister{keys: []tsapi.Key{
			{ID: "oauth", Type: "client", Description: "tf-runner", Scopes: []string{"devices:read", "dns:read"}},
		}}, 0, time.Hour, func() time.Time { return now })
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}

		r := findLog(t, rec.LogRecords(), "tailscale.key.scopes")
		if r.SeverityText != "INFO" {
			t.Errorf("severity = %q, want INFO", r.SeverityText)
		}
		if r.Attrs["tailscale.key.id"] != "oauth" {
			t.Errorf("key.id = %q, want oauth", r.Attrs["tailscale.key.id"])
		}
		if got := r.Attrs["tailscale.key.scope_values"]; got != "devices:read,dns:read" {
			t.Errorf("scope_values = %q, want %q", got, "devices:read,dns:read")
		}
		// Description must be present as a pii-gatable attr (free_text_details).
		if got := r.Attrs["tailscale.key.description"]; got != "tf-runner" {
			t.Errorf("description attr = %q, want tf-runner", got)
		}
		// Body must be generic: type + count only, no free-text key label or scope list.
		wantBody := "Tailscale key (client) has 2 scope(s)"
		if r.Body != wantBody {
			t.Errorf("body = %q, want %q", r.Body, wantBody)
		}
		// Must NOT embed the key label or raw scope strings in the body.
		for _, banned := range []string{"tf-runner", "devices:read", "dns:read"} {
			if strings.Contains(r.Body, banned) {
				t.Errorf("body %q must not contain %q (must be in attrs only)", r.Body, banned)
			}
		}

		// Count gauge must still be emitted.
		pts := rec.MetricPoints("tailscale.key.scopes")
		p := findPoint(t, pts, map[string]string{"tailscale.key.id": "oauth"})
		if p.Value != 2 {
			t.Errorf("scopes gauge value = %v, want 2", p.Value)
		}
	})

	t.Run("no scopes log when len(Scopes)==0", func(t *testing.T) {
		rec := telemetrytest.New()
		c := keys.New(&fakeLister{keys: []tsapi.Key{
			{ID: "auth", Type: "auth", Reusable: true},
		}}, 0, time.Hour, func() time.Time { return now })
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		for _, r := range rec.LogRecords() {
			if r.EventName == "tailscale.key.scopes" {
				t.Fatalf("unexpected tailscale.key.scopes log for key with no scopes: %+v", r)
			}
		}
	})

	t.Run("no scopes log when perEntity disabled", func(t *testing.T) {
		rec := telemetrytest.New()
		c := keys.New(&fakeLister{keys: []tsapi.Key{
			{ID: "oauth", Type: "client", Scopes: []string{"all:read"}},
		}}, 0, time.Hour, func() time.Time { return now }, keys.WithPerEntity(false))
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		for _, r := range rec.LogRecords() {
			if r.EventName == "tailscale.key.scopes" {
				t.Fatalf("unexpected tailscale.key.scopes log with WithPerEntity(false): %+v", r)
			}
		}
	})
}

func TestCollect_TypeAndAuthKind(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		{ID: "a-eph", Type: "auth", Ephemeral: true, Expires: now.Add(48 * time.Hour)},
		{ID: "a-reuse", Type: "auth", Reusable: true, Expires: now.Add(48 * time.Hour)},
		{ID: "a-once", Type: "auth", Expires: now.Add(48 * time.Hour)},
		{ID: "oauth", Type: "client", Scopes: []string{"all:read"}},
		{ID: "token", Type: "api", Scopes: []string{"all"}, Expires: now.Add(48 * time.Hour)},
		{ID: "legacy", Type: "", Reusable: true, Expires: now.Add(48 * time.Hour)},
	}}, 0, time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Per-key expiry gauge must report the real keyType + auth_kind.
	exp := rec.MetricPoints("tailscale.key.expiry")
	check := func(id, wantType, wantKind string) {
		p := findPoint(t, exp, map[string]string{"tailscale.key.id": id})
		if p.Attrs["tailscale.key.type"] != wantType {
			t.Errorf("%s type = %q, want %q", id, p.Attrs["tailscale.key.type"], wantType)
		}
		if p.Attrs["tailscale.key.auth_kind"] != wantKind {
			t.Errorf("%s auth_kind = %q, want %q", id, p.Attrs["tailscale.key.auth_kind"], wantKind)
		}
	}
	check("a-eph", "auth", "ephemeral")
	check("a-reuse", "auth", "reusable")
	check("a-once", "auth", "onetime")
	check("token", "api", "none")
	check("legacy", "auth", "reusable") // empty Type falls back to auth
	// "oauth" has no expiry, so no expiry point — assert via the count instead.

	// Count buckets must use the real keyType, not the old onetime catch-all.
	counts := rec.MetricPoints("tailscale.keys.count")
	findPoint(t, counts, map[string]string{"tailscale.key.type": "client", "tailscale.key.auth_kind": "none"})
	findPoint(t, counts, map[string]string{"tailscale.key.type": "api", "tailscale.key.auth_kind": "none"})
	findPoint(t, counts, map[string]string{"tailscale.key.type": "auth", "tailscale.key.auth_kind": "ephemeral"})
}
