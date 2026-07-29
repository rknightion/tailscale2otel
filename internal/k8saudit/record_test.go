package k8saudit

import (
	"os"
	"testing"
	"time"
)

func TestDecodeObject_RealCorpusShape(t *testing.T) {
	raw, err := os.ReadFile("testdata/exec_request.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	obj, err := DecodeObject(raw)
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	if obj.StableID != "nRECORDER01CNTRL" {
		t.Fatalf("StableID = %q", obj.StableID)
	}
	if obj.Event.Type != "kubernetes-api-request" {
		t.Fatalf("Type = %q", obj.Event.Type)
	}
	// PascalCase tags on the untagged upstream copy.
	if obj.Event.Kubernetes.Verb != "get" {
		t.Fatalf("Verb = %q, want get (PascalCase json tag wired wrong?)", obj.Event.Kubernetes.Verb)
	}
	if obj.Event.Kubernetes.Namespace != "otel-demo" {
		t.Fatalf("Namespace = %q", obj.Event.Kubernetes.Namespace)
	}
	if obj.Event.Kubernetes.Subresource != "exec" {
		t.Fatalf("Subresource = %q", obj.Event.Kubernetes.Subresource)
	}
	// camelCase siblings.
	if obj.Event.Source.NodeUser != "user@example.com" {
		t.Fatalf("NodeUser = %q", obj.Event.Source.NodeUser)
	}
	// Destination IS populated by the closed-source server despite the OSS
	// proxy never setting it.
	if obj.Event.Destination.Node == "" {
		t.Fatal("Destination.Node empty; the wrapper does populate it")
	}
	want := time.Unix(1785325858, 0).UTC()
	if got := EventTimestamp(obj); !got.Equal(want) {
		t.Fatalf("EventTimestamp = %v, want %v", got, want)
	}
}

func TestDecodeObject_RejectsNonEventPadding(t *testing.T) {
	for _, in := range []string{`null`, `{}`, `[]`, `{"stableID":"x"}`} {
		if _, err := DecodeObject([]byte(in)); err == nil {
			t.Fatalf("DecodeObject(%s) = nil error, want rejection", in)
		}
	}
}
