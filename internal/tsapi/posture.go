package tsapi

import (
	"context"
	"path"
	"time"
)

// PostureIntegration is a configured device-posture integration with its sync
// status. It is decoded directly because tsclient.PostureIntegration omits the
// status{} block. The sensitive clientId/tenantId/cloudId fields are
// deliberately NOT decoded, so they can never be surfaced as telemetry.
type PostureIntegration struct {
	ID       string
	Provider string
	Status   PostureIntegrationStatus
}

// PostureIntegrationStatus is the most recent sync status of an integration.
type PostureIntegrationStatus struct {
	LastSync             time.Time
	MatchedCount         int64
	PossibleMatchedCount int64
	ProviderHostCount    int64
}

type postureIntegrationsResponse struct {
	Integrations []postureIntegration `json:"integrations"`
}

type postureIntegration struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Status   struct {
		LastSync             time.Time `json:"lastSync"`
		MatchedCount         int64     `json:"matchedCount"`
		PossibleMatchedCount int64     `json:"possibleMatchedCount"`
		ProviderHostCount    int64     `json:"providerHostCount"`
	} `json:"status"`
}

// PostureIntegrations lists the configured device-posture integrations and their
// sync status.
func (c *Client) PostureIntegrations(ctx context.Context) ([]PostureIntegration, error) {
	var wire postureIntegrationsResponse
	if err := c.getJSON(ctx, c.postureIntegrationsURL(), &wire); err != nil {
		return nil, err
	}
	out := make([]PostureIntegration, 0, len(wire.Integrations))
	for _, p := range wire.Integrations {
		out = append(out, PostureIntegration{
			ID:       p.ID,
			Provider: p.Provider,
			Status: PostureIntegrationStatus{
				LastSync:             p.Status.LastSync,
				MatchedCount:         p.Status.MatchedCount,
				PossibleMatchedCount: p.Status.PossibleMatchedCount,
				ProviderHostCount:    p.Status.ProviderHostCount,
			},
		})
	}
	return out, nil
}

func (c *Client) postureIntegrationsURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "posture", "integrations")
	return u.String()
}
