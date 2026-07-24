package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// emptyPayloadHash is SHA-256 of the empty string. Every request this package
// makes is a GET or a HEAD with no body, so the payload hash is always this —
// and S3 requires it in the x-amz-content-sha256 header, not just in the
// signature.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

const (
	algorithm = "AWS4-HMAC-SHA256"
	service   = "s3"
	// terminator ends the credential scope. It is a fixed string in SigV4, not a
	// version we get to choose.
	terminator = "aws4_request"
)

// signer holds one set of credentials for one region.
type signer struct {
	region string
}

// sign adds the SigV4 headers to req in place.
//
// The signature covers the method, the URI path, the canonical query string, a
// chosen set of headers, and the payload hash. Getting any one of those to
// disagree with what the server reconstructs yields 403 SignatureDoesNotMatch —
// which is the honest failure mode here: loud, immediate, and impossible to
// mistake for working.
func (s signer) sign(req *http.Request, cred Credentials, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	scopeDate := now.UTC().Format("20060102")

	// Host is not in req.Header — net/http carries it on the request — but it IS
	// signed, so it has to be put where the canonicalisation below can see it.
	req.Header.Set("Host", req.Host)
	if req.Host == "" {
		req.Header.Set("Host", req.URL.Host)
	}
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	if cred.SessionToken != "" {
		// Temporary credentials only. It is a signed header, so it must be set
		// before the canonical headers are built, not after.
		req.Header.Set("X-Amz-Security-Token", cred.SessionToken)
	}

	canonHeaders, signedHeaders := canonicalHeaders(req)
	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		req.URL.RawQuery,
		canonHeaders,
		signedHeaders,
		emptyPayloadHash,
	}, "\n")

	scope := scopeDate + "/" + s.region + "/" + service + "/" + terminator
	hashed := sha256.Sum256([]byte(canonical))
	toSign := strings.Join([]string{
		algorithm, amzDate, scope, hex.EncodeToString(hashed[:]),
	}, "\n")

	sig := hex.EncodeToString(hmacSHA256(signingKey(cred.SecretAccessKey, scopeDate, s.region), toSign))
	req.Header.Set("Authorization", algorithm+
		" Credential="+cred.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+
		", Signature="+sig)
}

// signingKey derives the request-specific key. The chain is what makes a
// signature usable only for one day, one region and one service.
func signingKey(secret, scopeDate, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), scopeDate)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, terminator)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// canonicalHeaders renders the signed headers in the form SigV4 requires:
// lowercase names, sorted, values trimmed, each on its own line, followed by the
// semicolon-joined name list.
func canonicalHeaders(req *http.Request) (canonical, signed string) {
	names := make([]string, 0, len(req.Header))
	for k := range req.Header {
		names = append(names, strings.ToLower(k))
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(req.Header.Get(n)))
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

// canonicalURI is the path as SigV4 wants it. An empty path signs as "/", which
// is the shape a bucket-level list request has.
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
