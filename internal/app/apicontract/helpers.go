package apicontract

import (
	"os"
	"strings"
	"testing"
)

// AssertFileInSync is the shared drift-gate body: rendered must byte-for-byte
// equal the committed file at path. When update is true the file is
// (re)written instead of compared — wire a package's own -update flag to it,
// mirroring internal/config's TestConfigSchemaInSync. Exported so both this
// package's own contract_test.go and internal/app's test for the one
// response type that lives in package app (flowsExportEnvelope, unexported)
// share one implementation.
func AssertFileInSync(t *testing.T, path string, rendered []byte, update bool) {
	t.Helper()
	if update {
		if err := WriteGolden(path, rendered); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("regenerated %s", path)
		return
	}
	current, err := os.ReadFile(path) //nolint:gosec // G304: path is a fixed, code-controlled generated-artifact location
	if err != nil {
		t.Fatalf("read %s: %v — regenerate with -update", path, err)
	}
	if string(current) != string(rendered) {
		t.Errorf("%s is out of date with the live Go type — regenerate with -update and commit the result", path)
	}
}

// WriteGolden writes a generated artifact, world-readable like every other
// generated doc in this repo (config.schema.json, docs/metrics.md, ...).
func WriteGolden(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644) //nolint:gosec // G306: generated schema/baseline files are intentionally world-readable
}

// JoinLines renders a list of messages as an indented multi-line block for a
// test failure — every caller of CompareBaseline wants the same formatting.
func JoinLines(lines []string) string {
	return strings.Join(lines, "\n  ")
}
