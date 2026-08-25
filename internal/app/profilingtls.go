package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
)

// pyroscopeTransportOptions is the TLS + extra-header material for the Pyroscope
// upload client (#375), decoupled from the config struct on purpose:
//
//   - it keeps newProfilingUploadClient and the header-precedence rules testable
//     against real servers without constructing a whole config.Config, and
//   - it is the single seam where the operator-facing configuration is mapped, so
//     the mapping is one small function (pyroscopeTransportOptionsFromConfig)
//     rather than field accesses scattered through the transport code.
type pyroscopeTransportOptions struct {
	// InsecureSkipVerify keeps TLS on but skips server-certificate verification.
	InsecureSkipVerify bool
	// CAFile is a PEM bundle that REPLACES the system roots for the upload
	// connection (the same semantics as otlp.tls.ca_file).
	CAFile string
	// CertFile/KeyFile are the client certificate presented for mTLS. Both or
	// neither; config validation already enforces that and that they load as a
	// usable keypair, but tlsConfig still reports a read failure rather than
	// panicking, because the files can change between validation and use.
	CertFile string
	KeyFile  string
	// Headers are operator-supplied extra headers sent on every upload. See
	// sanitizePyroscopeHeaders for the precedence rule.
	Headers map[string]string
	// BasicAuthSet / TenantSet say whether the reserved Authorization and tenant
	// headers are in use, which is what makes them reserved.
	BasicAuthSet bool
	TenantSet    bool
}

// pyroscopeTenantHeader is the multi-tenancy header pyroscope-go sets from
// TenantID. Named here because the reserved-header rule has to know it and the
// SDK does not export it.
const pyroscopeTenantHeader = "X-Scope-OrgID"

// alwaysReservedPyroscopeHeaders are reserved regardless of configuration.
//
// Content-Type carries the multipart boundary the uploader generated for THIS
// request body; replacing it makes every upload fail in a way that looks like a
// server problem. The two length/encoding headers are computed by net/http and a
// supplied value would contradict the body.
var alwaysReservedPyroscopeHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Transfer-Encoding",
}

// sanitizePyroscopeHeaders applies the header-precedence rule:
//
//	RESERVED HEADERS ALWAYS WIN. A user-supplied header can never override
//	identity, authentication, tenancy, or the request framing.
//
// Reserved =
//   - Authorization, when basic auth is configured;
//   - X-Scope-OrgID, when tenant_id is configured;
//   - Content-Type / Content-Length / Transfer-Encoding, always.
//
// Enforcement is by REMOVAL here rather than by ordering at request time, and
// that is load-bearing: pyroscope-go's uploader applies cfg.HTTPHeaders with
// Header.Set AFTER SetBasicAuth and after the tenant header, so the SDK's own
// precedence is the exact REVERSE of this contract. Anything left in the map wins
// on the wire, so the only way for the reserved headers to win is for the
// colliding entries never to reach the SDK.
//
// Comparison is on the canonical header name because Header.Set canonicalizes:
// a lowercase "authorization" would otherwise slip past a literal match and then
// overwrite the real one.
//
// A user header named Authorization with NO basic auth configured is allowed and
// passed through — that is the documented way to use a bearer token against a
// server that does not do basic auth, and there is no reserved value for it to
// displace. Same for X-Scope-OrgID with no tenant_id.
//
// Dropped entries are returned so the caller can log the names (never values).
func sanitizePyroscopeHeaders(headers map[string]string, basicAuthSet, tenantSet bool) (kept map[string]string, dropped []string) {
	if len(headers) == 0 {
		return nil, nil
	}
	reserved := make(map[string]bool, len(alwaysReservedPyroscopeHeaders)+2)
	for _, h := range alwaysReservedPyroscopeHeaders {
		reserved[http.CanonicalHeaderKey(h)] = true
	}
	if basicAuthSet {
		reserved[http.CanonicalHeaderKey("Authorization")] = true
	}
	if tenantSet {
		reserved[http.CanonicalHeaderKey(pyroscopeTenantHeader)] = true
	}
	kept = make(map[string]string, len(headers))
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		switch {
		case canonical == "":
			dropped = append(dropped, "(empty)")
		case reserved[canonical]:
			dropped = append(dropped, canonical)
		default:
			kept[canonical] = value
		}
	}
	sort.Strings(dropped)
	if len(kept) == 0 {
		return nil, dropped
	}
	return kept, dropped
}

// tlsConfig builds the *tls.Config for the upload connection, or nil when no TLS
// customization is requested (leaving the transport's stdlib defaults, i.e.
// system roots and full verification).
func (o pyroscopeTransportOptions) tlsConfig() (*tls.Config, error) {
	if !o.InsecureSkipVerify && o.CAFile == "" && o.CertFile == "" && o.KeyFile == "" {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		//nolint:gosec // G402: opt-in, explicitly named insecure_skip_verify, default false
		InsecureSkipVerify: o.InsecureSkipVerify,
	}
	if o.CAFile != "" {
		pem, err := safefile.ReadRegular(o.CAFile, safefile.MaxPEMBytes, safefile.AllowSymlink)
		if err != nil {
			return nil, fmt.Errorf("read pyroscope tls ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("pyroscope tls ca_file %s contains no usable certificate", o.CAFile)
		}
		cfg.RootCAs = pool
	}
	if o.CertFile != "" || o.KeyFile != "" {
		if o.CertFile == "" || o.KeyFile == "" {
			return nil, fmt.Errorf("pyroscope tls cert_file and key_file must be set together")
		}
		pair, err := safefile.LoadX509KeyPair(o.CertFile, o.KeyFile, safefile.MaxPEMBytes)
		if err != nil {
			return nil, fmt.Errorf("load pyroscope tls client keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// headerNames returns the sorted CANONICAL names of the configured extra headers,
// for the status page. Names only, never values — a Pyroscope extra header is the
// documented place to put a bearer token, so the values are secrets.
func (o pyroscopeTransportOptions) headerNames() []string {
	if len(o.Headers) == 0 {
		return nil
	}
	kept, _ := sanitizePyroscopeHeaders(o.Headers, o.BasicAuthSet, o.TenantSet)
	names := make([]string, 0, len(kept))
	for name := range kept {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// secretValues returns every value that must be scrubbed from a log line: the
// basic-auth password is handled by the SDK, but the extra-header values are ours
// and the SDK's Debugf/Errorf paths are not credential-aware.
func (o pyroscopeTransportOptions) secretValues() []string {
	out := make([]string, 0, len(o.Headers))
	for _, v := range o.Headers {
		if len(v) >= minRedactableSecretLen {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// minRedactableSecretLen stops the redactor turning a trivially short header
// value (say "1" for a tenant number) into a substring filter that mangles every
// log line it appears in. A secret shorter than this is not usefully redactable
// by substring matching anyway.
const minRedactableSecretLen = 6

// redactSecretsFunc returns a function replacing every known secret value in a
// string with a fixed placeholder. Used to wrap the Pyroscope SDK's logger, which
// formats server responses and its own configuration into messages without any
// notion of which parts are credentials.
func redactSecretsFunc(secrets []string) func(string) string {
	secrets = slices.Clone(secrets)
	// Longest first, so a secret that contains another is redacted whole rather
	// than leaving a fragment behind.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	secrets = slices.DeleteFunc(secrets, func(s string) bool { return len(s) < minRedactableSecretLen })
	if len(secrets) == 0 {
		return func(s string) string { return s }
	}
	return func(s string) string {
		for _, secret := range secrets {
			s = strings.ReplaceAll(s, secret, redactedPlaceholder)
		}
		return s
	}
}

const redactedPlaceholder = "[REDACTED]"

// pyroscopeTransportOptionsFromConfig maps the Pyroscope config onto the
// transport options. It is the single place operator configuration crosses into
// the transport, so the TLS/header behavior below is reachable from exactly one
// set of field reads.
//
// BasicAuthSet mirrors the SDK's own condition rather than "a user is
// configured": pyroscope-go sets the Authorization header only when BOTH the user
// and the password are non-empty, so reserving Authorization on a half-configured
// credential would drop the operator's header while nothing replaced it.
func pyroscopeTransportOptionsFromConfig(p config.ProfilingPyroscope) pyroscopeTransportOptions {
	o := pyroscopeTransportOptions{
		InsecureSkipVerify: p.TLS.InsecureSkipVerify,
		CAFile:             p.TLS.CAFile,
		CertFile:           p.TLS.CertFile,
		KeyFile:            p.TLS.KeyFile,
		BasicAuthSet:       p.BasicAuthUser != "" && p.BasicAuthPassword != "",
		TenantSet:          p.TenantID != "",
	}
	if len(p.Headers) > 0 {
		// Reveal only here, at the point of use. config.Secret keeps the values out
		// of logs and the config surface by default, and everything downstream of
		// this struct treats them as secrets: sanitizePyroscopeHeaders passes them
		// through untouched, headerNames drops them, and secretValues feeds the log
		// redactor.
		o.Headers = make(map[string]string, len(p.Headers))
		for k, v := range p.Headers {
			o.Headers[k] = v.Reveal()
		}
	}
	return o
}
