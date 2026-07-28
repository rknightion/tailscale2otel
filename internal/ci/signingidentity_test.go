// Guard over the release-verification commands documented in
// docs/installation.md (#338).
//
// Those commands are the one piece of documentation where being subtly wrong is
// worse than being absent: an operator who runs a `cosign verify` that fails
// concludes the release is compromised, and one that *cannot* fail teaches them
// to ignore it. Both failure modes are one edit away, because the identity a
// signature carries is not written anywhere in this repository — it is the
// `uses:` reference of the shared reusable workflow that did the signing, over
// in rknightion/.github.
//
// Two things this test exists to catch:
//
//  1. A documented identity that no longer matches the workflow that signs.
//     Renaming binaries.yml, or moving the reusables to a different org, breaks
//     every documented command silently — nothing in this repo fails, and the
//     next release is simply unverifiable.
//
//  2. A documented identity pinned to a literal SHA. The certificate SAN ends
//     in the reusable's pinned commit, so `--certificate-identity` with the full
//     string is correct for exactly one release and wrong after the next
//     Renovate bump of that pin. Verified live against v3.0.0, whose signature
//     names binaries.yml@d1c590b2… while main now pins a4182f2…. Only the
//     `-regexp` form survives.
package ci_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sharedWorkflowUses matches a `uses:` reference to a reusable workflow in the
// shared rknightion/.github repository, capturing owner/repo, the workflow file
// and the ref.
var sharedWorkflowUses = regexp.MustCompile(`uses:\s+(rknightion/\.github)/\.github/workflows/([\w.-]+)@(\S+)`)

// documentedIdentityRe matches a --certificate-identity-regexp argument in the
// docs, capturing the regexp itself (single-quoted, as written in a shell
// snippet).
var documentedIdentityRe = regexp.MustCompile(`--certificate-identity-regexp\s+'([^']+)'`)

// documentedIdentityLiteral matches the ROTTING form: a --certificate-identity
// with a literal value rather than a regexp.
var documentedIdentityLiteral = regexp.MustCompile(`--certificate-identity\s+(\S+)`)

const installDoc = repoDir + "/docs/installation.md"

// signingIdentities returns the certificate SAN that each reusable signing
// workflow referenced by this repo's workflows will present, derived from the
// real `uses:` lines rather than restated.
func signingIdentities(t *testing.T) map[string]string {
	t.Helper()
	got := map[string]string{}
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatalf("read workflow dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(workflowDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range sharedWorkflowUses.FindAllStringSubmatch(string(b), -1) {
			repo, file, ref := m[1], m[2], m[3]
			got[file] = "https://github.com/" + repo + "/.github/workflows/" + file + "@" + ref
		}
	}
	if len(got) == 0 {
		t.Fatal("found no `uses: rknightion/.github/.github/workflows/...` reference in any workflow — " +
			"either the shared reusables moved, or sharedWorkflowUses no longer matches how they are " +
			"referenced. Either way the documented cosign identities can no longer be checked.")
	}
	return got
}

// TestDocumentedSigningIdentitiesMatchTheSigningWorkflows keeps every
// --certificate-identity-regexp in the installation docs matching a reusable
// workflow this repo actually calls, in both directions.
func TestDocumentedSigningIdentitiesMatchTheSigningWorkflows(t *testing.T) {
	raw, err := os.ReadFile(installDoc)
	if err != nil {
		t.Fatalf("read %s: %v", installDoc, err)
	}
	doc := string(raw)

	matches := documentedIdentityRe.FindAllStringSubmatch(doc, -1)
	if len(matches) == 0 {
		t.Fatalf("%s documents no --certificate-identity-regexp. The release-verification section is "+
			"the point of #338; a cosign command without a pinned signer identity verifies that SOMEONE "+
			"signed the artifact, which is not a security property.", installDoc)
	}

	identities := signingIdentities(t)

	// Every documented regexp must match a workflow that really signs. A regexp
	// matching nothing is a command that fails for every user.
	for _, m := range matches {
		pattern := m[1]
		re, err := regexp.Compile(pattern)
		if err != nil {
			t.Errorf("documented --certificate-identity-regexp %q does not compile: %v", pattern, err)
			continue
		}
		var matched []string
		for file, identity := range identities {
			if re.MatchString(identity) {
				matched = append(matched, file)
			}
		}
		if len(matched) == 0 {
			t.Errorf("documented --certificate-identity-regexp %q matches none of the reusable workflows "+
				"this repo calls: %v.\nA cosign command with this identity fails for every user. Fix the "+
				"regexp in %s to match the workflow that now does the signing.",
				pattern, identities, installDoc)
		}
	}

	// And every workflow that signs must be documented. binaries.yml signs the
	// release checksums, container-publish.yml signs the image and chart; they
	// have DIFFERENT identities, and documenting only one leaves the other
	// unverifiable.
	for _, file := range []string{"binaries.yml", "container-publish.yml"} {
		identity, ok := identities[file]
		if !ok {
			t.Errorf("no workflow calls the shared %s, but the docs describe verifying what it signs", file)
			continue
		}
		var covered bool
		for _, m := range matches {
			if re, err := regexp.Compile(m[1]); err == nil && re.MatchString(identity) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("%s signs release artifacts as %q, but no --certificate-identity-regexp in %s "+
				"matches it, so those artifacts have no documented verification command",
				file, identity, installDoc)
		}
	}
}

// TestDocumentedIdentitiesAreNotSHAPinned rejects the literal
// --certificate-identity form, which is correct for exactly one release.
func TestDocumentedIdentitiesAreNotSHAPinned(t *testing.T) {
	raw, err := os.ReadFile(installDoc)
	if err != nil {
		t.Fatalf("read %s: %v", installDoc, err)
	}
	doc := string(raw)

	// -regexp is a superset match of the literal flag, so strip those first.
	stripped := documentedIdentityRe.ReplaceAllString(doc, "")
	if m := documentedIdentityLiteral.FindStringSubmatch(stripped); m != nil {
		t.Errorf("%s documents `--certificate-identity %s`. The certificate SAN ends in the SHA the "+
			"signing reusable was pinned at, so a literal identity is correct for exactly one release "+
			"and silently wrong after the next Renovate bump of that pin. Use "+
			"--certificate-identity-regexp anchored on the workflow path instead.", installDoc, m[1])
	}
}

// TestAttestationVerifyIsDocumentedWithSignerRepo keeps the --signer-repo flag
// attached to every documented `gh attestation verify`.
//
// Without it the command exits 1 with `verifying with issuer "sigstore.dev"`,
// which names neither the cause nor the fix — verified live against v3.0.0.
// The cause is the same shared-workflow indirection as above: the attestation
// is signed by rknightion/.github, not by this repo, and gh defaults to
// requiring the signer to be the repo passed to -R.
func TestAttestationVerifyIsDocumentedWithSignerRepo(t *testing.T) {
	raw, err := os.ReadFile(installDoc)
	if err != nil {
		t.Fatalf("read %s: %v", installDoc, err)
	}

	// Each documented invocation runs until the fence that closes its block.
	blocks := strings.Split(string(raw), "gh attestation verify")
	if len(blocks) < 2 {
		t.Fatalf("%s documents no `gh attestation verify` command; build provenance is attached to "+
			"every release and verifying it is half of #338", installDoc)
	}
	for i, b := range blocks[1:] {
		cmd := b
		if end := strings.Index(cmd, "```"); end >= 0 {
			cmd = cmd[:end]
		}
		if !strings.Contains(cmd, "--signer-repo") {
			t.Errorf("documented `gh attestation verify` #%d has no --signer-repo. Provenance is signed "+
				"by the shared rknightion/.github reusable, so without it gh exits 1 with "+
				"`verifying with issuer \"sigstore.dev\"` — a message that names neither cause nor fix.\n"+
				"command was:\n%s", i+1, strings.TrimSpace(cmd))
		}
	}
}
