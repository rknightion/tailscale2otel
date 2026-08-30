package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
)

// GrafanaAnnotationsConfig configures the opt-in Grafana annotation writer:
// publishing a curated, closed set of tailnet events into a Grafana
// organization so a dashboard can explain "what changed at 14:00" without any
// external automation.
//
// Setting URL is the WHOLE opt-in. Unset (the default) registers no writer,
// opens no client and logs nothing. Once URL is set the process FAILS TO START
// unless the token can actually write an annotation — discovering that at the
// first real event means the annotations an operator relies on for incident
// context simply are not there when they look.
//
// The token is a credential: supply it via TS2OTEL_GRAFANA_ANNOTATIONS__TOKEN
// or TokenFile, never in committed YAML.
type GrafanaAnnotationsConfig struct {
	// URL is the Grafana base URL, e.g. https://mystack.grafana.net. Setting it
	// enables the feature.
	URL string `yaml:"url" reload:"restart"`
	// Token is a Grafana service-account token. It needs exactly one action —
	// `annotations:create` on scope `annotations:type:organization` — and
	// tailscale2otel uses no other Grafana permission.
	Token Secret `yaml:"token" reload:"restart"`
	// TokenFile reads Token from a file at Load (Docker-secrets style). Value
	// XOR file: setting both is a Validate error.
	TokenFile string `yaml:"token_file" reload:"restart"`
	// DashboardUID confines annotations to one dashboard. Empty (the default)
	// publishes ORGANIZATION annotations, visible to any dashboard whose
	// annotation layer queries the tag — which is the point of pushing them
	// rather than deriving them on one board.
	DashboardUID string `yaml:"dashboard_uid" reload:"restart"`
	// Timeout bounds each POST /api/annotations request.
	Timeout Duration `yaml:"timeout" reload:"restart"`
	// MaxPerMinute is a token-bucket CEILING on annotations written per
	// process. Overage is dropped and counted, never delayed: a marker that
	// arrives after the moment it explains is worse than absent.
	MaxPerMinute int `yaml:"max_per_minute" reload:"restart"`
	// QueueSize bounds the hand-off buffer between the collector goroutines and
	// the single publisher. A full queue drops and counts rather than blocking
	// collection.
	QueueSize int `yaml:"queue_size" reload:"restart"`
	// RollupInterval is the bucket width for rolled-up categories: one region
	// annotation per interval per category per tailnet, instead of one marker
	// per event.
	RollupInterval Duration `yaml:"rollup_interval" reload:"restart"`
	// DedupeRetention is how long a published annotation's dedupe key is
	// remembered, so a restart cannot republish it. It must comfortably exceed
	// the longest source overlap window; too short and a still-current
	// condition is republished, too long and the state file grows.
	DedupeRetention Duration `yaml:"dedupe_retention" reload:"restart"`
	// StateFile is where the dedupe set persists. Empty defaults to
	// "annotations.json" beside checkpoint.file_path — deliberately NOT inside
	// the checkpoint file, which the window pollers rewrite every tick and
	// whose keys the startup migration walks.
	StateFile string `yaml:"state_file" reload:"restart"`
	// ExtraTags are added to every annotation, for deployments separating
	// environments or overlaying these on an existing tag scheme. Every
	// annotation already carries `tailscale2otel`, `category:<c>` and
	// `rule:<id>`.
	ExtraTags []string `yaml:"extra_tags" reload:"restart"`
	// Categories gates each curated category.
	Categories AnnotationCategories `yaml:"categories"`
}

// AnnotationCategories gates the curated categories. The lifecycle marker has
// no entry on purpose: it is the startup write probe, and a toggle's only real
// effect would be a deployment whose markers silently stop.
type AnnotationCategories struct {
	// ConfigChange is the curated subset of the configuration audit log — ACL
	// edits, device approval and churn, key lifecycle, user role changes, DNS
	// and tailnet settings.
	ConfigChange AnnotationCategoryConfig `yaml:"config_change"`
	// Expiry is a node key or auth key entering its expiry warning window.
	Expiry AnnotationCategoryConfig `yaml:"expiry"`
	// PolicyChange is a policy snapshot revision or diff observed by the ACL collector.
	PolicyChange AnnotationCategoryConfig `yaml:"policy_change"`
	// Inventory is device inventory churn or a material field-level change.
	Inventory AnnotationCategoryConfig `yaml:"inventory"`
	// Risk is a newly observed ACL, SSH, or auto-approver risk finding.
	Risk AnnotationCategoryConfig `yaml:"risk"`
}

// AnnotationCategoryConfig is one category's gate.
type AnnotationCategoryConfig struct {
	Enabled bool `yaml:"enabled" reload:"restart"`
	// Rollup replaces the per-event markers with one region annotation per
	// rollup_interval, summarizing what happened in it.
	Rollup bool `yaml:"rollup" reload:"restart"`
}

// Enabled reports whether the annotation writer is configured at all.
func (c GrafanaAnnotationsConfig) Enabled() bool { return strings.TrimSpace(c.URL) != "" }

// validate rejects a configuration that would start cleanly and then fail on
// every write. The URL check is deliberately strict for the same reason
// PyroscopeConfig's is: a schemeless "host:3000" parses without error and only
// fails at request time, once per annotation, forever.
func (c GrafanaAnnotationsConfig) validate() error {
	if !c.Enabled() {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return fmt.Errorf("grafana_annotations.url is not a valid URL")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("grafana_annotations.url must be a full http(s) URL, e.g. https://mystack.grafana.net")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("grafana_annotations.url must contain only a scheme and host; put credentials in the token field")
	}
	if parsed.Scheme != "https" && !httpguard.IsLoopbackHost(parsed.Host) {
		return fmt.Errorf("grafana_annotations.url must use HTTPS except for a loopback development endpoint")
	}
	if c.Token.Reveal() == "" {
		return fmt.Errorf("grafana_annotations.token (or token_file) must be set when " +
			"grafana_annotations.url is: the writer proves the token at startup and refuses to run without one")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("grafana_annotations.timeout must be positive, got %s", c.Timeout.D())
	}
	if c.MaxPerMinute <= 0 {
		return fmt.Errorf("grafana_annotations.max_per_minute must be positive, got %d", c.MaxPerMinute)
	}
	if c.QueueSize <= 0 {
		return fmt.Errorf("grafana_annotations.queue_size must be positive, got %d", c.QueueSize)
	}
	if c.RollupInterval <= 0 {
		return fmt.Errorf("grafana_annotations.rollup_interval must be positive, got %s", c.RollupInterval.D())
	}
	if c.DedupeRetention <= 0 {
		return fmt.Errorf("grafana_annotations.dedupe_retention must be positive, got %s", c.DedupeRetention.D())
	}
	for i, tag := range c.ExtraTags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("grafana_annotations.extra_tags[%d] is empty", i)
		}
	}
	return nil
}

// warnings returns advisories that do not justify refusing to start.
func (c GrafanaAnnotationsConfig) warnings() []string {
	if !c.Enabled() {
		return nil
	}
	var out []string
	if c.DashboardUID != "" {
		out = append(out, "grafana_annotations.dashboard_uid is set, so annotations are attached to "+
			"that dashboard only and will NOT appear on any other board (including Explore). "+
			"Leave it empty for organization annotations, which is what makes a marker visible "+
			"wherever it is relevant")
	}
	if !c.Categories.ConfigChange.Enabled && !c.Categories.Expiry.Enabled &&
		!c.Categories.PolicyChange.Enabled && !c.Categories.Inventory.Enabled &&
		!c.Categories.Risk.Enabled {
		out = append(out, "grafana_annotations is enabled but every category is off, so the only "+
			"annotation ever written will be the startup marker")
	}
	return out
}
