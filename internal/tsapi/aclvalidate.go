package tsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
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

// ValidatePolicyFile validates the tailnet's CURRENTLY ACTIVE ACL policy by
// POSTing to /tailnet/{tailnet}/acl/validate with NO request body. Per the
// API, a populated body would either run externally supplied ACL tests (a
// JSON array) or validate a hypothetical replacement policy (a JSON object or
// HuJSON string) — neither of which this call wants; an empty body validates
// what is actually live, including any tests embedded in the policy's own
// "tests" section. This method does not modify the tailnet policy file in any
// way and requires only the policy_file:read OAuth scope.
func (c *Client) ValidatePolicyFile(ctx context.Context) (*PolicyValidation, error) {
	var raw policyValidateResponse
	if err := c.postJSON(ctx, c.aclValidateURL(), nil, &raw); err != nil {
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

// postJSON marshals body to JSON (or sends no body at all when body is nil)
// and POSTs it to urlStr through c.http (so auth, retry and the observer
// transport apply). On a non-2xx response it returns a *StatusError —
// distinct from putJSON, which returns a plain error and so cannot be
// classified by apistate.Classify. On success, when out is non-nil, the
// response body is decoded into it under the snapshot decode budget
// (tailscale.max_response_bytes); out is left untouched when the caller
// passes nil (a POST with no interesting response).
func (c *Client) postJSON(ctx context.Context, urlStr string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
