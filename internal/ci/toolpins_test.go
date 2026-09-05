package ci_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// These tests exercise the same boundary that protects the generation tools:
// just lint and just vuln must inspect the binary they are about to run. A
// stale local install otherwise reports a green result from a different rule
// or vulnerability database than the pinned CI tool.
func TestToolPinAssertions(t *testing.T) {
	root := repositoryRoot(t)
	justPath, err := exec.LookPath("just")
	if err != nil {
		t.Fatalf("just is required for the pin assertion tests: %v", err)
	}
	pins := readToolPins(t, root)

	cases := []toolPinCase{
		{
			name:       "golangci-lint",
			binary:     "golangci-lint",
			recipe:     "lint",
			versionArg: "version",
			version: func(pin string) string {
				return fmt.Sprintf("golangci-lint has version %s built with go1.27.1 from (unknown) on (unknown)", strings.TrimPrefix(pin, "v"))
			},
		},
		{
			name:       "govulncheck",
			binary:     "govulncheck",
			recipe:     "vuln",
			versionArg: "-version",
			version: func(pin string) string {
				return fmt.Sprintf("Go: go1.27.1\nScanner: govulncheck@%s\nDB: https://vuln.go.dev", pin)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+" rejects wrong version", func(t *testing.T) {
			pin := pins[tc.name]
			wrong := "99.99.99"
			if strings.TrimPrefix(pin, "v") == wrong {
				wrong = "0.0.0"
			}
			result := runPinnedTool(t, root, justPath, tc, tc.version(fmt.Sprintf("v%s", wrong)))
			if result.err == nil {
				t.Fatalf("just %s accepted %s at the wrong version; stdout=%q stderr=%q", tc.recipe, tc.name, result.stdout, result.stderr)
			}
			if !strings.Contains(result.stderr, pin) {
				t.Errorf("just %s error does not name the justfile pin %q: %s", tc.recipe, pin, result.stderr)
			}
			if !strings.Contains(result.stderr, "just setup") {
				t.Errorf("just %s error does not name the fix command `just setup`: %s", tc.recipe, result.stderr)
			}
			if result.invoked {
				t.Errorf("just %s reached the tool invocation after rejecting its version", tc.recipe)
			}
		})

		t.Run(tc.name+" rejects prerelease", func(t *testing.T) {
			result := runPinnedTool(t, root, justPath, tc, tc.version(pins[tc.name]+"-rc.1"))
			if result.err == nil || result.invoked {
				t.Fatalf("just %s accepted a prerelease of its pin", tc.recipe)
			}
		})

		t.Run(tc.name+" reaches matching version", func(t *testing.T) {
			result := runPinnedTool(t, root, justPath, tc, tc.version(pins[tc.name]))
			if result.err != nil {
				t.Fatalf("just %s rejected the pinned %s: stdout=%q stderr=%q: %v", tc.recipe, tc.name, result.stdout, result.stderr, result.err)
			}
			if !result.invoked {
				t.Fatalf("just %s passed the pin check without reaching %s", tc.recipe, tc.name)
			}
		})
	}
}

type toolPinCase struct {
	name       string
	binary     string
	recipe     string
	versionArg string
	version    func(string) string
}

type toolRunResult struct {
	stdout  string
	stderr  string
	err     error
	invoked bool
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate toolpins_test.go")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func readToolPins(t *testing.T, root string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("read justfile: %v", err)
	}
	re := regexp.MustCompile(`(?m)^(golangci_version|govulncheck_version)\s*:=\s*"([^"]+)"\s*$`)
	pins := make(map[string]string, 2)
	for _, match := range re.FindAllStringSubmatch(string(b), -1) {
		switch match[1] {
		case "golangci_version":
			pins["golangci-lint"] = match[2]
		case "govulncheck_version":
			pins["govulncheck"] = match[2]
		}
	}
	if len(pins) != 2 {
		t.Fatalf("justfile pins were not found for both tools: %v", pins)
	}
	return pins
}

func runPinnedTool(t *testing.T, root, justPath string, tc toolPinCase, versionOutput string) toolRunResult {
	t.Helper()
	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "invoked")
	shim := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = %q ]; then\nprintf '%%s\\n' \"$TOOLPIN_VERSION_OUTPUT\"\nexit 0\nfi\nprintf '%%s\\n' invoked >> \"$TOOLPIN_MARKER\"\nexit 0\n", tc.versionArg)
	shimPath := filepath.Join(shimDir, tc.binary)
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatalf("write %s shim: %v", tc.binary, err)
	}

	cmd := exec.Command(justPath, tc.recipe, ".")
	cmd.Dir = root
	cmd.Env = toolTestEnv(shimDir, marker, versionOutput)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	_, statErr := os.Stat(marker)
	invoked := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		t.Fatalf("stat invocation marker: %v", statErr)
	}
	return toolRunResult{stdout: stdout.String(), stderr: stderr.String(), err: err, invoked: invoked}
}

func toolTestEnv(shimDir, marker, versionOutput string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "JUST_YES=") || strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "TOOLPIN_MARKER=") || strings.HasPrefix(entry, "TOOLPIN_VERSION_OUTPUT=") {
			continue
		}
		env = append(env, entry)
	}
	pathValue := shimDir
	if path := os.Getenv("PATH"); path != "" {
		pathValue += string(os.PathListSeparator) + path
	}
	env = append(env, "PATH="+pathValue, "TOOLPIN_MARKER="+marker, "TOOLPIN_VERSION_OUTPUT="+versionOutput)
	return env
}
