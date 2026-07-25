package tsapi

import (
	"context"
	"path"
	"time"
)

// UserInvite is an open (not yet accepted) invitation for a user to join the
// tailnet. The list-user-invites endpoint returns only open invites and its
// schema has no "created"/"accepted" property, so this type deliberately does
// not model accepted-invite history (#411) — decoding fields the wire never
// sends produced an always-zero-value Accepted/Created that looked like real
// data but never was.
type UserInvite struct {
	ID              string
	Role            string
	TailnetID       string
	InviterID       string
	Email           string
	InviteURL       string
	LastEmailSentAt time.Time
}

// userInvite is the wire shape of a single user-invite record.
type userInvite struct {
	ID              string    `json:"id"`
	Role            string    `json:"role"`
	TailnetID       string    `json:"tailnetId"`
	InviterID       string    `json:"inviterId"`
	Email           string    `json:"email"`
	InviteURL       string    `json:"inviteUrl"`
	LastEmailSentAt time.Time `json:"lastEmailSentAt"`
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
