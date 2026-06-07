package tsapi

import (
	"context"
	"path"
)

// DeviceInvite is the curated, bounded subset of a device-share invite returned
// by GET /api/v2/device/{deviceId}/device-invites. Only the booleans used for
// metrics are decoded; the PII-bearing fields the endpoint also returns (email,
// inviteUrl, acceptedBy) are deliberately NOT decoded — see the PII fencing
// rule in the project CLAUDE.md / roadmap §0.2.
type DeviceInvite struct {
	// Accepted is true once the share invite has been accepted.
	Accepted bool
	// MultiUse is true if the invite can be accepted more than once.
	MultiUse bool
	// AllowExitNode is true if the invited user may use the device as an exit node.
	AllowExitNode bool
}

// wireDeviceInvite decodes only the bounded booleans used for metrics; the
// endpoint also returns id/created/tailnetId/deviceId/sharerId/email/
// inviteUrl/lastEmailSentAt/acceptedBy — all deliberately omitted (PII or unused).
type wireDeviceInvite struct {
	Accepted      bool `json:"accepted"`
	MultiUse      bool `json:"multiUse"`
	AllowExitNode bool `json:"allowExitNode"`
}

// DeviceInvites lists the share invites for a single device. The endpoint
// returns a bare JSON array (or null when there are none), so an empty/absent
// body yields (nil, nil), mirroring UserInvites. Requires the
// device_invites:read OAuth scope.
func (c *Client) DeviceInvites(ctx context.Context, deviceID string) ([]DeviceInvite, error) {
	var wire []wireDeviceInvite
	if err := c.getJSON(ctx, c.deviceInvitesURL(deviceID), &wire); err != nil {
		return nil, err
	}
	if len(wire) == 0 {
		return nil, nil
	}
	out := make([]DeviceInvite, 0, len(wire))
	for _, w := range wire {
		out = append(out, DeviceInvite(w))
	}
	return out, nil
}

// deviceInvitesURL builds the per-device invites endpoint URL. NOTE: this is
// under /api/v2/device/{id} (NOT /tailnet/{tailnet}), mirroring the device
// attributes path.
func (c *Client) deviceInvitesURL(deviceID string) string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/device", deviceID, "device-invites")
	return u.String()
}
