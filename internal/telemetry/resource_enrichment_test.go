package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry/pii"
)

// findAttr returns the value of key in res, and whether it was present.
func findAttr(res *resource.Resource, key string) (string, bool) {
	for _, kv := range res.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

// TestResourceEnrichmentZeroValueByteIdentical asserts a zero ResourceOptions
// produces exactly the pre-#380 Resource, for both the metrics
// (includeServiceVersion=false) and logs/traces (true) variants. This is the
// regression backstop for the whole issue: enrichment must be strictly
// additive-when-configured and inert otherwise.
func TestResourceEnrichmentZeroValueByteIdentical(t *testing.T) {
	ctx := context.Background()
	opts := Options{ServiceName: "svc", ServiceVersion: "1.2.3", InstanceID: "inst-1"}

	// Reconstruct the exact pre-#380 detector list by hand (no enrichment
	// concept at all) and compare against today's buildResource output.
	legacy := func(includeServiceVersion bool) (*resource.Resource, error) {
		attrs := []attribute.KeyValue{attribute.String("service.name", opts.ServiceName)}
		if includeServiceVersion {
			attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
		}
		attrs = append(attrs, attribute.String("service.instance.id", opts.InstanceID))
		res, err := resource.New(ctx,
			resource.WithAttributes(attrs...),
			resource.WithTelemetrySDK(),
			resource.WithOS(),
			resource.WithProcessPID(),
			resource.WithProcessExecutableName(),
			resource.WithProcessRuntimeName(),
			resource.WithProcessRuntimeVersion(),
			resource.WithHost(),
		)
		return res, err
	}

	for _, includeVersion := range []bool{false, true} {
		want, err := legacy(includeVersion)
		if err != nil && !isPartial(err) {
			t.Fatalf("legacy build: %v", err)
		}
		got, err := buildResource(ctx, opts, includeVersion)
		if err != nil {
			t.Fatalf("buildResource: %v", err)
		}
		if !got.Equal(want) {
			t.Errorf("includeServiceVersion=%v: zero-value ResourceOptions changed the Resource\nwant=%v\ngot=%v", includeVersion, want, got)
		}
	}
}

func isPartial(err error) bool {
	return err != nil && strings.Contains(err.Error(), "partial resource")
}

// TestResourceEnrichmentAppIdentityWins asserts that user-supplied service.name,
// service.instance.id and service.version in the enrichment channel (custom
// Attributes map) can never override the app's own identity attrs.
func TestResourceEnrichmentAppIdentityWins(t *testing.T) {
	ctx := context.Background()
	opts := Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "3.0.0",
		InstanceID:     "real-instance",
		Resource: ResourceOptions{
			Attributes: map[string]string{
				"service.name":        "attacker-service",
				"service.instance.id": "attacker-instance",
				"service.version":     "9.9.9",
			},
		},
	}

	for _, tc := range []struct {
		name                  string
		includeServiceVersion bool
	}{
		{"metrics", false},
		{"logs_traces", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := buildResource(ctx, opts, tc.includeServiceVersion)
			if err != nil {
				t.Fatalf("buildResource: %v", err)
			}
			if v, ok := findAttr(res, "service.name"); !ok || v != "tailscale2otel" {
				t.Errorf("service.name = %q, ok=%v; want app identity %q", v, ok, "tailscale2otel")
			}
			if v, ok := findAttr(res, "service.instance.id"); !ok || v != "real-instance" {
				t.Errorf("service.instance.id = %q, ok=%v; want app identity %q", v, ok, "real-instance")
			}
			if tc.includeServiceVersion {
				if v, ok := findAttr(res, "service.version"); !ok || v != "3.0.0" {
					t.Errorf("service.version = %q, ok=%v; want app identity %q", v, ok, "3.0.0")
				}
			} else {
				if _, ok := findAttr(res, "service.version"); ok {
					t.Errorf("service.version present on metrics resource; must stay omitted (#187)")
				}
			}
		})
	}
}

// TestResourceEnrichmentMetricsOmitsServiceVersionViaFromEnv asserts that even
// OTEL_RESOURCE_ATTRIBUTES cannot smuggle service.version back onto the metrics
// Resource (#187's guard must hold across every input channel, not just the
// config file / app fields).
func TestResourceEnrichmentMetricsOmitsServiceVersionViaFromEnv(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=9.9.9,custom.attr=ok")
	ctx := context.Background()
	opts := Options{
		ServiceName: "svc",
		Resource:    ResourceOptions{FromEnv: true},
	}
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if _, ok := findAttr(res, "service.version"); ok {
		t.Errorf("service.version present on metrics resource via from_env; #187 guard broken")
	}
	if v, ok := findAttr(res, "custom.attr"); !ok || v != "ok" {
		t.Errorf("custom.attr = %q, ok=%v; want %q (non-reserved from_env attrs should pass through)", v, ok, "ok")
	}
}

// TestResourceEnrichmentRejectsSignalScopedKeys asserts tailscale.tailnet and
// tailscale2otel.provider can never be placed on the Resource via enrichment —
// they remain signal-scoped const attributes (constLabelAttrs), per roadmap
// item L and this issue's explicit guard.
func TestResourceEnrichmentRejectsSignalScopedKeys(t *testing.T) {
	ctx := context.Background()
	opts := Options{
		ServiceName: "svc",
		Resource: ResourceOptions{
			Attributes: map[string]string{
				"tailscale.tailnet":       "evil-tailnet",
				"tailscale2otel.provider": "evil-provider",
			},
		},
	}
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if _, ok := findAttr(res, "tailscale.tailnet"); ok {
		t.Error("tailscale.tailnet leaked onto the Resource via enrichment")
	}
	if _, ok := findAttr(res, "tailscale2otel.provider"); ok {
		t.Error("tailscale2otel.provider leaked onto the Resource via enrichment")
	}
}

// TestResourceEnrichmentFromEnvOptIn asserts from_env is inert unless
// explicitly turned on, since resource.WithFromEnv() gives an operator's ambient
// environment unbounded reach over a per-series label surface.
func TestResourceEnrichmentFromEnvOptIn(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "custom.attr=should-not-appear")
	ctx := context.Background()
	opts := Options{ServiceName: "svc"} // Resource.FromEnv left false (zero value)
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if _, ok := findAttr(res, "custom.attr"); ok {
		t.Error("custom.attr from OTEL_RESOURCE_ATTRIBUTES present with from_env unset (default false)")
	}
}

// TestResourceEnrichmentHostnamesGateHoldsUnderEnrichment asserts the existing
// pii.CatHostnames gate (which drops WithHost()) cannot be bypassed by
// enrichment re-introducing host.name via the custom Attributes map or
// OTEL_RESOURCE_ATTRIBUTES.
func TestResourceEnrichmentHostnamesGateHoldsUnderEnrichment(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=leaky-env-host")
	ctx := context.Background()
	opts := Options{
		ServiceName: "svc",
		PIIFilter:   pii.Categories{pii.CatHostnames: false},
		Resource: ResourceOptions{
			FromEnv: true,
			Attributes: map[string]string{
				"host.name": "leaky-config-host",
			},
		},
	}
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if v, ok := findAttr(res, "host.name"); ok {
		t.Errorf("host.name = %q present despite the hostnames PII gate being off; enrichment must not reintroduce it", v)
	}
}

// TestResourceEnrichmentServiceNamespaceAndDeploymentEnvironment asserts the two
// dedicated fields land on the Resource under their documented keys, and that
// service.namespace becomes a reserved promoted label (Grafana Cloud promotes
// the whole service.* namespace).
func TestResourceEnrichmentServiceNamespaceAndDeploymentEnvironment(t *testing.T) {
	ctx := context.Background()
	opts := Options{
		ServiceName: "svc",
		Resource: ResourceOptions{
			ServiceNamespace:      "fleet-prod",
			DeploymentEnvironment: "production",
		},
	}
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if v, ok := findAttr(res, "service.namespace"); !ok || v != "fleet-prod" {
		t.Errorf("service.namespace = %q, ok=%v; want %q", v, ok, "fleet-prod")
	}
	if v, ok := findAttr(res, "deployment.environment.name"); !ok || v != "production" {
		t.Errorf("deployment.environment.name = %q, ok=%v; want %q", v, ok, "production")
	}

	reserved := reservedPromotedLabels(opts)
	if _, ok := reserved["service_namespace"]; !ok {
		t.Error("reservedPromotedLabels must reserve service_namespace once ServiceNamespace is set")
	}

	zero := reservedPromotedLabels(Options{ServiceName: "svc"})
	if _, ok := zero["service_namespace"]; ok {
		t.Error("reservedPromotedLabels must not reserve service_namespace when ServiceNamespace is unset")
	}
}

// TestResourceEnrichmentBoundedCount asserts the custom Attributes map is
// capped at maxResourceAttrCount entries rather than growing the metrics
// Resource's cardinality-multiplying attribute set without limit.
func TestResourceEnrichmentBoundedCount(t *testing.T) {
	ctx := context.Background()
	attrs := make(map[string]string, maxResourceAttrCount+8)
	for i := 0; i < maxResourceAttrCount+8; i++ {
		attrs[keyFor(i)] = "v"
	}
	opts := Options{
		ServiceName: "svc",
		Resource:    ResourceOptions{Attributes: attrs},
	}
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	count := 0
	for _, kv := range res.Attributes() {
		if strings.HasPrefix(string(kv.Key), "custom.attr.") {
			count++
		}
	}
	if count != maxResourceAttrCount {
		t.Errorf("custom attribute count = %d, want exactly the cap %d", count, maxResourceAttrCount)
	}
}

func keyFor(i int) string {
	return "custom.attr." + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

// TestResourceEnrichmentBoundedKeyValueLength asserts an oversized key or value
// is dropped rather than admitted, with a warning logged.
func TestResourceEnrichmentBoundedKeyValueLength(t *testing.T) {
	ctx := context.Background()
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	longKey := "custom." + strings.Repeat("k", maxResourceAttrKeyLen+1)
	longVal := strings.Repeat("v", maxResourceAttrValueLen+1)
	opts := Options{
		ServiceName: "svc",
		Logger:      logger,
		Resource: ResourceOptions{
			Attributes: map[string]string{
				longKey:          "ok",
				"custom.longval": longVal,
				"custom.ok":      "fine",
			},
		},
	}
	res, err := buildResource(ctx, opts, false)
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if _, ok := findAttr(res, longKey); ok {
		t.Error("oversized key was admitted onto the Resource")
	}
	if _, ok := findAttr(res, "custom.longval"); ok {
		t.Error("oversized value was admitted onto the Resource")
	}
	if v, ok := findAttr(res, "custom.ok"); !ok || v != "fine" {
		t.Errorf("custom.ok = %q, ok=%v; a compliant attribute must still pass through", v, ok)
	}
	if logBuf.Len() == 0 {
		t.Error("expected a warning to be logged for the dropped attributes")
	}
}
