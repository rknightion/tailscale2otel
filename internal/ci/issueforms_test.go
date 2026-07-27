// Guard tests over .github/ISSUE_TEMPLATE (#444).
//
// GitHub issue forms fail in a uniquely quiet way: a form whose YAML is
// malformed, or which assigns a label that does not exist, is not rejected with
// an error anyone sees. It just stops working — the chooser silently omits the
// template, or the issue opens without its labels — and the only way to notice
// is for a human to open "New issue" and spot what is missing.
//
// The specific trap these tests were written against is real and shipped once:
//
//	labels:
//	  - bug
//	  - area: api        # <- a MAPPING, not the string "area: api"
//
// Every label in this repository's taxonomy contains a colon, so an unquoted
// entry parses as a single-key map and the label is silently never applied.
// A validator that only checks "does the file parse" passes this happily.
package ci_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const issueTemplateDir = repoDir + "/.github/ISSUE_TEMPLATE"

// knownLabels is the repository's label taxonomy as it exists on GitHub.
//
// A form can only assign a label that already exists; GitHub creates nothing on
// demand. Keeping the set here rather than querying the API keeps the test
// offline and deterministic — the cost is that adding a label to a form means
// adding it here too, which is the intended friction.
var knownLabels = map[string]bool{
	"bug": true, "documentation": true, "duplicate": true, "enhancement": true,
	"good first issue": true, "help wanted": true, "invalid": true,
	"question": true, "wontfix": true, "security": true, "roadmap": true,
	"status: blocked":    true,
	"priority: critical": true, "priority: high": true,
	"priority: medium": true, "priority: low": true,
	"area: api": true, "area: config": true, "area: docs": true, "area: ci": true,
	"area: metrics": true, "area: performance": true, "area: collector": true,
	"area: deploy": true, "area: telemetry": true, "area: security": true,
	"area: streaming": true, "area: dx": true, "area: observability": true,
}

// driftLaneLabels are owned by the scheduled drift workflows, which open and
// close their own tracking issues. A human-facing form must never assign one:
// resolve-drift closes EVERY open issue carrying the label on the next green
// run, so a user's bug report would be auto-closed by a passing CI lane.
var driftLaneLabels = map[string]bool{
	"api-drift": true, "clientlib-drift": true,
	"live-contract": true, "api-operation-drift": true,
	"iana-drift": true,
}

var formBodyTypes = map[string]bool{
	"markdown": true, "input": true, "textarea": true,
	"dropdown": true, "checkboxes": true,
}

func issueForms(t *testing.T) map[string]map[string]any {
	t.Helper()
	entries, err := os.ReadDir(issueTemplateDir)
	if err != nil {
		t.Fatalf("read %s: %v", issueTemplateDir, err)
	}
	out := map[string]map[string]any{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(issueTemplateDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s is not valid YAML, so GitHub will silently drop it from the "+
				"chooser: %v", name, err)
		}
		out[name] = doc
	}
	if len(out) == 0 {
		t.Fatalf("found no issue forms under %s", issueTemplateDir)
	}
	return out
}

func TestIssueFormLabelsAreStringsThatExist(t *testing.T) {
	forms := issueForms(t)
	if _, ok := forms["config.yml"]; !ok {
		t.Fatalf("no config.yml: without it the chooser cannot disable blank issues or " +
			"route vulnerabilities to private reporting")
	}
	for name, doc := range forms {
		if name == "config.yml" {
			continue
		}
		raw, ok := doc["labels"]
		if !ok {
			continue // a form need not assign labels
		}
		list, ok := raw.([]any)
		if !ok {
			t.Errorf("%s labels is %T, want a list", name, raw)
			continue
		}
		for i, entry := range list {
			label, ok := entry.(string)
			if !ok {
				t.Errorf("%s labels[%d] parsed as %T (%v), not a string. Every label in this "+
					"repo contains a colon, so an unquoted `- area: api` becomes a mapping and "+
					"the label is silently never applied. Quote it.", name, i, entry, entry)
				continue
			}
			if driftLaneLabels[label] {
				t.Errorf("%s assigns the CI-owned label %q; resolve-drift closes every open "+
					"issue carrying it on the next green run, so a user's report would be "+
					"auto-closed by passing CI", name, label)
			}
			if !knownLabels[label] {
				t.Errorf("%s assigns label %q, which is not in the repository's taxonomy; "+
					"GitHub does not create labels on demand, so it would be dropped silently",
					name, label)
			}
		}
	}
}

func TestIssueFormBodiesConformToTheSchema(t *testing.T) {
	for name, doc := range issueForms(t) {
		if name == "config.yml" {
			continue
		}
		body, ok := doc["body"].([]any)
		if !ok {
			t.Errorf("%s has no body list", name)
			continue
		}
		seen := map[string]bool{}
		for i, raw := range body {
			el, ok := raw.(map[string]any)
			if !ok {
				t.Errorf("%s body[%d] is %T, want a mapping", name, i, raw)
				continue
			}
			kind, _ := el["type"].(string)
			if !formBodyTypes[kind] {
				t.Errorf("%s body[%d] has type %q, which is not a form element type", name, i, kind)
				continue
			}
			if kind == "markdown" {
				if _, has := el["validations"]; has {
					t.Errorf("%s body[%d] is a markdown block with a validations key; GitHub "+
						"rejects that", name, i)
				}
				continue
			}
			id, _ := el["id"].(string)
			if id == "" {
				t.Errorf("%s body[%d] (%s) has no id", name, i, kind)
			} else if seen[id] {
				t.Errorf("%s reuses body id %q; ids must be unique within a form", name, id)
			}
			seen[id] = true
			attrs, ok := el["attributes"].(map[string]any)
			if !ok {
				t.Errorf("%s body[%d] (%s) has no attributes", name, i, kind)
				continue
			}
			if label, _ := attrs["label"].(string); label == "" {
				t.Errorf("%s body[%d] (%s) has no attributes.label", name, i, kind)
			}
			if kind == "dropdown" || kind == "checkboxes" {
				if opts, ok := attrs["options"].([]any); !ok || len(opts) == 0 {
					t.Errorf("%s body[%q] is a %s with no options", name, id, kind)
				}
			}
		}
	}
}

// TestVulnerabilitiesAreRoutedAwayFromPublicIssues is a security assertion, not
// a usability one. The repository has private vulnerability reporting enabled;
// the failure this guards against is a reporter pasting a working exploit into
// a public issue because nothing told them not to.
func TestVulnerabilitiesAreRoutedAwayFromPublicIssues(t *testing.T) {
	const advisories = "security/advisories/new"
	forms := issueForms(t)

	cfg := forms["config.yml"]
	if blank, ok := cfg["blank_issues_enabled"].(bool); !ok || blank {
		t.Errorf("config.yml must set blank_issues_enabled: false, otherwise the chooser can "+
			"be bypassed entirely and every routing guarantee here is optional (got %v)",
			cfg["blank_issues_enabled"])
	}
	links, _ := cfg["contact_links"].([]any)
	var routed bool
	for _, raw := range links {
		l, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if url, _ := l["url"].(string); strings.Contains(url, advisories) {
			routed = true
		}
	}
	if !routed {
		t.Errorf("config.yml has no contact_link pointing at %q, so the chooser offers no "+
			"private route for a vulnerability", advisories)
	}

	bug, ok := forms["bug_report.yml"]
	if !ok {
		t.Fatalf("no bug_report.yml")
	}
	body, _ := yaml.Marshal(bug["body"])
	if !strings.Contains(string(body), advisories) {
		t.Errorf("bug_report.yml does not mention %q anywhere in its body; the chooser link "+
			"is easy to walk past, so the form itself must say it", advisories)
	}
}
