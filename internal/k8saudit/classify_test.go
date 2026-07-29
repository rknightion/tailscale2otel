package k8saudit

import "testing"

func TestClassifyCommand(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		// The real Freelens shell-spawn wrapper seen in the corpus.
		{"freelens shell", []string{"sh", "-c", "clear; (bash || ash || sh)"}, "interactive_shell"},
		// The real recon command seen in the corpus.
		{"recon", []string{"sh", "-c", "echo RECORDING_TEST_OK; id; uname -a"}, "recon"},
		{"bare shell", []string{"bash"}, "interactive_shell"},
		{"package mgmt", []string{"sh", "-c", "apt-get install -y curl"}, "package_mgmt"},
		{"net tool", []string{"curl", "https://example.com"}, "net_tool"},
		{"file transfer", []string{"tar", "cf", "-", "/data"}, "file_transfer"},
		{"credential read", []string{"cat", "/var/run/secrets/kubernetes.io/serviceaccount/token"}, "credential_read"},
		{"unknown", []string{"/opt/vendor/weird-binary"}, "other"},
		{"empty", nil, "none"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCommand(tc.argv); got != tc.want {
				t.Fatalf("ClassifyCommand(%q) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestClassifyCommand_IsBounded(t *testing.T) {
	// 2000 crafted commands must not mint 2000 classes.
	seen := map[string]struct{}{}
	for i := range 2000 {
		seen[ClassifyCommand([]string{"binary-" + string(rune('a'+i%26)), string(rune(i))})] = struct{}{}
	}
	for c := range seen {
		if !validCommandClasses[c] {
			t.Fatalf("ClassifyCommand minted unbounded class %q", c)
		}
	}
}

func TestNormalizeUserAgent(t *testing.T) {
	tests := []struct{ in, want string }{
		{"kubectl/v1.36.3 (darwin/arm64) kubernetes/0f29094", "kubectl"},
		{"Freelens/1.10.3", "freelens"},
		{"node-fetch", "node-fetch"},
		{"", "unknown"},
		{"Mozilla/5.0 (evil) totally-made-up", AttrOther},
	}
	for _, tc := range tests {
		if got := NormalizeUserAgent(tc.in); got != tc.want {
			t.Fatalf("NormalizeUserAgent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeVerbAndResourceAreBounded(t *testing.T) {
	if got := NormalizeVerb("DROP TABLE"); got != AttrOther {
		t.Fatalf("NormalizeVerb(hostile) = %q", got)
	}
	if got := NormalizeResource("my-crazy-crd-name"); got != AttrOther {
		t.Fatalf("NormalizeResource(unknown) = %q", got)
	}
	if got := NormalizeResource("secrets"); got != "secrets" {
		t.Fatalf("NormalizeResource(secrets) = %q", got)
	}
}

func TestExecCommand(t *testing.T) {
	obj := Object{Event: Event{Request: RequestInfo{
		QueryParameters: map[string][]string{"command": {"sh", "-c", "id"}},
	}}}
	got := ExecCommand(obj)
	if len(got) != 3 || got[0] != "sh" {
		t.Fatalf("ExecCommand = %q", got)
	}
}
