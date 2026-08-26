package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// persistCfg is a config with the flow view on and persistence pointed at dir.
func persistCfg(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Flows.Enabled = true
	cfg.Admin.Enabled = true
	cfg.Admin.LandingPage = true
	cfg.Flows.Store.Directory = dir
	return cfg
}

// An empty flows.store.directory must keep the historical in-memory behavior, since
// that is the stateless default the whole feature is opt-in relative to.
func TestNewFlowStoreDefaultsToMemory(t *testing.T) {
	cfg := config.Default()
	cfg.Flows.Enabled = true
	cfg.Admin.Enabled = true
	cfg.Admin.LandingPage = true

	store, _ := newFlowStore(cfg, "acme.example.com", discardLogger())
	if store == nil {
		t.Fatal("newFlowStore returned nil with the view enabled")
	}
	t.Cleanup(func() { _ = store.Close() })

	if got := store.Stats().Backend.Kind; got != flowstore.BackendMemory {
		t.Fatalf("Backend.Kind = %q, want %q", got, flowstore.BackendMemory)
	}
}

func TestNewFlowStoreBuildsPersistentBackend(t *testing.T) {
	dir := t.TempDir()
	store, _ := newFlowStore(persistCfg(t, dir), "acme.example.com", discardLogger())
	if store == nil {
		t.Fatal("newFlowStore returned nil with flows.store.directory set")
	}
	t.Cleanup(func() { _ = store.Close() })

	b := store.Stats().Backend
	if b.Kind != flowstore.BackendSQLite {
		t.Fatalf("Backend.Kind = %q, want %q", b.Kind, flowstore.BackendSQLite)
	}
	if !b.Healthy {
		t.Fatalf("Backend.Healthy = false, error = %q", b.Error)
	}
	// The file keeps a readable slug but includes a digest so distinct tailnet
	// names whose slugs collide cannot share a database.
	if base := filepath.Base(b.Path); !strings.HasPrefix(base, "flows-acme-example-com-") || !strings.HasSuffix(base, ".db") {
		t.Fatalf("Backend.Path = %q, want readable slug plus identity digest", b.Path)
	}
}

// Two tailnets must not share a database: a device name is unique only within a
// tailnet, so a shared file would merge two different machines into one vertex.
func TestNewFlowStorePerTailnetFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := persistCfg(t, dir)

	a, _ := newFlowStore(cfg, "acme.example.com", discardLogger())
	b, _ := newFlowStore(cfg, "beta.example.com", discardLogger())
	if a == nil || b == nil {
		t.Fatal("newFlowStore returned nil")
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	if pa, pb := a.Stats().Backend.Path, b.Stats().Backend.Path; pa == pb {
		t.Fatalf("both tailnets share %q, want distinct files", pa)
	}
}

// A store that cannot be opened must disable the view rather than silently
// serving the in-memory ring — an operator who configured persistence and sees
// a working /flows would otherwise believe they had history they do not have.
func TestNewFlowStoreOpenFailureDisablesViewNotFallsBackToMemory(t *testing.T) {
	// A path that exists as a FILE cannot be created as a directory.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	if err := writeFileForTest(blocked); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}

	store, err := newFlowStore(persistCfg(t, filepath.Join(blocked, "sub")), "acme.example.com", discardLogger())
	if err == nil {
		t.Fatal("newFlowStore error = nil, want the open failure so the status page can report it")
	}
	if store != nil {
		t.Fatalf("newFlowStore returned %T, want nil so the view 404s", store)
	}
}

// The persistent path must apply pii_filter, because rows outlive the process
// and land in backups. The memory path deliberately does not (#241).
func TestFlowRedactorStripsDisabledCategories(t *testing.T) {
	cats := piiCategories(config.Default().PIIFilter)
	if fn := flowRedactor(cats); fn != nil {
		t.Fatal("flowRedactor returned non-nil with every category enabled")
	}

	f := config.Default().PIIFilter
	f.Emails = false
	redact := flowRedactor(piiCategories(f))
	if redact == nil {
		t.Fatal("flowRedactor returned nil with emails disabled")
	}

	o := flowstore.Observation{
		Time:    time.Now(),
		SrcUser: "someone@example.com",
		DstUser: "other@example.com",
		SrcNode: "laptop",
		Counts:  flowstore.Counts{TxBytes: 1},
	}
	redact(&o)

	if o.SrcUser != "" || o.DstUser != "" {
		t.Fatalf("emails survived redaction: src=%q dst=%q", o.SrcUser, o.DstUser)
	}
	// Redaction must be surgical: a non-PII field losing its value would make the
	// row useless rather than redacted.
	if o.SrcNode != "laptop" {
		t.Fatalf("SrcNode = %q, want it untouched by the emails category", o.SrcNode)
	}
	if o.Counts.TxBytes != 1 {
		t.Fatalf("Counts.TxBytes = %d, want 1", o.Counts.TxBytes)
	}
}

// The export provenance label must name the store that actually answered, so an
// integration cannot mistake a bounded ring for a retention-bounded database.
func TestExportProvenanceNamesBackend(t *testing.T) {
	mem := flowstore.NewMemory(60)
	t.Cleanup(func() { _ = mem.Close() })
	if src, note := exportProvenance(mem); src != exportSource || note == "" {
		t.Fatalf("memory provenance = %q/%q, want %q with a note", src, note, exportSource)
	}

	disk, _ := newFlowStore(persistCfg(t, t.TempDir()), "acme.example.com", discardLogger())
	if disk == nil {
		t.Fatal("persistent store was not built")
	}
	t.Cleanup(func() { _ = disk.Close() })

	src, note := exportProvenance(disk)
	if src != exportSourceSQLite {
		t.Fatalf("sqlite provenance = %q, want %q", src, exportSourceSQLite)
	}
	if note == "" {
		t.Fatal("sqlite provenance note is empty")
	}
}

// A persistent store must report the export cap, not the memory ring's, or the
// export would stop at 2000 rows on a database holding millions.
func TestPersistentLimitsReportExportCap(t *testing.T) {
	cfg := persistCfg(t, t.TempDir())
	cfg.Flows.Store.MaxExportRows = 1234

	store, _ := newFlowStore(cfg, "acme.example.com", discardLogger())
	if store == nil {
		t.Fatal("persistent store was not built")
	}
	t.Cleanup(func() { _ = store.Close() })

	if got := maxExportRows(store); got != 1234 {
		t.Fatalf("maxExportRows() = %d, want 1234", got)
	}
}

// writeFileForTest creates an empty regular file, used to block a directory
// creation so the open-failure path can be exercised without relying on
// permissions (which behave differently for root in a container).
func writeFileForTest(path string) error {
	f, err := os.Create(path) //nolint:gosec // test-only, path from t.TempDir
	if err != nil {
		return err
	}
	return f.Close()
}

// The window clamp must follow the ACTIVE store's retention. Clamping a
// persistent store against flows.retention (capped at 24h) would make the
// multi-day history this feature exists to provide unreachable through the only
// UI that reads it — the query would silently come back trimmed to a day.
func TestFlowRetentionFollowsActiveBackend(t *testing.T) {
	a := &App{cfg: config.Default()}
	a.cfg.Flows.Retention = config.Duration(6 * time.Hour)

	if got := a.flowRetention(); got != 6*time.Hour {
		t.Fatalf("memory flowRetention() = %v, want 6h", got)
	}

	a.cfg.Flows.Store.Directory = "/var/lib/tailscale2otel/flows"
	a.cfg.Flows.Store.Retention = config.Duration(30 * 24 * time.Hour)

	if got := a.flowRetention(); got != 30*24*time.Hour {
		t.Fatalf("persistent flowRetention() = %v, want 720h — a 24h clamp would "+
			"make multi-day history unreachable", got)
	}
}

// The status page and /api/status.json must say WHICH backend is answering, and
// must not report the in-memory ring's bucket/capacity numbers for a store that
// has neither — a healthy persistent store rendering "0 of 0 buckets" and
// "0 B estimated" reads as broken rather than as not-applicable.
func TestFlowStoreInfoReportsPersistentBackend(t *testing.T) {
	cfg := persistCfg(t, t.TempDir())
	store, _ := newFlowStore(cfg, "acme.example.com", discardLogger())
	if store == nil {
		t.Fatal("persistent store was not built")
	}
	t.Cleanup(func() { _ = store.Close() })

	a := &App{cfg: cfg, runtimes: []*tailnetRuntime{{name: "acme.example.com", flowStore: store}}}
	info := a.flowStoreInfo()

	if !info.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if info.Backend.Kind != flowstore.BackendSQLite {
		t.Fatalf("Backend.Kind = %q, want %q", info.Backend.Kind, flowstore.BackendSQLite)
	}
	if !info.Backend.Persistent {
		t.Fatal("Backend.Persistent = false: the template keys its whole layout off this")
	}
	if !info.Backend.Healthy {
		t.Fatalf("Backend.Healthy = false, errors = %v", info.Backend.Errors)
	}
	// Retention must be the DISK retention, not the ring's.
	if want := cfg.Flows.Store.Retention.D().String(); info.Retention != want {
		t.Fatalf("Retention = %q, want %q", info.Retention, want)
	}
}

// The memory path must be unchanged by all of the above.
func TestFlowStoreInfoReportsMemoryBackend(t *testing.T) {
	cfg := config.Default()
	cfg.Flows.Enabled = true
	cfg.Admin.Enabled = true
	cfg.Admin.LandingPage = true

	store, _ := newFlowStore(cfg, "acme.example.com", discardLogger())
	if store == nil {
		t.Fatal("memory store was not built")
	}
	t.Cleanup(func() { _ = store.Close() })

	a := &App{cfg: cfg, runtimes: []*tailnetRuntime{{name: "acme.example.com", flowStore: store}}}
	info := a.flowStoreInfo()

	if info.Backend.Kind != flowstore.BackendMemory {
		t.Fatalf("Backend.Kind = %q, want %q", info.Backend.Kind, flowstore.BackendMemory)
	}
	if info.Backend.Persistent {
		t.Fatal("Backend.Persistent = true for the in-memory ring")
	}
	if info.Capacity == 0 {
		t.Fatal("Capacity = 0: the memory ring must still report its bucket capacity")
	}
}
