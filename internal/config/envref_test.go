package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateEnvDoc regenerates the committed docs/env-vars.md golden file instead of
// asserting it. Run: go test ./internal/config -run TestEnvReferenceDocInSync -update
var updateEnvDoc = flag.Bool("update", false, "rewrite generated golden files (docs/env-vars.md)")

func TestEnvVarName(t *testing.T) {
	cases := map[string]string{
		"log_level":                           "TS2OTEL_LOG_LEVEL",
		"tailscale.tailnet":                   "TS2OTEL_TAILSCALE__TAILNET",
		"tailscale.auth.oauth.client_id":      "TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID",
		"collectors.flowlogs.interval":        "TS2OTEL_COLLECTORS__FLOWLOGS__INTERVAL",
		"cardinality.flow.metrics_mode":       "TS2OTEL_CARDINALITY__FLOW__METRICS_MODE",
		"self_observability.instance_id":      "TS2OTEL_SELF_OBSERVABILITY__INSTANCE_ID",
		"collectors.node_metrics.drop_labels": "TS2OTEL_COLLECTORS__NODE_METRICS__DROP_LABELS",
	}
	for key, want := range cases {
		if got := envVarName(key); got != want {
			t.Errorf("envVarName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestEnvReferenceRowsClassification(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	rows, err := envReferenceRows(example)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	byKey := map[string]envRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}

	// A plain scalar: env-settable, default + description carried from the file.
	tn, ok := byKey["tailscale.tailnet"]
	if !ok {
		t.Fatal("tailscale.tailnet row missing")
	}
	if tn.FileOnly || tn.List {
		t.Errorf("tailscale.tailnet should be a plain scalar, got %+v", tn)
	}
	if tn.EnvVar != "TS2OTEL_TAILSCALE__TAILNET" || tn.Default != "-" {
		t.Errorf("tailscale.tailnet env/default = %q/%q", tn.EnvVar, tn.Default)
	}
	if tn.Desc == "" {
		t.Error("tailscale.tailnet description not carried from the example comment")
	}

	// A []string field: env-settable and flagged as a comma-separated list.
	sc := byKey["tailscale.auth.oauth.scopes"]
	if !sc.List || sc.FileOnly || sc.EnvVar == "" {
		t.Errorf("scopes should be a list env var, got %+v", sc)
	}

	// Structured values are file-only (no env var).
	for _, k := range []string{"otlp.headers", "profiling.pyroscope.tags", "collectors.node_metrics.targets"} {
		if r := byKey[k]; !r.FileOnly || r.EnvVar != "" {
			t.Errorf("%s should be file-only with no env var, got %+v", k, r)
		}
	}
}

func TestRenderEnvReferenceEscapesAndLists(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	block, err := renderEnvReference(example)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// log_level's comment contains pipes ("debug | info | …") — they must be
	// escaped so they don't break the markdown table.
	if strings.Contains(block, "debug | info") {
		t.Error("unescaped pipe in a table cell would break the markdown table")
	}
	if !strings.Contains(block, "TS2OTEL_LOG_LEVEL") {
		t.Error("expected log_level env var in the rendered table")
	}
	// File-only fields appear only in the trailing note, never as a row.
	if strings.Contains(block, "| `` |") {
		t.Error("a file-only field leaked into the table with an empty env var")
	}
	if !strings.Contains(block, "**File-only**") || !strings.Contains(block, "`otlp.headers`") {
		t.Error("file-only note missing")
	}
}

// TestEnvReferenceDocInSync is the drift gate: docs/env-vars.md must equal the
// table generated from config.example.yaml. It rides the normal `go test` run
// (no separate tool/module). Regenerate with `scripts/regen-generated.sh envref`
// or `go test ./internal/config -run TestEnvReferenceDocInSync -update`.
func TestEnvReferenceDocInSync(t *testing.T) {
	exPath := filepath.Join("..", "..", "config.example.yaml")
	docPath := filepath.Join("..", "..", "docs", "env-vars.md")

	example, err := os.ReadFile(exPath)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	block, err := renderEnvReference(example)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	current, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read docs/env-vars.md: %v", err)
	}
	want, err := spliceEnvReference(string(current), block)
	if err != nil {
		t.Fatalf("splice: %v", err)
	}

	if *updateEnvDoc {
		if err := os.WriteFile(docPath, []byte(want), 0o644); err != nil { //nolint:gosec // G306: generated docs file is intentionally world-readable
			t.Fatalf("write docs/env-vars.md: %v", err)
		}
		t.Logf("regenerated %s", docPath)
		return
	}
	if want != string(current) {
		t.Errorf("docs/env-vars.md is out of date with config.example.yaml — regenerate with " +
			"`scripts/regen-generated.sh envref` (or `go test ./internal/config -run TestEnvReferenceDocInSync -update`) and commit the result")
	}
}
