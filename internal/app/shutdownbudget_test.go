// Deployment shutdown budgets must exceed the binary's worst-case staged drain
// (#332).
//
// WHY THIS IS A TEST AND NOT A COMMENT. The drain is staged and each stage is
// separately bounded in Go, but the budget that decides whether the process is
// allowed to finish draining lives in YAML in another directory — Compose's
// `stop_grace_period` and the chart's `terminationGracePeriodSeconds`. Nothing
// connected the two. Compose defaults to 10 seconds and the chart inherited
// Kubernetes' 30, while the staged drain can legitimately run to 30 seconds, so
// the container was SIGKILLed mid-flush and the final interval's flow rollup,
// the WAL backlog and the last OTLP export were lost.
//
// Deriving the requirement from the constants rather than restating a number
// means raising any stage's timeout fails HERE, naming the deployment files to
// raise, instead of silently eroding the margin in production.
package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// worstCaseDrain is the wall-clock a stop can take with every stage at its
// bound. The stages run in sequence after the operator's context is canceled
// (see Run): the schedulers return immediately on cancellation, the receiver
// goroutines are joined, the ingress WAL performs one final drain, and the
// telemetry pipeline is flushed and shut down.
//
// Receivers are NOT summed: stream and webhook shut down in parallel goroutines
// joined by one receiverWG.Wait, so together they cost one receiverDrainTimeout.
func worstCaseDrain() time.Duration {
	return receiverDrainTimeout + ingressWALFlushTimeout + telemetryFlushTimeout
}

// requiredBudget is the drain plus headroom. A budget merely EQUAL to the drain
// is a coin flip: the stages are bounds, not durations, and process teardown,
// scheduler wake-up and container runtime overhead all land on top.
func requiredBudget() time.Duration { return worstCaseDrain() + shutdownBudgetHeadroom }

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

// TestComposeStopGracePeriodCoversDrain pins deploy/docker-compose.yaml.
func TestComposeStopGracePeriodCoversDrain(t *testing.T) {
	var doc struct {
		Services map[string]struct {
			StopGracePeriod string `yaml:"stop_grace_period"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(repoFile(t, "deploy/docker-compose.yaml"), &doc); err != nil {
		t.Fatalf("parse compose: %v", err)
	}
	svc, ok := doc.Services["tailscale2otel"]
	if !ok {
		t.Fatal("deploy/docker-compose.yaml has no tailscale2otel service")
	}
	if svc.StopGracePeriod == "" {
		t.Fatalf("deploy/docker-compose.yaml sets no stop_grace_period; Compose then defaults to "+
			"10s and SIGKILLs mid-drain, but a stop can take %s", worstCaseDrain())
	}
	got, err := time.ParseDuration(svc.StopGracePeriod)
	if err != nil {
		t.Fatalf("stop_grace_period %q is not a duration: %v", svc.StopGracePeriod, err)
	}
	if got < requiredBudget() {
		t.Errorf("stop_grace_period is %s; the worst-case staged drain is %s and the budget must "+
			"be at least %s (drain + %s headroom). Raise it in deploy/docker-compose.yaml.",
			got, worstCaseDrain(), requiredBudget(), shutdownBudgetHeadroom)
	}
}

// TestChartTerminationGracePeriodCoversDrain pins the chart default.
func TestChartTerminationGracePeriodCoversDrain(t *testing.T) {
	var doc struct {
		TerminationGracePeriodSeconds *int `yaml:"terminationGracePeriodSeconds"`
	}
	if err := yaml.Unmarshal(repoFile(t, "deploy/helm/tailscale2otel/values.yaml"), &doc); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}
	if doc.TerminationGracePeriodSeconds == nil {
		t.Fatalf("values.yaml sets no terminationGracePeriodSeconds; Kubernetes then defaults to "+
			"30s, but a stop can take %s and the budget must be at least %s",
			worstCaseDrain(), requiredBudget())
	}
	got := time.Duration(*doc.TerminationGracePeriodSeconds) * time.Second
	if got < requiredBudget() {
		t.Errorf("terminationGracePeriodSeconds is %s; the worst-case staged drain is %s and the "+
			"budget must be at least %s (drain + %s headroom). Raise it in values.yaml.",
			got, worstCaseDrain(), requiredBudget(), shutdownBudgetHeadroom)
	}
}

// TestChartMinimumMatchesDrain pins the chart's own guard: an operator
// who lowers terminationGracePeriodSeconds past the drain gets a render-time
// failure naming the minimum, not a pod that loses data on every rollout.
// The guard itself is exercised by deploy/helm/tests/render-tests.sh; this
// asserts the minimum it enforces still matches the code.
func TestChartMinimumMatchesDrain(t *testing.T) {
	body := string(repoFile(t, "deploy/helm/tailscale2otel/templates/deployment.yaml"))
	want := int(requiredBudget() / time.Second)
	needle := "terminationGracePeriodSeconds must be at least " + strconv.Itoa(want)
	if !strings.Contains(body, needle) {
		t.Errorf("deployment.yaml's minimum-budget guard does not name %d seconds.\n"+
			"The worst-case staged drain is %s, so the enforced minimum must be %s. "+
			"Expected the failure message to contain %q.",
			want, worstCaseDrain(), requiredBudget(), needle)
	}
}
