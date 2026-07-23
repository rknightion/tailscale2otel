package hsapi

import (
	"encoding/json"
	"errors"
	"io"
)

// maxResponseBytes bounds a single successful (200) Headscale API JSON
// response body before it is decoded. Headscale responses are small, so the
// cap is tight; it exists to stop a malicious or compromised configured
// Headscale endpoint from streaming an unbounded valid JSON body into memory
// (#211).
const maxResponseBytes = 32 << 20 // 32 MiB

// ErrResponseTooLarge is returned by getJSON when a successful response body
// exceeds maxResponseBytes. Callers can errors.Is against it to distinguish
// "upstream sent too much" from an ordinary malformed-JSON decode error.
var ErrResponseTooLarge = errors.New("hsapi: response body exceeds maximum allowed size")

// decodeJSONLimited decodes a single JSON value from r into out, never
// reading more than limit+1 bytes. If reading exhausts that allowance
// (limit+1 bytes consumed), the body is provably larger than limit — whether
// or not json managed to decode something along the way — so
// ErrResponseTooLarge is returned instead of leaking whatever incidental
// io/json error a truncated read happens to produce (typically
// io.ErrUnexpectedEOF, which is indistinguishable from other malformed-JSON
// errors and must not be relied on to signal an oversized body).
func decodeJSONLimited(r io.Reader, limit int64, out any) error {
	lr := &io.LimitedReader{R: r, N: limit + 1}
	err := json.NewDecoder(lr).Decode(out)
	if lr.N == 0 {
		return ErrResponseTooLarge
	}
	return err
}
