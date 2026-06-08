// Package provider abstracts the control plane (Tailscale or Headscale) behind a
// single ControlPlane interface plus a capability set, so the collectors and the
// app wiring stay provider-agnostic. The Tailscale path is the default and
// unchanged: *tsapi.Client already satisfies ControlPlane, and the Tailscale
// provider advertises every feature. A Headscale provider (internal/hsapi)
// implements the same interface for the subset of features Headscale exposes.
package provider

import (
	"context"
	"sort"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// Kind identifies the control-plane backend.
type Kind string

const (
	// KindTailscale is the Tailscale central API (the default backend).
	KindTailscale Kind = "tailscale"
	// KindHeadscale is the self-hosted Headscale control plane.
	KindHeadscale Kind = "headscale"
)

// AllFeatures is every gateable collector/feature name. Each value is also a
// collector config key, so collectors.go can gate registration on
// Provider.Supports(<key>). Kept in sync with the Collectors config struct.
var AllFeatures = []string{
	"devices", "users", "keys", "acl", "dns", "settings", "contacts", "webhooks",
	"posture_integrations", "log_stream", "services", "flowlogs", "auditlogs", "nodemetrics",
}

// headscaleFeatures is the subset of AllFeatures that Headscale's API supports.
// devices/users/keys/acl map to /api/v1/{node,user,preauthkey+apikey,policy};
// nodemetrics scrapes node-local tailscaled (provider-agnostic). The rest have
// no Headscale equivalent and auto-disable.
var headscaleFeatures = []string{"devices", "users", "keys", "acl", "nodemetrics"}

// ControlPlane is the data surface the abstracted collectors (devices, users,
// keys, acl) consume. It deliberately returns the existing tsapi/tsclient types
// so the collectors need no changes; *tsapi.Client satisfies it directly and a
// Headscale adapter constructs the same types (Tailscale-only fields zeroed).
type ControlPlane interface {
	DevicesRich(ctx context.Context) ([]tsapi.RichDevice, error)
	DevicePostureAttributes(ctx context.Context, deviceID string) (map[string]any, error)
	DeviceInvites(ctx context.Context, deviceID string) ([]tsapi.DeviceInvite, error)
	Users(ctx context.Context) ([]tsclient.User, error)
	UserInvites(ctx context.Context) ([]tsapi.UserInvite, error)
	KeysRich(ctx context.Context) ([]tsapi.Key, error)
	PolicyFileRaw(ctx context.Context) (*tsclient.RawACL, error)
}

// Provider bundles a ControlPlane with its kind and capability set.
type Provider struct {
	Kind   Kind
	Client ControlPlane
	caps   map[string]bool
}

// Supports reports whether the named feature/collector is available on this
// provider. Unknown names are unsupported.
func (p *Provider) Supports(feature string) bool { return p.caps[feature] }

// Capabilities returns the supported feature names, sorted.
func (p *Provider) Capabilities() []string {
	out := make([]string, 0, len(p.caps))
	for f, ok := range p.caps {
		if ok {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func featureSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Tailscale builds a provider backed by the Tailscale API client; it advertises
// every feature.
func Tailscale(c ControlPlane) *Provider {
	return &Provider{Kind: KindTailscale, Client: c, caps: featureSet(AllFeatures...)}
}

// Headscale builds a provider backed by a Headscale client; it advertises only
// the features Headscale's API exposes.
func Headscale(c ControlPlane) *Provider {
	return &Provider{Kind: KindHeadscale, Client: c, caps: featureSet(headscaleFeatures...)}
}

// *tsapi.Client already implements every ControlPlane method — this compile-time
// assertion is the seam guard: if the tsapi surface drifts, this breaks first.
var _ ControlPlane = (*tsapi.Client)(nil)
