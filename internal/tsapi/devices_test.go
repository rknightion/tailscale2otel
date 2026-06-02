package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// devicesRichFixture mirrors a trimmed /devices?fields=all response: one online
// device with DERP latency and distro, one offline minimal device, and one
// external device.
const devicesRichFixture = `{"devices":[
  {
    "id":"3690401478992208","nodeId":"nDdiWAbPpV11CNTRL","name":"laptop.example.ts.net",
    "hostname":"laptop","os":"linux","user":"rob@example.com","clientVersion":"1.99.0",
    "addresses":["100.64.0.1","fd7a::1"],
    "authorized":true,"isExternal":false,"updateAvailable":true,"keyExpiryDisabled":false,
    "connectedToControl":true,"blocksIncomingConnections":false,"sshEnabled":true,
    "created":"2026-03-24T15:45:46Z","lastSeen":"2026-06-02T21:50:25Z","expires":"2026-09-20T15:45:46Z",
    "advertisedRoutes":["10.0.0.0/24"],"enabledRoutes":["10.0.0.0/24"],
    "distro":{"name":"ubuntu","version":"24.04","codeName":"noble"},
    "clientConnectivity":{"latency":{"Frankfurt":{"preferred":true,"latencyMs":1.0156919999999998},"Amsterdam":{"latencyMs":8.675937999999999}}}
  },
  {
    "id":"346670268899695","nodeId":"nt5gYXS1i311CNTRL","name":"alex.example.ts.net",
    "hostname":"alex","os":"macOS","user":"alex@example.com","clientVersion":"1.80.0",
    "addresses":["100.64.0.2"],
    "authorized":true,"isExternal":false,"updateAvailable":false,"keyExpiryDisabled":false,
    "connectedToControl":false,"blocksIncomingConnections":true,"sshEnabled":false,
    "created":"2026-01-01T00:00:00Z","lastSeen":"2026-05-24T09:38:04Z","expires":"2026-08-26T19:09:04Z",
    "distro":{}
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
	if d0.Hostname != "laptop" || d0.OS != "linux" || d0.User != "rob@example.com" {
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

func TestDevicePostureAttributes_DecodesMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/device/dev123/attributes" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"attributes":{"custom:foo":"bar","node:os":"linux","posture:latestMacOSVersion":true,"num":3}}`))
	}))
	defer srv.Close()

	attrs, err := newClient(t, srv.URL).DevicePostureAttributes(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DevicePostureAttributes: %v", err)
	}
	if attrs["custom:foo"] != "bar" {
		t.Fatalf("custom:foo = %v", attrs["custom:foo"])
	}
	if attrs["node:os"] != "linux" {
		t.Fatalf("node:os = %v", attrs["node:os"])
	}
	if attrs["posture:latestMacOSVersion"] != true {
		t.Fatalf("posture flag = %v", attrs["posture:latestMacOSVersion"])
	}
}

func TestDevicePostureAttributes_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"attributes":{}}`))
	}))
	defer srv.Close()

	attrs, err := newClient(t, srv.URL).DevicePostureAttributes(context.Background(), "dev123")
	if err != nil {
		t.Fatalf("DevicePostureAttributes: %v", err)
	}
	if len(attrs) != 0 {
		t.Fatalf("attrs = %v, want empty", attrs)
	}
}
