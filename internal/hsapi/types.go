package hsapi

// Wire types mirror Headscale's grpc-gateway JSON (camelCase). uint64 ids
// marshal as quoted strings under grpc-gateway, so id fields are string.

type nodesResponse struct {
	Nodes []Node `json:"nodes"`
}

// Node is GET /api/v1/node's element.
type Node struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	GivenName       string   `json:"givenName"`
	IPAddresses     []string `json:"ipAddresses"`
	User            User     `json:"user"`
	LastSeen        string   `json:"lastSeen"`
	Expiry          string   `json:"expiry"`
	CreatedAt       string   `json:"createdAt"`
	RegisterMethod  string   `json:"registerMethod"`
	Online          bool     `json:"online"`
	ApprovedRoutes  []string `json:"approvedRoutes"`
	AvailableRoutes []string `json:"availableRoutes"`
	SubnetRoutes    []string `json:"subnetRoutes"`
	Tags            []string `json:"tags"`
}

type usersResponse struct {
	Users []User `json:"users"`
}

// User is GET /api/v1/user's element (also embedded in Node/PreAuthKey).
type User struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	ProviderID    string `json:"providerId"`
	Provider      string `json:"provider"`
	ProfilePicURL string `json:"profilePicUrl"`
	CreatedAt     string `json:"createdAt"`
}

type preAuthKeysResponse struct {
	PreAuthKeys []PreAuthKey `json:"preAuthKeys"`
}

// PreAuthKey is GET /api/v1/preauthkey's element.
type PreAuthKey struct {
	User       User     `json:"user"`
	ID         string   `json:"id"`
	Key        string   `json:"key"`
	Reusable   bool     `json:"reusable"`
	Ephemeral  bool     `json:"ephemeral"`
	Used       bool     `json:"used"`
	Expiration string   `json:"expiration"`
	CreatedAt  string   `json:"createdAt"`
	ACLTags    []string `json:"aclTags"`
}

type apiKeysResponse struct {
	APIKeys []APIKey `json:"apiKeys"`
}

// APIKey is GET /api/v1/apikey's element.
type APIKey struct {
	ID         string `json:"id"`
	Prefix     string `json:"prefix"`
	Expiration string `json:"expiration"`
	CreatedAt  string `json:"createdAt"`
	LastSeen   string `json:"lastSeen"`
}

// Policy is GET /api/v1/policy.
type Policy struct {
	Policy    string `json:"policy"`
	UpdatedAt string `json:"updatedAt"`
}
