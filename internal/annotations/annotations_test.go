package annotations_test

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/annotations"
	"github.com/rknightion/tailscale2otel/v4/internal/catalog"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// ---- tag contract --------------------------------------------------------

func TestTagsCarryTheContract(t *testing.T) {
	a := annotations.Annotation{
		Category: annotations.CategoryConfigChange,
		Tailnet:  "example.com",
		RuleID:   annotations.RuleConfigChange,
		Severity: "high",
	}
	want := []string{
		"tailscale2otel",
		"tailnet:example.com",
		"category:config_change",
		"rule:tailscale.config_change",
		"severity:high",
	}
	if got := a.Tags(); !reflect.DeepEqual(got, want) {
		t.Errorf("Tags() = %v, want %v", got, want)
	}
}

// TestTagsOmitEmptyDimensions pins the two conditional tags. A single-tailnet
// deployment must not carry a bare "tailnet:" tag, and a rule with no severity
// must not carry a bare "severity:" — both would be indexed by Grafana forever
// while matching nothing anyone would query for.
func TestTagsOmitEmptyDimensions(t *testing.T) {
	got := annotations.Annotation{
		Category: annotations.CategoryExpiry,
		RuleID:   annotations.RuleKeyExpiring,
	}.Tags()
	for _, tag := range got {
		if tag == annotations.TagTailnetPrefix || tag == annotations.TagSeverityPrefix {
			t.Errorf("Tags() = %v, contains an empty dimension tag %q", got, tag)
		}
	}
	if slices.Contains(got, annotations.TagRollup) {
		t.Errorf("Tags() = %v, tagged as a rollup without a TimeEnd", got)
	}
}

func TestTagsMarkRollupRegions(t *testing.T) {
	got := annotations.Annotation{
		Category: annotations.CategoryExpiry,
		RuleID:   annotations.RollupRuleID(annotations.CategoryExpiry),
		Time:     time.Unix(0, 0),
		TimeEnd:  time.Unix(300, 0),
	}.Tags()
	if !slices.Contains(got, annotations.TagRollup) {
		t.Errorf("Tags() = %v, want the rollup tag on a region annotation", got)
	}
}

// ---- dedupe key ----------------------------------------------------------

// TestDedupeKeyIsPureAndStable is the property the whole feature rests on: the
// same occurrence seen twice must derive the same key, with no clock or counter
// in it. A key that varied per call would make every re-delivery look like a
// second real event, which once in Grafana is indistinguishable from the truth.
func TestDedupeKeyIsPureAndStable(t *testing.T) {
	first := annotations.DedupeKey("tn", "rule", "a", "b")
	for range 5 {
		if got := annotations.DedupeKey("tn", "rule", "a", "b"); got != first {
			t.Fatalf("DedupeKey is not pure: %q then %q", first, got)
		}
	}
	if len(first) != 32 {
		t.Errorf("DedupeKey length = %d, want 32 hex characters", len(first))
	}
}

// TestDedupeKeyComponentsCannotCollide pins the length-prefixing. Without it
// ("a","bc") and ("ab","c") hash identically, and two unrelated events silently
// suppress each other.
func TestDedupeKeyComponentsCannotCollide(t *testing.T) {
	if annotations.DedupeKey("tn", "rule", "a", "bc") == annotations.DedupeKey("tn", "rule", "ab", "c") {
		t.Error("DedupeKey collides across a component boundary; the length prefix is not working")
	}
	if annotations.DedupeKey("tn1", "rule", "x") == annotations.DedupeKey("tn2", "rule", "x") {
		t.Error("DedupeKey ignores the tailnet; two tailnets would dedupe each other away")
	}
	if annotations.DedupeKey("tn", "rule1", "x") == annotations.DedupeKey("tn", "rule2", "x") {
		t.Error("DedupeKey ignores the rule id")
	}
}

// ---- rules ---------------------------------------------------------------

// TestRuleIDsAreUniqueAndNonEmpty guards the frozen ids: two rules sharing an
// id would share a dedupe namespace and suppress each other's annotations.
func TestRuleIDsAreUniqueAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for _, rule := range annotations.Rules() {
		if rule.ID == "" || rule.EventName == "" || rule.Identity == nil || rule.Title == nil {
			t.Errorf("rule %+v is missing a required field", rule.ID)
		}
		if seen[rule.ID] {
			t.Errorf("duplicate rule id %q", rule.ID)
		}
		seen[rule.ID] = true
		if !slices.Contains(annotations.Categories(), rule.Category) {
			t.Errorf("rule %q has category %q, which is not in the closed set", rule.ID, rule.Category)
		}
	}
}

// TestEveryRuleReadsADeclaredLogEvent is the anti-drift check. A rule naming an
// event nothing emits is silently dead — it compiles, it runs, it just never
// annotates — which is exactly how a renamed collector event would break this
// feature without a single failing test.
func TestEveryRuleReadsADeclaredLogEvent(t *testing.T) {
	declared := map[string]bool{}
	for _, ev := range catalog.LogEvents() {
		declared[ev.Name] = true
	}
	for _, rule := range annotations.Rules() {
		if !declared[rule.EventName] {
			t.Errorf("rule %q reads event %q, which no collector declares in internal/catalog",
				rule.ID, rule.EventName)
		}
	}
}

func auditRecord(property, targetType, action string) telemetry.Event {
	return telemetry.Event{
		Name:      "tailscale.config.audit",
		Timestamp: time.Unix(1_700_000_000, 0),
		Attrs: telemetry.Attrs{
			"tailscale.audit.action":         action,
			"tailscale.target.property":      property,
			"tailscale.target.type":          targetType,
			"tailscale.target.id":            "target-1",
			"tailscale.target.name":          "target one",
			"tailscale.audit.event_group_id": "group-1",
			semconv.AttrUserName:             "admin@example.com",
		},
	}
}

func TestConfigChangeRuleMatchesOnlyCuratedChanges(t *testing.T) {
	rule := ruleByID(t, annotations.RuleConfigChange)
	cases := []struct {
		name                     string
		property, target, action string
		want                     bool
	}{
		{"curated property", "ACL", "TAILNET", "UPDATE", true},
		{"device churn", "", "NODE", "DELETE", true},
		{"api key lifecycle", "", "API_KEY", "REVOKE", true},
		{"excluded property", "ACL_TAGS", "NODE", "UPDATE", false},
		{"routine node read", "", "NODE", "LOGIN", false},
		{"nothing at all", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rule.Match(auditRecord(tc.property, tc.target, tc.action).Attrs)
			if got != tc.want {
				t.Errorf("Match(%q, %q, %q) = %v, want %v", tc.property, tc.target, tc.action, got, tc.want)
			}
		})
	}
}

// TestExpiryIdentityIsStableAcrossPolls is THE regression test for the design
// decision replacing silent priming. Both expiry sources publish a COUNTDOWN
// that shrinks on every poll; if identity used it directly, every poll would
// mint a new key and annotate the same expiring key forever.
func TestExpiryIdentityIsStableAcrossPolls(t *testing.T) {
	t.Run("key", func(t *testing.T) {
		rule := ruleByID(t, annotations.RuleKeyExpiring)
		base := time.Unix(1_700_000_000, 0)
		var first []string
		// Six hours of polling, five minutes apart: the countdown drops by
		// exactly the elapsed time each tick, so the reconstructed instant —
		// and therefore the identity — must not move.
		for i := range 72 {
			elapsed := time.Duration(i) * 5 * time.Minute
			remaining := (72 * time.Hour) - elapsed
			attrs := telemetry.Attrs{
				"tailscale.key.id":                 "key-1",
				"tailscale.key.expires_in_seconds": remaining.Seconds(),
			}
			got := rule.Identity(attrs, base.Add(elapsed))
			if first == nil {
				first = got
				continue
			}
			if !reflect.DeepEqual(got, first) {
				t.Fatalf("identity moved at poll %d: %v, want %v (a countdown leaked into the key)", i, got, first)
			}
		}
	})

	t.Run("device", func(t *testing.T) {
		rule := ruleByID(t, annotations.RuleDeviceKeyExpiring)
		base := time.Unix(1_700_000_000, 0)
		var first []string
		for i := range 72 {
			elapsed := time.Duration(i) * 5 * time.Minute
			days := (10*24*time.Hour).Hours()/24 - elapsed.Hours()/24
			attrs := telemetry.Attrs{
				semconv.HostID:                         "dev-1",
				"tailscale.device.key_expires_in_days": days,
			}
			got := rule.Identity(attrs, base.Add(elapsed))
			if first == nil {
				first = got
				continue
			}
			if !reflect.DeepEqual(got, first) {
				t.Fatalf("identity moved at poll %d: %v, want %v", i, got, first)
			}
		}
	})
}

// TestExpiryIdentitySeparatesDistinctExpiries is the other half: stability must
// not have been bought by collapsing everything onto the entity id, or a key
// that is renewed and later expires again would never annotate a second time.
func TestExpiryIdentitySeparatesDistinctExpiries(t *testing.T) {
	rule := ruleByID(t, annotations.RuleKeyExpiring)
	at := time.Unix(1_700_000_000, 0)
	soon := rule.Identity(telemetry.Attrs{
		"tailscale.key.id":                 "key-1",
		"tailscale.key.expires_in_seconds": (24 * time.Hour).Seconds(),
	}, at)
	later := rule.Identity(telemetry.Attrs{
		"tailscale.key.id":                 "key-1",
		"tailscale.key.expires_in_seconds": (96 * time.Hour).Seconds(),
	}, at)
	if reflect.DeepEqual(soon, later) {
		t.Errorf("two different expiry instants share an identity: %v", soon)
	}
	other := rule.Identity(telemetry.Attrs{
		"tailscale.key.id":                 "key-2",
		"tailscale.key.expires_in_seconds": (24 * time.Hour).Seconds(),
	}, at)
	if reflect.DeepEqual(soon, other) {
		t.Errorf("two different keys share an identity: %v", soon)
	}
}

// TestRuleFunctionsSurviveGarbage: a rule must never be able to take the
// process down. Every predicate and renderer is fed missing, nil and
// wrongly-typed attributes.
func TestRuleFunctionsSurviveGarbage(t *testing.T) {
	garbage := []telemetry.Attrs{
		nil,
		{},
		{"tailscale.key.expires_in_seconds": "not a number"},
		{"tailscale.key.expires_in_seconds": nil},
		{"tailscale.device.key_expires_in_days": []string{"a", "b"}},
		{"tailscale.audit.action": 42, "tailscale.target.type": true},
	}
	for _, rule := range annotations.Rules() {
		for _, attrs := range garbage {
			if rule.Match != nil {
				_ = rule.Match(attrs)
			}
			_ = rule.Identity(attrs, time.Unix(0, 0))
			_ = rule.Title(attrs)
		}
	}
}

func ruleByID(t *testing.T, id string) annotations.Rule {
	t.Helper()
	for _, rule := range annotations.Rules() {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("no rule with id %q", id)
	return annotations.Rule{}
}

// ---- the tee -------------------------------------------------------------

// TestTeeForwardsEveryEmitterMethod is the structural guard, and it reads the
// SOURCE rather than using reflection.
//
// An Emitter method not overridden on teeEmitter is PROMOTED from the embedded
// interface, so the type still satisfies Emitter, still compiles, and — the
// trap — still reports the outer type as the receiver under reflection. A
// reflective version of this test therefore passes while asserting nothing;
// that exact mutation (deleting the Gauge override) was verified to slip
// straight through one. Parsing the declarations is the only check that
// actually fails when an override is missing.
func TestTeeForwardsEveryEmitterMethod(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "tee.go", nil, 0)
	if err != nil {
		t.Fatalf("parse tee.go: %v", err)
	}
	declared := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "teeEmitter" {
			declared[fn.Name.Name] = true
		}
	}

	emitterType := reflect.TypeOf((*telemetry.Emitter)(nil)).Elem()
	if emitterType.NumMethod() == 0 {
		t.Fatal("telemetry.Emitter has no methods; this test would assert nothing")
	}
	for i := range emitterType.NumMethod() {
		if name := emitterType.Method(i).Name; !declared[name] {
			t.Errorf("teeEmitter does not explicitly declare %s, so it is silently promoted "+
				"from the embedded Emitter; add an explicit forwarding method in tee.go", name)
		}
	}
}

// TestTeeForwardsRecordsUnchanged: the tee must be transparent. Anything it
// altered, dropped or reordered would be a data-loss path opened by a dashboard
// feature.
func TestTeeForwardsRecordsUnchanged(t *testing.T) {
	rec := telemetrytest.New()
	teed := annotations.Tee(rec.Emitter(), newTestRecorder(t, nil, nil), "tn")

	teed.Counter("tailscale.test.counter", "1", "d", 2, telemetry.Attrs{"k": "v"})
	teed.LogEvent(telemetry.Event{Name: "tailscale.test.event", Body: "body"})
	teed.LogEventCtx(context.Background(), telemetry.Event{Name: "tailscale.test.event", Body: "ctx"})

	if points := rec.MetricPoints("tailscale.test.counter"); len(points) != 1 {
		t.Fatalf("counter points = %d, want 1 (the tee dropped or duplicated a metric)", len(points))
	}
	logs := rec.LogRecords()
	if len(logs) != 2 {
		t.Fatalf("log records = %d, want 2 (the tee dropped or duplicated a log)", len(logs))
	}
	if logs[0].Body != "body" || logs[1].Body != "ctx" {
		t.Errorf("bodies = %q/%q, want body/ctx (the tee altered or reordered records)", logs[0].Body, logs[1].Body)
	}
}

// ---- recorder ------------------------------------------------------------

type capturingSink struct {
	published  []annotations.Annotation
	duplicates int
}

func (s *capturingSink) Publish(a annotations.Annotation) { s.published = append(s.published, a) }
func (s *capturingSink) Duplicate(string)                 { s.duplicates++ }

func newTestRecorder(t *testing.T, sink annotations.Sink, cats pii.Categories) *annotations.Recorder {
	t.Helper()
	if sink == nil {
		sink = &capturingSink{}
	}
	return annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryConfigChange: {Enabled: true},
				annotations.CategoryExpiry:       {Enabled: true},
			},
		},
		Sink:     sink,
		Dedupe:   annotations.NewDedupeStoreForTest(nil, time.Hour),
		Redactor: pii.New(cats),
	})
}

func TestRecorderPublishesOnceAndThenDeduplicates(t *testing.T) {
	sink := &capturingSink{}
	rec := newTestRecorder(t, sink, nil)
	ev := auditRecord("ACL", "TAILNET", "UPDATE")

	for range 4 {
		rec.ObserveEvent("tn", ev)
	}
	if len(sink.published) != 1 {
		t.Fatalf("published = %d, want 1 (re-delivery must dedupe)", len(sink.published))
	}
	if sink.duplicates != 3 {
		t.Errorf("duplicates counted = %d, want 3", sink.duplicates)
	}
	got := sink.published[0]
	if got.Tailnet != "tn" || got.RuleID != annotations.RuleConfigChange {
		t.Errorf("annotation = %+v, want tailnet tn and the config-change rule", got)
	}
	if !got.Time.Equal(ev.Timestamp) {
		t.Errorf("annotation time = %v, want the source event time %v", got.Time, ev.Timestamp)
	}
}

func TestRecorderIgnoresDisabledCategories(t *testing.T) {
	sink := &capturingSink{}
	rec := annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryConfigChange: {Enabled: false},
			},
		},
		Sink:   sink,
		Dedupe: annotations.NewDedupeStoreForTest(nil, time.Hour),
	})
	rec.ObserveEvent("tn", auditRecord("ACL", "TAILNET", "UPDATE"))
	if len(sink.published) != 0 {
		t.Errorf("published %d annotations for a disabled category", len(sink.published))
	}
}

// TestRecorderIgnoresUnknownCategoryByDefault: a category present in code but
// absent from an operator's config map must be OFF, or upgrading would start
// publishing a new marker class nobody asked for.
func TestRecorderIgnoresUnknownCategoryByDefault(t *testing.T) {
	sink := &capturingSink{}
	rec := annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{Categories: nil},
		Sink:   sink,
		Dedupe: annotations.NewDedupeStoreForTest(nil, time.Hour),
	})
	rec.ObserveEvent("tn", auditRecord("ACL", "TAILNET", "UPDATE"))
	if len(sink.published) != 0 {
		t.Errorf("published %d annotations with no category configuration", len(sink.published))
	}
}

// TestRecorderAppliesPIIFilterToText is the #518 privacy regression test. The
// tee wraps OUTSIDE otelEmitter, where pii_filter is applied, so the records
// the recorder sees are RAW. Without redaction here, disabling a category would
// suppress a value from OTLP while still publishing it to Grafana.
func TestRecorderAppliesPIIFilterToText(t *testing.T) {
	sink := &capturingSink{}
	rec := newTestRecorder(t, sink, pii.Categories{pii.CatEmails: false})
	rec.ObserveEvent("tn", auditRecord("ACL", "TAILNET", "UPDATE"))

	if len(sink.published) != 1 {
		t.Fatalf("published = %d, want 1", len(sink.published))
	}
	if strings.Contains(sink.published[0].Text, "admin@example.com") {
		t.Errorf("annotation text leaked a suppressed attribute: %q", sink.published[0].Text)
	}
}

// TestRecorderPublishesIdentityWhenPIIFilteringIsOff is the DEFAULT-BEHAVIOR
// contract, and it is the more important half of the pair above.
//
// pii_filter is opt-OUT redaction: every category defaults to true (emitted), so
// pii.New sees nothing disabled and takes its no-op fast path. An annotation on
// a default deployment must therefore carry the identifying detail that makes it
// worth clicking — who made the change, and to what. A well-meaning "redact
// annotations harder than OTLP" change would pass every other test in this file
// while quietly reducing every marker to "something changed".
func TestRecorderPublishesIdentityWhenPIIFilteringIsOff(t *testing.T) {
	sink := &capturingSink{}
	// nil categories is exactly what config.Default() resolves to: all-on.
	rec := newTestRecorder(t, sink, nil)
	rec.ObserveEvent("tn", auditRecord("ACL", "TAILNET", "UPDATE"))

	if len(sink.published) != 1 {
		t.Fatalf("published = %d, want 1", len(sink.published))
	}
	text := sink.published[0].Text
	for _, want := range []string{"admin@example.com", "target one", "UPDATE"} {
		if !strings.Contains(text, want) {
			t.Errorf("annotation text is missing %q with PII filtering off: %q", want, text)
		}
	}
}

// TestRecorderTextHonoursTheDetailAllowList: an attribute the rule does not
// name must never reach Grafana, so a field added to a source record later
// cannot silently ride out.
func TestRecorderTextHonoursTheDetailAllowList(t *testing.T) {
	sink := &capturingSink{}
	rec := newTestRecorder(t, sink, nil)
	ev := auditRecord("ACL", "TAILNET", "UPDATE")
	ev.Attrs["tailscale.audit.details"] = "SECRET-NOT-ALLOW-LISTED"
	rec.ObserveEvent("tn", ev)

	if len(sink.published) != 1 {
		t.Fatalf("published = %d, want 1", len(sink.published))
	}
	if strings.Contains(sink.published[0].Text, "SECRET-NOT-ALLOW-LISTED") {
		t.Errorf("annotation text carried a non-allow-listed attribute: %q", sink.published[0].Text)
	}
}

func TestRecorderRollsUpIntoRegionAnnotations(t *testing.T) {
	sink := &capturingSink{}
	start := time.Unix(1_700_000_000, 0).UTC().Truncate(5 * time.Minute)
	rec := annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryConfigChange: {Enabled: true, Rollup: true},
			},
			RollupInterval: 5 * time.Minute,
		},
		Sink:   sink,
		Dedupe: annotations.NewDedupeStoreForTest(nil, time.Hour),
		Now:    func() time.Time { return start },
	})

	for i := range 3 {
		ev := auditRecord("ACL", "TAILNET", "UPDATE")
		ev.Attrs["tailscale.target.id"] = string(rune('a' + i))
		ev.Timestamp = start.Add(time.Duration(i) * time.Second)
		rec.ObserveEvent("tn", ev)
	}
	if len(sink.published) != 0 {
		t.Fatalf("published %d annotations before the bucket closed", len(sink.published))
	}

	rec.Flush(start.Add(5 * time.Minute))
	if len(sink.published) != 1 {
		t.Fatalf("published = %d after flush, want 1 rollup", len(sink.published))
	}
	got := sink.published[0]
	if got.TimeEnd.IsZero() {
		t.Error("rollup has no TimeEnd, so it renders as an instant rather than a region")
	}
	if !strings.Contains(got.Text, "3 config_change events") {
		t.Errorf("rollup text = %q, want the occurrence count", got.Text)
	}
	if got.RuleID != annotations.RollupRuleID(annotations.CategoryConfigChange) {
		t.Errorf("rollup rule id = %q, want the distinct rollup id", got.RuleID)
	}
}

// TestFlushAllDrainsTheOpenBucket: a shutdown must not silently discard events
// already recorded into a bucket that has not closed yet.
func TestFlushAllDrainsTheOpenBucket(t *testing.T) {
	sink := &capturingSink{}
	start := time.Unix(1_700_000_000, 0).UTC()
	rec := annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryConfigChange: {Enabled: true, Rollup: true},
			},
			RollupInterval: time.Hour,
		},
		Sink:   sink,
		Dedupe: annotations.NewDedupeStoreForTest(nil, 24*time.Hour),
		Now:    func() time.Time { return start },
	})
	rec.ObserveEvent("tn", auditRecord("ACL", "TAILNET", "UPDATE"))
	rec.FlushAll()
	if len(sink.published) != 1 {
		t.Errorf("published = %d after FlushAll, want the open bucket drained", len(sink.published))
	}
}

// ---- dedupe persistence --------------------------------------------------

// TestDedupeSurvivesRestart: the whole reason the set is on disk. A restart
// must not republish everything still inside the collectors' overlap windows.
func TestDedupeSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	at := time.Unix(1_700_000_000, 0)

	first := annotations.NewDedupeStoreAtPathForTest(t, path, 24*time.Hour)
	if !first.Claim("rule", "key-1", at) {
		t.Fatal("first claim was refused")
	}
	if err := first.Persist(at); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	second := annotations.NewDedupeStoreAtPathForTest(t, path, 24*time.Hour)
	if second.Claim("rule", "key-1", at) {
		t.Error("a restart re-claimed an already-published key; the set did not persist")
	}
}

// TestDedupeEvictsPastRetention bounds the file. Without eviction the set grows
// for the life of the deployment.
func TestDedupeEvictsPastRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	old := time.Unix(1_700_000_000, 0)

	store := annotations.NewDedupeStoreAtPathForTest(t, path, time.Hour)
	store.Claim("rule", "old-key", old)
	if err := store.Persist(old.Add(2 * time.Hour)); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	reopened := annotations.NewDedupeStoreAtPathForTest(t, path, time.Hour)
	if !reopened.Claim("rule", "old-key", old.Add(2*time.Hour)) {
		t.Error("an entry past the retention window was not evicted")
	}
}

// TestDedupeRefreshesStillCurrentEntries: a key inside its expiry window is
// re-observed every poll for days. If a re-delivery did not refresh the entry's
// clock, it would age out of retention while still being observed and be
// republished — an annotation storm on exactly the longest-lived condition.
func TestDedupeRefreshesStillCurrentEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	base := time.Unix(1_700_000_000, 0)

	store := annotations.NewDedupeStoreAtPathForTest(t, path, time.Hour)
	store.Claim("rule", "key-1", base)
	// Re-observed every 30 minutes for three hours — longer than the retention.
	for i := 1; i <= 6; i++ {
		at := base.Add(time.Duration(i) * 30 * time.Minute)
		if store.Claim("rule", "key-1", at) {
			t.Fatalf("re-delivery at %v was treated as a new occurrence", at)
		}
		if err := store.Persist(at); err != nil {
			t.Fatalf("Persist: %v", err)
		}
	}
	if store.Claim("rule", "key-1", base.Add(3*time.Hour)) {
		t.Error("a continuously-observed entry aged out of retention and would be republished")
	}
}

// ---- client --------------------------------------------------------------

// TestPayloadUsesEpochMilliseconds is the silent-failure guard. Grafana accepts
// second-resolution times with a 200 and puts the annotation in 1970, where it
// is invisible on every dashboard and nothing reports a problem.
func TestPayloadUsesEpochMilliseconds(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, "")
	at := time.Unix(1_700_000_000, 500_000_000)
	err := client.Publish(context.Background(), annotations.Annotation{
		Category: annotations.CategoryLifecycle,
		RuleID:   annotations.RuleStartup,
		Time:     at,
		Text:     "started",
	}, []string{"env:prod"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var got struct {
		Time    int64    `json:"time"`
		TimeEnd int64    `json:"timeEnd"`
		Tags    []string `json:"tags"`
		Text    string   `json:"text"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Time != at.UnixMilli() {
		t.Errorf("time = %d, want epoch milliseconds %d", got.Time, at.UnixMilli())
	}
	if got.TimeEnd != 0 {
		t.Errorf("timeEnd = %d on a non-region annotation, want it omitted", got.TimeEnd)
	}
	if !slices.Contains(got.Tags, "env:prod") {
		t.Errorf("tags = %v, want the operator's extra tag appended", got.Tags)
	}
	if !slices.Contains(got.Tags, annotations.TagRoot) {
		t.Errorf("tags = %v, want the root selector", got.Tags)
	}
}

func TestPublishClassifiesFailures(t *testing.T) {
	cases := []struct {
		status int
		want   annotations.FailureCode
	}{
		{http.StatusUnauthorized, annotations.FailureUnauthorized},
		{http.StatusForbidden, annotations.FailureUnauthorized},
		{http.StatusTooManyRequests, annotations.FailureRateLimited},
		{http.StatusBadRequest, annotations.FailureRejected},
		{http.StatusInternalServerError, annotations.FailureServerError},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "42")
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			err := newTestClient(t, srv.URL, "").Publish(context.Background(),
				annotations.Annotation{RuleID: "r"}, nil)
			var pubErr *annotations.PublishError
			if !errors.As(err, &pubErr) {
				t.Fatalf("Publish error = %v, want a *PublishError", err)
			}
			if pubErr.Code != tc.want {
				t.Errorf("code = %q, want %q", pubErr.Code, tc.want)
			}
			if pubErr.RetryAfter != 42*time.Second {
				t.Errorf("RetryAfter = %v, want 42s parsed from the header", pubErr.RetryAfter)
			}
		})
	}
}

func TestNewClientRejectsUnusableConfiguration(t *testing.T) {
	cases := []struct {
		name string
		cfg  annotations.ClientConfig
	}{
		{"no scheme", annotations.ClientConfig{URL: "grafana.example.com:3000", Token: "t"}},
		{"wrong scheme", annotations.ClientConfig{URL: "ftp://grafana.example.com", Token: "t"}},
		{"no token", annotations.ClientConfig{URL: "https://grafana.example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := annotations.NewClient(tc.cfg); err == nil {
				t.Error("NewClient accepted a configuration that would fail on every write")
			}
		})
	}
}

// ---- publisher -----------------------------------------------------------

// TestStartIsSilentWhenUnconfigured: the default deployment must cost nothing.
func TestStartIsSilentWhenUnconfigured(t *testing.T) {
	a, err := annotations.Start(context.Background(), annotations.Options{})
	if err != nil {
		t.Fatalf("Start with no URL returned an error: %v", err)
	}
	if a != nil {
		t.Error("Start built a writer with no URL configured")
	}
	// A nil *Annotator must be a working no-op at every call site.
	base := telemetrytest.New().Emitter()
	if got := a.Decorate("tn", base); got != base {
		t.Error("a nil Annotator's Decorate did not pass the emitter through")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Errorf("a nil Annotator's Close returned %v", err)
	}
}

// TestStartRefusesAnUnwritableToken is the fatal-preflight contract. Finding
// this at the first real event means the context an operator came looking for
// was never there, and nothing said so.
func TestStartRefusesAnUnwritableToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := annotations.Start(context.Background(), testOptions(t, srv.URL))
	if err == nil {
		t.Fatal("Start succeeded with a token that cannot write")
	}
	if !strings.Contains(err.Error(), "annotations:create") {
		t.Errorf("error does not name the required Grafana action: %v", err)
	}
}

// TestStartWritesTheLifecycleMarker: the probe and the marker are the same
// write, so a successful start leaves exactly one real annotation rather than
// junk needing a delete permission to clean up.
func TestStartWritesTheLifecycleMarker(t *testing.T) {
	var posted atomic.Int64
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted.Add(1)
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, err := annotations.Start(context.Background(), testOptions(t, srv.URL))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = a.Close(context.Background()) }()

	if posted.Load() != 1 {
		t.Fatalf("preflight wrote %d annotations, want exactly 1", posted.Load())
	}
	if !strings.Contains(string(body), "tailscale2otel v-test started") {
		t.Errorf("lifecycle marker text = %s, want the version", body)
	}
	if !strings.Contains(string(body), "category:lifecycle") {
		t.Errorf("lifecycle marker is not tagged as lifecycle: %s", body)
	}
}

// TestPublishNeverBlocks: the caller is a collector goroutine mid-poll. A full
// queue must drop and count, never wait on Grafana.
func TestPublishNeverBlocks(t *testing.T) {
	workerStarted := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Start's first request is the synchronous lifecycle preflight. Hold the
		// first worker request after that until the test has a full queue, giving
		// the Publish calls a deterministic stalled-worker barrier.
		if requests.Add(1) == 2 {
			close(workerStarted)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := testOptions(t, srv.URL)
	opts.Config.QueueSize = 1
	a, err := annotations.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = a.Close(context.Background()) }()

	a.Publish(annotations.Annotation{RuleID: "r", Time: time.Unix(1, 0)})
	<-workerStarted
	// The worker owns the first item and is blocked in the server; QueueSize=1
	// therefore makes the next item fill the queue. Calling Publish directly is
	// the completion barrier: every call must return without waiting on Grafana.
	for range 10_000 {
		a.Publish(annotations.Annotation{RuleID: "r", Time: time.Unix(1, 0)})
	}
	close(release)
}

func testOptions(t *testing.T, url string) annotations.Options {
	t.Helper()
	return annotations.Options{
		Config: annotations.Config{
			Client: annotations.ClientConfig{
				URL: url, Token: "test-token", Timeout: 5 * time.Second,
			},
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryConfigChange: {Enabled: true},
			},
			RollupInterval:  5 * time.Minute,
			DedupeRetention: time.Hour,
			StateFile:       filepath.Join(t.TempDir(), "annotations.json"),
			QueueSize:       16,
			MaxPerMinute:    600,
		},
		Version:   "v-test",
		StartedAt: time.Unix(1_700_000_000, 0),
	}
}

func newTestClient(t *testing.T, url, dashboardUID string) *annotations.Client {
	t.Helper()
	client, err := annotations.NewClient(annotations.ClientConfig{
		URL: url, Token: "test-token", DashboardUID: dashboardUID, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}
