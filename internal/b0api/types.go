package b0api

import (
	"encoding/json"
	"time"
)

// Pagination is the pagination object returned by Border0 list endpoints.
// TotalRecords is a pointer because a socket-scoped empty session response is
// literally {} and therefore has no pagination object (and no total_records
// value). Keeping that distinction prevents an empty response from being
// mistaken for a populated page with a zero total.
type Pagination struct {
	CurrentPage    int  `json:"current_page"`
	NextPage       int  `json:"next_page"`
	TotalRecords   *int `json:"total_records"`
	TotalPages     int  `json:"total_pages"`
	RecordsPerPage int  `json:"records_per_page"`
	ActualPageSize int  `json:"actual_page_size"`
}

// PageOptions selects one page from a Border0 paginated endpoint. A zero
// PageOptions value is normalized to Border0's first page and its observed
// default page size. The API ignores unrelated filters, so this client exposes
// only the two parameters the API actually honors.
type PageOptions struct {
	Page     int
	PageSize int
}

// SessionListOptions is an explicit name for the same page selector used by
// session methods. It is an alias so inventory and session paging cannot drift
// into two subtly different query contracts.
type SessionListOptions = PageOptions

const defaultPageSize = 100

func (o PageOptions) normalized() (PageOptions, error) {
	if o.Page < 0 {
		return PageOptions{}, invalidPageOption("page", o.Page)
	}
	if o.PageSize < 0 {
		return PageOptions{}, invalidPageOption("page_size", o.PageSize)
	}
	if o.Page == 0 {
		o.Page = 1
	}
	if o.PageSize == 0 {
		o.PageSize = defaultPageSize
	}
	return o, nil
}

// Organization is the organization configuration returned by GET
// /organization. Fields containing identity or credential material are decoded
// because this package is the wire client; collectors must not emit those
// fields as telemetry.
type Organization struct {
	ID                    string               `json:"id"`
	Name                  string               `json:"name"`
	Subdomain             string               `json:"subdomain"`
	AccountID             int64                `json:"account_id"`
	OwnerEmail            string               `json:"owner_email"`
	Role                  string               `json:"role"`
	MFARequired           bool                 `json:"mfa_required"`
	PrivateNetworkEnabled bool                 `json:"private_network_enabled"`
	DNSManagementEnabled  bool                 `json:"dns_management_enabled"`
	NeedsReauth           bool                 `json:"needs_reauth"`
	Metadata              OrganizationMetadata `json:"metadata"`
	SetupWizard           SetupWizard          `json:"setup_wizard"`
	Subscription          Subscription         `json:"subscription"`
	Certificate           Certificate          `json:"certificate"`
}

type Certificate struct {
	MTLSCertificate string `json:"mtls_certificate"`
	SSHPublicKey    string `json:"ssh_public_key"`
}

type OrganizationMetadata struct {
	AIAssistantsDisabled      bool `json:"ai_assistants_disabled"`
	AISessionAnalysisDisabled bool `json:"ai_session_analysis_disabled"`
	IsTailscale               bool `json:"is_ts"`
}

type SetupWizard struct {
	Completed bool                       `json:"completed"`
	Steps     map[string]SetupWizardStep `json:"steps"`
}

type SetupWizardStep struct {
	Completed        bool      `json:"completed"`
	CompletedAt      time.Time `json:"completed_at"`
	CompletedByEmail string    `json:"completed_by_email"`
	CompletedByID    string    `json:"completed_by_id"`
	Skipped          bool      `json:"skipped"`
}

type Subscription struct {
	Plan              Plan              `json:"plan"`
	SubscriptionLimit SubscriptionLimit `json:"subscription_limit"`
}

type Plan struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type SubscriptionLimit struct {
	SubscriptionID    int64 `json:"subscription_id"`
	SocketCount       int64 `json:"socket_count"`
	SocketTCPCount    int64 `json:"socket_tcp_count"`
	OrganizationCount int64 `json:"organization_count"`
	AdminUserCount    int64 `json:"admin_user_count"`
	UserCount         int64 `json:"user_count"`
	CustomDomainCount int64 `json:"custom_domain_count"`
	CustomIDPCount    int64 `json:"custom_idp_count"`
	NotificationCount int64 `json:"notification_count"`
}

// ServerInfo is the small response returned by GET /serverinfo.
type ServerInfo struct {
	DataConsistency DataConsistency `json:"data_consistency"`
}

type DataConsistency struct {
	RXAfterTXDelayMS int64 `json:"rx_after_tx_delay_ms"`
}

// Connector is one Border0 connector. The metadata block contains the
// connector's public IP and geolocation; it is intentionally decoded for wire
// completeness but is not safe telemetry input.
type Connector struct {
	Name                     string             `json:"name"`
	ConnectorID              string             `json:"connector_id"`
	Description              string             `json:"description"`
	ActiveTokens             int                `json:"active_tokens"`
	ActivePlugins            int                `json:"active_plugins"`
	Sockets                  int                `json:"sockets"`
	CreatedAt                time.Time          `json:"created_at"`
	UpdatedAt                time.Time          `json:"updated_at"`
	LastSeenAt               time.Time          `json:"last_seen_at"`
	IsConnected              bool               `json:"is_connected"`
	NotificationsEnabled     bool               `json:"notifications_enabled"`
	NotifyAfterSeconds       int                `json:"notify_after_seconds"`
	BuiltInSSHServiceEnabled bool               `json:"built_in_ssh_service_enabled"`
	BuiltInSSHService        *BuiltInSSHService `json:"built_in_ssh_service"`
	Metadata                 ConnectorMetadata  `json:"metadata"`
}

type BuiltInSSHService struct {
	SocketID           string  `json:"socket_id"`
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	DNSName            string  `json:"dnsname"`
	SocketType         string  `json:"socket_type"`
	Alive              bool    `json:"alive"`
	ConnectorManaged   bool    `json:"connector_managed"`
	AutocreationRuleID *string `json:"autocreation_rule_id"`
}

type ConnectorMetadata struct {
	ConnectorInternalMetadata ConnectorInternalMetadata `json:"connector_internal_metadata"`
}

type ConnectorInternalMetadata struct {
	Version      string       `json:"version"`
	BuiltDate    string       `json:"built_date"`
	IPAddress    string       `json:"ip_address"`
	IPMetadata   IPMetadata   `json:"ip_metadata"`
	HostMetadata HostMetadata `json:"host_metadata"`
}

type IPMetadata struct {
	ISP         string  `json:"isp"`
	CityName    string  `json:"city_name"`
	RegionName  string  `json:"region_name"`
	RegionCode  string  `json:"region_code"`
	CountryCode string  `json:"country_code"`
	CountryName string  `json:"country_name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type HostMetadata struct {
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	KernelArch      string `json:"kernel_arch"`
	KernelVersion   string `json:"kernel_version"`
	Uptime          int64  `json:"uptime"`
	Hostname        string `json:"hostname"`
}

// ConnectorToken is metadata returned by the connector token endpoint. The
// token value itself is not returned by Border0 and is intentionally absent.
type ConnectorToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Plugin is kept as raw keyed JSON because the captured plugin list is empty
// and Border0 does not publish a plugin schema. Using RawMessage values keeps
// additive plugin fields retained rather than silently discarded.
type Plugin map[string]json.RawMessage

// Socket is a Border0 PAM service. Password and username fields are wire
// fields only; callers must treat them as secrets and must never serialize
// this object into telemetry without explicitly removing authentication data.
type Socket struct {
	SocketID                                 string            `json:"socket_id"`
	Name                                     string            `json:"name"`
	Description                              string            `json:"description"`
	DNSName                                  string            `json:"dnsname"`
	SocketType                               string            `json:"socket_type"`
	SocketTCPPorts                           json.RawMessage   `json:"socket_tcp_ports"`
	UpstreamType                             string            `json:"upstream_type"`
	UpstreamHTTPHostname                     string            `json:"upstream_http_hostname"`
	RecordingEnabled                         bool              `json:"recording_enabled"`
	ConnectorAuthenticationEnabled           bool              `json:"connector_authentication_enabled"`
	EndToEndEncryptionEnabled                bool              `json:"end_to_end_encryption_enabled"`
	CloudAuthenticationEnabled               bool              `json:"cloud_authentication_enabled"`
	CloudAuthenticationEmailAllowedAddresses []string          `json:"cloud_authentication_email_allowed_addressses"`
	CloudAuthenticationEmailAllowedDomains   []string          `json:"cloud_authentication_email_allowed_domains"`
	CustomDomains                            []string          `json:"custom_domains"`
	PrivateSocket                            bool              `json:"private_socket"`
	PrivateNetworkEnabled                    bool              `json:"private_network_enabled"`
	PrivateNetworkIPv4                       string            `json:"private_network_ipv4"`
	PrivateNetworkIPv6                       string            `json:"private_network_ipv6"`
	ProtectedSocket                          bool              `json:"protected_socket"`
	ProtectedUsername                        string            `json:"protected_username"`
	ProtectedPassword                        string            `json:"protected_password"`
	UpstreamUsername                         *string           `json:"upstream_username"`
	UpstreamPassword                         *string           `json:"upstream_password"`
	Tags                                     map[string]any    `json:"tags"`
	Alive                                    bool              `json:"alive"`
	ConnectorManaged                         bool              `json:"connector_managed"`
	AutocreationRuleID                       *string           `json:"autocreation_rule_id"`
	Connectors                               []SocketConnector `json:"connectors"`
}

type SocketConnector struct {
	Name        string `json:"name"`
	ConnectorID string `json:"connector_id"`
}

// SocketConnectorLink is returned by GET /socket/{id}/connectors. The
// socket_upstream_config field repeats the service configuration shape and can
// contain cleartext credentials.
type SocketConnectorLink struct {
	ID                   int64                `json:"id"`
	SocketID             string               `json:"socket_id"`
	ConnectorID          string               `json:"connector_id"`
	ConnectorName        string               `json:"connector_name"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	SocketUpstreamConfig ServiceConfiguration `json:"socket_upstream_config"`
}

// UpstreamConfiguration is returned by GET /socket/{id}/upstream_configurations.
// Authentication sub-objects are retained for decoding fidelity only and are
// a hard secret fence for later snapshot/telemetry code.
type UpstreamConfiguration struct {
	Config    ServiceConfiguration `json:"config"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type ServiceConfiguration struct {
	ServiceType                  string                        `json:"service_type"`
	DatabaseServiceConfiguration *DatabaseServiceConfiguration `json:"database_service_configuration"`
	SSHServiceConfiguration      *SSHServiceConfiguration      `json:"ssh_service_configuration"`
}

type DatabaseServiceConfiguration struct {
	DatabaseServiceType                  string                                `json:"database_service_type"`
	StandardDatabaseServiceConfiguration *StandardDatabaseServiceConfiguration `json:"standard_database_service_configuration"`
}

type StandardDatabaseServiceConfiguration struct {
	Hostname                             string                                `json:"hostname"`
	Port                                 int                                   `json:"port"`
	Protocol                             string                                `json:"protocol"`
	DatabaseName                         string                                `json:"database_name"`
	AuthenticationType                   string                                `json:"authentication_type"`
	UsernameAndPasswordAuthConfiguration *UsernameAndPasswordAuthConfiguration `json:"username_and_password_auth_configuration"`
	PrivateKeyAuthConfiguration          json.RawMessage                       `json:"private_key_auth_configuration"`
	Border0CertificateAuthConfiguration  json.RawMessage                       `json:"border0_certificate_auth_configuration"`
}

type SSHServiceConfiguration struct {
	StandardSSHServiceConfiguration *StandardSSHServiceConfiguration `json:"standard_ssh_service_configuration"`
}

type StandardSSHServiceConfiguration struct {
	Hostname                             string                                `json:"hostname"`
	Port                                 int                                   `json:"port"`
	SSHAuthenticationType                string                                `json:"ssh_authentication_type"`
	UsernameAndPasswordAuthConfiguration *UsernameAndPasswordAuthConfiguration `json:"username_and_password_auth_configuration"`
	PrivateKeyAuthConfiguration          json.RawMessage                       `json:"private_key_auth_configuration"`
	Border0CertificateAuthConfiguration  json.RawMessage                       `json:"border0_certificate_auth_configuration"`
}

type UsernameAndPasswordAuthConfiguration struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Policy is one Border0 access policy.
type Policy struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	OrgID       string     `json:"org_id"`
	OrgWide     bool       `json:"org_wide"`
	ReadOnly    bool       `json:"read_only"`
	Version     string     `json:"version"`
	Expires     bool       `json:"expires"`
	Deleted     bool       `json:"deleted"`
	CreatedAt   time.Time  `json:"created_at"`
	SocketIDs   []string   `json:"socket_ids"`
	PolicyData  PolicyData `json:"policy_data"`
}

type PolicyData struct {
	Condition   PolicyCondition   `json:"condition"`
	Permissions PolicyPermissions `json:"permissions"`
}

type PolicyCondition struct {
	When PolicyWhen `json:"when"`
	Who  PolicyWho  `json:"who"`
}

type PolicyWhen struct {
	After           *string `json:"after"`
	Before          *string `json:"before"`
	TimeOfDayAfter  string  `json:"time_of_day_after"`
	TimeOfDayBefore string  `json:"time_of_day_before"`
}

type PolicyWho struct {
	Email          []string `json:"email"`
	Group          []string `json:"group"`
	ServiceAccount []string `json:"service_account"`
}

// PermissionSettings is intentionally an opaque keyed object. Border0 uses a
// different settings shape per service type and the current captures contain
// empty objects. Raw values preserve any future settings, including fields the
// collector must later adjudicate before using.
type PermissionSettings map[string]json.RawMessage

type PolicyPermissions struct {
	SSH        PermissionSettings `json:"ssh"`
	Database   PermissionSettings `json:"database"`
	HTTP       PermissionSettings `json:"http"`
	Kubernetes PermissionSettings `json:"kubernetes"`
	RDP        PermissionSettings `json:"rdp"`
	VNC        PermissionSettings `json:"vnc"`
	AWSS3      PermissionSettings `json:"aws_s3"`
}

// DirectoryService is shared by IAM users, groups and mirrored service
// accounts.
type DirectoryService struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	ServiceType string `json:"service_type"`
}

type IAMUser struct {
	ID               string           `json:"id"`
	Email            string           `json:"email"`
	DisplayName      string           `json:"display_name"`
	UserType         string           `json:"user_type"`
	Role             string           `json:"role"`
	ImageURL         string           `json:"image_url"`
	DirectoryService DirectoryService `json:"directory_service"`
}

type IAMGroup struct {
	ID               string           `json:"id"`
	DisplayName      string           `json:"display_name"`
	GroupType        string           `json:"group_type"`
	DirectoryService DirectoryService `json:"directory_service"`
}

type ServiceAccount struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	ServiceAccountID string            `json:"service_account_id"`
	Role             string            `json:"role"`
	Active           bool              `json:"active"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	LastSeenAt       time.Time         `json:"last_seen_at"`
	DirectoryService *DirectoryService `json:"directory_service"`
}

type ServiceAccountToken struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	CreatedBy      string `json:"created_by"`
	ServiceAccount string `json:"service_account"`
	TokenID        string `json:"token_id"`
}

// SessionPage is the response from /sessions and /socket/{id}/sessions. The
// pointer pagination field is significant: an empty socket-scoped response is
// {} rather than an empty paginated envelope.
type SessionPage struct {
	Pagination  *Pagination `json:"pagination"`
	SessionLogs []Session   `json:"session_logs"`
}

type Session struct {
	SessionID   string     `json:"session_id"`
	SocketID    string     `json:"socket_id"`
	SocketName  string     `json:"socket_name"`
	ServerName  string     `json:"server_name"`
	ServerPort  string     `json:"server_port"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time"`
	LastSeen    time.Time  `json:"last_seen"`
	SessionType string     `json:"session_type"`
	Result      string     `json:"result"`
	Killed      bool       `json:"killed"`
	AuditLog    bool       `json:"audit_log"`
	SSHUser     *string    `json:"sshuser"`
	UserEmail   string     `json:"user_email"`
	Name        string     `json:"name"`
	Picture     string     `json:"picture"`
	Subject     string     `json:"sub"`
	Nickname    string     `json:"nickname"`
	ClientIP    string     `json:"client_ip"`
	// ClientPort is opaque because the live API has emitted both a JSON string
	// and a number for this PII-only field. Collectors never expose it.
	ClientPort            json.RawMessage `json:"client_port"`
	CountryCode           string          `json:"country_code"`
	CountryFlag           string          `json:"country_flag"`
	AuthInfo              string          `json:"auth_info"`
	Recordings            []Recording     `json:"recordings"`
	RecordingLockedByPlan bool            `json:"recording_locked_by_plan"`
	Metadata              SessionMetadata `json:"metadata"`
	Events                []SessionEvent  `json:"events"`
}

type Recording struct {
	RecordingID   string    `json:"recording_id"`
	StartTime     time.Time `json:"start_time"`
	RecordingType string    `json:"recording_type"`
}

type SessionEvent struct {
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	// Metadata is a JSON object encoded inside a JSON string by Border0. It is
	// kept opaque here because it carries command lines and other PII.
	Metadata string `json:"metadata"`
}

type SessionMetadata struct {
	IPMetadata map[string]json.RawMessage `json:"ip_metadata"`
	Device     SessionDevice              `json:"device"`
}

type SessionDevice struct {
	IP   string `json:"ip"`
	Name string `json:"name"`
}

// The following page wrappers preserve the pagination envelope for callers
// that need to walk every page. The ordinary list methods below intentionally
// return slices, matching internal/tsapi's list-method convention.
type SocketPage struct {
	Pagination *Pagination `json:"pagination"`
	List       []Socket    `json:"list"`
}

type IAMUserPage struct {
	Pagination *Pagination `json:"pagination"`
	List       []IAMUser   `json:"list"`
}

type IAMGroupPage struct {
	Pagination *Pagination `json:"pagination"`
	List       []IAMGroup  `json:"list"`
}

type ServiceAccountPage struct {
	Pagination *Pagination      `json:"pagination"`
	List       []ServiceAccount `json:"list"`
}
