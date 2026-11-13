package ci_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type renovateRule struct {
	MatchDepTypes     []string `json:"matchDepTypes"`
	MatchUpdateTypes  []string `json:"matchUpdateTypes"`
	MatchFileNames    []string `json:"matchFileNames"`
	MatchPackageNames []string `json:"matchPackageNames"`
	Enabled           *bool    `json:"enabled"`
	Automerge         *bool    `json:"automerge"`
	AllowedVersions   string   `json:"allowedVersions"`
	GroupName         string   `json:"groupName"`
}

func renovateIndirectErrors(rules []renovateRule) []string {
	var errs []string
	enabled, major, otel := 0, 0, 0
	if len(rules) == 0 || rules[0].GroupName != "Go dependencies" {
		errs = append(errs, "Go dependencies group must be first")
	}
	for _, rule := range rules {
		if rule.GroupName == "OpenTelemetry" {
			otel++
			if rule.Automerge == nil || *rule.Automerge {
				errs = append(errs, "OpenTelemetry automerge must be disabled")
			}
		}
		if slices.Contains(rule.MatchPackageNames, "google.golang.org/grpc") && rule.AllowedVersions != "" {
			errs = append(errs, "stale gRPC version restriction")
		}
		if !slices.Equal(rule.MatchDepTypes, []string{"indirect"}) {
			continue
		}
		selected := false
		if len(rule.MatchUpdateTypes) == 0 && rule.Enabled != nil && *rule.Enabled {
			enabled++
			selected = true
		}
		if slices.Equal(rule.MatchUpdateTypes, []string{"major"}) && rule.Enabled != nil && !*rule.Enabled {
			major++
			selected = true
		}
		if selected && (!slices.Contains(rule.MatchFileNames, "go.mod") || !slices.Contains(rule.MatchFileNames, "tools/**/go.mod")) {
			errs = append(errs, "indirect rule must cover root and tool modules")
		}
	}
	if enabled != 1 || major != 1 || otel != 1 {
		errs = append(errs, fmt.Sprintf("rule counts enabled=%d major=%d otel=%d", enabled, major, otel))
	}
	return errs
}

func TestRenovateIndirectRulesCoverTheRootModule(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	decode := func() []renovateRule {
		t.Helper()
		var doc struct {
			PackageRules []renovateRule `json:"packageRules"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc.PackageRules
	}
	if errs := renovateIndirectErrors(decode()); len(errs) > 0 {
		t.Errorf("Renovate policy: %v", errs)
	}
	for _, mutation := range []string{"enabled root", "major root", "grpc restriction"} {
		t.Run(mutation, func(t *testing.T) {
			rules := decode()
			if mutation == "grpc restriction" {
				rules = append(rules, renovateRule{MatchPackageNames: []string{"google.golang.org/grpc"}, AllowedVersions: "<1.84.0 || >=1.85.0"})
			} else {
				changed := false
				for i := range rules {
					rule := &rules[i]
					if !slices.Equal(rule.MatchDepTypes, []string{"indirect"}) || rule.Enabled == nil {
						continue
					}
					if (mutation == "enabled root" && *rule.Enabled) || (mutation == "major root" && !*rule.Enabled) {
						rule.MatchFileNames = []string{"tools/**/go.mod"}
						changed = true
					}
				}
				if !changed {
					t.Fatal("mutation did not select a rule")
				}
			}
			if len(renovateIndirectErrors(rules)) == 0 {
				t.Fatal("mutation escaped policy check")
			}
		})
	}
}
