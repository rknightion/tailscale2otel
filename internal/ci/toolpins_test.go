package ci_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/repotest"
	"go.yaml.in/yaml/v3"
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

// TestToolVersionsHaveOneOwner guards the actual workflow inputs and the
// Renovate extraction against a second source of tool versions.
func TestToolVersionsHaveOneOwner(t *testing.T) {
	root := repositoryRoot(t)
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	justfile := read("justfile")
	renovate := read("renovate.json")
	paths, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.y*ml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find workflows: %v", err)
	}
	workflows := make(map[string][]byte, len(paths))
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		workflows[filepath.Base(path)] = b
	}
	if err := checkToolVersionOwnership(justfile, renovate, workflows); err != nil {
		t.Fatal(err)
	}

	ci := string(workflows["ci.yml"])
	controls := []struct {
		name           string
		just, renovate []byte
		ci             string
	}{
		{"literal action version", justfile, renovate, strings.Replace(ci, "${{ steps.pins.outputs.golangci }}", "v2.15.0", 1)},
		{"padded literal action version", justfile, renovate, strings.Replace(ci, "${{ steps.pins.outputs.golangci }}", "'  v2.15.0  '", 1)},
		{"literal govulncheck install", justfile, renovate, strings.Replace(ci, "$(just --evaluate govulncheck_version)", "v1.3.0", 1)},
		{"missing lint annotation", []byte(strings.Replace(string(justfile), "# renovate: datasource=github-releases depName=golangci/golangci-lint\n", "", 1)), renovate, ci},
		{"missing vuln annotation", []byte(strings.Replace(string(justfile), "# renovate: datasource=go depName=golang.org/x/vuln\n", "", 1)), renovate, ci},
		{"broken renovate pattern", justfile, []byte(strings.Replace(string(renovate), "(?<currentValue>", "(?<brokenValue>", 1)), ci},
	}
	for _, tc := range controls {
		t.Run(tc.name, func(t *testing.T) {
			mutated := make(map[string][]byte, len(workflows))
			for k, v := range workflows {
				mutated[k] = v
			}
			mutated["ci.yml"] = []byte(tc.ci)
			if err := checkToolVersionOwnership(tc.just, tc.renovate, mutated); err == nil {
				t.Fatal("mutation was not detected")
			}
		})
	}
}

func TestLintPinReadFailsClosed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["lint"].Steps {
		if step.ID == "pins" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("missing lint pin step")
	}
	for _, tc := range []struct {
		name, fake string
		wantOK     bool
	}{
		{"failed evaluation", "exit 1", false},
		{"empty evaluation", "exit 0", false},
		{"valid evaluation", "echo v2.15.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "just"), []byte("#!/bin/sh\n"+tc.fake+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "output")
			cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GITHUB_OUTPUT="+output)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantOK {
				t.Fatalf("exit=%v output=%s", err, out)
			}
			if tc.wantOK {
				b, err := os.ReadFile(output)
				if err != nil || string(b) != "golangci=v2.15.0\n" {
					t.Fatalf("output=%q err=%v", b, err)
				}
			}
		})
	}
}

func checkToolVersionOwnership(justfile, renovate []byte, workflows map[string][]byte) error {
	literalVersion := regexp.MustCompile(`^v?[0-9]`)
	literalVuln := regexp.MustCompile(`govulncheck@v[0-9]`)
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			if run, ok := v["run"].(string); ok && literalVuln.MatchString(run) {
				return fmt.Errorf("literal govulncheck version in run")
			}
			if uses, ok := v["uses"].(string); ok && strings.HasPrefix(uses, "golangci/golangci-lint-action@") {
				if inputs, ok := v["with"].(map[string]any); ok {
					if version, ok := inputs["version"].(string); ok && literalVersion.MatchString(strings.TrimSpace(version)) {
						return fmt.Errorf("literal golangci-lint action version")
					}
				}
			}
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for name, data := range workflows {
		var doc any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := walk(doc); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	var config struct {
		CustomManagers []struct {
			CustomType          string   `json:"customType"`
			ManagerFilePatterns []string `json:"managerFilePatterns"`
			MatchStrings        []string `json:"matchStrings"`
		} `json:"customManagers"`
	}
	if err := json.Unmarshal(renovate, &config); err != nil {
		return err
	}
	pins := []struct{ name, datasource, dep string }{{"golangci_version", "github-releases", "golangci/golangci-lint"}, {"govulncheck_version", "go", "golang.org/x/vuln"}}
	for _, pin := range pins {
		annotation := "# renovate: datasource=" + pin.datasource + " depName=" + pin.dep + "\n" + pin.name + " := \""
		if !strings.Contains(string(justfile), annotation) {
			return fmt.Errorf("missing annotation for %s", pin.name)
		}
		assignment := regexp.MustCompile(`(?m)^` + pin.name + ` := "([^"]+)"$`).FindSubmatch(justfile)
		if assignment == nil {
			return fmt.Errorf("missing pin %s", pin.name)
		}
		found := false
		for _, manager := range config.CustomManagers {
			if manager.CustomType != "regex" {
				continue
			}
			applies := false
			for _, p := range manager.ManagerFilePatterns {
				if p == "/^justfile$/" {
					applies = true
				}
			}
			if !applies {
				continue
			}
			for _, pattern := range manager.MatchStrings {
				re, err := regexp.Compile(pattern)
				if err != nil {
					return err
				}
				for _, match := range re.FindAllStringSubmatch(string(justfile), -1) {
					groups := map[string]string{}
					for i, name := range re.SubexpNames() {
						if name != "" {
							groups[name] = match[i]
						}
					}
					if groups["datasource"] == pin.datasource && groups["depName"] == pin.dep && groups["currentValue"] == string(assignment[1]) {
						found = true
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("Renovate does not extract %s", pin.name)
		}
	}
	return nil
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
	root, err := repotest.Root()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
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
