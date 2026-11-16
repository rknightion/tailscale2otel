package collector_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var synchronousGaugeAllowlist = map[string]string{
	"collector/selfobs.go#docScrapeDuration.Name":                "registered collector names are fixed for the process",
	"collector/selfobs.go#docScrapeSuccess.Name":                 "registered collector names are fixed for the process",
	"collector/selfobs.go#docScrapeLastTimestamp.Name":           "registered collector names are fixed for the process",
	"collector/selfobs.go#docScrapeStaleness.Name":               "registered collector names are fixed for the process",
	"collector/selfobs.go#docScrapeBudget.Name":                  "registered collector names are fixed for the process",
	"collector/contacts/contacts.go#docNeedsVerification.Name":   "three fixed contact types",
	"collector/flowlogs/flowlogs.go#docFeatureEnabled.Name":      "one fixed feature name",
	"collector/flowlogs/feature_probe.go#docFeatureEnabled.Name": "one fixed feature name",
	"collector/logstream/logstream.go#docConfigured.Name":        "two fixed log types",
	"collector/webhooks/webhooks.go#docEventSubscriptions.Name":  "fixed event vocabulary",
	"collector/webhooks/webhooks.go#docDesiredCovered.Name":      "static configured desired event list",
	"collector/pam/pam.go#docOrgSettingEnabled.Name":             "fixed organization setting names",
	"collector/pam/pam.go#docSubscriptionLimit.Name":             "fixed subscription limit names",
	"collector/nodemetrics/nodemetrics.go#s.name":                "forwarded passthrough samples are the documented exception",
	"apistate/emit.go#docAvailability.Name":                      "fixed collector, operation and state sets",
	"apistate/emit.go#docLastProbe.Name":                         "fixed collector and operation sets",
	"apistate/emit.go#docSubrequestCoverage.Name":                "fixed collector and subrequest sets",
	"geoip/geoip.go#MetricBuildTime":                             "two fixed database names",
}

func attributeGaugeCalls(path string, source []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return nil, err
	}
	var calls []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 5 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Gauge" {
			return true
		}
		if id, ok := call.Args[4].(*ast.Ident); ok && id.Name == "nil" {
			return true
		}
		if literal, ok := call.Args[4].(*ast.CompositeLit); ok && len(literal.Elts) == 0 {
			return true
		}
		argument := source[fset.Position(call.Args[0].Pos()).Offset:fset.Position(call.Args[0].End()).Offset]
		calls = append(calls, filepath.ToSlash(path)+"#"+string(argument))
		return true
	})
	return calls, nil
}

func gaugeAllowlistViolations(calls []string, allowed map[string]string) []string {
	seen := map[string]bool{}
	var failures []string
	for _, key := range calls {
		seen[key] = true
		if reason, ok := allowed[key]; !ok || strings.TrimSpace(reason) == "" {
			failures = append(failures, key+": use telemetry.GaugeSnapshotBuilder, or add an allowlist entry with the reason the label set cannot change")
		}
	}
	for key := range allowed {
		if !seen[key] {
			failures = append(failures, key+": stale synchronous gauge allowlist entry")
		}
	}
	slices.Sort(failures)
	return failures
}

func TestNoChurningSynchronousGauges(t *testing.T) {
	var calls []string
	for _, root := range []string{".", "../flowlog", "../audit", "../apistate", "../geoip"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			internalRoot, err := filepath.Abs("..")
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(internalRoot, absolute)
			if err != nil {
				return err
			}
			found, err := attributeGaugeCalls(relative, source)
			if err != nil {
				return err
			}
			calls = append(calls, found...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, failure := range gaugeAllowlistViolations(calls, synchronousGaugeAllowlist) {
		t.Error(failure)
	}
	t.Run("detector", func(t *testing.T) {
		calls, err := attributeGaugeCalls("collector/example.go", []byte(`package example
func emit(){e.Gauge(docMetric.Name,"1","description",1,telemetry.Attrs{"entity":"example"});e.Gauge(docNil.Name,"1","description",1,nil);e.Gauge(docEmpty.Name,"1","description",1,telemetry.Attrs{})}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) != 1 || calls[0] != "collector/example.go#docMetric.Name" {
			t.Fatalf("detected calls = %v", calls)
		}
		if failures := gaugeAllowlistViolations(calls, nil); len(failures) != 1 || !strings.Contains(failures[0], calls[0]) {
			t.Errorf("unlisted call not reported: %v", failures)
		}
		if failures := gaugeAllowlistViolations(nil, map[string]string{"collector/stale.go#docOld.Name": "fixed labels"}); len(failures) != 1 || !strings.Contains(failures[0], "stale") {
			t.Errorf("stale entry not reported: %v", failures)
		}
		if failures := gaugeAllowlistViolations(calls, map[string]string{calls[0]: ""}); len(failures) != 1 {
			t.Errorf("missing reason not reported: %v", failures)
		}
	})
}
