package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/oas"
)

// The scheduled lane's report is the only thing a maintainer sees: it becomes the
// body of a deduplicated tracking issue, read months later by someone with no
// memory of this code. Before #432 it was a four-column table of
// severity/op/kind/detail with no indication of WHERE upstream to look or what
// each severity obliges you to do.

func sampleChanges() []oas.Change {
	return []oas.Change{
		{
			OpID: "listUsers", Kind: oas.ParamDefaultChanged,
			Where:    "GET /tailnet/{tailnet}/users",
			Detail:   `query:type: default "member" → "all"`,
			Severity: oas.Warning,
		},
		{
			OpID: "listTailnetDevices", Kind: oas.RequiredParamAdded,
			Where:    "GET /tailnet/{tailnet}/devices",
			Detail:   "query:since: required parameter added",
			Severity: oas.Breaking,
		},
		{
			OpID: "listWebhooks", Kind: oas.NewOptionalField,
			Where:    "GET /tailnet/{tailnet}/webhooks",
			Detail:   "webhooks[].lastError",
			Severity: oas.Info,
		},
	}
}

func TestRenderMarkdown_CarriesTheEvidenceAMaintainerNeeds(t *testing.T) {
	report := renderMarkdownString(sampleChanges())

	for _, want := range []string{
		// Where the change is, not just which operation it belongs to.
		"GET /tailnet/{tailnet}/devices",
		"GET /tailnet/{tailnet}/users",
		// The old → new values, so the report stands alone without re-running the tool.
		`default "member" → "all"`,
		// What each severity obliges the reader to do. A bare "warning" tells a
		// reader six months from now nothing about whether to act.
		"breaking",
		"behavioral",
		"additive",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
}

// Breaking changes must be readable first. The classifier sorts by severity, and
// the renderer must not undo that.
func TestRenderMarkdown_BreakingChangesComeFirst(t *testing.T) {
	sorted := oas.SortChanges(sampleChanges())
	report := renderMarkdownString(sorted)

	breaking := strings.Index(report, "required parameter added")
	info := strings.Index(report, "webhooks[].lastError")
	if breaking < 0 || info < 0 {
		t.Fatalf("report lost a change:\n%s", report)
	}
	if breaking > info {
		t.Errorf("the informational change is rendered above the breaking one:\n%s", report)
	}
}

// The scheduled lane compares report bodies to decide whether to update its
// tracking issue, so identical drift must render byte-identically.
func TestRenderMarkdown_IsDeterministic(t *testing.T) {
	first := renderMarkdownString(oas.SortChanges(sampleChanges()))
	for range 20 {
		if got := renderMarkdownString(oas.SortChanges(sampleChanges())); got != first {
			t.Fatalf("report is not deterministic:\n--- first ---\n%s\n--- later ---\n%s", first, got)
		}
	}
}

// The JSON form must carry Where too, or a consumer of -format json gets less
// than a human reading the Markdown.
func TestRenderJSON_IncludesWhere(t *testing.T) {
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(renderJSONString(sampleChanges())), &decoded); err != nil {
		t.Fatalf("unmarshal JSON report: %v", err)
	}
	if len(decoded) != 3 {
		t.Fatalf("decoded %d changes, want 3", len(decoded))
	}
	for _, c := range decoded {
		if c["Where"] == "" || c["Where"] == nil {
			t.Errorf("change %v has no Where", c)
		}
	}
}

// A clean run must stay clean: no table, no legend, nothing that reads as drift.
func TestRenderMarkdown_CleanRunSaysNothingElse(t *testing.T) {
	report := renderMarkdownString(nil)
	if !strings.Contains(report, "No drift detected") {
		t.Errorf("clean report does not say so: %q", report)
	}
	for _, unwanted := range []string{"| Severity", "breaking", "Where"} {
		if strings.Contains(report, unwanted) {
			t.Errorf("clean report leaks %q: %q", unwanted, report)
		}
	}
}
