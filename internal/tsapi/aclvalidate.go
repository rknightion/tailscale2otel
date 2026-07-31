package tsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
)

// PolicyValidation is the summarized result of validating the tailnet's
// CURRENT ACL policy (including any tests embedded in its own "tests"
// section) via ValidatePolicyFile. Only bounded counts are decoded — the
// upstream response's free-text messages (rule text, usernames, addresses)
// are deliberately discarded here so a caller can never accidentally surface
// them on a metric label (#428).
type PolicyValidation struct {
	// OK is true only for a bare `{}` success response: no message, no data.
	OK bool
	// Errors counts issues carried under a data[] element's "errors" key for
	// any response message OTHER than "test(s) failed" (forward-compatible
	// generic-error bucket; the documented 200 responses never populate this
	// today — see TestFailures).
	Errors int
	// Warnings counts issues carried under a data[] element's "warnings" key
	// (the documented "warning(s) found" response, e.g. an unsynced SCIM
	// group).
	Warnings int
	// TestFailures counts issues carried under a data[] element's "errors" key
	// when the response message is "test(s) failed" — i.e. failures of the
	// tests embedded in the policy's own "tests" section, evaluated against
	// the policy's own rules.
	TestFailures int
}

// policyValidateResponse is the raw shape of a 200 response from
// POST /tailnet/{tailnet}/acl/validate: `{}` on success, or a message plus a
// free-form data array on failure/warning.
type policyValidateResponse struct {
	Message string            `json:"message"`
	Data    []json.RawMessage `json:"data"`
}

// policyValidateItem decodes the two possible free-text arrays out of one
// data[] element. Only their LENGTH is ever used — the string contents (rule
// text, usernames, addresses) are never retained past this decode.
type policyValidateItem struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// ValidatePolicyFile validates the supplied ACL policy document by POSTing it
// to /tailnet/{tailnet}/acl/validate. It does not modify the tailnet policy
// file in any way and requires only the policy_file:read OAuth scope.
//
// CORRECTED 2026-07-31 (#523). This method previously sent NO request body, on
// the belief — stated in a comment here, and pinned by a test — that an empty
// body validates whatever policy is currently live. That belief was WRONG. The
// live API answers an empty body with HTTP 400 "unexpected end of JSON input",
// so the call failed on every tick and, lacking a terminal 4xx state at the
// time, reported as transient_failure. The feature never validated anything.
//
// Verified live against api.tailscale.com:
//
//	no body                      -> 400 {"message":"unexpected end of JSON input"}
//	{}                           -> 200 {}
//	the tailnet's live policy    -> 200 {}
//	a policy naming a bogus tag  -> 200 {"message":"src=tag not found: ..."}
//
// The last case is the load-bearing one: the endpoint validates the document it
// is GIVEN, not the live one. So sending `{}` is NOT an acceptable shortcut — it
// returns 200 while validating an empty policy, which passes unconditionally and
// would report a permanently healthy validation of nothing. The caller must pass
// the real policy; the acl collector already holds it from its own fetch.
func (c *Client) ValidatePolicyFile(ctx context.Context, policy string) (*PolicyValidation, error) {
	// Refuse a blank document locally rather than letting it become an
	// always-passing validation of nothing (see the note above).
	if strings.TrimSpace(policy) == "" {
		return nil, errors.New("tsapi: ValidatePolicyFile requires the policy document; " +
			"an empty body is rejected by the API (400) and an empty policy validates nothing")
	}

	var raw policyValidateResponse
	if err := c.postRawJSON(ctx, c.aclValidateURL(), []byte(policy), &raw); err != nil {
		return nil, err
	}

	out := &PolicyValidation{OK: raw.Message == "" && len(raw.Data) == 0}
	for _, d := range raw.Data {
		var item policyValidateItem
		if err := json.Unmarshal(d, &item); err != nil {
			// A data[] element that doesn't match the expected shape is
			// skipped defensively rather than failing the whole validation —
			// the bounded counts already collected from prior elements are
			// still meaningful.
			continue
		}
		if len(item.Errors) > 0 {
			if raw.Message == "test(s) failed" {
				out.TestFailures += len(item.Errors)
			} else {
				out.Errors += len(item.Errors)
			}
		}
		out.Warnings += len(item.Warnings)
	}
	return out, nil
}

// aclValidateURL builds the ACL validate endpoint URL, mirroring settingsURL.
func (c *Client) aclValidateURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "acl", "validate")
	return u.String()
}

// postRawJSON POSTs pre-encoded bytes verbatim, without re-marshaling them.
// The ACL policy is HuJSON (comments, trailing commas), which is NOT valid JSON
// and must reach the API byte-for-byte — running it through json.Marshal would
// wrap it in a JSON string literal and change what is being validated.
// Otherwise identical to postJSON: same auth, retry, observer transport,
// *StatusError on non-2xx, and budgeted decode.
func (c *Client) postRawJSON(ctx context.Context, urlStr string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return &StatusError{Method: http.MethodPost, URL: urlStr, Code: resp.StatusCode, Body: string(snippet)}
	}
	if out == nil {
		return nil
	}
	budget := c.apiBudget()
	if resp.ContentLength > budget.MaxBytes {
		return budget.ByteCeilingError()
	}
	return decodeJSONBudgeted(resp.Body, budget, out)
}
