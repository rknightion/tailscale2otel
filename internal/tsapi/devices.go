package tsapi

import (
	"context"
	"net/url"
	"path"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"
)

// RichDevice is the full device record returned by GET
// /api/v2/tailnet/{tailnet}/devices?fields=all. It carries far more detail than
// the thin tsclient.Device returned by Devices.
type RichDevice struct {
	ID            string
	NodeID        string
	Name          string
	Hostname      string
	OS            string
	User          string
	ClientVersion string

	Addresses []string
	Tags      []string

	Authorized                bool
	IsExternal                bool
	UpdateAvailable           bool
	KeyExpiryDisabled         bool
	ConnectedToControl        bool
	BlocksIncomingConnections bool
	SSHEnabled                bool
	IsEphemeral               bool

	// MultipleConnections is the wire multipleConnections flag: true when more
	// than one client has simultaneously connected using this device's identity
	// (an anomaly/security signal). Present-when-true on the wire — absent means
	// false, not "unknown".
	MultipleConnections bool

	// PostureIdentity carries only the postureIdentity.disabled field from the
	// wire; nil when the device's payload has no postureIdentity object at all.
	// postureIdentity.serialNumbers are deliberately NOT decoded — same fencing
	// pattern as VIPService in services.go:8-11, which skips addrs so it can
	// never become telemetry.
	PostureIdentity *PostureIdentity

	// TailnetLockKey is the device's tailnet-lock public key (present on every
	// node regardless of whether tailnet lock is enabled); TailnetLockError is
	// non-empty when the node has a tailnet-lock problem (e.g. an unsigned node).
	TailnetLockKey   string
	TailnetLockError string

	Created  time.Time
	LastSeen time.Time
	Expires  time.Time

	AdvertisedRoutes []string
	EnabledRoutes    []string

	Distro DistroInfo

	// DERPLatency is keyed by DERP region name, e.g. "Frankfurt".
	DERPLatency map[string]DERPRegion

	// HardNAT is clientConnectivity.mappingVariesByDestIP: true when the host's
	// NAT mappings vary by destination IP (hard/symmetric NAT), which inhibits
	// direct connections. This is connection *eligibility*, not the live path.
	HardNAT bool

	// Endpoints is clientConnectivity.endpoints: the device's magicsock UDP
	// IP:port candidates. Only the COUNT is exported as a metric — the list is
	// never a label (unbounded).
	Endpoints []string

	// ClientSupports is clientConnectivity.clientSupports (capability tri-states).
	ClientSupports ClientSupports
}

// PostureIdentity is the subset of the wire postureIdentity object this
// package decodes: only whether device-identity posture checks are disabled.
// serialNumbers are deliberately never decoded (see the fence comment on
// RichDevice.PostureIdentity) so they can never become telemetry.
type PostureIdentity struct {
	Disabled bool
}

// DistroInfo describes the operating-system distribution reported by a device.
type DistroInfo struct {
	Name     string
	Version  string
	CodeName string
}

// DERPRegion is a device's measured latency to a single DERP region.
type DERPRegion struct {
	Preferred bool
	LatencyMs float64
}

// ClientSupports reports per-device direct-connectivity capabilities from
// clientConnectivity.clientSupports. Each field is a tri-state: nil = the API
// did not report it (or reported null, as hairPinning always does — it is no
// longer tracked and is therefore omitted from this struct entirely).
type ClientSupports struct {
	IPv6 *bool
	PCP  *bool
	PMP  *bool
	UDP  *bool
	UPnP *bool
}

// richDevicesResponse is the wire shape of the rich devices endpoint.
type richDevicesResponse struct {
	Devices []richDevice `json:"devices"`
}

type richDevice struct {
	ID            string `json:"id"`
	NodeID        string `json:"nodeId"`
	Name          string `json:"name"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	User          string `json:"user"`
	ClientVersion string `json:"clientVersion"`

	Addresses []string `json:"addresses"`
	Tags      []string `json:"tags"`

	Authorized                bool `json:"authorized"`
	IsExternal                bool `json:"isExternal"`
	UpdateAvailable           bool `json:"updateAvailable"`
	KeyExpiryDisabled         bool `json:"keyExpiryDisabled"`
	ConnectedToControl        bool `json:"connectedToControl"`
	BlocksIncomingConnections bool `json:"blocksIncomingConnections"`
	SSHEnabled                bool `json:"sshEnabled"`
	IsEphemeral               bool `json:"isEphemeral"`

	MultipleConnections bool `json:"multipleConnections"`

	// PostureIdentity intentionally decodes ONLY the disabled field.
	// serialNumbers (a wire sibling field) is never given a struct field here,
	// so it cannot be decoded, stored, or reach telemetry no matter what else
	// changes downstream — see the fence comment on RichDevice.PostureIdentity.
	PostureIdentity *struct {
		Disabled bool `json:"disabled"`
	} `json:"postureIdentity"`

	TailnetLockKey   string `json:"tailnetLockKey"`
	TailnetLockError string `json:"tailnetLockError"`

	// tsclient.Time tolerates an empty-string wire value: the Tailscale API returns
	// created:"" (and can return empty lastSeen/expires) for EXTERNAL/shared devices,
	// which a plain time.Time rejects — failing the whole /devices?fields=all decode
	// and taking every device metric + the enrichment cache down with it (#48).
	Created  tsclient.Time `json:"created"`
	LastSeen tsclient.Time `json:"lastSeen"`
	Expires  tsclient.Time `json:"expires"`

	AdvertisedRoutes []string `json:"advertisedRoutes"`
	EnabledRoutes    []string `json:"enabledRoutes"`

	Distro struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		CodeName string `json:"codeName"`
	} `json:"distro"`

	ClientConnectivity struct {
		Latency map[string]struct {
			Preferred bool    `json:"preferred"`
			LatencyMs float64 `json:"latencyMs"`
		} `json:"latency"`
		MappingVariesByDestIP bool     `json:"mappingVariesByDestIP"`
		Endpoints             []string `json:"endpoints"`
		ClientSupports        struct {
			IPv6 *bool `json:"ipv6"`
			PCP  *bool `json:"pcp"`
			PMP  *bool `json:"pmp"`
			UDP  *bool `json:"udp"`
			UPnP *bool `json:"upnp"`
		} `json:"clientSupports"`
	} `json:"clientConnectivity"`
}

// DevicesRich lists all devices in the tailnet with the full field set
// (fields=all), including DERP latency, distro details and connectivity flags.
func (c *Client) DevicesRich(ctx context.Context) ([]RichDevice, error) {
	var wire richDevicesResponse
	if err := c.getJSON(ctx, c.devicesURL(), &wire); err != nil {
		return nil, err
	}
	out := make([]RichDevice, 0, len(wire.Devices))
	for _, d := range wire.Devices {
		rd := RichDevice{
			ID:                        d.ID,
			NodeID:                    d.NodeID,
			Name:                      d.Name,
			Hostname:                  d.Hostname,
			OS:                        d.OS,
			User:                      d.User,
			ClientVersion:             d.ClientVersion,
			Addresses:                 d.Addresses,
			Tags:                      d.Tags,
			Authorized:                d.Authorized,
			IsExternal:                d.IsExternal,
			UpdateAvailable:           d.UpdateAvailable,
			KeyExpiryDisabled:         d.KeyExpiryDisabled,
			ConnectedToControl:        d.ConnectedToControl,
			BlocksIncomingConnections: d.BlocksIncomingConnections,
			SSHEnabled:                d.SSHEnabled,
			IsEphemeral:               d.IsEphemeral,
			MultipleConnections:       d.MultipleConnections,
			TailnetLockKey:            d.TailnetLockKey,
			TailnetLockError:          d.TailnetLockError,
			Created:                   d.Created.Time,
			LastSeen:                  d.LastSeen.Time,
			Expires:                   d.Expires.Time,
			AdvertisedRoutes:          d.AdvertisedRoutes,
			EnabledRoutes:             d.EnabledRoutes,
			Distro: DistroInfo{
				Name:     d.Distro.Name,
				Version:  d.Distro.Version,
				CodeName: d.Distro.CodeName,
			},
		}
		if d.PostureIdentity != nil {
			rd.PostureIdentity = &PostureIdentity{Disabled: d.PostureIdentity.Disabled}
		}
		rd.HardNAT = d.ClientConnectivity.MappingVariesByDestIP
		rd.Endpoints = d.ClientConnectivity.Endpoints
		rd.ClientSupports = ClientSupports{
			IPv6: d.ClientConnectivity.ClientSupports.IPv6,
			PCP:  d.ClientConnectivity.ClientSupports.PCP,
			PMP:  d.ClientConnectivity.ClientSupports.PMP,
			UDP:  d.ClientConnectivity.ClientSupports.UDP,
			UPnP: d.ClientConnectivity.ClientSupports.UPnP,
		}
		if len(d.ClientConnectivity.Latency) > 0 {
			rd.DERPLatency = make(map[string]DERPRegion, len(d.ClientConnectivity.Latency))
			for region, l := range d.ClientConnectivity.Latency {
				rd.DERPLatency[region] = DERPRegion{Preferred: l.Preferred, LatencyMs: l.LatencyMs}
			}
		}
		out = append(out, rd)
	}
	return out, nil
}

// DevicePostureAttributes returns the posture attribute map for deviceID.
func (c *Client) DevicePostureAttributes(ctx context.Context, deviceID string) (map[string]any, error) {
	attrs, err := c.ts.Devices().GetPostureAttributes(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return attrs.Attributes, nil
}

// devicesURL builds the rich devices endpoint URL (fields=all), mirroring
// logURL's construction.
func (c *Client) devicesURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "devices")
	q := url.Values{}
	q.Set("fields", "all")
	u.RawQuery = q.Encode()
	return u.String()
}
