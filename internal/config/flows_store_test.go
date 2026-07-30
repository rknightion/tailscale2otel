package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// #294: flows.store.* configures the opt-in on-disk backend
// (internal/flowstore/sqlitestore). The section is absent from a bare config,
// so every default here MUST equal that package's Default* constants —
// wiring them any other way silently produces a store that behaves
// differently from what the operator's config says.
func TestDefaults_FlowsStore(t *testing.T) {
	c := config.Default()

	if c.Flows.Store.Directory != "" {
		t.Errorf("flows.store.directory default = %q, want empty (memory-only)", c.Flows.Store.Directory)
	}
	if want := 30 * 24 * time.Hour; c.Flows.Store.Retention.D() != want {
		t.Errorf("flows.store.retention default = %v, want %v", c.Flows.Store.Retention.D(), want)
	}
	if want := int64(5_000_000); c.Flows.Store.MaxRows != want {
		t.Errorf("flows.store.max_rows default = %d, want %d", c.Flows.Store.MaxRows, want)
	}
	if want := 50_000; c.Flows.Store.MaxExportRows != want {
		t.Errorf("flows.store.max_export_rows default = %d, want %d", c.Flows.Store.MaxExportRows, want)
	}
	if want := 8192; c.Flows.Store.QueueSize != want {
		t.Errorf("flows.store.queue_size default = %d, want %d", c.Flows.Store.QueueSize, want)
	}
	if want := 512; c.Flows.Store.BatchSize != want {
		t.Errorf("flows.store.batch_size default = %d, want %d", c.Flows.Store.BatchSize, want)
	}
	if want := 5 * time.Second; c.Flows.Store.FlushInterval.D() != want {
		t.Errorf("flows.store.flush_interval default = %v, want %v", c.Flows.Store.FlushInterval.D(), want)
	}
	if want := 15 * time.Second; c.Flows.Store.QueryTimeout.D() != want {
		t.Errorf("flows.store.query_timeout default = %v, want %v", c.Flows.Store.QueryTimeout.D(), want)
	}
	if want := time.Hour; c.Flows.Store.SweepInterval.D() != want {
		t.Errorf("flows.store.sweep_interval default = %v, want %v", c.Flows.Store.SweepInterval.D(), want)
	}

	// A bare-default config must validate cleanly: the section is inert
	// because path is empty.
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() on defaults = %v, want nil", err)
	}
}

// validFlowsStore returns a Config with flows.enabled=true and a
// flows.store.* section populated entirely with in-bounds values, so a single
// field can be pushed out of range per test case without every other check
// firing too.
func validFlowsStore(t *testing.T) *config.Config {
	t.Helper()
	c := config.Default()
	c.Flows.Enabled = true
	c.Flows.Store.Directory = t.TempDir()
	c.Flows.Store.Retention = config.Duration(30 * 24 * time.Hour)
	c.Flows.Store.MaxRows = 5_000_000
	c.Flows.Store.MaxExportRows = 50_000
	c.Flows.Store.QueueSize = 8192
	c.Flows.Store.BatchSize = 512
	c.Flows.Store.FlushInterval = config.Duration(5 * time.Second)
	c.Flows.Store.QueryTimeout = config.Duration(15 * time.Second)
	c.Flows.Store.SweepInterval = config.Duration(time.Hour)
	return c
}

// Every flows.store.* check must be a complete no-op when path is empty —
// that is the documented "memory only" state, and there is nothing to
// validate.
func TestValidate_FlowsStoreSkippedWhenPathEmpty(t *testing.T) {
	c := config.Default()
	c.Flows.Enabled = true
	c.Flows.Store.Directory = "" // stays the default
	// Every other field pushed absurdly out of range: must not matter.
	c.Flows.Store.Retention = config.Duration(-time.Hour)
	c.Flows.Store.MaxRows = -1
	c.Flows.Store.MaxExportRows = -1
	c.Flows.Store.QueueSize = -1
	c.Flows.Store.BatchSize = -1
	c.Flows.Store.FlushInterval = config.Duration(-time.Hour)
	c.Flows.Store.QueryTimeout = config.Duration(-time.Hour)
	c.Flows.Store.SweepInterval = config.Duration(-time.Hour)

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with flows.store.directory empty = %v, want nil (section is inert)", err)
	}
}

// Also inert when flows.enabled=false, even with a path set — the store is
// never built either way.
func TestValidate_FlowsStoreSkippedWhenFlowsDisabled(t *testing.T) {
	c := validFlowsStore(t)
	c.Flows.Enabled = false
	c.Flows.Store.MaxRows = -1

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with flows.enabled=false = %v, want nil (section is inert)", err)
	}
}

// flows.store.directory follows #310's path convention rather than rejecting
// relative values: it is registered in pathFields(), so a relative path resolves
// against the config file's own directory exactly as ingress_wal.directory does.
// An earlier revision required an absolute path, which would have made this the
// one filesystem field in the config that behaved differently from every other.
func TestValidate_FlowsStoreDirectoryAcceptsRelativeAndAbsolute(t *testing.T) {
	for _, dir := range []string{"/var/lib/tailscale2otel/flows", "flows", "./data/flows"} {
		t.Run(dir, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.Directory = dir

			if err := c.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil: relative paths are resolved, not rejected", err)
			}
		})
	}
}

func TestValidate_FlowsStoreRetentionBounds(t *testing.T) {
	tests := []struct {
		name      string
		retention time.Duration
		wantErr   bool
	}{
		{name: "minimum", retention: time.Hour},
		{name: "maximum", retention: 365 * 24 * time.Hour},
		{name: "below floor", retention: 59 * time.Minute, wantErr: true},
		{name: "above ceiling", retention: 366 * 24 * time.Hour, wantErr: true},
		{name: "zero", retention: 0, wantErr: true},
		{name: "negative", retention: -time.Hour, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.Retention = config.Duration(tc.retention)

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for retention %v", tc.retention)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.retention"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

func TestValidate_FlowsStoreMaxRowsBounds(t *testing.T) {
	tests := []struct {
		name    string
		maxRows int64
		wantErr bool
	}{
		{name: "minimum", maxRows: 10_000},
		{name: "maximum", maxRows: 1_000_000_000},
		{name: "below floor", maxRows: 9_999, wantErr: true},
		{name: "above ceiling", maxRows: 1_000_000_001, wantErr: true},
		{name: "zero", maxRows: 0, wantErr: true},
		{name: "negative", maxRows: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.MaxRows = tc.maxRows

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for max_rows %d", tc.maxRows)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.max_rows"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

func TestValidate_FlowsStoreMaxExportRowsBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "minimum", value: 100},
		{name: "maximum", value: 1_000_000},
		{name: "below floor", value: 99, wantErr: true},
		{name: "above ceiling", value: 1_000_001, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.MaxExportRows = tc.value

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for max_export_rows %d", tc.value)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.max_export_rows"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

func TestValidate_FlowsStoreQueueSizeBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "minimum", value: 64},
		{name: "maximum", value: 1_048_576},
		{name: "below floor", value: 63, wantErr: true},
		{name: "above ceiling", value: 1_048_577, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.QueueSize = tc.value
			// Keep batch_size legal relative to the new queue_size so this test
			// isolates the queue_size bound rather than tripping the cross-field
			// rule too.
			if c.Flows.Store.BatchSize > tc.value && tc.value > 0 {
				c.Flows.Store.BatchSize = 1
			}

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for queue_size %d", tc.value)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.queue_size"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

func TestValidate_FlowsStoreBatchSizeBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "minimum", value: 1},
		{name: "maximum", value: 100_000, wantErr: true}, // exceeds the 8192 default queue_size
		{name: "below floor", value: 0, wantErr: true},
		{name: "negative", value: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.BatchSize = tc.value

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for batch_size %d", tc.value)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.batch_size"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

// The cross-field rule: batch_size must not exceed queue_size, independent of
// either bound being individually satisfied.
func TestValidate_FlowsStoreBatchSizeExceedsQueueSize(t *testing.T) {
	c := validFlowsStore(t)
	c.Flows.Store.QueueSize = 100
	c.Flows.Store.BatchSize = 101 // both individually in-bounds, but 101 > 100

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error: batch_size exceeds queue_size")
	}
	if !strings.Contains(err.Error(), "flows.store.batch_size") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestValidate_FlowsStoreBatchSizeEqualsQueueSizeIsAllowed(t *testing.T) {
	c := validFlowsStore(t)
	c.Flows.Store.QueueSize = 100
	c.Flows.Store.BatchSize = 100 // equal is fine, only exceeding is rejected

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil: batch_size == queue_size must be allowed", err)
	}
}

func TestValidate_FlowsStoreFlushIntervalBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Duration
		wantErr bool
	}{
		{name: "minimum", value: 100 * time.Millisecond},
		{name: "maximum", value: 5 * time.Minute},
		{name: "below floor", value: 99 * time.Millisecond, wantErr: true},
		{name: "above ceiling", value: 5*time.Minute + time.Second, wantErr: true},
		{name: "zero", value: 0, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.FlushInterval = config.Duration(tc.value)

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for flush_interval %v", tc.value)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.flush_interval"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

func TestValidate_FlowsStoreQueryTimeoutBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Duration
		wantErr bool
	}{
		{name: "minimum", value: time.Second},
		{name: "maximum", value: 5 * time.Minute},
		{name: "below floor", value: 999 * time.Millisecond, wantErr: true},
		{name: "above ceiling", value: 5*time.Minute + time.Second, wantErr: true},
		{name: "zero", value: 0, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.QueryTimeout = config.Duration(tc.value)

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for query_timeout %v", tc.value)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.query_timeout"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

func TestValidate_FlowsStoreSweepIntervalBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Duration
		wantErr bool
	}{
		{name: "minimum", value: time.Minute},
		{name: "maximum", value: 24 * time.Hour},
		{name: "below floor", value: 59 * time.Second, wantErr: true},
		{name: "above ceiling", value: 25 * time.Hour, wantErr: true},
		{name: "zero", value: 0, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validFlowsStore(t)
			c.Flows.Store.SweepInterval = config.Duration(tc.value)

			err := c.Validate()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Validate() = nil, want an error for sweep_interval %v", tc.value)
			case !tc.wantErr && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr && !strings.Contains(err.Error(), "flows.store.sweep_interval"):
				t.Errorf("error %q does not name the offending key", err)
			}
		})
	}
}

// flows.store.directory being set while /flows itself has no consumer is dead
// configuration, mirroring TestWarnings_FlowsUnreachableWithoutAdminPage.
func TestWarnings_FlowsStoreUnreachableWithoutAdminPage(t *testing.T) {
	tests := []struct {
		name        string
		flowsOn     bool
		adminOn     bool
		landingPage bool
		wantWarn    bool
	}{
		{name: "reachable", flowsOn: true, adminOn: true, landingPage: true},
		{name: "flows disabled", flowsOn: false, adminOn: true, landingPage: true, wantWarn: true},
		{name: "admin off", flowsOn: true, adminOn: false, landingPage: true, wantWarn: true},
		{name: "landing page off", flowsOn: true, adminOn: true, landingPage: false, wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Flows.Enabled = tc.flowsOn
			c.Flows.Store.Directory = t.TempDir()
			c.Admin.Enabled = tc.adminOn
			c.Admin.LandingPage = tc.landingPage

			var found bool
			for _, w := range c.Warnings() {
				if strings.Contains(w, "flows.store.directory is set but has no effect") {
					found = true
				}
			}
			if found != tc.wantWarn {
				t.Errorf("flows.store.directory unreachable warning present = %v, want %v", found, tc.wantWarn)
			}
		})
	}
}

// The data-at-rest warning fires whenever a path is set, and ONLY then —
// unlike the "unreachable" warning above it does not depend on whether the
// admin page can actually reach the store, because the exposure (writing PII
// to disk) exists the moment the process starts persisting rows.
func TestWarnings_FlowsStoreDataAtRest(t *testing.T) {
	c := config.Default()
	c.Flows.Enabled = true
	c.Admin.Enabled = true
	c.Admin.LandingPage = true
	c.Flows.Store.Directory = "" // memory-only default

	for _, w := range c.Warnings() {
		if strings.Contains(w, "will be written to disk") {
			t.Errorf("unexpected data-at-rest warning with flows.store.directory empty: %q", w)
		}
	}

	dir := t.TempDir()
	c.Flows.Store.Directory = dir
	var found bool
	for _, w := range c.Warnings() {
		if strings.Contains(w, "will be written to disk") {
			found = true
			if !strings.Contains(w, "email addresses") {
				t.Errorf("data-at-rest warning does not mention identities: %q", w)
			}
			if !strings.Contains(w, dir) {
				t.Errorf("data-at-rest warning does not name the configured path: %q", w)
			}
		}
	}
	if !found {
		t.Error("expected a data-at-rest warning once flows.store.directory is set, found none")
	}
}

// TS2OTEL_FLOWS__STORE__DIRECTORY must reach Flows.Store.Directory, proving the nested
// struct participates in the standard env-override layer.
func TestEnv_FlowsStoreDirectory(t *testing.T) {
	t.Setenv("TS2OTEL_TAILSCALE__TAILNET", "example.ts.net")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__METHOD", "apikey")
	t.Setenv("TS2OTEL_TAILSCALE__AUTH__APIKEY", "tskey-api-xxxxx")
	dir := t.TempDir()
	t.Setenv("TS2OTEL_FLOWS__STORE__DIRECTORY", dir)

	c, err := config.Load("")
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if c.Flows.Store.Directory != dir {
		t.Errorf("Flows.Store.Directory = %q, want %q", c.Flows.Store.Directory, dir)
	}
}

// The point of following the convention: a relative flows.store.directory is
// resolved against the CONFIG FILE's directory, not the process working
// directory. This is what registering the field in pathFields() buys, and it is
// the behavior ingress_wal.directory and checkpoint.file_path already have.
func TestFlowsStoreDirectoryResolvesRelativeToConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "" +
		"tailscale:\n" +
		"  tailnet: example.ts.net\n" +
		"  auth:\n" +
		"    method: apikey\n" +
		"    apikey: tskey-api-xxxxx\n" +
		"flows:\n" +
		"  store:\n" +
		"    directory: flows\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	want := filepath.Join(dir, "flows")
	if c.Flows.Store.Directory != want {
		t.Fatalf("Flows.Store.Directory = %q, want %q (resolved against the config file, "+
			"not the working directory)", c.Flows.Store.Directory, want)
	}
}
