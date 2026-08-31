package ci_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestBrokerAndReadWIFSecretsStayDistinct pins the collision that broke three
// workflows on 2026-08-31.
//
// secrets.TS_WIF_* named ONE identity while two groups of workflows needed
// mutually exclusive ones:
//
//   - the broker-token consumers (release-please, trigger-docs-sync,
//     grafana-sync) JOIN THE TAILNET, which needs a federated identity with the
//     auth_keys scope and a tag;
//   - live-contract.yml calls the Tailscale API directly, which needs all:read.
//
// A federated identity cannot hold both scope sets, so whichever group was
// fixed last silently broke the other. live-contract had failed every scheduled
// run for nine days; repointing the shared secret to an all:read identity fixed
// it and broke the other three with
// "creating authkey: Status: 403, calling actor does not have enough
// permissions" — a failure in the tailnet JOIN, which reads like an OpenBao or
// camden outage and is neither.
//
// Nothing else catches this: each workflow is individually valid, actionlint is
// happy, and three of the four only run on a path filter or a schedule, so the
// breakage surfaces days later in an unrelated workflow.
func TestBrokerAndReadWIFSecretsStayDistinct(t *testing.T) {
	brokerRe := regexp.MustCompile(`tailscale-(client-id|audience):\s*\$\{\{\s*secrets\.([A-Z0-9_]+)\s*\}\}`)
	secretRe := regexp.MustCompile(`secrets\.([A-Z0-9_]+)`)

	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatalf("read workflow dir: %v", err)
	}

	brokerSecrets := map[string][]string{} // secret name -> workflows using it via broker-token
	var liveContract []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(workflowDir, e.Name()))
		if readErr != nil {
			t.Fatalf("read %s: %v", e.Name(), readErr)
		}
		text := string(body)

		for _, m := range brokerRe.FindAllStringSubmatch(text, -1) {
			brokerSecrets[m[2]] = append(brokerSecrets[m[2]], e.Name())
		}
		// live-contract does its own token exchange rather than going through
		// the broker action, so it is identified by name, not by shape.
		if e.Name() == "live-contract.yml" {
			for _, m := range secretRe.FindAllStringSubmatch(text, -1) {
				if strings.HasPrefix(m[1], "TS_WIF") {
					liveContract = append(liveContract, m[1])
				}
			}
		}
	}

	if len(brokerSecrets) == 0 {
		t.Fatal("no broker-token WIF secret references found; this guard has stopped guarding anything")
	}
	if len(liveContract) == 0 {
		t.Fatal("no TS_WIF secret references found in live-contract.yml; this guard has stopped guarding anything")
	}

	for secret, workflows := range brokerSecrets {
		for _, lc := range liveContract {
			if secret == lc {
				sort.Strings(workflows)
				t.Errorf("secrets.%s is used BOTH by broker-token (%s), which needs an auth_keys "+
					"identity to join the tailnet, AND by live-contract.yml, which needs all:read. "+
					"One identity cannot hold both scope sets, so fixing either side breaks the other.",
					secret, strings.Join(workflows, ", "))
			}
		}
	}
}
