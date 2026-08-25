package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// longKeyListing builds a ListObjectsV2 response of keys entries whose keys are
// each exactly keyLen bytes, in the shape AWS actually returns — ETag,
// StorageClass, the xmlns attribute and all. The synthetic listings elsewhere in
// these tests carry only the fields this client reads, which understates the
// response size by roughly 100 bytes per object; the whole point here is the
// real size, so nothing is left out.
func longKeyListing(t *testing.T, keys, keyLen int) string {
	t.Helper()
	const (
		prefix = "flow/2026/07/24/"
		suffix = ".ndjson"
	)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` + "\n")
	b.WriteString("<Name>flows</Name><Prefix>" + prefix + "</Prefix>")
	fmt.Fprintf(&b, "<KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys>", keys)
	b.WriteString("<IsTruncated>false</IsTruncated>\n")
	for i := range keys {
		id := fmt.Sprintf("%06d", i)
		fill := keyLen - len(prefix) - len(id) - len(suffix)
		if fill < 0 {
			t.Fatalf("keyLen %d is too short for the %d-byte prefix/suffix", keyLen, keyLen-fill)
		}
		key := prefix + id + strings.Repeat("k", fill) + suffix
		if len(key) != keyLen {
			t.Fatalf("built a %d-byte key, want %d", len(key), keyLen)
		}
		b.WriteString("<Contents><Key>" + key + "</Key>" +
			"<LastModified>2026-07-24T09:00:00.000Z</LastModified>" +
			`<ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>` +
			"<Size>4096</Size><StorageClass>STANDARD</StorageClass></Contents>\n")
	}
	b.WriteString("</ListBucketResult>\n")
	return b.String()
}

// The largest page a bucket can legitimately answer with: ListObjectsV2 returns
// at most 1000 keys and an S3 key may be 1024 bytes, so ~1.2 MiB of XML. That is
// past the 1 MiB the shared metadata reader allows, and it used to be truncated
// mid-document and handed to the XML decoder anyway — surfacing as a syntax
// error that reads like a broken bucket (#291).
func TestList_ThousandMaximumLengthKeys(t *testing.T) {
	const keys, keyLen = 1000, 1024
	body := longKeyListing(t, keys, keyLen)
	t.Logf("ListObjectsV2 response for %d keys of %d bytes = %d bytes (%.2f MiB)",
		keys, keyLen, len(body), float64(len(body))/(1<<20))
	if len(body) <= maxMetadataResponseBytes {
		t.Fatalf("fixture is %d bytes; it only tests anything if it exceeds the %d-byte metadata cap",
			len(body), maxMetadataResponseBytes)
	}
	if len(body) > maxListResponseBytes {
		t.Fatalf("fixture is %d bytes, past the %d-byte listing cap; a legitimate page must fit",
			len(body), maxListResponseBytes)
	}
	srv, _ := serve(t, body, http.StatusOK)

	got, err := newClient(t, srv.URL, true).List(context.Background(), "flow/2026/07/24/", "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Objects) != keys || got.Truncated {
		t.Fatalf("List returned %d objects, Truncated=%v; want %d and false",
			len(got.Objects), got.Truncated, keys)
	}
	for i, o := range got.Objects {
		if len(o.Key) != keyLen {
			t.Fatalf("object %d key is %d bytes, want %d — the response was cut short", i, len(o.Key), keyLen)
		}
		if o.Size != 4096 {
			t.Fatalf("object %d size = %d, want 4096", i, o.Size)
		}
	}
	if want := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC); !got.Objects[keys-1].LastModified.Equal(want) {
		t.Errorf("last object LastModified = %v, want %v", got.Objects[keys-1].LastModified, want)
	}
}

// oversizedListing serves a well-formed listing prologue and then an element that
// never closes, for more bytes than any cap here allows: what a hostile or badly
// broken endpoint looks like on the wire.
func oversizedListing(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		filler := strings.Repeat("k", 64<<10)
		if _, err := io.WriteString(w, "<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>"); err != nil {
			return
		}
		for written := 0; written < maxListResponseBytes+(1<<20); written += len(filler) {
			if _, err := io.WriteString(w, filler); err != nil {
				// The client stopped reading, which is the behavior under test.
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Hitting the cap must be reported as hitting the cap. The failure mode this
// replaces was an XML syntax error, which blames the bucket for a limit of ours
// and sends an operator looking at the wrong end.
func TestList_OversizedResponseIsRefusedByName(t *testing.T) {
	srv := oversizedListing(t)

	_, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
	if err == nil {
		t.Fatal("List succeeded against a response larger than the cap")
	}
	if !errors.Is(err, errListResponseTooLarge) {
		t.Errorf("error = %v, want one matching errListResponseTooLarge", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", maxListResponseBytes)) {
		t.Errorf("error = %v, want the %d-byte limit named", err, maxListResponseBytes)
	}
	for _, unwanted := range []string{"decode list response", "XML syntax error", "unexpected EOF"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("error = %v, contains %q — it reads as a malformed response from the server", err, unwanted)
		}
	}
}

// paddedListing returns a valid one-object listing padded to exactly total bytes.
// Whitespace between elements is insignificant to XML, so the document parses
// identically at any length — which is what makes the cap boundary itself
// testable.
func paddedListing(t *testing.T, total int) string {
	t.Helper()
	const (
		head = `<ListBucketResult><IsTruncated>false</IsTruncated>` +
			`<Contents><Key>flow/a.ndjson</Key><Size>7</Size></Contents>`
		tail = `</ListBucketResult>`
	)
	pad := total - len(head) - len(tail)
	if pad < 0 {
		t.Fatalf("total %d is shorter than the %d-byte document", total, len(head)+len(tail))
	}
	body := head + strings.Repeat(" ", pad) + tail
	if len(body) != total {
		t.Fatalf("built a %d-byte body, want %d", len(body), total)
	}
	return body
}

// The cap is inclusive: a document that ends on the last permitted byte is a
// document that fitted, and refusing it would be an off-by-one that rejects
// legitimate data. This is the case the one-byte-past allowance exists to keep
// distinguishable from a genuine overrun.
func TestList_DocumentEndingOnTheLastPermittedByteIsParsed(t *testing.T) {
	srv, _ := serve(t, paddedListing(t, maxListResponseBytes), http.StatusOK)

	got, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Objects) != 1 || got.Objects[0].Key != "flow/a.ndjson" {
		t.Fatalf("objects = %+v, want the single object parsed", got.Objects)
	}
}

// One byte over is refused, and refused as a size limit rather than as a syntax
// error: the document cannot be completed within the cap, so the bytes that did
// arrive must not be parsed.
func TestList_OneByteOverTheCapIsRefused(t *testing.T) {
	srv, _ := serve(t, paddedListing(t, maxListResponseBytes+1), http.StatusOK)

	_, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
	if !errors.Is(err, errListResponseTooLarge) {
		t.Fatalf("error = %v, want one matching errListResponseTooLarge", err)
	}
}

// The nasty case, and the reason the cap is checked independently of the decoder:
// a response can be cut off at an element boundary, so the bytes inside the cap
// parse cleanly as a complete listing while more objects were still coming. The
// decoder reports nothing at all here — only the exhausted allowance does.
func TestList_CompleteDocumentAtTheCapWithMoreToComeIsRefused(t *testing.T) {
	body := paddedListing(t, maxListResponseBytes) + strings.Repeat("\n", 8<<10)
	srv, _ := serve(t, body, http.StatusOK)

	_, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
	if !errors.Is(err, errListResponseTooLarge) {
		t.Fatalf("error = %v, want one matching errListResponseTooLarge", err)
	}
}

// Genuine server-side faults must keep reading as server-side faults: a malformed
// document is still a decode error naming the syntax problem, and an HTTP error
// status still carries the bucket's own explanation.
func TestList_ServerFaultsAreUnchanged(t *testing.T) {
	t.Run("malformed XML", func(t *testing.T) {
		srv, _ := serve(t, "<ListBucketResult><IsTruncated>false", http.StatusOK)
		_, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
		if err == nil {
			t.Fatal("List succeeded against a malformed document")
		}
		if !strings.Contains(err.Error(), "s3: decode list response:") {
			t.Errorf("error = %v, want the decode-error wording", err)
		}
		if errors.Is(err, errListResponseTooLarge) {
			t.Errorf("error = %v, reported as a size limit", err)
		}
	})
	t.Run("HTTP error status", func(t *testing.T) {
		srv, _ := serve(t, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
		_, err := newClient(t, srv.URL, true).List(context.Background(), "", "", 0)
		if err == nil {
			t.Fatal("List succeeded against a 403")
		}
		if !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "AccessDenied") {
			t.Errorf("error = %v, want status without peer-controlled response text", err)
		}
	})
}
