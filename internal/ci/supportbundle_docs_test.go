package ci_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/repotest"
)

func TestSupportBundleDocsStateWhatItContains(t *testing.T) {
	root, err := repotest.Root()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "troubleshooting.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	_, section, found := strings.Cut(doc, "## Generating a support bundle")
	if !found {
		t.Fatal("support bundle section missing")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	section = strings.Join(strings.Fields(section), " ")
	for _, phrase := range missingBundleDisclosures(section) {
		t.Errorf("support bundle section does not mention %q", phrase)
	}
	if strings.Contains(strings.Join(strings.Fields(doc), " "), "did not come from the support bundle") {
		t.Error("troubleshooting exempts support bundle material from redaction")
	}
}

func missingBundleDisclosures(section string) []string {
	var missing []string
	for _, phrase := range []string{"can contain the tailnet name", "can contain endpoint hostnames", "can contain cloud account IDs", "recent process log", "can contain sensitive log text"} {
		if !strings.Contains(section, phrase) {
			missing = append(missing, phrase)
		}
	}
	return missing
}

func TestSupportBundleDisclosureGuard(t *testing.T) {
	complete := "can contain the tailnet name; can contain endpoint hostnames; can contain cloud account IDs; recent process log; can contain sensitive log text; review endpoint and account settings"
	for _, tc := range []struct {
		name, text  string
		wantMissing bool
	}{
		{"complete", complete, false},
		{"endpoint absent", strings.Replace(complete, "can contain endpoint hostnames; ", "", 1), true},
		{"endpoint negated", strings.Replace(complete, "can contain endpoint hostnames", "cannot contain endpoint hostnames", 1), true},
		{"account absent", strings.Replace(complete, "can contain cloud account IDs; ", "", 1), true},
		{"account negated", strings.Replace(complete, "can contain cloud account IDs", "cannot contain cloud account IDs", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(missingBundleDisclosures(tc.text)) > 0; got != tc.wantMissing {
				t.Errorf("missing disclosures = %v, want %v", got, tc.wantMissing)
			}
		})
	}
}
