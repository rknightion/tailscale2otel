package b0api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

const (
	fixtureToken       = "fixture-bearer-token"
	fixtureConnectorID = "00000000-0000-4000-8000-000000000001"
	fixtureDatabaseID  = "00000000-0000-4000-8000-000000000007"
	fixtureSSHID       = "00000000-0000-4000-8000-000000000008"
	fixturePolicyID    = "00000000-0000-4000-8000-000000000037"
)

func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()

	paths := map[string]string{
		"/api/v1/organization":                                             "pam_organization.json",
		"/api/v1/serverinfo":                                               "pam_serverinfo.json",
		"/api/v1/connectors":                                               "pam_connectors.json",
		"/api/v1/connector/" + fixtureConnectorID:                          "pam_connector_one.json",
		"/api/v1/connector/" + fixtureConnectorID + "/tokens":              "pam_connector_tokens.json",
		"/api/v1/connector/" + fixtureConnectorID + "/plugins":             "pam_connector_plugins.json",
		"/api/v1/sockets":                                                  "pam_sockets.json",
		"/api/v1/socket/" + fixtureDatabaseID:                              "pam_socket_one.json",
		"/api/v1/socket/" + fixtureSSHID:                                   "pam_socket_builtin_ssh.json",
		"/api/v1/socket/" + fixtureDatabaseID + "/connectors":              "pam_socket_connectors.json",
		"/api/v1/socket/" + fixtureDatabaseID + "/upstream_configurations": "pam_socket_upstream_config.json",
		"/api/v1/sessions":                                                 "pam_sessions.json",
		"/api/v1/socket/" + fixtureSSHID + "/sessions":                     "pam_socket_sessions_empty.json",
		"/api/v1/policies":                                                 "pam_policies.json",
		"/api/v1/policy/" + fixturePolicyID:                                "pam_policy_one.json",
		"/api/v1/organizations/iam/users":                                  "pam_iam_users.json",
		"/api/v1/organizations/iam/groups":                                 "pam_iam_groups.json",
		"/api/v1/organizations/iam/service_accounts":                       "pam_iam_service_accounts.json",
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method must be GET", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+fixtureToken {
			http.Error(w, "unexpected authorization", http.StatusUnauthorized)
			return
		}
		fixture, ok := paths[r.URL.Path]
		if !ok {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func fixtureClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Options{BaseURL: srv.URL, Token: fixtureToken})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestClientDecodesTrackedEndpointFixtures(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := fixtureClient(t, srv)

	tests := []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "organization",
			call: func(c *Client) error {
				out, err := c.Organization(context.Background())
				if err == nil && (out.Name == "" || out.Subscription.Plan.Slug == "") {
					return errors.New("organization fields were not decoded")
				}
				return err
			},
		},
		{
			name: "serverinfo",
			call: func(c *Client) error {
				out, err := c.ServerInfo(context.Background())
				if err == nil && out.DataConsistency.RXAfterTXDelayMS == 0 {
					return errors.New("serverinfo field was not decoded")
				}
				return err
			},
		},
		{
			name: "connectors",
			call: func(c *Client) error {
				out, err := c.Connectors(context.Background())
				if err == nil && len(out) != 2 {
					return errors.New("connector list length was not decoded")
				}
				return err
			},
		},
		{
			name: "connector",
			call: func(c *Client) error {
				out, err := c.Connector(context.Background(), fixtureConnectorID)
				if err == nil && (out.Name == "" || out.Metadata.ConnectorInternalMetadata.Version == "") {
					return errors.New("connector fields were not decoded")
				}
				return err
			},
		},
		{
			name: "connector tokens",
			call: func(c *Client) error {
				out, err := c.ConnectorTokens(context.Background(), fixtureConnectorID)
				if err == nil && len(out) != 1 {
					return errors.New("connector token list was not decoded")
				}
				return err
			},
		},
		{
			name: "connector plugins",
			call: func(c *Client) error {
				out, err := c.ConnectorPlugins(context.Background(), fixtureConnectorID)
				if err == nil && out == nil {
					return errors.New("empty plugin list was not decoded")
				}
				return err
			},
		},
		{
			name: "sockets",
			call: func(c *Client) error {
				out, err := c.Sockets(context.Background())
				if err == nil && len(out) != 2 {
					return errors.New("socket list length was not decoded")
				}
				return err
			},
		},
		{
			name: "socket",
			call: func(c *Client) error {
				out, err := c.Socket(context.Background(), fixtureDatabaseID)
				if err == nil && (out.Name == "" || out.UpstreamPassword == nil) {
					return errors.New("socket fields were not decoded")
				}
				return err
			},
		},
		{
			name: "built-in ssh socket",
			call: func(c *Client) error {
				out, err := c.Socket(context.Background(), fixtureSSHID)
				if err == nil && (out.Name == "" || out.SocketType != "ssh") {
					return errors.New("built-in ssh socket was not decoded")
				}
				return err
			},
		},
		{
			name: "socket connectors",
			call: func(c *Client) error {
				out, err := c.SocketConnectors(context.Background(), fixtureDatabaseID)
				if err == nil && (len(out) != 1 || out[0].SocketUpstreamConfig.ServiceType != "database") {
					return errors.New("socket connector fields were not decoded")
				}
				return err
			},
		},
		{
			name: "socket upstream configurations",
			call: func(c *Client) error {
				out, err := c.SocketUpstreamConfigurations(context.Background(), fixtureDatabaseID)
				if err == nil && (len(out) != 1 || out[0].Config.ServiceType != "database") {
					return errors.New("upstream configuration was not decoded")
				}
				return err
			},
		},
		{
			name: "sessions",
			call: func(c *Client) error {
				out, err := c.Sessions(context.Background(), PageOptions{Page: 1, PageSize: 100})
				if err == nil && (out.Pagination == nil || out.Pagination.TotalRecords == nil || *out.Pagination.TotalRecords != 9 || len(out.SessionLogs) != 9) {
					return errors.New("session pagination envelope was not decoded")
				}
				return err
			},
		},
		{
			name: "empty socket sessions",
			call: func(c *Client) error {
				out, err := c.SocketSessions(context.Background(), fixtureSSHID, PageOptions{Page: 1, PageSize: 100})
				if err == nil && (out.Pagination != nil || len(out.SessionLogs) != 0) {
					return errors.New("literal empty session object was not preserved")
				}
				return err
			},
		},
		{
			name: "policies",
			call: func(c *Client) error {
				out, err := c.Policies(context.Background())
				if err == nil && (len(out) != 1 || !out[0].ReadOnly) {
					return errors.New("bare policy array was not decoded")
				}
				return err
			},
		},
		{
			name: "policy",
			call: func(c *Client) error {
				out, err := c.Policy(context.Background(), fixturePolicyID)
				if err == nil && (out.Name == "" || out.PolicyData.Condition.When.After == nil) {
					return errors.New("policy fields were not decoded")
				}
				return err
			},
		},
		{
			name: "iam users",
			call: func(c *Client) error {
				out, err := c.IAMUsers(context.Background())
				if err == nil && len(out) != 1 {
					return errors.New("IAM user list was not decoded")
				}
				return err
			},
		},
		{
			name: "iam groups",
			call: func(c *Client) error {
				out, err := c.IAMGroups(context.Background())
				if err == nil && len(out) != 3 {
					return errors.New("IAM group list was not decoded")
				}
				return err
			},
		},
		{
			name: "iam service accounts",
			call: func(c *Client) error {
				out, err := c.IAMServiceAccounts(context.Background())
				if err == nil && len(out) == 0 {
					return errors.New("IAM service-account list was not decoded")
				}
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(c); err != nil {
				t.Fatalf("client call: %v", err)
			}
		})
	}
}

// TestTrackedPAMFixturesHaveNoUnhandledFields is the replacement for an
// OpenAPI drift check: Border0 publishes no schema, so DisallowUnknownFields
// makes an additive key in any tracked response fail this table immediately.
// Dynamic maps (tags, IP metadata, policy setting objects and plugins) retain
// their keys as RawMessage instead of ignoring them; the opaque JSON strings
// are retained verbatim by design because they carry PII and command text.
func TestTrackedPAMFixturesHaveNoUnhandledFields(t *testing.T) {
	tests := []struct {
		name string
		file string
		out  any
	}{
		{name: "organization", file: "pam_organization.json", out: new(Organization)},
		{name: "serverinfo", file: "pam_serverinfo.json", out: new(ServerInfo)},
		{name: "connectors", file: "pam_connectors.json", out: new(struct {
			List []Connector `json:"list"`
		})},
		{name: "connector", file: "pam_connector_one.json", out: new(Connector)},
		{name: "connector tokens", file: "pam_connector_tokens.json", out: new(struct {
			Connector ConnectorRef     `json:"connector"`
			List      []ConnectorToken `json:"list"`
		})},
		{name: "connector plugins", file: "pam_connector_plugins.json", out: new(struct {
			Connector ConnectorRef `json:"connector"`
			List      []Plugin     `json:"list"`
		})},
		{name: "sockets", file: "pam_sockets.json", out: new(SocketPage)},
		{name: "socket", file: "pam_socket_one.json", out: new(Socket)},
		{name: "built-in ssh socket", file: "pam_socket_builtin_ssh.json", out: new(Socket)},
		{name: "socket connectors", file: "pam_socket_connectors.json", out: new(struct {
			List []SocketConnectorLink `json:"list"`
		})},
		{name: "upstream configurations", file: "pam_socket_upstream_config.json", out: new(struct {
			List []UpstreamConfiguration `json:"list"`
		})},
		{name: "sessions", file: "pam_sessions.json", out: new(SessionPage)},
		{name: "socket sessions", file: "pam_socket_sessions.json", out: new(SessionPage)},
		{name: "empty socket sessions", file: "pam_socket_sessions_empty.json", out: new(SessionPage)},
		{name: "policies", file: "pam_policies.json", out: new([]Policy)},
		{name: "policy", file: "pam_policy_one.json", out: new(Policy)},
		{name: "IAM users", file: "pam_iam_users.json", out: new(IAMUserPage)},
		{name: "IAM groups", file: "pam_iam_groups.json", out: new(IAMGroupPage)},
		{name: "IAM service accounts", file: "pam_iam_service_accounts.json", out: new(ServiceAccountPage)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(tc.out); err != nil {
				t.Fatalf("%s has an unhandled field or shape: %v", tc.file, err)
			}
			var extra any
			if err := dec.Decode(&extra); err != io.EOF {
				t.Fatalf("%s contains trailing JSON: %v", tc.file, err)
			}
		})
	}
}

func TestClientForbiddenIsScopeDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c, err := NewClient(Options{BaseURL: srv.URL, Token: fixtureToken})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Organization(context.Background())
	if err == nil {
		t.Fatal("Organization error = nil, want 403 error")
	}
	var statusErr *tsapi.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error %T (%v) is not a *tsapi.StatusError", err, err)
	}
	if statusErr.Code != http.StatusForbidden {
		t.Fatalf("StatusError.Code = %d, want %d", statusErr.Code, http.StatusForbidden)
	}
	if got := apistate.Classify(err, apistate.Disposition{}); got != apistate.StateScopeDenied {
		t.Fatalf("apistate.Classify(403) = %q, want %q", got, apistate.StateScopeDenied)
	}
}

func TestNewClientBaseURLAndCredentialValidation(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		wantErr bool
		check   func(*testing.T, *Client)
	}{
		{
			name:    "default URL",
			options: Options{Token: fixtureToken},
			check: func(t *testing.T, c *Client) {
				if got := c.baseURL.String(); got != defaultBaseURL {
					t.Errorf("base URL = %q, want %q", got, defaultBaseURL)
				}
			},
		},
		{
			name:    "origin gets API path",
			options: Options{BaseURL: "http://127.0.0.1:12345", Token: fixtureToken},
			check: func(t *testing.T, c *Client) {
				if got := c.baseURL.Path; got != "/api/v1" {
					t.Errorf("base path = %q, want /api/v1", got)
				}
			},
		},
		{
			name:    "explicit API path",
			options: Options{BaseURL: "http://127.0.0.1:12345/api/v1/", Token: fixtureToken},
			check: func(t *testing.T, c *Client) {
				if got := c.baseURL.Path; got != "/api/v1" {
					t.Errorf("base path = %q, want /api/v1", got)
				}
			},
		},
		{
			name:    "missing token",
			options: Options{},
			wantErr: true,
		},
		{
			name:    "missing token with injected HTTP client",
			options: Options{HTTPClient: &http.Client{}},
			wantErr: true,
		},
		{
			name:    "invalid URL",
			options: Options{BaseURL: "not an URL", Token: fixtureToken},
			wantErr: true,
		},
		{
			name:    "out-of-range port",
			options: Options{BaseURL: "https://example.invalid:99999/api/v1", Token: fixtureToken},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(tc.options)
			if tc.wantErr {
				if err == nil {
					t.Fatal("NewClient error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if tc.check != nil {
				tc.check(t, c)
			}
		})
	}
}

func TestClientDoesNotFollowRedirectWithBearer(t *testing.T) {
	var targetAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/organization", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	c, err := NewClient(Options{BaseURL: origin.URL, Token: fixtureToken})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Organization(context.Background()); err == nil {
		t.Fatal("Organization error = nil, want redirect status error")
	}
	if targetAuth != "" {
		t.Fatalf("redirect target received Authorization %q", targetAuth)
	}
}
