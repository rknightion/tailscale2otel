package k8saudit

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// CastHeader is the first line of an asciinema v2 .cast object.
//
// Only the header is ever read. The frames after it are raw terminal output —
// arbitrary secrets, in plain text — and this package never inspects them for
// meaning. Reading the header alone is also what makes an in-progress .cast
// safe: tsrecorder's server is closed source and there is no documented way to
// tell a finished recording from a live one, so anything that depended on the
// frames being complete would be guessing.
type CastHeader struct {
	Version   int    `json:"version"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Timestamp int64  `json:"timestamp"`
	Command   string `json:"command,omitempty"`
	SrcNode   string `json:"srcNode"`
	SrcNodeID string `json:"srcNodeID"`
	// DstNode/DstNodeID name the recorder that captured the session. Upstream's
	// sessionrecording.CastHeader declares NEITHER field, yet both are populated
	// in every header a live bucket contains — the same server-side enrichment
	// that fills Event.Destination on .event objects, which the OSS API-server
	// proxy likewise never sets. They are decoded here so a session log can say
	// which recorder holds the recording, exactly as an API-request log does.
	DstNode       string          `json:"dstNode"`
	DstNodeID     string          `json:"dstNodeID"`
	SrcNodeTags   []string        `json:"srcNodeTags,omitempty"`
	SrcNodeUserID int64           `json:"srcNodeUserID,omitempty"`
	SrcNodeUser   string          `json:"srcNodeUser,omitempty"`
	SSHUser       string          `json:"sshUser"`
	LocalUser     string          `json:"localUser"`
	ConnectionID  string          `json:"connectionID"`
	Kubernetes    *CastKubernetes `json:"kubernetes,omitempty"`
}

// CastKubernetes is untagged upstream, exactly like KubernetesInfo, so its four
// fields serialize PascalCase while every sibling on CastHeader is camelCase.
// As on KubernetesInfo, the tags restate the wire names for readability rather
// than out of necessity: encoding/json matches case-insensitively, so only a
// tag with genuinely different letters (json:"pod_name") would break decoding.
type CastKubernetes struct {
	PodName     string `json:"PodName"`
	Namespace   string `json:"Namespace"`
	Container   string `json:"Container"`
	SessionType string `json:"SessionType"`
}

// IsCastFrame reports whether a line is an asciinema output frame rather than a
// header. Frames are JSON arrays; the header is a JSON object. Recognizing them
// keeps a long session from minting thousands of decode-failure observations.
func IsCastFrame(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// DecodeCastHeader decodes an asciinema header line, rejecting anything that is
// not one.
func DecodeCastHeader(line []byte) (CastHeader, error) {
	var h CastHeader
	if err := json.Unmarshal(line, &h); err != nil {
		return CastHeader{}, fmt.Errorf("k8saudit: decode cast header: %w", err)
	}
	// version is mandatory in asciinema v2 and absent from every other shape we
	// might be handed, so it is the discriminator.
	if h.Version == 0 {
		return CastHeader{}, fmt.Errorf("%w: no asciinema version", ErrNotEvent)
	}
	return h, nil
}
