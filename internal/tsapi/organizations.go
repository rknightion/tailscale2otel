package tsapi

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// OrganizationTailnet is the stable identifier returned by the alpha
// Organizations API. The display name and creation metadata are intentionally
// not decoded: roster discovery needs only this opaque ID, and neither other
// field is safe or useful as telemetry.
type OrganizationTailnet struct {
	ID string `json:"id"`
}

type organizationTailnetsResponse struct {
	Tailnets []OrganizationTailnet `json:"tailnets"`
	Cursor   string                `json:"cursor"`
}

// OrganizationTailnets lists every tailnet in organization through the alpha
// Organizations API. The API allows at most 100 records per page; use that
// maximum to bound requests while preserving the roster order returned by the
// control plane. A repeated cursor is rejected to avoid an unbounded scrape if
// an upstream pagination response is inconsistent.
//
// The caller must authenticate with an OAuth client carrying tailnets:read.
func (c *Client) OrganizationTailnets(ctx context.Context, organization string) ([]OrganizationTailnet, error) {
	if err := validOrganization(organization); err != nil {
		return nil, err
	}
	var out []OrganizationTailnet
	seenCursors := map[string]struct{}{}
	for cursor := ""; ; {
		var page organizationTailnetsResponse
		if err := c.getJSON(ctx, c.organizationTailnetsURL(organization, cursor), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Tailnets...)
		if page.Cursor == "" {
			return out, nil
		}
		if _, ok := seenCursors[page.Cursor]; ok {
			return nil, fmt.Errorf("tsapi: organization tailnet pagination repeated cursor")
		}
		seenCursors[page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
}

// validOrganization refuses any organization that is not a single path segment.
// organizationTailnetsURL used to build its path with path.Join, which CLEANS
// the joined result: an organization of "../../evil" produced
// /api/evil/tailnets, silently targeting a different endpoint, and one
// containing "/" added a segment. The value comes from config rather than a
// remote caller, so this is hardening — but a roster call that quietly requests
// something else should fail loudly instead.
func validOrganization(organization string) error {
	switch {
	case organization == "":
		return fmt.Errorf("tsapi: organization must not be empty")
	case organization == "." || organization == "..":
		return fmt.Errorf("tsapi: invalid organization %q: path traversal", organization)
	case strings.ContainsAny(organization, "/\\"):
		return fmt.Errorf("tsapi: invalid organization %q: must be a single path segment", organization)
	}
	return nil
}

func (c *Client) organizationTailnetsURL(organization, cursor string) string {
	u := *c.baseURL
	// Path carries the decoded segment and RawPath the escaped one, so a
	// organization needing escaping survives round-tripping without path.Join
	// collapsing it. validOrganization has already refused separators.
	base := path.Join(c.baseURL.Path, "/api/v2/organizations")
	u.Path = base + "/" + organization + "/tailnets"
	u.RawPath = base + "/" + url.PathEscape(organization) + "/tailnets"
	q := url.Values{"limit": {"100"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
