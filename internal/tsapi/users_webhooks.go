package tsapi

// Users and Webhooks list decoding.
//
// Both used to delegate to tsclient, which decodes each response into a
// `map[string][]T` and then reads one key out of it. That makes the whole call
// fail on a purely ADDITIVE upstream change: a new top-level key whose value is
// not an array of the element type cannot be unmarshalled into the map, so
// `{"users":[…],"nextPageToken":"x"}` returns an error and the collector goes dark
// rather than degrading. Found by the boundary matrix (#433).
//
// Every other list operation here already decodes through getJSON into a wire
// struct, which ignores unknown keys (DevicesRich, KeysRich, PostureIntegrations,
// …). These were the last two exceptions.

import (
	"context"
	"net/url"
	"path"

	tsclient "github.com/tailscale/tailscale-client-go/v2"
)

// usersResponse is the listUsers envelope. Unknown sibling keys are ignored.
type usersResponse struct {
	Users []tsclient.User `json:"users"`
}

// webhooksResponse is the listWebhooks envelope.
type webhooksResponse struct {
	Webhooks []tsclient.Webhook `json:"webhooks"`
}

// Users lists all users in the tailnet.
//
// No type/role query parameters are sent, which is what the previous
// tsclient.Users().List(ctx, nil, nil) call did too: upstream defaults `type` to
// "member" and `role` to "all", and the users collector wants that default set.
func (c *Client) Users(ctx context.Context) ([]tsclient.User, error) {
	var wire usersResponse
	if err := c.getJSON(ctx, c.usersURL(), &wire); err != nil {
		return nil, err
	}
	return wire.Users, nil
}

// Webhooks lists configured webhook endpoints.
func (c *Client) Webhooks(ctx context.Context) ([]tsclient.Webhook, error) {
	var wire webhooksResponse
	if err := c.getJSON(ctx, c.webhooksURL(), &wire); err != nil {
		return nil, err
	}
	return wire.Webhooks, nil
}

// usersURL builds the tailnet users endpoint URL.
func (c *Client) usersURL() string {
	return c.tailnetURL("users")
}

// webhooksURL builds the tailnet webhooks endpoint URL.
func (c *Client) webhooksURL() string {
	return c.tailnetURL("webhooks")
}

// tailnetURL builds /api/v2/tailnet/<tailnet>/<leaf> against the configured base,
// mirroring devicesURL/keysURL construction.
func (c *Client) tailnetURL(leaf string) string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, leaf)
	u.RawQuery = url.Values{}.Encode()
	return u.String()
}
