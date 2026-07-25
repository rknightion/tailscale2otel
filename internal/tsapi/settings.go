package tsapi

import (
	"context"
	"path"
)

// TailnetSettings is the full tailnet feature-settings record from GET
// /api/v2/tailnet/{tailnet}/settings. It is decoded directly (not via the
// official tsclient type, which omits httpsEnabled and aclsExternallyManagedOn).
type TailnetSettings struct {
	DevicesApprovalOn      bool `json:"devicesApprovalOn"`
	DevicesAutoUpdatesOn   bool `json:"devicesAutoUpdatesOn"`
	DevicesKeyDurationDays int  `json:"devicesKeyDurationDays"`

	UsersApprovalOn bool `json:"usersApprovalOn"`
	// UsersRoleAllowedToJoinExternalTailnets is a bounded enum (e.g. "none",
	// "member", "admin").
	UsersRoleAllowedToJoinExternalTailnets string `json:"usersRoleAllowedToJoinExternalTailnets"`

	NetworkFlowLoggingOn        bool `json:"networkFlowLoggingOn"`
	RegionalRoutingOn           bool `json:"regionalRoutingOn"`
	PostureIdentityCollectionOn bool `json:"postureIdentityCollectionOn"`

	// HTTPSEnabled and ACLsExternallyManagedOn are present on the wire but absent
	// from tsclient.TailnetSettings, which is why this raw decode exists.
	HTTPSEnabled            bool `json:"httpsEnabled"`
	ACLsExternallyManagedOn bool `json:"aclsExternallyManagedOn"`

	// ACLsExternalLink is the configured external ACL policy source (e.g.
	// "https://github.com/example/tailnet-policy"), gated behind the SAME
	// policy_file:read scope as ACLsExternallyManagedOn. A pointer so a caller
	// can distinguish three states the flat string cannot: key absent from the
	// wire response entirely (nil — unsupported for this credential/plan, or
	// policy_file:read not granted; #418 says treat this as absence, never a
	// definite false), key present but empty (non-nil, *ptr == "" — genuinely
	// not configured), and key present and non-empty (configured). The URI
	// itself can leak an internal repo path, so callers must derive only a
	// presence boolean from this field and never emit the string.
	ACLsExternalLink *string `json:"aclsExternalLink"`
}

// TailnetSettings returns the tailnet feature settings, decoding the full field
// set (including the fields the official client drops).
func (c *Client) TailnetSettings(ctx context.Context) (*TailnetSettings, error) {
	var out TailnetSettings
	if err := c.getJSON(ctx, c.settingsURL(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// settingsURL builds the tailnet settings endpoint URL, mirroring devicesURL.
func (c *Client) settingsURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "settings")
	return u.String()
}
