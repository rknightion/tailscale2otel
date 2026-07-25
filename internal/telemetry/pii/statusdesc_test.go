package pii

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// --- #473 (security:SEC-10): span status descriptions follow the free-text policy ---

// A status description is free text: it is written by whatever code failed and can
// embed anything the error string carried. With free_text_details disabled it must
// go, exactly like the exception.message that carries the same text.
func TestRedactStatusDescriptionRemovesFreeTextWhenCategoryOff(t *testing.T) {
	c := allOn()
	c[CatFreeTextDetails] = false
	r := New(c)
	cases := []struct {
		name string
		desc string
	}{
		{"collector failure", "list devices: api unavailable"},
		{"recovered panic", "panic: runtime error: index out of range [3] with length 2"},
		{"wrapped error", "devices collector: fetch: Get \"https://api.tailscale.com/api/v2/tailnet/example.com/devices\": context deadline exceeded"},
		{"transport error", "unexpected status 500 from /api/v2/tailnet/-/devices"},
		{"error with an email", "user alice@example.com is not authorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.RedactStatusDescription(tc.desc, nil)
			if got != bodyRedactedPlaceholder {
				t.Fatalf("free_text_details off: got %q, want %q", got, bodyRedactedPlaceholder)
			}
		})
	}
}

// Diagnosis must survive the redaction: a bounded, code-defined description (a
// fixed reject reason, an HTTP status text) names an error CLASS and carries no
// free text, so it is passed through even with the category off.
func TestRedactStatusDescriptionKeepsBoundedClasses(t *testing.T) {
	c := allOn()
	c[CatFreeTextDetails] = false
	r := New(c)
	for _, desc := range []string{
		"method not allowed",
		"unauthorized",
		"body too large",
		"corrupt batch",
		"record decode failure",
		"failed to parse webhook body",
		http.StatusText(http.StatusTooManyRequests),
		http.StatusText(http.StatusInternalServerError),
	} {
		if got := r.RedactStatusDescription(desc, nil); got != desc {
			t.Errorf("bounded description %q was redacted to %q", desc, got)
		}
	}
}

// With the category enabled the description keeps its current useful behavior, and
// the attr-value scrub (#197) still removes an identifier from some OTHER disabled
// category that the text happens to embed.
func TestRedactStatusDescriptionKeepsTextWhenCategoryOn(t *testing.T) {
	c := allOn()
	c[CatEndpointPaths] = false // a different category is off
	r := New(c)
	const url = "https://api.tailscale.com/api/v2/tailnet/example.com/devices"
	got := r.RedactStatusDescription("Get "+url+": context deadline exceeded", map[string]any{"url.full": url})
	if strings.Contains(got, url) {
		t.Fatalf("endpoint_paths off: URL must be scrubbed from the description, got %q", got)
	}
	if !strings.Contains(got, "context deadline exceeded") {
		t.Fatalf("free_text_details on: the error text must survive, got %q", got)
	}
}

func TestRedactStatusDescriptionFastPaths(t *testing.T) {
	const desc = "panic: boom"
	if got := New(allOn()).RedactStatusDescription(desc, nil); got != desc {
		t.Errorf("all categories on: description must be unchanged, got %q", got)
	}
	c := allOn()
	c[CatFreeTextDetails] = false
	if got := New(c).RedactStatusDescription("", nil); got != "" {
		t.Errorf("empty description must stay empty, got %q", got)
	}
}

// setStatusLiteral matches a span status written with a Go string literal, e.g.
//
//	span.SetStatus(codes.Error, "method not allowed")
var setStatusLiteral = regexp.MustCompile(`SetStatus\(codes\.Error,\s*"((?:[^"\\]|\\.)*)"\)`)

// Every literal status description in the tree must be declared bounded, or it will
// be redacted wholesale once free_text_details is disabled. Unlisted descriptions
// fail CLOSED (redacted), so this guard is about not silently losing diagnosis, not
// about safety.
func TestBoundedStatusDescriptionsCoverEveryLiteralSetStatus(t *testing.T) {
	root := repoRoot(t)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", ".capture":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range setStatusLiteral.FindAllStringSubmatch(string(src), -1) {
			if !BoundedStatusDescription(m[1]) {
				rel, _ := filepath.Rel(root, path)
				missing = append(missing, rel+": "+m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(missing) > 0 {
		t.Fatalf("literal span status descriptions not in boundedStatusDescriptions "+
			"(internal/telemetry/pii/statusdesc.go) — add them or they are redacted whenever "+
			"free_text_details is disabled:\n  %s", strings.Join(missing, "\n  "))
	}
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			if parent := filepath.Dir(dir); parent != dir {
				// tools/* are nested modules; keep climbing to the repo root.
				if _, outerErr := os.Stat(filepath.Join(parent, "go.mod")); outerErr == nil {
					dir = parent
					continue
				}
			}
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test working directory")
		}
		dir = parent
	}
}
