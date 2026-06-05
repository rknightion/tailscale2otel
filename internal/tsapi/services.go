package tsapi

import (
	"context"
	"path"
)

// VIPService is a Tailscale Service (VIP) — a tailnet-internal virtual service
// fronted by one or more backing hosts. Only the non-sensitive name/ports/tags
// are decoded; the addrs/comment/annotations fields (which carry IP addresses
// and operator IDs) are deliberately ignored so they cannot become telemetry.
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
	NodeID        string `json:"nodeId"`
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
