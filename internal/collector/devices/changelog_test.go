package devices_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

const deviceChangeEvent = "tailscale.device.change"

func changeDevice() tsapi.RichDevice {
	return tsapi.RichDevice{
		ID:                "device-1",
		NodeID:            "node-1",
		Name:              "laptop.tailnet.ts.net",
		Hostname:          "laptop",
		OS:                "linux",
		User:              "alice@example.com",
		ClientVersion:     "1.80.0",
		Tags:              []string{"tag:work", "tag:laptop"},
		AdvertisedRoutes:  []string{"10.0.0.0/24", "0.0.0.0/0"},
		EnabledRoutes:     []string{"10.0.0.0/24"},
		Expires:           time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Distro:            tsapi.DistroInfo{Version: "24.04"},
		KeyExpiryDisabled: false,
	}
}

func collectChanges(t *testing.T, c *devices.Collector, rec *telemetrytest.Recorder) {
	t.Helper()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
}

func changeLogs(rec *telemetrytest.Recorder) []telemetrytest.LogRecord {
	var out []telemetrytest.LogRecord
	for _, log := range rec.LogRecords() {
		if log.EventName == deviceChangeEvent {
			out = append(out, log)
		}
	}
	return out
}

func TestChangeLogBaselineAndFieldChanges(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{changeDevice()}}
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithChangeLog(true))
	rec := telemetrytest.New()

	// The first successful poll is a silent baseline, even though it contains a
	// full fleet. Reordering set-like fields alone must not create a change.
	collectChanges(t, c, rec)
	if got := changeLogs(rec); len(got) != 0 {
		t.Fatalf("baseline logs = %d, want 0", len(got))
	}
	api.devices[0].Tags = []string{"tag:laptop", "tag:work"}
	api.devices[0].AdvertisedRoutes = []string{"0.0.0.0/0", "10.0.0.0/24"}
	api.devices[0].EnabledRoutes = []string{"10.0.0.0/24"}
	collectChanges(t, c, rec)
	if got := changeLogs(rec); len(got) != 0 {
		t.Fatalf("set-order-only logs = %d, want 0", len(got))
	}

	api.devices[0].Name = "renamed.tailnet.ts.net"
	api.devices[0].Hostname = "renamed"
	api.devices[0].OS = "windows"
	api.devices[0].Distro.Version = "11"
	api.devices[0].User = "bob@example.com"
	api.devices[0].ClientVersion = "1.81.0"
	api.devices[0].Tags = []string{"tag:new"}
	api.devices[0].AdvertisedRoutes = []string{"10.1.0.0/24"}
	api.devices[0].EnabledRoutes = []string{"10.1.0.0/24"}
	api.devices[0].Expires = api.devices[0].Expires.Add(24 * time.Hour)
	api.devices[0].KeyExpiryDisabled = true
	collectChanges(t, c, rec)

	logs := changeLogs(rec)
	if len(logs) != 11 {
		t.Fatalf("changed fields = %d, want 11: %+v", len(logs), logs)
	}
	wantFields := []string{
		"name", "hostname", "os", "os_version", "user", "client_version",
		"tags", "routes_advertised", "routes_enabled", "key_expiry", "key_expiry_disabled",
	}
	for i, log := range logs {
		if log.Attrs["host.id"] != "device-1" {
			t.Errorf("log %d host.id = %q, want device-1", i, log.Attrs["host.id"])
		}
		if log.Attrs["tailscale.device.change"] != "changed" {
			t.Errorf("log %d change = %q, want changed", i, log.Attrs["tailscale.device.change"])
		}
		if got := log.Attrs["tailscale.device.field"]; got != wantFields[i] {
			t.Errorf("log %d field = %q, want %q", i, got, wantFields[i])
		}
		if log.Attrs["tailscale.audit.old"] == "" || log.Attrs["tailscale.audit.new"] == "" {
			t.Errorf("log %d missing before/after values: %+v", i, log.Attrs)
		}
	}
	if got := logs[0].Attrs["tailscale.audit.old"]; got != "laptop.tailnet.ts.net" {
		t.Errorf("name old = %q, want laptop.tailnet.ts.net", got)
	}
	if got := logs[0].Attrs["tailscale.audit.new"]; got != "renamed.tailnet.ts.net" {
		t.Errorf("name new = %q, want renamed.tailnet.ts.net", got)
	}

	collectChanges(t, c, rec)
	if got := len(changeLogs(rec)); got != 11 {
		t.Fatalf("unchanged follow-up logs = %d, want 11 total", got)
	}
	// The event is a structured record, and all emitted keys are cataloged.
	telemetrytest.AssertCatalogAttrs(t, rec, nil, devices.LogCatalog())
}

func TestChangeLogAddRemoveAndReappearance(t *testing.T) {
	d1 := changeDevice()
	d2 := changeDevice()
	d2.ID = "device-2"
	d2.NodeID = "node-2"
	d2.Hostname = "desktop"
	api := &fakeAPI{devices: []tsapi.RichDevice{d1}}
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithChangeLog(true))
	rec := telemetrytest.New()

	collectChanges(t, c, rec)
	api.devices = []tsapi.RichDevice{d1, d2}
	collectChanges(t, c, rec)
	logs := changeLogs(rec)
	if len(logs) != 1 || logs[0].Attrs["host.id"] != "device-2" || logs[0].Attrs["tailscale.device.change"] != "added" {
		t.Fatalf("addition logs = %+v, want one added device-2", logs)
	}
	if logs[0].Attrs["tailscale.device.field"] != "device" || logs[0].Attrs["tailscale.audit.new"] == "" {
		t.Fatalf("addition record = %+v, want device field and new state", logs[0])
	}

	api.devices = []tsapi.RichDevice{d2}
	collectChanges(t, c, rec)
	logs = changeLogs(rec)
	if len(logs) != 2 || logs[1].Attrs["host.id"] != "device-1" || logs[1].Attrs["tailscale.device.change"] != "removed" {
		t.Fatalf("removal logs = %+v, want one removed device-1", logs)
	}
	if logs[1].Attrs["tailscale.device.field"] != "device" || logs[1].Attrs["tailscale.audit.old"] == "" {
		t.Fatalf("removal record = %+v, want device field and old state", logs[1])
	}

	// A reappearing identity is an addition, not a comparison against stale
	// state retained from before its removal.
	api.devices = []tsapi.RichDevice{d1, d2}
	collectChanges(t, c, rec)
	logs = changeLogs(rec)
	if len(logs) != 3 || logs[2].Attrs["host.id"] != "device-1" || logs[2].Attrs["tailscale.device.change"] != "added" {
		t.Fatalf("reappearance logs = %+v, want added device-1", logs)
	}
}

func TestChangeLogRestartRebaselinesSilently(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{changeDevice()}}
	rec := telemetrytest.New()

	first := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithChangeLog(true))
	collectChanges(t, first, rec)
	api.devices[0].Hostname = "changed-before-restart"

	// The new collector has no persisted state by design. Its first successful
	// observation is the new baseline, so a restart cannot dump the whole fleet.
	restarted := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithChangeLog(true))
	collectChanges(t, restarted, rec)
	if got := len(changeLogs(rec)); got != 0 {
		t.Fatalf("post-restart baseline logs = %d, want 0", got)
	}
	collectChanges(t, restarted, rec)
	if got := len(changeLogs(rec)); got != 0 {
		t.Fatalf("unchanged post-restart logs = %d, want 0", got)
	}
}

func TestChangeLogFailedPollDoesNotAdvanceBaseline(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{changeDevice()}}
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithChangeLog(true))
	rec := telemetrytest.New()
	collectChanges(t, c, rec)

	api.devices[0].ClientVersion = "1.82.0"
	api.err = errDeviceChangePoll
	if err := c.Collect(context.Background(), rec.Emitter()); !errors.Is(err, errDeviceChangePoll) {
		t.Fatalf("failed Collect error = %v, want %v", err, errDeviceChangePoll)
	}
	if got := len(changeLogs(rec)); got != 0 {
		t.Fatalf("failed poll logs = %d, want 0", got)
	}

	api.err = nil
	collectChanges(t, c, rec)
	logs := changeLogs(rec)
	if len(logs) != 1 || logs[0].Attrs["tailscale.device.field"] != "client_version" {
		t.Fatalf("post-failure logs = %+v, want one client_version change", logs)
	}
}

func TestChangeLogPIIUsesExistingFilters(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{changeDevice()}}
	api.devices[0].Name = "private-before.tailnet.ts.net"
	api.devices[0].Hostname = "private-before"
	api.devices[0].User = "private-before@example.com"
	rec := telemetrytest.NewWithPII(deviceChangePIIOff())
	c := devices.New(api, enrich.NewDeviceCache(), 0, false, false, devices.WithChangeLog(true))
	collectChanges(t, c, rec)

	api.devices[0].Name = "private-after.tailnet.ts.net"
	api.devices[0].Hostname = "private-after"
	api.devices[0].User = "private-after@example.com"
	collectChanges(t, c, rec)
	logs := changeLogs(rec)
	if len(logs) != 3 {
		t.Fatalf("PII change logs = %d, want name/hostname/user changes", len(logs))
	}
	for _, log := range logs {
		for key, value := range log.Attrs {
			if strings.Contains(value, "private-") || strings.Contains(value, "@example.com") {
				t.Errorf("PII value survived filter in %s=%q: %+v", key, value, log.Attrs)
			}
		}
		if strings.Contains(log.Body, "private-") || strings.Contains(log.Body, "@example.com") {
			t.Errorf("PII value survived in body %q", log.Body)
		}
	}
	// All three control surfaces are exercised: host.name/tailscale.node.hostname,
	// tailscale.user, and before/after free-text values.
	for _, log := range logs {
		if _, ok := log.Attrs["host.name"]; ok {
			t.Errorf("host.name survived hostname filter: %+v", log.Attrs)
		}
		if _, ok := log.Attrs["tailscale.node.hostname"]; ok {
			t.Errorf("tailscale.node.hostname survived hostname filter: %+v", log.Attrs)
		}
		if _, ok := log.Attrs["tailscale.user"]; ok {
			t.Errorf("tailscale.user survived email filter: %+v", log.Attrs)
		}
		if _, ok := log.Attrs["tailscale.audit.old"]; ok {
			t.Errorf("audit.old survived free-text filter: %+v", log.Attrs)
		}
		if _, ok := log.Attrs["tailscale.audit.new"]; ok {
			t.Errorf("audit.new survived free-text filter: %+v", log.Attrs)
		}
	}
}

func deviceChangePIIOff() pii.Categories {
	c := pii.Categories{}
	for _, category := range pii.AllCategories {
		c[category] = true
	}
	c[pii.CatHostnames] = false
	c[pii.CatNodeIDs] = false
	c[pii.CatEmails] = false
	c[pii.CatFreeTextDetails] = false
	return c
}

var errDeviceChangePoll = &deviceChangePollError{}

type deviceChangePollError struct{}

func (*deviceChangePollError) Error() string { return "device change poll failed" }
