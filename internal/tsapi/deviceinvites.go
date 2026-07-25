package tsapi

import (
	"context"
	"path"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"
)

// DeviceInvite is the curated, bounded subset of a device-share invite returned
// by GET /api/v2/device/{deviceId}/device-invites. The booleans are used for
// the count gauge; email and AcceptedByLogin are decoded for the per-invite log
// event (J-A2). inviteUrl is deliberately NOT decoded — it is a bearer token
// that must never be emitted to telemetry (PII fencing rule).
type DeviceInvite struct {
	// Accepted is true once the share invite has been accepted.
	Accepted bool
	// MultiUse is true if the invite can be accepted more than once.
	MultiUse bool
	// AllowExitNode is true if the invited user may use the device as an exit node.
	AllowExitNode bool
	// Email is the email address the invite was sent to, or empty if it was
	// distributed as a link without an email recipient.
	Email string
	// AcceptedByLogin is the loginName of the user who accepted the invite, or
	// empty when the invite is still pending. Populated from acceptedBy.loginName
	// on the wire; acceptedBy.profilePicUrl is deliberately not decoded.
	AcceptedByLogin string
	// Created is the invite's creation time, or the zero time when the wire
	// record omits it (or sends the empty string). Drives the pending-invite age
	// distribution (#413); a zero value means "unknown", never "brand new".
	Created time.Time
	// LastEmailSentAt is the last time upstream attempted to email this invite.
	// Upstream documents it as "only ever set if email is not empty", so a zero
	// value on an emailed invite means it has not been resent — and a zero value
	// with no email at all is a link-only share. Feeds the bounded delivery
	// classification; the timestamp itself never becomes a metric dimension.
	LastEmailSentAt time.Time
}

// wireAcceptedBy captures the nested acceptedBy object on the device-invite
// wire response. Only loginName is retained; id and profilePicUrl are omitted.
type wireAcceptedBy struct {
	LoginName string `json:"loginName"`
}

// wireDeviceInvite decodes the fields used for metrics and the per-invite log
// event. inviteUrl is deliberately omitted — it is a bearer token that must
// never reach telemetry.
type wireDeviceInvite struct {
	Accepted      bool           `json:"accepted"`
	MultiUse      bool           `json:"multiUse"`
	AllowExitNode bool           `json:"allowExitNode"`
	Email         string         `json:"email"`
	AcceptedBy    wireAcceptedBy `json:"acceptedBy"`

	// tsclient.Time (not time.Time) tolerates an empty-string wire value. Both
	// fields are optional upstream — lastEmailSentAt is documented as set only
	// when email is non-empty — and an empty string on either one would fail the
	// WHOLE array decode with a plain time.Time, silently zeroing every invite
	// signal for that device (the #48 failure mode, applied here pre-emptively).
	Created         tsclient.Time `json:"created"`
	LastEmailSentAt tsclient.Time `json:"lastEmailSentAt"`
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
		out = append(out, DeviceInvite{
			Accepted:        w.Accepted,
			MultiUse:        w.MultiUse,
			AllowExitNode:   w.AllowExitNode,
			Email:           w.Email,
			AcceptedByLogin: w.AcceptedBy.LoginName,
			Created:         w.Created.Time,
			LastEmailSentAt: w.LastEmailSentAt.Time,
		})
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
