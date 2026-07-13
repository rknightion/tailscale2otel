package tsapi

import (
	"context"
	"path"
)

// VIPService is a Tailscale Service (VIP) — a tailnet-internal virtual service
// fronted by one or more backing hosts. Only the non-sensitive name/ports/tags
// are decoded; the addrs/comment/annotations fields (which carry IP addresses
// and operator IDs) are deliberately ignored so they cannot become telemetry.
//
// Carve-out: ServiceAddrs below DOES decode addrs, but only to populate the
// in-memory enrich.DeviceCache service-VIP map (so flow-log peers destined for
// a Service resolve to the service name instead of "unknown"). The decoded
// addresses must never be attached to an emitted metric/log attribute — only
// the derived service name may be. This VIPService type and the Services()
// decode path are unaffected and still never see addrs.
type VIPService struct {
	Name  string
	Ports []string
	Tags  []string
}

// ServiceHost is a device backing a VIP service, with its approval and
// configuration state.
type ServiceHost struct {
	NodeID        string
	ApprovalLevel string
	Configured    string
}

type vipServicesResponse struct {
	VIPServices []vipService `json:"vipServices"`
}

type vipService struct {
	Name  string   `json:"name"`
	Ports []string `json:"ports"`
	Tags  []string `json:"tags"`
}

type serviceHostsResponse struct {
	Hosts []serviceHost `json:"hosts"`
}

type serviceHost struct {
	// The OAS ServiceHostInfo schema names this wire field stableNodeID, not
	// nodeId; encoding/json only does case-insensitive matching, so json:"nodeId"
	// never matched and NodeID always decoded empty (#72).
	NodeID        string `json:"stableNodeID"`
	ApprovalLevel string `json:"approvalLevel"`
	Configured    string `json:"configured"`
}

// Services lists the tailnet's Tailscale Services (VIP services).
func (c *Client) Services(ctx context.Context) ([]VIPService, error) {
	var wire vipServicesResponse
	if err := c.getJSON(ctx, c.servicesURL(), &wire); err != nil {
		return nil, err
	}
	out := make([]VIPService, 0, len(wire.VIPServices))
	for _, s := range wire.VIPServices {
		out = append(out, VIPService(s))
	}
	return out, nil
}

// ServiceHosts lists the devices backing the named VIP service (e.g. "svc:argocd").
func (c *Client) ServiceHosts(ctx context.Context, name string) ([]ServiceHost, error) {
	var wire serviceHostsResponse
	if err := c.getJSON(ctx, c.serviceHostsURL(name), &wire); err != nil {
		return nil, err
	}
	out := make([]ServiceHost, 0, len(wire.Hosts))
	for _, h := range wire.Hosts {
		out = append(out, ServiceHost(h))
	}
	return out, nil
}

// ServiceAddr pairs a Tailscale Service (VIP service) name with its backing
// addresses (both the IPv4 and IPv6 VIP, as decoded off the wire). It exists
// ONLY to feed the in-memory enrich.DeviceCache service-VIP map — the Addrs
// here must never be surfaced as an emitted attribute value; only Name may be.
type ServiceAddr struct {
	Name  string
	Addrs []string
}

type vipServiceAddrsWire struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
}

type vipServiceAddrsResponse struct {
	VIPServices []vipServiceAddrsWire `json:"vipServices"`
}

// ServiceAddrs lists each tailnet Service's name and backing addresses, hits
// the same endpoint as Services but additionally decodes addrs. This is the
// deliberate carve-out documented on VIPService above: the result must be used
// ONLY to populate the in-memory enrich.DeviceCache service-VIP map so
// flow-log peers resolve to a service name — the addresses themselves must
// never become telemetry.
func (c *Client) ServiceAddrs(ctx context.Context) ([]ServiceAddr, error) {
	var wire vipServiceAddrsResponse
	if err := c.getJSON(ctx, c.servicesURL(), &wire); err != nil {
		return nil, err
	}
	out := make([]ServiceAddr, 0, len(wire.VIPServices))
	for _, s := range wire.VIPServices {
		out = append(out, ServiceAddr{Name: s.Name, Addrs: s.Addrs})
	}
	return out, nil
}

func (c *Client) servicesURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "services")
	return u.String()
}

func (c *Client) serviceHostsURL(name string) string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "services", name, "devices")
	return u.String()
}
