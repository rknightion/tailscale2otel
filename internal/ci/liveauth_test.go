// Guard over how the live-contract lane authenticates to the Tailscale API (#355).
//
// The lane used to mint its token from a stored OAuth client secret. That worked,
// but it meant a long-lived credential for a real tailnet sat in GitHub secrets
// indefinitely, and the workflow's own header asserted — wrongly — that no
// OIDC path to the Tailscale API existed. It does: a federated identity pinned to
// this repo's issuer and `sub` claim exchanges a GitHub OIDC token for a
// short-lived API token, so nothing exploitable is stored at all.
//
// That property is one careless edit from being undone, and undoing it would look
// like the lane still working. These tests make the regression loud.
package ci_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const liveWorkflow = workflowDir + "/live-contract.yml"

func liveWorkflowSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(liveWorkflow))
	if err != nil {
		t.Fatalf("read %s: %v", liveWorkflow, err)
	}
	return string(b)
}

// TestLiveLaneStoresNoLongLivedTailscaleSecret is the acceptance criterion of
// #355 expressed as a test.
//
// TS_WIF_CLIENT_ID and TS_WIF_AUDIENCE are identifiers, not credentials — they
// grant nothing without a GitHub-signed OIDC token whose subject matches. A
// client SECRET is the opposite: possession is sufficient. So the secret name is
// what this asserts on, not the client ID.
func TestLiveLaneStoresNoLongLivedTailscaleSecret(t *testing.T) {
	src := liveWorkflowSource(t)
	doc := readWorkflow(t, "live-contract.yml")
	script := workflowScript(requiredSteps(t, requiredJob(t, doc, "live")))
	if problems := liveAuthProblems(script, src); len(problems) > 0 {
		t.Errorf("%v", problems)
	}
	t.Run("exchange removed from scripts", func(t *testing.T) {
		mutated := strings.ReplaceAll(script, "oauth/token-exchange", "other-token")
		if problems := liveAuthProblems(mutated, src); len(problems) == 0 {
			t.Fatal("exchange mutation escaped guard")
		}
	})
	t.Run("extra stored secret", func(t *testing.T) {
		if problems := liveAuthProblems(script, src+"\n${{ secrets.EXTRA_CREDENTIAL }}"); len(problems) == 0 {
			t.Fatal("secret mutation escaped guard")
		}
	})

}

// TestLiveLaneRequestsAnOIDCToken keeps the two halves of the OIDC path together.
//
// `id-token: write` without the request is a permission granted for nothing;
// the request without the permission leaves ACTIONS_ID_TOKEN_REQUEST_URL unset
// and the lane fails at runtime rather than at review time.
func TestLiveLaneRequestsAnOIDCToken(t *testing.T) {
	src := liveWorkflowSource(t)

	// Parsed, not substring-matched. `strings.Contains(src, "id-token: write")`
	// matches a COMMENTED-OUT line just as happily as a live one — caught by
	// mutation while writing this, which is the fifth time that class of vacuous
	// assertion has appeared in this repository.
	doc := readWorkflow(t, "live-contract.yml")
	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		t.Fatal("live-contract.yml has no top-level permissions mapping")
	}
	if perms["id-token"] != "write" {
		t.Errorf("live-contract.yml grants id-token: %v, want \"write\". Without it GitHub does "+
			"not expose ACTIONS_ID_TOKEN_REQUEST_URL, so no OIDC token can be minted and the "+
			"token exchange cannot happen.", perms["id-token"])
	}
	if !strings.Contains(src, "ACTIONS_ID_TOKEN_REQUEST_URL") {
		t.Error("live-contract.yml never reads ACTIONS_ID_TOKEN_REQUEST_URL, so it never requests " +
			"a GitHub OIDC token — `id-token: write` would then be a permission granted for nothing.")
	}
}

// TestLiveLaneKeepsTheNegativeExchangeCase.
//
// The positive case alone cannot distinguish "the identity is correctly pinned"
// from "the exchange accepts anything". Only a token the identity must REFUSE
// proves the binding, and this repo has shipped guards that asserted nothing
// four times — a security guard is the worst place for a fifth.
//
// It asserts on the wrong-audience token being minted and on the response being
// checked for an access_token, because a non-200 status alone would still pass
// if the endpoint ever returned a token alongside an error status.
func TestLiveLaneKeepsTheNegativeExchangeCase(t *testing.T) {
	src := liveWorkflowSource(t)

	if !strings.Contains(src, "audience=api.tailscale.com/not-this-identity") {
		t.Error("live-contract.yml no longer mints a wrong-audience OIDC token. Without a token " +
			"the identity must refuse, the lane proves only that SOME exchange succeeds — not " +
			"that this federated identity is actually pinned to this repository.")
	}
	if !strings.Contains(src, "SECURITY: the exchange ACCEPTED a token minted for a different audience.") {
		t.Error("live-contract.yml no longer fails when a wrong-audience token is ACCEPTED. " +
			"Minting the negative token without asserting on the outcome is a guard that " +
			"cannot fail.")
	}
	if !strings.Contains(src, "SECURITY: a rejected exchange still returned an access_token.") {
		t.Error("live-contract.yml checks the status code of the rejected exchange but not its " +
			"body. Tailscale returns an identical opaque 400 for every failure mode, so the " +
			"absence of an access_token is the half of the assertion that carries real weight.")
	}
}

func liveAuthProblems(script, source string) []string {
	var problems []string
	exchanges := 0
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "oauth/token-exchange") {
			exchanges++
		}
	}
	if exchanges < 2 {
		problems = append(problems, "missing two token-exchange script lines")
	}
	allowed := map[string]bool{"TS_WIF_CLIENT_ID": true, "TS_WIF_AUDIENCE": true, "ANTHROPIC_API_KEY": true, "CLAUDE_CODE_OAUTH_TOKEN": true}
	for _, match := range regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(source, -1) {
		if !allowed[match[1]] {
			problems = append(problems, "unexpected secrets."+match[1])
		}
	}
	return problems
}
