package ingresswal

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	recordVersion     = byte(1)
	recordMagic       = "TS2WAL01"
	recordHeaderBytes = len(recordMagic) + 1 + 8
	checksumBytes     = sha256.Size
	idLength          = sha256.Size * 2

	// These bounds cover the largest existing receiver body (HEC, 64 MiB)
	// while keeping identity inputs independently bounded before hashing.
	maxTailnetBytes = 1024
	maxSourceBytes  = 64
	maxSignalBytes  = 64
	maxBodyBytes    = 64 << 20
)

// Envelope is the complete receiver-neutral unit persisted by the WAL. Body is
// already-decoded receiver input; transport authentication and tracing headers
// have no place in the record format.
type Envelope struct {
	ID       string
	Tailnet  string
	Source   string
	Signal   string
	Accepted time.Time
	Body     []byte
}

type diskEnvelope struct {
	Sequence uint64 `json:"sequence"`
	ID       string `json:"id"`
	Tailnet  string `json:"tailnet"`
	Source   string `json:"source"`
	Signal   string `json:"signal"`
	Accepted string `json:"accepted"`
	Body     []byte `json:"body"`
}

type storedRecord struct {
	Sequence uint64
	Envelope Envelope
}

// NewID returns the deterministic SHA-256 identity of one route and exact body.
// Accepted is deliberately excluded: a retry gets the same ID, and the first
// durable envelope retains the accepted timestamp used for replay.
func NewID(tailnet, source, signal string, body []byte) (string, error) {
	if err := validateIdentityInputs(tailnet, source, signal, body); err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte{recordVersion})
	for _, field := range [][]byte{[]byte(tailnet), []byte(source), []byte(signal), body} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(field)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validID(id string) bool {
	if len(id) != idLength || id != strings.ToLower(id) {
		return false
	}
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == sha256.Size
}

func encodeRecord(envelope Envelope, sequence uint64) ([]byte, error) {
	if sequence == 0 {
		return nil, fmt.Errorf("ingress WAL encode record: sequence must be positive")
	}
	if err := validateEnvelope(envelope); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(diskEnvelope{
		Sequence: sequence,
		ID:       envelope.ID,
		Tailnet:  envelope.Tailnet,
		Source:   envelope.Source,
		Signal:   envelope.Signal,
		Accepted: envelope.Accepted.Format(time.RFC3339Nano),
		Body:     envelope.Body,
	})
	if err != nil {
		return nil, fmt.Errorf("ingress WAL encode record: %w", err)
	}

	record := make([]byte, recordHeaderBytes+len(payload)+checksumBytes)
	copy(record, recordMagic)
	record[len(recordMagic)] = recordVersion
	binary.BigEndian.PutUint64(record[len(recordMagic)+1:recordHeaderBytes], uint64(len(payload)))
	copy(record[recordHeaderBytes:], payload)
	sum := sha256.Sum256(record[:recordHeaderBytes+len(payload)])
	copy(record[recordHeaderBytes+len(payload):], sum[:])
	return record, nil
}

func decodeRecord(data []byte) (storedRecord, error) {
	if len(data) < recordHeaderBytes+checksumBytes {
		return storedRecord{}, newCorruptError("entry is truncated")
	}
	if string(data[:len(recordMagic)]) != recordMagic {
		return storedRecord{}, newCorruptError("entry magic is invalid")
	}
	if got := data[len(recordMagic)]; got != recordVersion {
		return storedRecord{}, &IncompatibleError{Version: got}
	}
	payloadLength := binary.BigEndian.Uint64(data[len(recordMagic)+1 : recordHeaderBytes])
	actualPayloadLength := len(data) - recordHeaderBytes - checksumBytes
	if strconv.FormatUint(payloadLength, 10) != strconv.Itoa(actualPayloadLength) {
		return storedRecord{}, newCorruptError("entry is truncated or has trailing data")
	}
	payloadEnd := recordHeaderBytes + actualPayloadLength
	wantChecksum := data[payloadEnd:]
	gotChecksum := sha256.Sum256(data[:payloadEnd])
	if !bytes.Equal(wantChecksum, gotChecksum[:]) {
		return storedRecord{}, newCorruptError("entry checksum mismatch")
	}

	var disk diskEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data[recordHeaderBytes:payloadEnd]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&disk); err != nil {
		return storedRecord{}, newCorruptError("entry envelope is malformed")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return storedRecord{}, newCorruptError("entry envelope has trailing JSON")
	}
	if disk.Sequence == 0 {
		return storedRecord{}, newCorruptError("entry sequence is invalid")
	}
	accepted, err := time.Parse(time.RFC3339Nano, disk.Accepted)
	if err != nil {
		return storedRecord{}, newCorruptError("entry accepted time is invalid")
	}
	envelope := Envelope{
		ID:       disk.ID,
		Tailnet:  disk.Tailnet,
		Source:   disk.Source,
		Signal:   disk.Signal,
		Accepted: accepted,
		Body:     disk.Body,
	}
	if err := validateEnvelope(envelope); err != nil {
		return storedRecord{}, newCorruptError("entry envelope is invalid")
	}
	return storedRecord{Sequence: disk.Sequence, Envelope: envelope}, nil
}

func validateEnvelope(envelope Envelope) error {
	if !validID(envelope.ID) {
		return fmt.Errorf("ingress WAL envelope: invalid opaque ID")
	}
	if envelope.Accepted.IsZero() {
		return fmt.Errorf("ingress WAL envelope: accepted time is required")
	}
	want, err := NewID(envelope.Tailnet, envelope.Source, envelope.Signal, envelope.Body)
	if err != nil {
		return err
	}
	if envelope.ID != want {
		return fmt.Errorf("ingress WAL envelope: opaque ID does not match route and body")
	}
	return nil
}

func validateIdentityInputs(tailnet, source, signal string, body []byte) error {
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{name: "tailnet", value: tailnet, max: maxTailnetBytes},
		{name: "source", value: source, max: maxSourceBytes},
		{name: "signal", value: signal, max: maxSignalBytes},
	} {
		if field.value == "" {
			return fmt.Errorf("ingress WAL identity: %s is required", field.name)
		}
		if len(field.value) > field.max {
			return fmt.Errorf("ingress WAL identity: %s exceeds %d bytes", field.name, field.max)
		}
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("ingress WAL identity: body exceeds %d bytes", maxBodyBytes)
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("extra JSON value")
	}
	return err
}
