package b0api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPAMFixtureRedactorRemovesSensitiveValues(t *testing.T) {
	input := t.TempDir()
	output := t.TempDir()
	document := `{
		"id":"real-id","name":"real-name","owner_email":"real@example.com",
		"cloud_authentication_email_allowed_domains":["real-domain.example"],
		"client_ip":"203.0.113.99","client_port":"61234","picture":"https://real.example/avatar",
		"upstream_password":"real-password","protected_username":"real-user",
		"token":"real-token","access_token":"real-access-token",
		"refresh_token":"real-refresh-token","bearer_token":"real-bearer-token",
		"authorization":"real-authorization",
		"certificate":{"ssh_public_key":"real-public-key"},
		"future":{"unrecognized_sensitive_field":"real-future-secret"},
		"auth_info":"{\"allowed\":[\"real authorization detail\"],\"status\":\"real embedded status\"}",
		"events":[{"metadata":"{\"command\":\"cat /real/secret\",\"username\":\"real-user\",\"real-dynamic-secret-key\":\"real-dynamic-secret-value\"}"}],
		"tags":{"status":"real tag value","tag:real-operator-name":true}
	}`
	if err := os.WriteFile(filepath.Join(input, "pam_synthetic.json"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "../../scripts/redact-pam-fixtures.py", "--allow-partial", "--input", input, "--output", output)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run redactor: %v\n%s", err, result)
	}
	redacted, err := os.ReadFile(filepath.Join(output, "pam_synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"real-id", "real-name", "real@example.com", "203.0.113.99", "61234",
		"real.example", "real-password", "real-user", "real-public-key",
		"real-token", "real-access-token", "real-refresh-token", "real-bearer-token", "real-authorization",
		"real-future-secret",
		"real-dynamic-secret-key", "real-dynamic-secret-value",
		"real-domain.example", "real authorization detail", "real embedded status",
		"real tag value", "cat /real/secret", "tag:real-operator-name",
	} {
		if strings.Contains(string(redacted), forbidden) {
			t.Errorf("redacted fixture retains forbidden value %q", forbidden)
		}
	}
	var shape map[string]any
	if err := json.Unmarshal(redacted, &shape); err != nil {
		t.Fatal(err)
	}
	if _, ok := shape["client_port"].(string); !ok {
		t.Fatalf("redacted client_port type = %T, want string to preserve the live wire shape", shape["client_port"])
	}
	domains, ok := shape["cloud_authentication_email_allowed_domains"].([]any)
	if !ok || len(domains) != 1 {
		t.Fatalf("redacted domain list = %#v, want one domain", shape["cloud_authentication_email_allowed_domains"])
	}
	domain, ok := domains[0].(string)
	if !ok || !strings.HasPrefix(domain, "host-") || strings.Contains(domain, "@") {
		t.Fatalf("redacted domain = %#v, want hostname-shaped replacement", domains[0])
	}
}

func TestPAMFixtureRedactorRefusesToOverwriteInput(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "pam_synthetic.json")
	original := []byte(`{"owner_email":"real@example.com"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "../../scripts/redact-pam-fixtures.py", "--allow-partial", "--input", directory, "--output", directory)
	if result, err := command.CombinedOutput(); err == nil || !strings.Contains(string(result), "must not overwrite") {
		t.Fatalf("redactor result = %q, err = %v; want overwrite refusal", result, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("input fixture changed despite overwrite refusal: %q", after)
	}
}

func TestPAMFixtureRedactorRejectsAddressCapacityOverflow(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		count      int
		wantMarker string
	}{
		{name: "IPv4", field: "client_ip", count: 256, wantMarker: "IPv4 replacement capacity exhausted"},
		{name: "IPv6", field: "private_network_ipv6", count: 65536, wantMarker: "IPv6 replacement capacity exhausted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := t.TempDir()
			output := t.TempDir()
			documents := make([]map[string]string, tt.count)
			for i := range documents {
				documents[i] = map[string]string{tt.field: fmt.Sprintf("captured-address-%d", i)}
			}
			body, err := json.Marshal(documents)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(input, "pam_synthetic.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("python3", "../../scripts/redact-pam-fixtures.py", "--allow-partial", "--input", input, "--output", output)
			if result, err := command.CombinedOutput(); err == nil || !strings.Contains(string(result), tt.wantMarker) {
				t.Fatalf("redactor result = %q, err = %v; want %s", result, err, tt.wantMarker)
			}
		})
	}
}

func TestTrackedPAMFixturesAreValidAndPublicSafe(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/pam_*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 19 {
		t.Fatalf("tracked PAM fixture count = %d, want 19", len(fixtures))
	}
	for _, fixture := range fixtures {
		body, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(body, &document); err != nil {
			t.Errorf("%s: invalid JSON: %v", fixture, err)
		}
		assertRedactedPAMValue(t, document, "", filepath.Base(fixture))
		for _, forbidden := range []string{"@gmail.com", "@m7kni", "BEGIN PRIVATE KEY", "ssh-rsa ", "ssh-ed25519 "} {
			if strings.Contains(string(body), forbidden) {
				t.Errorf("%s retains forbidden marker %q", fixture, forbidden)
			}
		}
	}
}

func assertRedactedPAMValue(t *testing.T, value any, key, path string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if key == "tags" && !strings.HasPrefix(childKey, "tag:fixture-") {
				t.Errorf("%s contains unredacted tag key %q", path, childKey)
			}
			assertRedactedPAMValue(t, child, childKey, path+"."+childKey)
		}
	case []any:
		for index, child := range typed {
			assertRedactedPAMValue(t, child, key, fmt.Sprintf("%s[%d]", path, index))
		}
	case string:
		if key == "auth_info" || key == "metadata" {
			var nested any
			if json.Unmarshal([]byte(typed), &nested) == nil {
				assertRedactedPAMValue(t, nested, key, path+"<json>")
				return
			}
		}
		prefix := ""
		switch {
		case key == "id" || strings.HasSuffix(key, "_id") || key == "sub" || key == "created_by" || key == "service_account" || key == "group" || key == "socket_ids":
			prefix = "00000000-0000-4000-8000-"
		case strings.HasSuffix(key, "email") || key == "email":
			prefix = "person-"
		case key == "client_ip" || key == "ip" || key == "ip_address" || key == "private_network_ipv4":
			prefix = "192.0.2."
		case key == "private_network_ipv6":
			prefix = "2001:db8::"
		case key == "picture" || key == "image_url":
			prefix = "https://example.invalid/"
		case key == "hostname" || key == "dnsname" || key == "upstream_http_hostname" || key == "server_name" || key == "subdomain":
			prefix = "host-"
		case key == "token" || key == "access_token" || key == "refresh_token" || key == "bearer_token" || key == "authorization" || key == "password" || key == "upstream_password" || key == "protected_password" || key == "private_key" || key == "mtls_certificate" || key == "ssh_public_key":
			prefix = "fixture-secret-"
		case key == "protected_username" || key == "upstream_username" || key == "username" || key == "sshuser":
			prefix = "fixture-username-"
		case key == "name" || key == "display_name" || key == "connector_name" || key == "database_name" || key == "socket_name" || key == "nickname":
			prefix = "fixture-name-"
		}
		if prefix != "" && !strings.HasPrefix(typed, prefix) {
			t.Errorf("%s value is not deterministically redacted", path)
		}
	}
}
