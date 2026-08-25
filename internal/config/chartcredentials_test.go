package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// chartCredentialPathsFile is the Helm template holding the authoritative list
// of credential-bearing keys inside .Values.config. A non-empty value at any of
// them moves the whole rendered config.yaml out of the ConfigMap and into a
// Secret, because a ConfigMap is readable by anyone with namespace
// `get configmaps` — in practice granted far more widely than `get secrets`.
const chartCredentialPathsFile = "../../deploy/helm/tailscale2otel/templates/_helpers.tpl"

// chartCredentialExemptions are Secret-typed config fields deliberately absent
// from the chart's credentialPaths list, each with the reason.
//
// Keep this map SMALL and reasoned. It is the only legitimate way for a Secret
// field to be missing from the chart list, and an entry here is a claim that
// leaking the value to a ConfigMap reader is acceptable — which is almost never
// true of something typed Secret.
var chartCredentialExemptions = map[string]string{
	// A list-of-structs key has no TS2OTEL_* env form and is handled by its own
	// branch in the chart (inlineCredentialKeys walks tailnets[] and
	// node_metrics.targets[] separately, since a dotted path cannot address a
	// list element).
	"tailnets[*].auth.apikey":                             "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].auth.oauth.client_secret":                "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.flow.access_key_id":          "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.flow.secret_access_key":      "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.flow.session_token":          "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.audit.access_key_id":         "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.audit.secret_access_key":     "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.audit.session_token":         "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.k8s_audit.access_key_id":     "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.k8s_audit.secret_access_key": "structural carrier, handled by the chart's own tailnets[] branch",
	"tailnets[*].objectstore.k8s_audit.session_token":     "structural carrier, handled by the chart's own tailnets[] branch",
	"streaming.routes[*].token":                           "structural carrier, handled by the chart's own routes branch",
	"webhook.routes[*].secret":                            "structural carrier, handled by the chart's own routes branch",
	"collectors.node_metrics.targets[*].bearer_token":     "structural carrier, handled by the chart's own targets branch",
	"collectors.node_metrics.targets[*].headers":          "structural carrier, handled by the chart's own targets branch",
}

// TestChartCredentialPathsCoversEverySecretField is the guard that should have
// existed already.
//
// The chart's credentialPaths list is hand-written, and nothing checked it
// against the struct — so a config field typed Secret could be added, wired,
// documented and shipped while the chart still rendered it into a plainly
// readable ConfigMap. That is not hypothetical: `enrichment.geoip.download.license_key`
// had been missing since the GeoIP downloader landed, and the annotation
// writer's `grafana_annotations.token` was only listed because the author
// happened to remember. The type system already knows which fields are
// credentials; this test makes the chart agree with it.
//
// It reads the template as TEXT rather than rendering it, because the point is
// the declared list, not what a particular values.yaml produces.
func TestChartCredentialPathsCoversEverySecretField(t *testing.T) {
	listed, err := chartCredentialPaths()
	if err != nil {
		t.Fatalf("read chart credential paths: %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("parsed no credential paths out of the chart; this test would assert nothing")
	}

	secrets := secretFieldKeys(reflect.TypeFor[Config](), "")
	sort.Strings(secrets)
	if len(secrets) == 0 {
		t.Fatal("found no Secret-typed fields on Config; this test would assert nothing")
	}

	for _, key := range secrets {
		if listed[key] {
			continue
		}
		if reason, ok := chartCredentialExemptions[key]; ok {
			t.Logf("exempt: %s (%s)", key, reason)
			continue
		}
		t.Errorf("config.%s is typed Secret but is not in tailscale2otel.credentialPaths "+
			"(%s), so setting it inline under `config:` renders it into a ConfigMap — readable by "+
			"anyone with `get configmaps`. Add it to that list, or add a reasoned entry to "+
			"chartCredentialExemptions.", key, chartCredentialPathsFile)
	}

	// The reverse direction: a stale entry naming a field that no longer exists
	// is a list nobody can trust, and it fails open (the key simply never
	// matches).
	valid := map[string]bool{}
	for _, key := range secrets {
		valid[key] = true
	}
	for key := range listed {
		// otlp.headers is a whole map rather than a Secret field: raw OTLP
		// headers are where an Authorization header goes, so the chart treats
		// any non-empty value there as a credential.
		if key == "otlp.headers" {
			continue
		}
		if !valid[key] {
			t.Errorf("tailscale2otel.credentialPaths lists %q, which is not a Secret-typed "+
				"config field — a renamed or removed key that now matches nothing", key)
		}
	}
}

// chartCredentialPaths parses the dotted paths out of the credentialPaths
// template definition.
func chartCredentialPaths() (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Clean(chartCredentialPathsFile))
	if err != nil {
		return nil, err
	}
	const marker = `{{- define "tailscale2otel.credentialPaths" -}}`
	_, rest, found := strings.Cut(string(raw), marker)
	if !found {
		return nil, os.ErrNotExist
	}
	body, _, _ := strings.Cut(rest, "{{- end -}}")

	out := map[string]bool{}
	for line := range strings.SplitSeq(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out[line] = true
		}
	}
	return out, nil
}

// secretFieldKeys derives every Secret-typed field's dotted yaml key from the
// struct tags, independent of any hand-written list — the same approach
// expectedPathKeys uses for path-bearing fields, and for the same reason.
func secretFieldKeys(t reflect.Type, prefix string) []string {
	var keys []string
	secretType := reflect.TypeFor[Secret]()
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		ft := f.Type
		switch {
		case ft == secretType:
			keys = append(keys, prefix+tag)
		case ft.Kind() == reflect.Map && ft.Elem() == secretType:
			keys = append(keys, prefix+tag)
		case ft.Kind() == reflect.Struct:
			keys = append(keys, secretFieldKeys(ft, prefix+tag+".")...)
		case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
			keys = append(keys, secretFieldKeys(ft.Elem(), prefix+tag+".")...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			keys = append(keys, secretFieldKeys(ft.Elem(), prefix+tag+"[*].")...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Pointer && ft.Elem().Elem().Kind() == reflect.Struct:
			keys = append(keys, secretFieldKeys(ft.Elem().Elem(), prefix+tag+"[*].")...)
		}
	}
	return keys
}
