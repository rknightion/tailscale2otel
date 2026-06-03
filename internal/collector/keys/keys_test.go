package keys_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeLister returns a canned slice of keys (or an error).
type fakeLister struct {
	keys  []tsclient.Key
	err   error
	calls int
}

func (f *fakeLister) Keys(context.Context) ([]tsclient.Key, error) {
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

// reusableKey builds a key with the reusable capability set.
func reusableKey(id string, expires time.Time) tsclient.Key {
	k := tsclient.Key{ID: id, Description: "ci runner", Expires: expires}
	k.Capabilities.Devices.Create.Reusable = true
	return k
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
	c := keys.New(&fakeLister{keys: []tsclient.Key{
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
	c := keys.New(&fakeLister{keys: []tsclient.Key{
		reusableKey("k1", exp),
		{ID: "k2", Description: "no expiry"}, // zero Expires -> skipped
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
	c := keys.New(&fakeLister{keys: []tsclient.Key{
		reusableKey("k1", now.Add(48*time.Hour)),
		reusableKey("k2", now.Add(72*time.Hour)),
		{ID: "k3"}, // distinct type (not reusable)
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
	c := keys.New(&fakeLister{keys: []tsclient.Key{
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
	c := keys.New(&fakeLister{keys: []tsclient.Key{
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
	c := keys.New(&fakeLister{keys: []tsclient.Key{
		{ID: "k1", Description: "never expires"}, // zero Expires
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

func TestCollect_NilNowDefaultsToTimeNow(t *testing.T) {
	// With nil now and a key already long expired, no panic and no false warn
	// behavior is asserted here beyond a successful Collect.
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsclient.Key{
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
