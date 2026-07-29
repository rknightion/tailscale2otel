package k8saudit

import "testing"

const realCastHeader = `{"command":"sh -c clear; (bash || ash || sh)","connectionID":"","dstNode":"recorder.example.ts.net","dstNodeID":"nRECORDER01CNTRL","env":null,"height":16,"kubernetes":{"Container":"kafka","Namespace":"otel-demo","PodName":"kafka-65df5445b6-f4ttt","SessionType":"exec"},"localUser":"","srcNode":"laptop.example.ts.net","srcNodeID":"nLAPTOP001CNTRL","srcNodeUser":"user@example.com","srcNodeUserID":16793133573424020,"sshUser":"","timestamp":1785325858,"version":2,"width":155}`

func TestDecodeCastHeader(t *testing.T) {
	h, err := DecodeCastHeader([]byte(realCastHeader))
	if err != nil {
		t.Fatalf("DecodeCastHeader: %v", err)
	}
	if h.Version != 2 {
		t.Fatalf("Version = %d", h.Version)
	}
	// The nested Kubernetes struct is ALSO untagged upstream -> PascalCase.
	if h.Kubernetes == nil {
		t.Fatal("Kubernetes nil")
	}
	if h.Kubernetes.PodName != "kafka-65df5445b6-f4ttt" {
		t.Fatalf("PodName = %q (PascalCase tag wired wrong?)", h.Kubernetes.PodName)
	}
	if h.Kubernetes.SessionType != "exec" {
		t.Fatalf("SessionType = %q", h.Kubernetes.SessionType)
	}
	if h.Command != "sh -c clear; (bash || ash || sh)" {
		t.Fatalf("Command = %q", h.Command)
	}
}

func TestIsCastFrame(t *testing.T) {
	// Asciinema frames must be recognized so they are never counted as
	// corruption: they share an object with the header line.
	frames := []string{
		`[0.124251882,"o","RECORDING_TEST_OK\n"]`,
		`[1.5,"o","uid=1654(app)\n"]`,
	}
	for _, f := range frames {
		if !IsCastFrame([]byte(f)) {
			t.Fatalf("IsCastFrame(%s) = false", f)
		}
	}
	if IsCastFrame([]byte(realCastHeader)) {
		t.Fatal("header misidentified as a frame")
	}
}

func TestDecodeCastHeader_RejectsNonHeader(t *testing.T) {
	if _, err := DecodeCastHeader([]byte(`{"stableID":"x","event":{}}`)); err == nil {
		t.Fatal("an event object must not decode as a cast header")
	}
}

// TestDecodeCastHeader_DecodesRecorderIdentity pins the fields upstream does not
// declare. sessionrecording.CastHeader has no dstNode/dstNodeID, yet every
// header written by a live recorder carries them, so a decoder built from the
// upstream struct alone would silently drop the only link from a session back to
// the recording that holds it.
func TestDecodeCastHeader_DecodesRecorderIdentity(t *testing.T) {
	h, err := DecodeCastHeader([]byte(realCastHeader))
	if err != nil {
		t.Fatalf("DecodeCastHeader: %v", err)
	}
	if h.DstNode == "" {
		t.Error("DstNode empty: the recorder identity is present on the wire and must be decoded")
	}
	if h.DstNodeID == "" {
		t.Error("DstNodeID empty: the recorder identity is present on the wire and must be decoded")
	}
}
