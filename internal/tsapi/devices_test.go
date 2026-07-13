package tsapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// devicesRichFixture mirrors a trimmed /devices?fields=all response: one online
// device with DERP latency and distro, one offline minimal device, and one
// external device.
const devicesRichFixture = `{"devices":[
  {
    "id":"3690401478992208","nodeId":"nDdiWAbPpV11CNTRL","name":"laptop.example.ts.net",
    "hostname":"laptop","os":"linux","user":"alice@example.com","clientVersion":"1.99.0",
    "addresses":["100.64.0.1","fd7a::1"],"tags":["tag:server","tag:prod"],
    "authorized":true,"isExternal":false,"updateAvailable":true,"keyExpiryDisabled":false,
    "isEphemeral":true,"multipleConnections":true,
    "connectedToControl":true,"blocksIncomingConnections":false,"sshEnabled":true,
    "created":"2026-03-24T15:45:46Z","lastSeen":"2026-06-02T21:50:25Z","expires":"2026-09-20T15:45:46Z",
    "advertisedRoutes":["10.0.0.0/24"],"enabledRoutes":["10.0.0.0/24"],
    "distro":{"name":"ubuntu","version":"24.04","codeName":"noble"},
    "clientConnectivity":{"latency":{"Frankfurt":{"preferred":true,"latencyMs":1.0156919999999998},"Amsterdam":{"latencyMs":8.675937999999999}}},
    "postureIdentity":{"serialNumbers":["TESTSERIAL123ABC"]}
  },
  {
    "id":"346670268899695","nodeId":"nExampleFlow11CNTRL","name":"alex.example.ts.net",
    "hostname":"alex","os":"macOS","user":"alex@example.com","clientVersion":"1.80.0",
    "addresses":["100.64.0.2"],
    "authorized":true,"isExternal":false,"updateAvailable":false,"keyExpiryDisabled":false,
    "connectedToControl":false,"blocksIncomingConnections":true,"sshEnabled":false,
    "created":"2026-01-01T00:00:00Z","lastSeen":"2026-05-24T09:38:04Z","expires":"2026-08-26T19:09:04Z",
    "distro":{},
    "postureIdentity":{"disabled":true}
  },
  {
    "id":"999","nodeId":"nExternal11CNTRL","name":"shared.other.ts.net",
    "hostname":"shared","os":"windows","user":"guest@other.com","clientVersion":"1.70.0",
    "addresses":["100.64.0.9"],
    "authorized":true,"isExternal":true,"updateAvailable":false,"keyExpiryDisabled":true,
    "connectedToControl":true,"blocksIncomingConnections":false,"sshEnabled":false,
    "created":"2026-02-02T00:00:00Z","lastSeen":"2026-06-01T00:00:00Z","expires":"0001-01-01T00:00:00Z"
  }
]}`

func TestDevicesRich_DecodesRichFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/devices" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("fields"); got != "all" {
			http.Error(w, "fields = "+got, http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if len(devs) != 3 {
		t.Fatalf("len(devs) = %d, want 3", len(devs))
	}

	d0 := devs[0]
	if d0.ID != "3690401478992208" || d0.NodeID != "nDdiWAbPpV11CNTRL" {
		t.Fatalf("ids = %q/%q", d0.ID, d0.NodeID)
	}
	if d0.Hostname != "laptop" || d0.OS != "linux" || d0.User != "alice@example.com" {
		t.Fatalf("basic = %+v", d0)
	}
	if d0.ClientVersion != "1.99.0" {
		t.Fatalf("clientVersion = %q", d0.ClientVersion)
	}
	if len(d0.Addresses) != 2 || d0.Addresses[0] != "100.64.0.1" {
		t.Fatalf("addresses = %v", d0.Addresses)
	}
	if !d0.Authorized || d0.IsExternal || !d0.UpdateAvailable || d0.KeyExpiryDisabled {
		t.Fatalf("flags1 = %+v", d0)
	}
	if !d0.ConnectedToControl || d0.BlocksIncomingConnections || !d0.SSHEnabled {
		t.Fatalf("flags2 = %+v", d0)
	}
	wantCreated, _ := time.Parse(time.RFC3339, "2026-03-24T15:45:46Z")
	if !d0.Created.Equal(wantCreated) {
		t.Fatalf("created = %v, want %v", d0.Created, wantCreated)
	}
	wantSeen, _ := time.Parse(time.RFC3339, "2026-06-02T21:50:25Z")
	if !d0.LastSeen.Equal(wantSeen) {
		t.Fatalf("lastSeen = %v", d0.LastSeen)
	}
	wantExpires, _ := time.Parse(time.RFC3339, "2026-09-20T15:45:46Z")
	if !d0.Expires.Equal(wantExpires) {
		t.Fatalf("expires = %v", d0.Expires)
	}
	if len(d0.AdvertisedRoutes) != 1 || d0.AdvertisedRoutes[0] != "10.0.0.0/24" {
		t.Fatalf("advertisedRoutes = %v", d0.AdvertisedRoutes)
	}
	if len(d0.EnabledRoutes) != 1 || d0.EnabledRoutes[0] != "10.0.0.0/24" {
		t.Fatalf("enabledRoutes = %v", d0.EnabledRoutes)
	}
	if d0.Distro.Name != "ubuntu" || d0.Distro.Version != "24.04" || d0.Distro.CodeName != "noble" {
		t.Fatalf("distro = %+v", d0.Distro)
	}
	fr, ok := d0.DERPLatency["Frankfurt"]
	if !ok {
		t.Fatalf("missing Frankfurt latency: %v", d0.DERPLatency)
	}
	if !fr.Preferred || fr.LatencyMs == 0 {
		t.Fatalf("frankfurt = %+v", fr)
	}
	am, ok := d0.DERPLatency["Amsterdam"]
	if !ok || am.Preferred || am.LatencyMs == 0 {
		t.Fatalf("amsterdam = %+v ok=%v", am, ok)
	}
}

func TestDevicesRich_OfflineMinimalAndExternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}

	// offline minimal device: empty distro, no clientConnectivity at all.
	off := devs[1]
	if off.ConnectedToControl {
		t.Fatalf("offline ConnectedToControl = true")
	}
	if off.Distro.Name != "" || off.Distro.Version != "" {
		t.Fatalf("offline distro = %+v, want empty", off.Distro)
	}
	if len(off.DERPLatency) != 0 {
		t.Fatalf("offline DERPLatency = %v, want empty", off.DERPLatency)
	}
	if !off.BlocksIncomingConnections {
		t.Fatalf("offline BlocksIncomingConnections = false")
	}

	// external device.
	ext := devs[2]
	if !ext.IsExternal {
		t.Fatalf("ext IsExternal = false")
	}
	if !ext.KeyExpiryDisabled {
		t.Fatalf("ext KeyExpiryDisabled = false")
	}
	if !ext.Expires.IsZero() {
		t.Fatalf("ext Expires = %v, want zero", ext.Expires)
	}
}

// TestDevicesRich_EmptyTimestamps pins #48: the Tailscale API returns created:""
// (and can return empty lastSeen/expires) for external/shared devices; a plain
// time.Time field rejects that and fails the whole decode. The tolerant wrapper
// must decode them to a zero time without error.
func TestDevicesRich_EmptyTimestamps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"devices":[
		  {"id":"ext1","name":"shared.example.ts.net","isExternal":true,
		   "created":"","lastSeen":"","expires":""}
		]}`))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich with empty timestamps errored (the #48 regression): %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("len = %d, want 1", len(devs))
	}
	if !devs[0].Created.IsZero() || !devs[0].LastSeen.IsZero() || !devs[0].Expires.IsZero() {
		t.Errorf("empty timestamps should decode to zero time; got created=%v lastSeen=%v expires=%v",
			devs[0].Created, devs[0].LastSeen, devs[0].Expires)
	}
}

// TestDevicesRich_DecodesTags verifies the per-device `tags` array is decoded.
// Verified against .capture/devices.json: a tagged device carries
// "tags":[...]; an untagged device OMITS the field (never "tags":[]) so it
// decodes to a nil slice.
func TestDevicesRich_DecodesTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if got := devs[0].Tags; len(got) != 2 || got[0] != "tag:server" || got[1] != "tag:prod" {
		t.Fatalf("devs[0].Tags = %v, want [tag:server tag:prod]", got)
	}
	if devs[1].Tags != nil {
		t.Fatalf("devs[1].Tags = %v, want nil (untagged device omits the field)", devs[1].Tags)
	}
}

// TestDevicesRich_DecodesEphemeral verifies the per-device `isEphemeral` flag is
// decoded. Verified against .capture/devices.json: ephemeral devices carry
// "isEphemeral":true; non-ephemeral devices OMIT the field (it decodes false).
func TestDevicesRich_DecodesEphemeral(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if !devs[0].IsEphemeral {
		t.Error("devs[0].IsEphemeral = false, want true")
	}
	if devs[1].IsEphemeral {
		t.Error("devs[1].IsEphemeral = true, want false (field omitted)")
	}
}

func TestDevicePostureAttributes_DecodesMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/device/dev123/attributes" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"attributes":{"custom:foo":"bar","node:os":"linux","posture:latestMacOSVersion":true,"num":3}}`))
	}))
	defer srv.Close()

	got, err := newClient(t, srv.URL).DevicePostureAttributes(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DevicePostureAttributes: %v", err)
	}
	attrs := got.Attributes
	if attrs["custom:foo"] != "bar" {
		t.Fatalf("custom:foo = %v", attrs["custom:foo"])
	}
	if attrs["node:os"] != "linux" {
		t.Fatalf("node:os = %v", attrs["node:os"])
	}
	if attrs["posture:latestMacOSVersion"] != true {
		t.Fatalf("posture flag = %v", attrs["posture:latestMacOSVersion"])
	}
	if len(got.Expiries) != 0 {
		t.Fatalf("Expiries = %v, want empty (envelope carries no expiries key)", got.Expiries)
	}
}

// TestDevicePostureAttributes_DecodesExpiries verifies the "expiries" envelope
// sibling (present only for attributes explicitly set with an expiry, e.g. a
// custom: namespace attribute) is decoded alongside the attribute map. This
// fixture is schema-derived (issue #164): every live lab capture in
// .capture/device-attrs-*-20260713.json omits "expiries" entirely (no lab
// attribute currently carries one), so there is no live capture to mirror for
// the present case.
func TestDevicePostureAttributes_DecodesExpiries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"attributes":{"custom:foo":"bar","node:os":"linux"},` +
			`"expiries":{"custom:foo":"2026-08-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	got, err := newClient(t, srv.URL).DevicePostureAttributes(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DevicePostureAttributes: %v", err)
	}
	if len(got.Expiries) != 1 {
		t.Fatalf("Expiries = %v, want exactly 1 entry", got.Expiries)
	}
	want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if !got.Expiries["custom:foo"].Equal(want) {
		t.Fatalf("Expiries[custom:foo] = %v, want %v", got.Expiries["custom:foo"], want)
	}
	if _, ok := got.Expiries["node:os"]; ok {
		t.Fatalf("Expiries carries node:os, want only attributes with an explicit expiry")
	}
}

func TestDevicesRich_ClientConnectivity(t *testing.T) {
	const body = `{"devices":[{
		"id":"1","nodeId":"n1","hostname":"host-a",
		"advertisedRoutes":["0.0.0.0/0","::/0","10.0.50.0/24"],
		"enabledRoutes":["0.0.0.0/0","::/0"],
		"clientConnectivity":{
			"endpoints":["18.192.206.183:39211","[2a05:d014::1]:39211"],
			"mappingVariesByDestIP":true,
			"latency":{"Frankfurt":{"preferred":true,"latencyMs":1.0}},
			"clientSupports":{"hairPinning":null,"ipv6":true,"pcp":false,"pmp":false,"udp":true,"upnp":false}
		}
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("got %d devices, want 1", len(devs))
	}
	d := devs[0]
	if !d.HardNAT {
		t.Errorf("HardNAT = false, want true")
	}
	if len(d.Endpoints) != 2 {
		t.Errorf("Endpoints = %v, want 2", d.Endpoints)
	}
	if d.ClientSupports.UDP == nil || !*d.ClientSupports.UDP {
		t.Errorf("UDP = %v, want *true", d.ClientSupports.UDP)
	}
	if d.ClientSupports.IPv6 == nil || !*d.ClientSupports.IPv6 {
		t.Errorf("IPv6 = %v, want *true", d.ClientSupports.IPv6)
	}
	if d.ClientSupports.PCP == nil || *d.ClientSupports.PCP {
		t.Errorf("PCP = %v, want *false", d.ClientSupports.PCP)
	}
	if d.DERPLatency["Frankfurt"].LatencyMs != 1.0 {
		t.Errorf("DERP latency lost: %+v", d.DERPLatency)
	}
}

// TestDevicesRich_DecodesMultipleConnections verifies the per-device
// `multipleConnections` flag is decoded. It has present-when-true semantics on
// the wire (verified against .capture/devices-rich-live-20260713.json: every
// live device omits it), so a device that omits the field must decode false.
func TestDevicesRich_DecodesMultipleConnections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if !devs[0].MultipleConnections {
		t.Error("devs[0].MultipleConnections = false, want true")
	}
	if devs[1].MultipleConnections {
		t.Error("devs[1].MultipleConnections = true, want false (field omitted)")
	}
}

// TestDevicesRich_PostureIdentity verifies the postureIdentity.disabled field
// is decoded, and that the object's mere presence is distinguishable from its
// absence: devs[0] carries a serialNumbers-only object (disabled omitted,
// decodes false, object still present), devs[1] carries disabled:true, and
// devs[2] has no postureIdentity key at all (nil).
func TestDevicesRich_PostureIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if devs[0].PostureIdentity == nil {
		t.Fatal("devs[0].PostureIdentity = nil, want present (serialNumbers-only object)")
	}
	if devs[0].PostureIdentity.Disabled {
		t.Error("devs[0].PostureIdentity.Disabled = true, want false (disabled key absent)")
	}
	if devs[1].PostureIdentity == nil || !devs[1].PostureIdentity.Disabled {
		t.Errorf("devs[1].PostureIdentity = %+v, want present with Disabled=true", devs[1].PostureIdentity)
	}
	if devs[2].PostureIdentity != nil {
		t.Errorf("devs[2].PostureIdentity = %+v, want nil (no postureIdentity key on the wire)", devs[2].PostureIdentity)
	}
}

// TestDevicesRich_PostureIdentitySerialNumbersNeverDecoded guards the seam
// freeze: postureIdentity.serialNumbers must never surface anywhere in the
// decoded RichDevice, even though devs[0]'s wire payload carries one. Marshal
// the decoded value back to JSON — a generic scan rather than a field-by-field
// check, so it also catches a future field added without the same fencing —
// and assert the serial string is gone.
func TestDevicesRich_PostureIdentitySerialNumbersNeverDecoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(devicesRichFixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	raw, err := json.Marshal(devs[0])
	if err != nil {
		t.Fatalf("marshal devs[0]: %v", err)
	}
	if strings.Contains(string(raw), "TESTSERIAL123ABC") {
		t.Fatalf("serial number leaked into decoded RichDevice: %s", raw)
	}
}

func TestDevicePostureAttributes_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"attributes":{}}`))
	}))
	defer srv.Close()

	got, err := newClient(t, srv.URL).DevicePostureAttributes(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DevicePostureAttributes: %v", err)
	}
	if len(got.Attributes) != 0 {
		t.Fatalf("attrs = %v, want empty", got.Attributes)
	}
	if len(got.Expiries) != 0 {
		t.Fatalf("Expiries = %v, want empty", got.Expiries)
	}
}
