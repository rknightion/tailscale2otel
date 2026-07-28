package app

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/app/apicontract"
	"github.com/rknightion/tailscale2otel/v3/internal/app/eventsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// updateExportSchema/-update-baseline mirror internal/app/apicontract's own
// flags (contract_test.go), but scoped to flowsExportEnvelope — the ONE
// published response type that lives here in package app rather than in an
// exported leaf package, because /api/flows/export.json's envelope is
// unexported (admin_flows_export.go). Everything else about the contract
// (BuildSchema/Flatten/CompareBaseline, the schema/baseline file pair under
// docs/api/schemas/) is shared with apicontract's own tests via its exported
// helpers.
//
// Regenerate: go test ./internal/app -run TestFlowsExportSchemaInSync -update
// Acknowledge a breaking change: go test ./internal/app -run TestFlowsExportSchemaIsBackwardCompatible -update-baseline
var updateExportSchema = flag.Bool("update", false, "rewrite docs/api/schemas/flows-export.schema.json")
var updateExportBaseline = flag.Bool("update-baseline", false, "rewrite docs/api/schemas/flows-export.baseline.json — a DELIBERATE breaking-change acknowledgement")

func flowsExportSchemaPath() string {
	return filepath.Join("..", "..", "docs", "api", "schemas", "flows-export.schema.json")
}

func flowsExportBaselinePath() string {
	return filepath.Join("..", "..", "docs", "api", "schemas", "flows-export.baseline.json")
}

// TestFlowsExportSchemaInSync is the drift gate for /api/flows/export.json's
// envelope, mirroring apicontract.TestSchemasInSync for the other five
// responses (see that package's contract_test.go for the full rationale).
func TestFlowsExportSchemaInSync(t *testing.T) {
	typ := reflect.TypeFor[flowsExportEnvelope]()
	schema := apicontract.BuildSchema(typ)
	rendered := apicontract.RenderSchema(
		"tailscale2otel /api/flows/export.json",
		"The provenance-carrying JSON export of the filtered recent-connection ring, served by GET /api/flows/export.json. See docs/api/compatibility.md.",
		schema,
	)
	apicontract.AssertFileInSync(t, flowsExportSchemaPath(), rendered, *updateExportSchema)
}

// TestFlowsExportSchemaIsBackwardCompatible is the breaking-change gate for
// the same envelope, mirroring apicontract.TestSchemasAreBackwardCompatible.
func TestFlowsExportSchemaIsBackwardCompatible(t *testing.T) {
	typ := reflect.TypeFor[flowsExportEnvelope]()
	current := apicontract.Flatten(apicontract.BuildSchema(typ))
	path := flowsExportBaselinePath()
	if *updateExportBaseline {
		b := apicontract.Baseline{SchemaVersion: flowsExportSchemaVersion, Paths: current}
		if err := apicontract.WriteGolden(path, apicontract.RenderBaseline(b)); err != nil {
			t.Fatalf("write baseline %s: %v", path, err)
		}
		t.Logf("regenerated baseline %s (schema_version=%d, %d paths) — this is a DELIBERATE breaking-change acknowledgement",
			path, flowsExportSchemaVersion, len(current))
		return
	}
	baseline, err := apicontract.LoadBaseline(path)
	if err != nil {
		t.Fatalf("%v — bootstrap it with -update-baseline", err)
	}
	if broken := apicontract.CompareBaseline(baseline, flowsExportSchemaVersion, current); len(broken) > 0 {
		t.Errorf("breaking change(s) against the published compatibility baseline %s:\n  %s\n"+
			"acceptable only as a DELIBERATE change: bump flowsExportSchemaVersion, then re-run with -update-baseline",
			path, apicontract.JoinLines(broken))
	}
}

// --- End-to-end wiring: schema_version must actually be ON THE WIRE -----
//
// The struct-level tests above prove the SHAPE is published and guarded; they
// say nothing about whether SchemaVersion is actually SET on a response the
// real composition root serves — a field that exists on the type but is
// never assigned would pass every test above while /api/status.json still
// omitted it. Each of these drives buildAdminServer() (the real
// composition root, not a hand-built DTO) over httptest and decodes the
// response, matching this package's existing TestStatusJSON_* pattern.
// Verified by mutation: deleting the `SchemaVersion: ...Version,` assignment
// this lane added to status.go/admin_flows.go/admin_events.go/
// admin_flows_export.go makes the corresponding case below fail with
// "schema_version = 0, want 1" (checked by hand during development; the
// assertion itself is what a reviewer can re-run to confirm).

func TestAdminServer_SchemaVersionsAreWiredOnTheWire(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	get := func(t *testing.T, path string) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = adminLoopbackHost
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		return w.Body.Bytes()
	}

	t.Run("status", func(t *testing.T) {
		var got statusdata.Status
		if err := json.Unmarshal(get(t, "/api/status.json"), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SchemaVersion != statusdata.StatusSchemaVersion {
			t.Errorf("status.schema_version = %d, want %d", got.SchemaVersion, statusdata.StatusSchemaVersion)
		}
		if got.Config.SchemaVersion != statusdata.ConfigSummarySchemaVersion {
			t.Errorf("status.config.schema_version = %d, want %d", got.Config.SchemaVersion, statusdata.ConfigSummarySchemaVersion)
		}
		if got.Cardinality.SchemaVersion != statusdata.CardinalitySchemaVersion {
			t.Errorf("status.cardinality.schema_version = %d, want %d", got.Cardinality.SchemaVersion, statusdata.CardinalitySchemaVersion)
		}
	})

	t.Run("config", func(t *testing.T) {
		var got statusdata.ConfigSummary
		if err := json.Unmarshal(get(t, "/api/config.json"), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SchemaVersion != statusdata.ConfigSummarySchemaVersion {
			t.Errorf("config.schema_version = %d, want %d", got.SchemaVersion, statusdata.ConfigSummarySchemaVersion)
		}
	})

	t.Run("cardinality", func(t *testing.T) {
		var got statusdata.CardinalityInfo
		if err := json.Unmarshal(get(t, "/api/cardinality.json"), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SchemaVersion != statusdata.CardinalitySchemaVersion {
			t.Errorf("cardinality.schema_version = %d, want %d", got.SchemaVersion, statusdata.CardinalitySchemaVersion)
		}
	})

	t.Run("flows", func(t *testing.T) {
		var got flowsdata.Response
		if err := json.Unmarshal(get(t, "/api/flows.json"), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SchemaVersion != flowsdata.SchemaVersion {
			t.Errorf("flows.schema_version = %d, want %d", got.SchemaVersion, flowsdata.SchemaVersion)
		}
	})

	t.Run("flows export json", func(t *testing.T) {
		var got struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(get(t, "/api/flows/export.json"), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SchemaVersion != flowsExportSchemaVersion {
			t.Errorf("flows export.schema_version = %d, want %d", got.SchemaVersion, flowsExportSchemaVersion)
		}
	})

	t.Run("events", func(t *testing.T) {
		var got eventsdata.Response
		if err := json.Unmarshal(get(t, "/api/events.json"), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SchemaVersion != eventsdata.SchemaVersion {
			t.Errorf("events.schema_version = %d, want %d", got.SchemaVersion, eventsdata.SchemaVersion)
		}
	})
}
