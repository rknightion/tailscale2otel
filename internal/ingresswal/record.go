package ingresswal

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
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
	identity := sha256.New()
	writeIdentityPrefix(identity, tailnet, source, signal, uint64(len(body)))
	_, _ = identity.Write(body)
	return hex.EncodeToString(identity.Sum(nil)), nil
}

// writeIdentityPrefix hashes everything in an ID that precedes the body bytes,
// so a streaming reader and NewID cannot disagree on the identity format.
func writeIdentityPrefix(identity hash.Hash, tailnet, source, signal string, bodyLength uint64) {
	_, _ = identity.Write([]byte{recordVersion})
	var length [8]byte
	for _, field := range []string{tailnet, source, signal} {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = identity.Write(length[:])
		_, _ = io.WriteString(identity, field)
	}
	binary.BigEndian.PutUint64(length[:], bodyLength)
	_, _ = identity.Write(length[:])
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

// recordMeta is the body-free result of verifying one stored record.
type recordMeta struct {
	Sequence     uint64
	ID           string
	Tailnet      string
	Source       string
	Signal       string
	Accepted     time.Time
	EncodedBytes int64
}

const (
	recordScanBufferBytes = 32 << 10
	// A multiple of four, so every chunk is whole base64 groups.
	recordScanChunkBytes = 48 << 10
	maxAcceptedBytes     = 64
	maxSequenceDigits    = 20
)

var strictBase64 = base64.StdEncoding.Strict()

// recordSource is the open state file: read in order, with one positioned read
// of the final base64 group to learn the body length before hashing the body.
type recordSource interface {
	io.Reader
	io.ReaderAt
}

// scanCanonicalRecord verifies a record in the exact form encodeRecord writes
// without holding its body: checksum, route, accepted time and body-derived ID
// are all checked from a fixed buffer. When compare is true, bodyEqual reports
// whether the stored body equals want.
//
// ok=false claims nothing about the record. Any other shape, including forms
// encoding/json accepts (duplicate or case-folded keys, CRLF in base64, numeric
// array bodies, escapes), and any read error must go to decodeRecord, which
// stays the only authority on rejection and on non-canonical input.
func scanCanonicalRecord(
	file recordSource,
	size, maxBytes int64,
	want []byte,
	compare bool,
) (meta recordMeta, bodyEqual, ok bool) {
	minimum := int64(recordHeaderBytes + checksumBytes)
	if size < minimum || size > maxBytes {
		return recordMeta{}, false, false
	}
	payload := size - minimum
	scan := recordScanner{
		reader: bufio.NewReaderSize(file, int(min(size, recordScanBufferBytes))),
		sum:    sha256.New(),
		left:   int64(recordHeaderBytes) + payload,
	}
	var header [recordHeaderBytes]byte
	if !scan.take(header[:]) ||
		string(header[:len(recordMagic)]) != recordMagic ||
		header[len(recordMagic)] != recordVersion ||
		binary.BigEndian.Uint64(header[len(recordMagic)+1:]) != uint64(payload) {
		return recordMeta{}, false, false
	}

	sequence, sequenceOK := scan.sequence()
	if !sequenceOK || !scan.literal(`"id":"`) {
		return recordMeta{}, false, false
	}
	id, idOK := scan.text(idLength)
	if !idOK || !validID(id) || !scan.literal(`,"tailnet":"`) {
		return recordMeta{}, false, false
	}
	tailnet, tailnetOK := scan.text(maxTailnetBytes)
	if !tailnetOK || !scan.literal(`,"source":"`) {
		return recordMeta{}, false, false
	}
	source, sourceOK := scan.text(maxSourceBytes)
	if !sourceOK || !scan.literal(`,"signal":"`) {
		return recordMeta{}, false, false
	}
	signal, signalOK := scan.text(maxSignalBytes)
	if !signalOK || !scan.literal(`,"accepted":"`) {
		return recordMeta{}, false, false
	}
	acceptedText, acceptedOK := scan.text(maxAcceptedBytes)
	if !acceptedOK || !scan.literal(`,"body":"`) {
		return recordMeta{}, false, false
	}
	accepted, err := time.Parse(time.RFC3339Nano, acceptedText)
	if err != nil || accepted.IsZero() {
		return recordMeta{}, false, false
	}
	if validateIdentityInputs(tailnet, source, signal, nil) != nil {
		return recordMeta{}, false, false
	}

	encoded := scan.left - 2
	if encoded < 0 || encoded%4 != 0 {
		return recordMeta{}, false, false
	}
	padding := int64(0)
	if encoded > 0 {
		var last [2]byte
		if _, err := file.ReadAt(last[:], int64(recordHeaderBytes)+payload-4); err != nil {
			return recordMeta{}, false, false
		}
		if last[1] == '=' {
			padding = 1
			if last[0] == '=' {
				padding = 2
			}
		}
	}
	bodyLength := encoded/4*3 - padding
	if bodyLength < 0 || bodyLength > maxBodyBytes {
		return recordMeta{}, false, false
	}
	identity := sha256.New()
	writeIdentityPrefix(identity, tailnet, source, signal, uint64(bodyLength))
	bodyEqual = !compare || bodyLength == int64(len(want))

	chunkBytes := int(min(encoded, recordScanChunkBytes))
	raw := make([]byte, chunkBytes)
	decoded := make([]byte, chunkBytes/4*3)
	var decodedTotal int64
	for remaining := encoded; remaining > 0; {
		// The last group alone may carry padding.
		n := int(min(remaining-4, int64(chunkBytes)))
		final := n <= 0
		if final {
			n = 4
		}
		group := raw[:n]
		if !scan.take(group) || bytes.ContainsAny(group, "\r\n") {
			return recordMeta{}, false, false
		}
		if !final && bytes.IndexByte(group, '=') >= 0 {
			return recordMeta{}, false, false
		}
		written, err := strictBase64.Decode(decoded, group)
		if err != nil {
			return recordMeta{}, false, false
		}
		_, _ = identity.Write(decoded[:written])
		if compare && bodyEqual {
			end := decodedTotal + int64(written)
			bodyEqual = end <= int64(len(want)) && bytes.Equal(decoded[:written], want[decodedTotal:end])
		}
		decodedTotal += int64(written)
		remaining -= int64(n)
	}
	if decodedTotal != bodyLength || !scan.literal(`"}`) || scan.left != 0 {
		return recordMeta{}, false, false
	}
	var stored [checksumBytes]byte
	if _, err := io.ReadFull(scan.reader, stored[:]); err != nil {
		return recordMeta{}, false, false
	}
	if !bytes.Equal(stored[:], scan.sum.Sum(nil)) {
		return recordMeta{}, false, false
	}
	if _, err := scan.reader.ReadByte(); err != io.EOF {
		return recordMeta{}, false, false
	}
	if hex.EncodeToString(identity.Sum(nil)) != id {
		return recordMeta{}, false, false
	}
	return recordMeta{
		Sequence: sequence, ID: id, Tailnet: tailnet, Source: source, Signal: signal,
		Accepted: accepted, EncodedBytes: size,
	}, bodyEqual, true
}

// recordScanner consumes the checksummed region, hashing exactly what it reads.
type recordScanner struct {
	reader *bufio.Reader
	sum    hash.Hash
	left   int64
}

func (r *recordScanner) take(into []byte) bool {
	if int64(len(into)) > r.left {
		return false
	}
	if _, err := io.ReadFull(r.reader, into); err != nil {
		return false
	}
	_, _ = r.sum.Write(into)
	r.left -= int64(len(into))
	return true
}

func (r *recordScanner) next() (byte, bool) {
	var one [1]byte
	if !r.take(one[:]) {
		return 0, false
	}
	return one[0], true
}

func (r *recordScanner) literal(want string) bool {
	var stack [16]byte
	got := stack[:len(want)]
	return r.take(got) && string(got) == want
}

// sequence reads the opening key, the digits and the comma after them.
func (r *recordScanner) sequence() (uint64, bool) {
	if !r.literal(`{"sequence":`) {
		return 0, false
	}
	var digits [maxSequenceDigits]byte
	for n := 0; ; n++ {
		b, ok := r.next()
		if !ok {
			return 0, false
		}
		if b == ',' {
			if n == 0 || digits[0] == '0' {
				return 0, false
			}
			value, err := strconv.ParseUint(string(digits[:n]), 10, 64)
			return value, err == nil
		}
		if b < '0' || b > '9' || n == len(digits) {
			return 0, false
		}
		digits[n] = b
	}
}

// text reads a JSON string body through its closing quote. Only printable ASCII
// without escapes qualifies, so the raw bytes are exactly the decoded value.
func (r *recordScanner) text(limit int) (string, bool) {
	var value []byte
	for {
		b, ok := r.next()
		if !ok {
			return "", false
		}
		if b == '"' {
			return string(value), true
		}
		if b < 0x20 || b > 0x7e || b == '\\' || len(value) == limit {
			return "", false
		}
		value = append(value, b)
	}
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
