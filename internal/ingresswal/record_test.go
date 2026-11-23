package ingresswal

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const scanTestAccepted = "2026-01-02T03:04:05.000000006Z"

func frameRecord(payload string) []byte {
	record := make([]byte, recordHeaderBytes+len(payload)+checksumBytes)
	copy(record, recordMagic)
	record[len(recordMagic)] = recordVersion
	binary.BigEndian.PutUint64(record[len(recordMagic)+1:recordHeaderBytes], uint64(len(payload)))
	copy(record[recordHeaderBytes:], payload)
	sum := sha256.Sum256(record[:recordHeaderBytes+len(payload)])
	copy(record[recordHeaderBytes+len(payload):], sum[:])
	return record
}

func scanTestID(t testing.TB, tailnet, source, signal string, body []byte) string {
	t.Helper()
	id, err := NewID(tailnet, source, signal, body)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func canonicalPayload(id, body string) string {
	return fmt.Sprintf(`{"sequence":7,"id":%q,"tailnet":"net","source":"stream","signal":"flow","accepted":%q,"body":%s}`,
		id, scanTestAccepted, body)
}

// assertScanAgrees is the differential: a scanner acceptance must be one the
// real decoder makes with the same fields and body comparison.
func assertScanAgrees(t *testing.T, data, want []byte) (accepted bool) {
	t.Helper()
	meta, bodyEqual, ok := scanCanonicalRecord(bytes.NewReader(data), int64(len(data)), 1<<30, want, true)
	if !ok {
		return false
	}
	record, err := decodeRecord(data)
	if err != nil {
		t.Fatalf("scanner accepted a record decodeRecord rejects: %v", err)
	}
	envelope := record.Envelope
	if meta.Sequence != record.Sequence || meta.ID != envelope.ID || meta.Tailnet != envelope.Tailnet ||
		meta.Source != envelope.Source || meta.Signal != envelope.Signal ||
		!meta.Accepted.Equal(envelope.Accepted) || meta.EncodedBytes != int64(len(data)) {
		t.Fatalf("scanner meta %+v differs from decoded %+v", meta, record)
	}
	if bodyEqual != bytes.Equal(envelope.Body, want) {
		t.Fatalf("scanner bodyEqual = %v, decoded body comparison = %v", bodyEqual, !bodyEqual)
	}
	return true
}

func TestScanCanonicalRecordMatchesEncodeRecord(t *testing.T) {
	accepted := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for size := range 200 {
		body := bytes.Repeat([]byte{0xfb, 0x00, 0x7f}, size/3+1)[:size]
		envelope := Envelope{
			ID: scanTestID(t, "net", "stream", "flow", body), Tailnet: "net", Source: "stream", Signal: "flow",
			Accepted: accepted, Body: body,
		}
		data, err := encodeRecord(envelope, uint64(size+1))
		if err != nil {
			t.Fatal(err)
		}
		if !assertScanAgrees(t, data, body) {
			t.Fatalf("body size %d: canonical record was not streamed", size)
		}
		if size > 0 {
			other := bytes.Clone(body)
			other[size-1] ^= 1
			if !assertScanAgrees(t, data, other) {
				t.Fatalf("body size %d: differing body was not streamed", size)
			}
			if _, equal, _ := scanCanonicalRecord(bytes.NewReader(data), int64(len(data)), 1<<30, other, true); equal {
				t.Fatalf("body size %d: differing body compared equal", size)
			}
			if !assertScanAgrees(t, data, body[:size-1]) {
				t.Fatalf("body size %d: shorter want was not streamed", size)
			}
		}
	}
}

func TestReadRecordMetaAgreesWithDecodeRecord(t *testing.T) {
	body := []byte("hello")
	encodedBody := base64.StdEncoding.EncodeToString(body)
	id := scanTestID(t, "net", "stream", "flow", body)
	good := canonicalPayload(id, strconvQuote(encodedBody))
	numeric := []byte{1, 2, 3}
	numericID := scanTestID(t, "net", "stream", "flow", numeric)
	escapedRoute := scanTestID(t, "a<b", "stream", "flow", body)
	emptyID := scanTestID(t, "net", "stream", "flow", nil)
	trailingBitsBody, err := base64.StdEncoding.DecodeString("aGVsbG9=")
	if err != nil {
		t.Fatal(err)
	}
	trailingBitsID := scanTestID(t, "net", "stream", "flow", trailingBitsBody)
	replaceOnce := func(old, next string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(good, old, next, 1)
	}
	framed := frameRecord(good)
	badChecksum := bytes.Clone(framed)
	badChecksum[len(badChecksum)-1] ^= 1
	badMagic := bytes.Clone(framed)
	badMagic[0] ^= 1
	badVersion := bytes.Clone(framed)
	badVersion[len(recordMagic)] = 9

	cases := map[string]struct {
		data     []byte
		streamed bool
	}{
		"canonical":          {data: framed, streamed: true},
		"empty body":         {data: frameRecord(canonicalPayload(emptyID, `""`)), streamed: true},
		"null body":          {data: frameRecord(canonicalPayload(emptyID, "null"))},
		"duplicate id key":   {data: frameRecord(replaceOnce(`"tailnet"`, fmt.Sprintf(`"id":%q,"tailnet"`, id)))},
		"duplicate body key": {data: frameRecord(replaceOnce(`"body":`, `"body":"AAAA","body":`))},
		"case-folded id":     {data: frameRecord(replaceOnce(`"id"`, `"ID"`))},
		"case-folded body":   {data: frameRecord(replaceOnce(`"body"`, `"BODY"`))},
		"escaped key":        {data: frameRecord(replaceOnce(`"body"`, `"\u0062ody"`))},
		"unknown field":      {data: frameRecord(replaceOnce(`"body"`, `"extra":1,"body"`))},
		"reordered fields": {data: frameRecord(fmt.Sprintf(
			`{"id":%q,"sequence":7,"tailnet":"net","source":"stream","signal":"flow","accepted":%q,"body":%q}`,
			id, scanTestAccepted, encodedBody))},
		"crlf base64":    {data: frameRecord(canonicalPayload(id, strconvQuote(encodedBody[:4])[:5]+`\r\n`+encodedBody[4:]+`"`))},
		"numeric array":  {data: frameRecord(canonicalPayload(numericID, "[1,2,3]"))},
		"trailing JSON":  {data: frameRecord(good + "{}")},
		"trailing space": {data: frameRecord(good + " ")},
		"leading space":  {data: frameRecord(" " + good)},
		"id mismatch":    {data: frameRecord(canonicalPayload(strings.Repeat("0", idLength), strconvQuote(encodedBody)))},
		"uppercase id":   {data: frameRecord(canonicalPayload(strings.ToUpper(id), strconvQuote(encodedBody)))},
		"zero sequence":  {data: frameRecord(replaceOnce(`"sequence":7`, `"sequence":0`))},
		"leading zero":   {data: frameRecord(replaceOnce(`"sequence":7`, `"sequence":07`))},
		"exponent":       {data: frameRecord(replaceOnce(`"sequence":7`, `"sequence":7e0`))},
		"negative":       {data: frameRecord(replaceOnce(`"sequence":7`, `"sequence":-7`))},
		"overflow":       {data: frameRecord(replaceOnce(`"sequence":7`, `"sequence":18446744073709551616`))},
		"nonzero base64 trailing bits": {
			data: frameRecord(canonicalPayload(trailingBitsID, `"aGVsbG9="`)),
		},
		"unpadded base64":  {data: frameRecord(canonicalPayload(id, `"aGVsbG8"`))},
		"url base64":       {data: frameRecord(canonicalPayload(id, `"_-_-"`))},
		"escaped route":    {data: frameRecord(strings.Replace(canonicalPayload(escapedRoute, strconvQuote(encodedBody)), `"net"`, `"a\u003cb"`, 1))},
		"bad time":         {data: frameRecord(replaceOnce(scanTestAccepted, "yesterday"))},
		"offset time":      {data: frameRecord(replaceOnce(scanTestAccepted, "2026-01-02T04:04:05+01:00")), streamed: true},
		"empty source":     {data: frameRecord(replaceOnce(`"source":"stream"`, `"source":""`))},
		"bad checksum":     {data: badChecksum},
		"bad magic":        {data: badMagic},
		"bad version":      {data: badVersion},
		"truncated":        {data: framed[:len(framed)-1]},
		"short":            {data: framed[:recordHeaderBytes]},
		"bytes after data": {data: append(bytes.Clone(framed), 0)},
	}

	store, err := New(Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := assertScanAgrees(t, tc.data, body); got != tc.streamed {
				t.Fatalf("streamed = %v, want %v", got, tc.streamed)
			}
			const fileName = "state-under-test"
			path := filepath.Join(store.opts.Directory, fileName)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(path) })

			meta, bodyEqual, gotErr := store.readRecordMeta(fileName, body, true)
			record, wantErr := decodeRecord(tc.data)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("readRecordMeta err = %v, decodeRecord err = %v", gotErr, wantErr)
			}
			if wantErr != nil {
				if gotErr.Error() != wantErr.Error() {
					t.Fatalf("readRecordMeta err = %q, decodeRecord err = %q", gotErr, wantErr)
				}
				for _, class := range []error{ErrCorrupt, ErrIncompatible} {
					if errors.Is(gotErr, class) != errors.Is(wantErr, class) {
						t.Fatalf("error class differs for %v: %v vs %v", class, gotErr, wantErr)
					}
				}
				return
			}
			if meta.Sequence != record.Sequence || meta.ID != record.Envelope.ID ||
				meta.Tailnet != record.Envelope.Tailnet || meta.Source != record.Envelope.Source ||
				meta.Signal != record.Envelope.Signal || !meta.Accepted.Equal(record.Envelope.Accepted) ||
				meta.EncodedBytes != int64(len(tc.data)) {
				t.Fatalf("meta %+v differs from decoded %+v", meta, record)
			}
			if bodyEqual != bytes.Equal(record.Envelope.Body, body) {
				t.Fatalf("bodyEqual = %v for decoded body %q", bodyEqual, record.Envelope.Body)
			}
		})
	}
}

func strconvQuote(value string) string { return `"` + value + `"` }

// Every single-byte mutation is reframed with a valid checksum, so it reaches
// the JSON and identity rules rather than stopping at the checksum.
func TestScanCanonicalRecordNeverAcceptsWhatDecodeRejects(t *testing.T) {
	body := []byte("differential body!")
	id := scanTestID(t, "net", "stream", "flow", body)
	payload := canonicalPayload(id, strconvQuote(base64.StdEncoding.EncodeToString(body)))
	random := rand.New(rand.NewPCG(795, 1))
	interesting := []byte("\"\\,:{}[]0189azAZ+/=\r\n \x00\x7f\xc3")
	for position := range len(payload) {
		for range 12 {
			mutated := []byte(payload)
			if random.IntN(2) == 0 {
				mutated[position] = interesting[random.IntN(len(interesting))]
			} else {
				mutated[position] = byte(random.IntN(256))
			}
			assertScanAgrees(t, frameRecord(string(mutated)), body)
			assertScanAgrees(t, frameRecord(rebindMutatedID(string(mutated))), body)
		}
		deleted := payload[:position] + payload[position+1:]
		assertScanAgrees(t, frameRecord(deleted), body)
		assertScanAgrees(t, frameRecord(rebindMutatedID(deleted)), body)
		for _, inserted := range interesting {
			grown := payload[:position] + string(inserted) + payload[position:]
			assertScanAgrees(t, frameRecord(grown), body)
			assertScanAgrees(t, frameRecord(rebindMutatedID(grown)), body)
		}
	}
}

// rebindMutatedID gives a mutation that still parses the ID of its own route
// and body, so it gets past the identity check and tests the rules behind it.
func rebindMutatedID(payload string) string {
	var disk diskEnvelope
	if json.Unmarshal([]byte(payload), &disk) != nil || !validID(disk.ID) {
		return payload
	}
	id, err := NewID(disk.Tailnet, disk.Source, disk.Signal, disk.Body)
	if err != nil {
		return payload
	}
	return strings.Replace(payload, disk.ID, id, 1)
}

func TestReadRecordMetaKeepsCapacityRefusal(t *testing.T) {
	body := []byte("capacity")
	data := frameRecord(canonicalPayload(scanTestID(t, "net", "stream", "flow", body),
		strconvQuote(base64.StdEncoding.EncodeToString(body))))
	store, err := New(Options{
		Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: int64(len(data) - 1), MaxEntries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const fileName = "oversize"
	if err := os.WriteFile(filepath.Join(store.opts.Directory, fileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.readRecordMeta(fileName, nil, false)
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) || corrupt.Reason != "state file exceeds configured byte capacity" {
		t.Fatalf("readRecordMeta err = %v, want the capacity refusal", err)
	}
}
