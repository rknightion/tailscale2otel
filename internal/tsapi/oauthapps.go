package tsapi

import (
	"context"
	"path"
	"time"
)

// OAuthApp is a tailnet OAuth application (client) as returned by
// GET /api/v2/tailnet/{tailnet}/oauth-apps. Only the fields the oauth_apps
// collector needs are decoded; redirectURIs and clientSecret (a write-only
// secret, only ever populated on creation) are deliberately never surfaced.
type OAuthApp struct {
	ID                    string
	Name                  string
	Description           string
	Scopes                []string
	AllowedNodeAttributes []string

	Created time.Time
	Updated time.Time
}

type oauthAppsResponse struct {
	OAuthApps []wireOAuthApp `json:"oauthApps"`
}

type wireOAuthApp struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description"`
	Scopes                []string  `json:"scopes"`
	AllowedNodeAttributes []string  `json:"allowedNodeAttributes"`
	Created               time.Time `json:"created"`
	Updated               time.Time `json:"updated"`
}

// OAuthApps lists the tailnet's OAuth applications (list op only — the
// create/get/update/delete ops are not consumed). This is an alpha API
// endpoint: a tailnet without it enabled, or a credential lacking the scope,
// returns a 403/404 rather than a body — callers should treat that as
// "feature not available", not a failure (see the oauth_apps collector).
func (c *Client) OAuthApps(ctx context.Context) ([]OAuthApp, error) {
	var wire oauthAppsResponse
	if err := c.getJSON(ctx, c.oauthAppsURL(), &wire); err != nil {
		return nil, err
	}
	out := make([]OAuthApp, 0, len(wire.OAuthApps))
	for _, a := range wire.OAuthApps {
		out = append(out, OAuthApp(a))
	}
	return out, nil
}

// oauthAppsURL builds the OAuth-apps list endpoint URL, mirroring
// servicesURL/keysURL construction.
func (c *Client) oauthAppsURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "oauth-apps")
	return u.String()
}
