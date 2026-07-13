package tsapi

import (
	"context"
	"net/url"
	"path"
	"time"
)

// Key is the full unified-key record returned by
// GET /api/v2/tailnet/{tailnet}/keys?all=true. It carries fields the thin
// tsclient.Key drops — notably Type (keyType) and Scopes — so the collector can
// distinguish machine auth keys from OAuth clients and API tokens. Federated
// identities (keyType "federated") are not returned under our auth model and are
// tracked separately (roadmap A5).
type Key struct {
	ID          string
	Description string
	// Type is the wire keyType: "auth" | "client" | "api" (| "federated").
	Type   string
	Scopes []string

	Created time.Time
	Updated time.Time
	Expires time.Time
	Revoked time.Time
	Invalid bool

	// Capability flags from capabilities.devices.create (auth keys only).
	Reusable      bool
	Ephemeral     bool
	Preauthorized bool
	// Tags are the auto-applied device tags from capabilities.devices.create.tags
	// (auth keys only — the only key type carrying create capabilities).
	Tags []string

	// UserID is the id of the user who created this key (wire: userId); empty
	// for keys created by trust credentials (OAuth clients, federated identities).
	UserID string
}

type keysResponse struct {
	Keys []wireKey `json:"keys"`
}

type wireKey struct {
	ID           string    `json:"id"`
	Description  string    `json:"description"`
	KeyType      string    `json:"keyType"`
	Scopes       []string  `json:"scopes"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	Expires      time.Time `json:"expires"`
	Revoked      time.Time `json:"revoked"`
	Invalid      bool      `json:"invalid"`
	UserID       string    `json:"userId"`
	Capabilities struct {
		Devices struct {
			Create struct {
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Preauthorized bool     `json:"preauthorized"`
				Tags          []string `json:"tags"`
			} `json:"create"`
		} `json:"devices"`
	} `json:"capabilities"`
}

// KeysRich lists all auth keys, API access tokens and OAuth clients for the
// tailnet (?all=true), decoding the full key model the tsclient drops.
func (c *Client) KeysRich(ctx context.Context) ([]Key, error) {
	var wire keysResponse
	if err := c.getJSON(ctx, c.keysURL(), &wire); err != nil {
		return nil, err
	}
	out := make([]Key, 0, len(wire.Keys))
	for _, k := range wire.Keys {
		out = append(out, Key{
			ID:            k.ID,
			Description:   k.Description,
			Type:          k.KeyType,
			Scopes:        k.Scopes,
			Created:       k.Created,
			Updated:       k.Updated,
			Expires:       k.Expires,
			Revoked:       k.Revoked,
			Invalid:       k.Invalid,
			Reusable:      k.Capabilities.Devices.Create.Reusable,
			Ephemeral:     k.Capabilities.Devices.Create.Ephemeral,
			Preauthorized: k.Capabilities.Devices.Create.Preauthorized,
			Tags:          k.Capabilities.Devices.Create.Tags,
			UserID:        k.UserID,
		})
	}
	return out, nil
}

// keysURL builds the unified keys endpoint URL (all=true), mirroring
// devicesURL/logURL construction.
func (c *Client) keysURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "keys")
	q := url.Values{}
	q.Set("all", "true")
	u.RawQuery = q.Encode()
	return u.String()
}
