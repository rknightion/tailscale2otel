package webhook

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func newTrustworthyTestServer(t *testing.T, opts Options, options ...Option) (*Server, *telemetrytest.Recorder) {
	t.Helper()
	rec := telemetrytest.New()
	if opts.Path == "" {
		opts.Path = "/webhook"
	}
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:0"
	}
	return New(opts, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), options...), rec
}

func TestHandler_TypedDataAllowlistAndUnknownVersion(t *testing.T) {
	s, rec := newTrustworthyTestServer(t, Options{Secret: testSecret})
	body := `[` +
		`{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"e.com","message":"node","data":{"nodeID":"n1","deviceName":"laptop","managedBy":"m","actor":"a","url":"https://x","unknown":"drop","policy":"drop"}},` +
		`{"timestamp":"2026-06-02T10:01:00Z","version":1,"type":"nodeKeyExpired","tailnet":"e.com","message":"expiry","data":{"nodeID":"n2","expiration":"2026-07-01T00:00:00Z","deviceName":null}},` +
		`{"timestamp":"2026-06-02T10:02:00Z","version":1,"type":"policyUpdate","tailnet":"e.com","message":"policy","data":{"actor":"a","url":"https://x","newPolicy":"secret policy","oldPolicy":{"also":"secret"}}},` +
		`{"timestamp":"2026-06-02T10:03:00Z","version":1,"type":"userRoleUpdated","tailnet":"e.com","message":"role","data":{"user":"u","actor":"a","url":"https://x","oldRoles":["member"],"newRoles":["admin"],"bad":1}},` +
		`{"timestamp":"2026-06-02T10:04:00Z","version":2,"type":"future","tailnet":"e.com","message":"future","data":{"nodeID":"must-not-leak"}}` +
		`]`
	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	if resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, ts, body)); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	logs := map[string]telemetrytest.LogRecord{}
	for _, lr := range rec.LogRecords() {
		logs[lr.EventName] = lr
	}
	assertAttr := func(name, key, want string) {
		t.Helper()
		if got := logs[name].Attrs[key]; got != want {
			t.Errorf("%s %s = %#v, want %q", name, key, got, want)
		}
	}
	assertMissing := func(name, key string) {
		t.Helper()
		if _, ok := logs[name].Attrs[key]; ok {
			t.Errorf("%s unexpectedly contains %s: %#v", name, key, logs[name].Attrs[key])
		}
	}
	assertAttr("tailscale.webhook.nodeCreated", AttrNodeID, "n1")
	assertAttr("tailscale.webhook.nodeCreated", AttrDeviceName, "laptop")
	assertAttr("tailscale.webhook.nodeCreated", AttrManagedBy, "m")
	assertAttr("tailscale.webhook.nodeCreated", AttrActor, "a")
	assertAttr("tailscale.webhook.nodeCreated", AttrURL, "https://x")
	assertMissing("tailscale.webhook.nodeCreated", "unknown")
	assertMissing("tailscale.webhook.nodeCreated", "policy")
	assertAttr("tailscale.webhook.nodeKeyExpired", AttrKeyExpiration, "2026-07-01T00:00:00Z")
	assertMissing("tailscale.webhook.nodeKeyExpired", AttrDeviceName)
	assertAttr("tailscale.webhook.policyUpdate", AttrActor, "a")
	assertAttr("tailscale.webhook.policyUpdate", AttrURL, "https://x")
	assertMissing("tailscale.webhook.policyUpdate", "newPolicy")
	assertAttr("tailscale.webhook.userRoleUpdated", AttrUser, "u")
	assertAttr("tailscale.webhook.userRoleUpdated", AttrOldRoles, "member")
	assertAttr("tailscale.webhook.userRoleUpdated", AttrNewRoles, "admin")
	assertMissing("tailscale.webhook.future", AttrNodeID)

	var known, unknown float64
	for _, p := range rec.MetricPoints(MetricSchemaDrift) {
		if p.Attrs[attrSchemaField] != "version" {
			t.Errorf("schema drift field = %#v, want version", p.Attrs[attrSchemaField])
		}
		switch p.Attrs[attrSchemaStatus] {
		case "known":
			known += p.Value
		case "unknown":
			unknown += p.Value
		default:
			t.Errorf("schema drift status = %#v", p.Attrs[attrSchemaStatus])
		}
	}
	if known != 4 || unknown != 1 {
		t.Fatalf("schema drift known=%v unknown=%v, want 4 and 1", known, unknown)
	}
}

func TestHandler_TypedDataLeniencyDoesNotRelaxBaseEventTypes(t *testing.T) {
	s, _ := newTrustworthyTestServer(t, Options{Secret: testSecret})
	body := `[{"timestamp":"2026-06-02T10:00:00Z","version":"1","type":"nodeCreated","tailnet":"e.com","message":"node","data":{"nodeID":"n1"}}]`
	ts := time.Date(2026, 6, 2, 10, 1, 0, 0, time.UTC)

	resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, ts, body))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a wrong-typed base event field", resp.StatusCode)
	}
}

func TestHandler_NoSecretRequiresLoopbackBeforeBodyRead(t *testing.T) {
	cases := []struct {
		name   string
		listen string
		want   int
	}{
		{"ipv4_loopback", "127.0.0.1:0", http.StatusOK},
		{"ipv6_loopback", "[::1]:0", http.StatusOK},
		{"localhost", "localhost:9099", http.StatusOK},
		{"wildcard", ":9099", http.StatusForbidden},
		{"ipv4_wildcard", "0.0.0.0:9099", http.StatusForbidden},
		{"ipv6_wildcard", "[::]:9099", http.StatusForbidden},
		{"lan_ipv4", "192.168.1.20:9099", http.StatusForbidden},
		{"tailnet_ipv4", "100.64.0.1:9099", http.StatusForbidden},
		{"hostname", "example.test:9099", http.StatusForbidden},
		{"unix_style", "unix:/tmp/tailscale2otel.sock", http.StatusForbidden},
		{"empty", "", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			s := New(Options{Listen: tc.listen, Path: "/webhook"}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			payload := `[{"version":1,"type":"nodeCreated","data":{"nodeID":"n1"}}]`
			if tc.want == http.StatusOK {
				resp := doPost(t, s.Handler(), "/webhook", payload, "")
				if resp.StatusCode != tc.want {
					t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
				}
				return
			}
			body := newBlockingBody(payload)
			rw := postBody(s.Handler(), body)
			if rw.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%q", rw.Code, tc.want, rw.Body.String())
			}
			select {
			case <-body.reading:
				t.Fatal("network-reachable no-secret request read its body")
			default:
			}
			if len(rec.LogRecords()) != 0 || len(rec.MetricPoints(MetricEvents)) != 0 {
				t.Fatalf("forbidden request emitted telemetry: logs=%d metrics=%+v", len(rec.LogRecords()), rec.MetricPoints(MetricEvents))
			}
		})
	}
}

func TestHandler_DeliveryDedupCanonicalPerEvent(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	s, rec := newTrustworthyTestServer(t, Options{Secret: testSecret}, WithClock(func() time.Time { return now }), withDeliveryDedup(time.Hour, 2))
	first := `[{"version":1,"type":"nodeCreated","tailnet":"e.com","message":"one","data":{"nodeID":"n1","unknown":{"b":2,"a":1}}}]`
	second := `[{"data":{"unknown":{"a":1,"b":2},"nodeID":"n1"},"message":"one","tailnet":"e.com","type":"nodeCreated","version":1},{"version":1,"type":"nodeDeleted","tailnet":"e.com","message":"new","data":{"nodeID":"n2"}}]`
	for _, body := range []string{first, second} {
		if resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, now, body)); resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	}
	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("logs = %d, want 2 (canonical duplicate suppressed; new event emitted)", got)
	}
	var duplicates float64
	for _, p := range rec.MetricPoints(MetricDuplicates) {
		if len(p.Attrs) != 0 {
			t.Fatalf("duplicate attrs = %#v, want none", p.Attrs)
		}
		duplicates += p.Value
	}
	if duplicates != 1 {
		t.Fatalf("duplicates = %v, want 1", duplicates)
	}

	now = now.Add(2 * time.Hour)
	if resp := doPost(t, s.Handler(), "/webhook", first, signBody(testSecret, now, first)); resp.StatusCode != http.StatusOK {
		t.Fatalf("post-expiry status = %d, want 200", resp.StatusCode)
	}
	if got := len(rec.LogRecords()); got != 3 {
		t.Fatalf("logs after TTL expiry = %d, want 3", got)
	}
}

func TestDeliveryDeduperEvictsOldestAtCapacity(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	d := newDeliveryDeduper(time.Hour, 2, func() time.Time { return now })
	for _, key := range []string{"a", "b", "c"} {
		if !d.Add(key) {
			t.Fatalf("Add(%q) = duplicate, want new", key)
		}
	}
	if got := d.Len(); got != 2 {
		t.Fatalf("Len = %d, want bounded 2", got)
	}
	if !d.Add("a") {
		t.Fatal("oldest entry a was not evicted at capacity")
	}
	if d.Add("c") {
		t.Fatal("newest entry c was unexpectedly evicted")
	}
}
