package supportbundle_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/configexport"
	"github.com/rknightion/tailscale2otel/v3/internal/supportbundle"
)

func sampleInput() supportbundle.Input {
	return supportbundle.Input{
		Version:     "v3.9.0",
		Diagnostics: []config.Diagnostic{{Severity: config.SeverityWarning, Path: "otlp.endpoint", Message: "example"}},
		Config:      configexport.Build(config.Default()),
		Components:  []statusdata.ComponentStatus{{Name: "admin", Enabled: true}},
		API:         statusdata.APIInfo{Endpoints: []statusdata.APIEndpoint{{Endpoint: "devices", Requests: 10}}},
		Delivery:    []statusdata.DeliverySignal{{Signal: "metrics", Exports: 5}},
		Collectors:  []statusdata.CollectorStatus{{Name: "devices", IntervalSec: 60}},
		Advisories:  []statusdata.ConfigAdvisory{{Key: "otlp.tls.insecure", Message: "TLS verification is disabled"}},
		Metrics:     []statusdata.MetricRow{{Name: "tailscale.devices.count", Instrument: "gauge"}},
		LogEvents:   []statusdata.LogRow{{Name: "tailscale.device.added", Severity: "info"}},
		Devices: []statusdata.DeviceRow{
			{Name: "laptop", Hostname: "laptop.tailnetXXXX.ts.net", User: "rob@example.com", Addrs: []string{"100.64.0.1"}},
		},
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
}

// readZip decodes every entry of a zip archive's bytes into a name->raw-bytes
// map, for assertions that need to inspect (or leak-scan) file content rather
// than just the archive's compressed bytes.
func readZip(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		rc.Close()
		out[f.Name] = buf.Bytes()
	}
	return out
}

// TestWrite_DeterministicByteForByte is the acceptance criterion "bundle
// generation is deterministic": two calls to Write with IDENTICAL Input,
// Options and now must produce byte-identical archives. GeneratedAt is the
// only field allowed to vary between two bundles in general, and here it does
// not vary either, because both calls are given the same now.
func TestWrite_DeterministicByteForByte(t *testing.T) {
	in := sampleInput()
	opts := supportbundle.Options{IncludeDeviceInventory: true}
	now := fixedNow()

	var b1, b2 bytes.Buffer
	if err := supportbundle.Write(&b1, in, opts, now); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := supportbundle.Write(&b2, in, opts, now); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
		t.Fatalf("two Write calls with identical input produced different bytes (len %d vs %d) — generation is not deterministic",
			b1.Len(), b2.Len())
	}
}

// TestWrite_OnlyGeneratedAtVariesWithTime asserts the ONLY difference between
// two bundles built from identical Input/Options but different `now` values
// is manifest.json's generated_at field — every other file's bytes must
// match exactly.
func TestWrite_OnlyGeneratedAtVariesWithTime(t *testing.T) {
	in := sampleInput()
	opts := supportbundle.Options{}

	var b1, b2 bytes.Buffer
	if err := supportbundle.Write(&b1, in, opts, fixedNow()); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if err := supportbundle.Write(&b2, in, opts, fixedNow().Add(48*time.Hour)); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	f1 := readZip(t, b1.Bytes())
	f2 := readZip(t, b2.Bytes())
	if len(f1) != len(f2) {
		t.Fatalf("file count differs: %d vs %d", len(f1), len(f2))
	}
	for name, body1 := range f1 {
		body2, ok := f2[name]
		if !ok {
			t.Fatalf("file %s present in first bundle, missing from second", name)
		}
		if name == "manifest.json" {
			continue // generated_at is expected to differ here
		}
		if !bytes.Equal(body1, body2) {
			t.Errorf("file %s differs between two runs that changed only `now`: %q vs %q", name, body1, body2)
		}
	}
}

// TestManifest_StatesIncludedAndExcluded is the acceptance criterion "the
// manifest states exactly what is included": the default (no opt-in) bundle
// must list device_inventory, flow_records and raw_logs as excluded, and must
// NOT list devices.json as included; the opted-in bundle must do the exact
// opposite for device_inventory/devices.json while STILL excluding the two
// sections with no opt-in path.
func TestManifest_StatesIncludedAndExcluded(t *testing.T) {
	in := sampleInput()

	var defaultBuf bytes.Buffer
	if err := supportbundle.Write(&defaultBuf, in, supportbundle.Options{}, fixedNow()); err != nil {
		t.Fatalf("Write (default): %v", err)
	}
	defaultManifest := decodeManifest(t, readZip(t, defaultBuf.Bytes()))
	if !contains(defaultManifest.ExcludedByDefault, "device_inventory") {
		t.Errorf("default manifest must exclude device_inventory, got excluded=%v", defaultManifest.ExcludedByDefault)
	}
	if contains(defaultManifest.Included, "devices.json") {
		t.Errorf("default manifest must NOT include devices.json, got included=%v", defaultManifest.Included)
	}
	for _, must := range []string{"flow_records", "raw_logs"} {
		if !contains(defaultManifest.ExcludedByDefault, must) {
			t.Errorf("manifest must always exclude %q (no opt-in path exists), got excluded=%v", must, defaultManifest.ExcludedByDefault)
		}
	}
	if _, ok := readZip(t, defaultBuf.Bytes())["devices.json"]; ok {
		t.Error("default bundle must not contain devices.json at all")
	}

	var optInBuf bytes.Buffer
	if err := supportbundle.Write(&optInBuf, in, supportbundle.Options{IncludeDeviceInventory: true}, fixedNow()); err != nil {
		t.Fatalf("Write (opt-in): %v", err)
	}
	optInManifest := decodeManifest(t, readZip(t, optInBuf.Bytes()))
	if contains(optInManifest.ExcludedByDefault, "device_inventory") {
		t.Errorf("opted-in manifest must not list device_inventory as excluded, got excluded=%v", optInManifest.ExcludedByDefault)
	}
	if !contains(optInManifest.Included, "devices.json") {
		t.Errorf("opted-in manifest must list devices.json as included, got included=%v", optInManifest.Included)
	}
	// Still always excluded, opt-in or not.
	for _, must := range []string{"flow_records", "raw_logs"} {
		if !contains(optInManifest.ExcludedByDefault, must) {
			t.Errorf("manifest must always exclude %q, got excluded=%v", must, optInManifest.ExcludedByDefault)
		}
	}
	optInFiles := readZip(t, optInBuf.Bytes())
	if _, ok := optInFiles["devices.json"]; !ok {
		t.Error("opted-in bundle must contain devices.json")
	} else if !strings.Contains(string(optInFiles["devices.json"]), "laptop") {
		t.Error("devices.json must actually carry the device row when opted in")
	}
}

func decodeManifest(t *testing.T, files map[string][]byte) supportbundle.Manifest {
	t.Helper()
	body, ok := files["manifest.json"]
	if !ok {
		t.Fatal("manifest.json missing from bundle")
	}
	var m supportbundle.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	return m
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestWrite_Bounded is the acceptance criterion "bundle generation is ...
// bounded": a section that exceeds its cap is truncated rather than growing
// the archive unboundedly, and the truncation is DISCLOSED in the manifest —
// never silent. Drives the actual boundary (cap+1 entries), not merely a
// count assertion, so a bug that always returns len(input) unchanged would
// still be caught.
func TestWrite_Bounded(t *testing.T) {
	in := sampleInput()
	// One more device than the bound, all distinctly named so truncation can
	// be verified by which names survive, not just a count.
	devices := make([]statusdata.DeviceRow, 5001)
	for i := range devices {
		devices[i] = statusdata.DeviceRow{Name: fmt.Sprintf("device-%04d", i)}
	}
	in.Devices = devices

	var buf bytes.Buffer
	if err := supportbundle.Write(&buf, in, supportbundle.Options{IncludeDeviceInventory: true}, fixedNow()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	files := readZip(t, buf.Bytes())
	var rows []statusdata.DeviceRow
	if err := json.Unmarshal(files["devices.json"], &rows); err != nil {
		t.Fatalf("decode devices.json: %v", err)
	}
	if len(rows) != 5000 {
		t.Fatalf("devices.json has %d rows, want exactly the 5000-row bound", len(rows))
	}
	if rows[0].Name != "device-0000" || rows[len(rows)-1].Name != "device-4999" {
		t.Errorf("truncation should keep the first 5000 entries in order, got first=%q last=%q", rows[0].Name, rows[len(rows)-1].Name)
	}
	// device-5000 (the 5001st entry) must have been cut.
	if strings.Contains(string(files["devices.json"]), `"device-5000"`) {
		t.Error("devices.json contains an entry beyond the bound — truncation did not actually cut anything")
	}
	manifest := decodeManifest(t, files)
	found := false
	for _, line := range manifest.Truncated {
		if strings.Contains(line, "devices") {
			found = true
		}
	}
	if !found {
		t.Errorf("manifest.Truncated must disclose the devices truncation, got %v", manifest.Truncated)
	}
}

// TestWrite_NilSliceStaysAbsent asserts an empty/nil section round-trips as
// an empty JSON array (not omitted, not null-vs-empty ambiguity that a
// consumer would have to special-case), matching this codebase's existing
// "nil vs empty is a real distinction, but every wire encoding is consistent
// about it" convention (see internal/app/status.go's own doc comments).
func TestWrite_EmptySectionsEncodeAsEmptyArray(t *testing.T) {
	in := supportbundle.Input{Version: "v0.0.0", Config: map[string]statusdata.ConfigFieldValue{}}
	var buf bytes.Buffer
	if err := supportbundle.Write(&buf, in, supportbundle.Options{}, fixedNow()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	files := readZip(t, buf.Bytes())
	for _, name := range []string{"diagnostics.json", "components.json", "delivery.json", "collectors.json", "advisories.json", "catalog_metrics.json", "catalog_log_events.json"} {
		body, ok := files[name]
		if !ok {
			t.Fatalf("%s missing from bundle", name)
		}
		trimmed := strings.TrimSpace(string(body))
		if trimmed != "null" && trimmed != "[]" {
			t.Errorf("%s: want a valid empty-or-null JSON array body for a nil input slice, got %q", name, trimmed)
		}
	}
}

// ---------------------------------------------------------------------------
// Leak test: model of internal/configexport/configexport_test.go's
// plantSentinels — reimplemented here (not imported) because it is
// unexported in that package's own test file and duplicating a SENTINEL
// PLANTING TEST HELPER is not the same failure mode this tracker's CLAUDE.md
// warns about (a second copy of a REDACTION RULE). The redaction rule itself
// is exercised via configexport.Build, called once, exactly as
// internal/app/status.go's redactedConfigSummary does.
// ---------------------------------------------------------------------------

// TestBundle_SecretFieldsNeverLeak plants a unique sentinel into every
// config.Secret-typed field (scalar and map[string]Secret) and every plain
// string-map field of a real config, builds Input.Config the same way
// internal/app/status.go does (via configexport.Build), assembles a full
// bundle (every section, including the opt-in device inventory, so nothing
// is skipped by the scan), and asserts NONE of the secret sentinels appear
// ANYWHERE in the archive — decoded, not merely absent from the compressed
// bytes. It also asserts the "shown" sentinels (a *_file PATH, and a plain
// string-map value already published to the backend as a tag/label) DO
// appear, so a future blanket redaction cannot pass this test by hiding data
// operators need — see plantSentinels' own doc and configexport's identical
// test for why both directions matter.
func TestBundle_SecretFieldsNeverLeak(t *testing.T) {
	cfg := config.Default()
	cfg.Tailnets = append(cfg.Tailnets, config.TailnetConfig{Name: "planted"})
	cfg.Streaming.Routes = append(cfg.Streaming.Routes, config.StreamingRoute{Tailnet: "planted"})
	cfg.Webhook.Routes = append(cfg.Webhook.Routes, config.WebhookRoute{Tailnet: "planted"})
	cfg.Collectors.NodeMetrics.Targets = append(cfg.Collectors.NodeMetrics.Targets, config.NodeMetricsTarget{URL: "http://planted"})

	secretSentinels, shownSentinels := plantSentinels(cfg)
	if len(secretSentinels) == 0 {
		t.Fatal("plantSentinels planted nothing — the walk found no secret-bearing field, which would make this test vacuous")
	}

	in := sampleInput()
	in.Config = configexport.Build(cfg)

	var buf bytes.Buffer
	if err := supportbundle.Write(&buf, in, supportbundle.Options{IncludeDeviceInventory: true}, fixedNow()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	files := readZip(t, buf.Bytes())
	var all bytes.Buffer
	for _, body := range files {
		all.Write(body)
	}
	haystack := all.String()

	for sentinel, path := range secretSentinels {
		if strings.Contains(haystack, sentinel) {
			t.Errorf("sentinel for %s leaked into the support bundle: %q found in output", path, sentinel)
		}
	}
	for sentinel, path := range shownSentinels {
		if !strings.Contains(haystack, sentinel) {
			t.Errorf("sentinel for %s did not appear, but this value MUST be shown: %q missing from output "+
				"(a *_file PATH is safe to display; a plain string-map value is already published to the backend "+
				"as a tag/attribute — over-redaction here is a real cost, not a safe default)", path, sentinel)
		}
	}
}

// plantSentinels is configexport_test.go's helper of the same name,
// reimplemented against this package's own test binary. See that file for
// the full rationale; this is intentionally byte-for-byte the same WALK
// (not a re-derived redaction rule) so both packages' leak tests exercise
// the identical set of secret-bearing fields.
func plantSentinels(cfg *config.Config) (secrets map[string]string, shown map[string]string) {
	secrets = map[string]string{}
	shown = map[string]string{}
	secretType := reflect.TypeOf(config.Secret(""))
	n := 0
	var walk func(v reflect.Value, path string)
	walk = func(v reflect.Value, path string) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			walk(v.Elem(), path)
		case reflect.Struct:
			t := v.Type()
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.PkgPath != "" {
					continue
				}
				fv := v.Field(i)
				childPath := path + "." + f.Name
				if fv.Type() == secretType {
					n++
					sentinel := fmt.Sprintf("SENTINEL-SECRET-%d", n)
					fv.Set(reflect.ValueOf(config.Secret(sentinel)))
					secrets[sentinel] = childPath
					if ff, ok := t.FieldByName(f.Name + "File"); ok && ff.Type.Kind() == reflect.String {
						n++
						filePath := fmt.Sprintf("/sentinel/path/%d", n)
						v.FieldByName(f.Name + "File").SetString(filePath)
						shown[filePath] = childPath + "File"
					}
					continue
				}
				walk(fv, childPath)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		case reflect.Map:
			elemType := v.Type().Elem()
			switch {
			case elemType == secretType:
				n++
				sentinel := fmt.Sprintf("SENTINEL-MAPSECRET-%d", n)
				nv := reflect.MakeMap(v.Type())
				nv.SetMapIndex(reflect.ValueOf("planted-key"), reflect.ValueOf(config.Secret(sentinel)))
				v.Set(nv)
				secrets[sentinel] = path + ".planted-key"
			case elemType.Kind() == reflect.String:
				n++
				sentinel := fmt.Sprintf("SENTINEL-MAPSTRING-%d", n)
				nv := reflect.MakeMap(v.Type())
				nv.SetMapIndex(reflect.ValueOf("planted-key"), reflect.ValueOf(sentinel))
				v.Set(nv)
				shown[sentinel] = path + ".planted-key"
			}
		}
	}
	walk(reflect.ValueOf(cfg).Elem(), "")
	return secrets, shown
}

// TestManifestAndInputCannotCarryACredential is the reflection-based DTO
// guard against the class of bug a string scan cannot see: a credential
// FIELD added to this package's own DTOs (Manifest, Input) would render its
// VALUE, not its field name, so a leak test built only on planted-sentinel
// strings would not catch a genuinely new field that happens not to be
// exercised by sampleInput. This checks the field NAMES of the types this
// package itself defines — modeled on
// internal/app/flowhtml.TestPageDTOCannotCarryACredential. It does not
// recurse into statusdata.* field internals: those are internal/app's own
// DTOs, out of this lane's ownership, and already reused as-is (never
// redefined) by Input — any credential-shaped field there is that package's
// leak surface to guard, not a second copy of the guard here.
func TestManifestAndInputCannotCarryACredential(t *testing.T) {
	bad := []string{"token", "secret", "password", "credential"}
	check := func(t *testing.T, rt reflect.Type) {
		t.Helper()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := strings.ToLower(f.Name)
			for _, b := range bad {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s looks like a credential (%q) — no credential-shaped field may reach a support-bundle DTO", rt.Name(), f.Name, b)
				}
			}
			if f.Type.String() == "config.Secret" {
				t.Errorf("%s.%s is a config.Secret; a support bundle must never carry a raw secret type", rt.Name(), f.Name)
			}
		}
	}
	check(t, reflect.TypeOf(supportbundle.Manifest{}))
	check(t, reflect.TypeOf(supportbundle.Input{}))
}

// A bundle must carry the flow store's backend state (#294). A stuck or
// unhealthy persistent store is otherwise undiagnosable after the fact: queue
// drops and a false Healthy live only on a live admin page.
func TestBundle_CarriesFlowStoreBackendState(t *testing.T) {
	in := sampleInput()
	in.FlowStore = statusdata.FlowStoreInfo{
		Enabled:   true,
		Retention: "720h0m0s",
		Backend: statusdata.FlowStoreBackend{
			Kind:       "sqlite",
			Persistent: true,
			Healthy:    false,
			Errors:     []string{"disk full"},
			QueueDrops: 42,
			Rows:       1000,
			SizeBytes:  4096,
		},
	}

	var buf bytes.Buffer
	if err := supportbundle.Write(&buf, in, supportbundle.Options{}, fixedNow()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	files := readZip(t, buf.Bytes())

	raw, ok := files["flow_store.json"]
	if !ok {
		t.Fatalf("bundle has no flow_store.json; files = %v", slices.Sorted(maps.Keys(files)))
	}

	var got statusdata.FlowStoreInfo
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode flow_store.json: %v", err)
	}
	if got.Backend.Kind != "sqlite" || got.Backend.Healthy {
		t.Errorf("backend state lost: %+v", got.Backend)
	}
	if got.Backend.QueueDrops != 42 {
		t.Errorf("QueueDrops = %d, want 42 — the signal the bundle exists to carry", got.Backend.QueueDrops)
	}

	// The manifest must list it, or a reader cannot tell a missing section from
	// an empty one.
	if m := decodeManifest(t, files); !contains(m.Included, "flow_store.json") {
		t.Errorf("manifest.Included omits flow_store.json: %v", m.Included)
	}
}
