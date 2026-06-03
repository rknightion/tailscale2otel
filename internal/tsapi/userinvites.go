package tsapi

import (
	"context"
	"path"
	"time"
)

// UserInvite is a pending or accepted invitation for a user to join the tailnet.
type UserInvite struct {
	ID        string
	Role      string
	TailnetID string
	InviterID string
	Email     string
	InviteURL string
	Created   time.Time
	Accepted  bool
}

// userInvite is the wire shape of a single user-invite record.
type userInvite struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	TailnetID string    `json:"tailnetId"`
	InviterID string    `json:"inviterId"`
	Email     string    `json:"email"`
	InviteURL string    `json:"inviteUrl"`
	Created   time.Time `json:"created"`
	Accepted  bool      `json:"accepted"`
}

// UserInvites lists user invitations for the tailnet. The endpoint returns a
// JSON array, but on tailnets with no invites the body is the literal null; in
// that case an empty slice is returned rather than an error.
func (c *Client) UserInvites(ctx context.Context) ([]UserInvite, error) {
	var wire []userInvite
	if err := c.getJSON(ctx, c.userInvitesURL(), &wire); err != nil {
		return nil, err
	}
	if len(wire) == 0 {
		return nil, nil
	}
	out := make([]UserInvite, 0, len(wire))
	for _, i := range wire {
		out = append(out, UserInvite(i))
	}
	return out, nil
}

// userInvitesURL builds the user-invites endpoint URL.
func (c *Client) userInvitesURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "user-invites")
	return u.String()
}
