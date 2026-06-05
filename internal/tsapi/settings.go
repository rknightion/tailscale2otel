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
