package hsapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/provider"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// parseTime parses an RFC3339 timestamp, returning the zero time on empty/invalid
// input (Headscale omits/zeroes optional timestamps like expiry).
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// userIdentity prefers the OIDC email, falling back to the Headscale username.
func userIdentity(u User) string {
	if u.Email != "" {
		return u.Email
	}
	return u.Name
}

// adaptNode maps a Headscale Node into tsapi.RichDevice. Tailscale-only fields
// (OS, DERP latency, posture, tailnet-lock, connectivity) are left zero.
func adaptNode(n Node) tsapi.RichDevice {
	return tsapi.RichDevice{
		ID:                 n.ID,
		NodeID:             n.ID,
		Name:               n.GivenName,
		Hostname:           n.Name,
		User:               userIdentity(n.User),
		Addresses:          n.IPAddresses,
		Tags:               n.Tags,
		ConnectedToControl: n.Online,
		Created:            parseTime(n.CreatedAt),
		LastSeen:           parseTime(n.LastSeen),
		Expires:            parseTime(n.Expiry),
		AdvertisedRoutes:   n.AvailableRoutes,
		EnabledRoutes:      n.ApprovedRoutes,
	}
}

// adaptUser maps a Headscale User into tsclient.User (lossy: no device count,
// status, role, last-seen on the Headscale user API).
func adaptUser(u User) tsclient.User {
	return tsclient.User{
		ID:            u.ID,
		LoginName:     userIdentity(u),
		DisplayName:   u.DisplayName,
		ProfilePicURL: u.ProfilePicURL,
		Created:       parseTime(u.CreatedAt),
	}
}

// adaptPreAuthKey maps a Headscale pre-auth key into tsapi.Key (Type "auth").
func adaptPreAuthKey(p PreAuthKey) tsapi.Key {
	return tsapi.Key{
		ID:          p.ID,
		Description: "preauthkey for " + userIdentity(p.User),
		Type:        "auth",
		Reusable:    p.Reusable,
		Ephemeral:   p.Ephemeral,
		Created:     parseTime(p.CreatedAt),
		Expires:     parseTime(p.Expiration),
	}
}

// adaptAPIKey maps a Headscale API key into tsapi.Key (Type "api").
func adaptAPIKey(a APIKey) tsapi.Key {
	return tsapi.Key{
		ID:          a.ID,
		Description: a.Prefix,
		Type:        "api",
		Created:     parseTime(a.CreatedAt),
		Expires:     parseTime(a.Expiration),
	}
}

// adaptPolicy wraps the Headscale policy HuJSON in a RawACL, synthesizing a
// stable ETag (the acl collector tracks ETag change to detect edits; Headscale
// has none, so hash the body).
func adaptPolicy(p *Policy) *tsclient.RawACL {
	sum := sha256.Sum256([]byte(p.Policy))
	return &tsclient.RawACL{HuJSON: p.Policy, ETag: hex.EncodeToString(sum[:])}
}

// Provider adapts a Headscale *Client to the provider.ControlPlane interface by
// fetching /api/v1/* and adapting into tsapi/tsclient types. Tailscale-only
// surfaces (posture attributes, device/user invites) return empty results.
type Provider struct {
	c *Client
}

// NewProvider wraps a Headscale client as a control-plane provider.
func NewProvider(c *Client) *Provider { return &Provider{c: c} }

func (p *Provider) DevicesRich(ctx context.Context) ([]tsapi.RichDevice, error) {
	nodes, err := p.c.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tsapi.RichDevice, len(nodes))
	for i, n := range nodes {
		out[i] = adaptNode(n)
	}
	return out, nil
}

func (p *Provider) DevicePostureAttributes(context.Context, string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (p *Provider) DeviceInvites(context.Context, string) ([]tsapi.DeviceInvite, error) {
	return nil, nil
}

func (p *Provider) Users(ctx context.Context) ([]tsclient.User, error) {
	users, err := p.c.HSUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tsclient.User, len(users))
	for i, u := range users {
		out[i] = adaptUser(u)
	}
	return out, nil
}

func (p *Provider) UserInvites(context.Context) ([]tsapi.UserInvite, error) { return nil, nil }

func (p *Provider) KeysRich(ctx context.Context) ([]tsapi.Key, error) {
	pks, err := p.c.PreAuthKeys(ctx)
	if err != nil {
		return nil, err
	}
	aks, err := p.c.APIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tsapi.Key, 0, len(pks)+len(aks))
	for _, k := range pks {
		out = append(out, adaptPreAuthKey(k))
	}
	for _, k := range aks {
		out = append(out, adaptAPIKey(k))
	}
	return out, nil
}

func (p *Provider) PolicyFileRaw(ctx context.Context) (*tsclient.RawACL, error) {
	doc, err := p.c.PolicyDoc(ctx)
	if err != nil {
		return nil, err
	}
	return adaptPolicy(doc), nil
}

// compile-time assertion: *Provider satisfies provider.ControlPlane.
// provider imports only tsapi+tsclient and never hsapi, so no import cycle.
var _ provider.ControlPlane = (*Provider)(nil)
