package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The audit object-store credentials declare *_file siblings (they share
// ObjectStoreConfig with flowlogs) and are consumed at runtime via
// objectStoreAuditSpec, but resolveSecretFiles only ever walked the FLOW ones.
// Setting a *_file path for an audit object store therefore did nothing at all:
// no file read, no error, and an empty credential handed to the S3 client —
// which surfaces much later as an authentication failure against the bucket,
// pointing at the wrong thing entirely.
//
// The Helm chart already treats these paths as credential-bearing
// (tailscale2otel.credentialPaths lists collectors.auditlogs.objectstore.*),
// so the gap was only ever in resolution.
func TestResolveSecretFiles_AuditObjectStore(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}

	c := Default()
	c.Collectors.Auditlogs.ObjectStore.AccessKeyIDFile = write("ak", "AUDIT_AK")
	c.Collectors.Auditlogs.ObjectStore.SecretAccessKeyFile = write("sk", "AUDIT_SK")
	c.Collectors.Auditlogs.ObjectStore.SessionTokenFile = write("st", "AUDIT_ST")

	if err := c.resolveSecretFiles(); err != nil {
		t.Fatalf("resolveSecretFiles: %v", err)
	}
	os := c.Collectors.Auditlogs.ObjectStore
	if os.AccessKeyID != "AUDIT_AK" {
		t.Errorf("access_key_id = %q, want AUDIT_AK — access_key_id_file was ignored", os.AccessKeyID)
	}
	if os.SecretAccessKey != "AUDIT_SK" {
		t.Errorf("secret_access_key = %q, want AUDIT_SK — secret_access_key_file was ignored", os.SecretAccessKey)
	}
	if os.SessionToken != "AUDIT_ST" {
		t.Errorf("session_token = %q, want AUDIT_ST — session_token_file was ignored", os.SessionToken)
	}
}

// Same gap on the per-tailnet side: the loop resolved t.ObjectStore.Flow and
// silently skipped t.ObjectStore.Audit. For a tailnets[] entry the *_file path
// is the ONLY way to supply a static credential without writing it into YAML,
// because a list-of-structs key has no TS2OTEL_* env form (#79) — so this was
// the one place with no working alternative.
func TestResolveSecretFiles_TailnetAuditObjectStore(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sk")
	if err := os.WriteFile(p, []byte("TN_AUDIT_SK\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := Default()
	c.Tailnets = []TailnetConfig{{Name: "a.example.com"}}
	c.Tailnets[0].ObjectStore.Audit.SecretAccessKeyFile = p

	if err := c.resolveSecretFiles(); err != nil {
		t.Fatalf("resolveSecretFiles: %v", err)
	}
	if got := c.Tailnets[0].ObjectStore.Audit.SecretAccessKey; got != "TN_AUDIT_SK" {
		t.Errorf("tailnets[0].objectstore.audit.secret_access_key = %q, want TN_AUDIT_SK — "+
			"the *_file sibling was ignored, and for a tailnets[] entry there is no env-var alternative", got)
	}
}

// An unreadable *_file path is a hard Load error everywhere else. The audit
// paths must not be quietly exempt, or a typo degrades to an empty credential.
func TestResolveSecretFiles_AuditObjectStoreMissingFileIsAnError(t *testing.T) {
	c := Default()
	c.Collectors.Auditlogs.ObjectStore.SecretAccessKeyFile = filepath.Join(t.TempDir(), "nope")
	err := c.resolveSecretFiles()
	if err == nil {
		t.Fatal("expected an error for an unreadable audit secret_access_key_file, got nil")
	}
	if !contains(err.Error(), "auditlogs.objectstore.secret_access_key") {
		t.Errorf("error %q should name collectors.auditlogs.objectstore.secret_access_key", err.Error())
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
