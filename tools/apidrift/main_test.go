package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_OperationDiscoveryFlagsNewReadOperation(t *testing.T) {
	dir := t.TempDir()
	liveSpec := filepath.Join(dir, "live.json")
	if err := os.WriteFile(liveSpec, []byte(`{
  "openapi": "3.0.0",
  "paths": {
    "/brand-new": {
      "get": {
        "operationId": "brandNewRead",
        "summary": "A newly published read endpoint",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write live spec: %v", err)
	}

	bin := filepath.Join(dir, "apidrift")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build apidrift: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "-operations", "-new", liveSpec, "-format", "md")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("operation discovery exit = %v, want 3; output:\n%s", err, out)
	}
	report := string(out)
	for _, want := range []string{"brandNewRead", "READ-CAPABLE"} {
		if !strings.Contains(report, want) {
			t.Errorf("operation discovery report missing %q:\n%s", want, report)
		}
	}
}
