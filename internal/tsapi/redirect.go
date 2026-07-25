package tsapi

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxRedirects mirrors net/http's own default cap. Supplying a CheckRedirect
// REPLACES that default, so the cap has to be re-stated here or a redirect loop
// would run forever.
const maxRedirects = 10

var (
	// ErrCrossOriginRedirect is the stable sentinel behind every refused
	// cross-origin redirect (#466, #467). errors.Is against it — including
	// through the *url.Error that http.Client wraps a CheckRedirect failure in —
	// identifies the class without string matching.
	ErrCrossOriginRedirect = errors.New("tsapi: cross-origin redirect refused")

	// ErrTooManyRedirects replaces net/http's untyped "stopped after 10
	// redirects" error, since installing a CheckRedirect removes it.
	ErrTooManyRedirects = errors.New("tsapi: too many redirects")
)

// CrossOriginRedirectError reports a redirect that would have carried a
// credential off the origin it was issued for. It names ORIGINS only — scheme,
// host and port — never the full destination URL, its path/query, or any
// userinfo the URL carried, so the refusal is observable in logs and traces
// without becoming its own disclosure.
type CrossOriginRedirectError struct {
	// From is the origin the request started on (scheme://host:port).
	From string
	// To is the origin the redirect pointed at (scheme://host:port).
	To string
}

func (e *CrossOriginRedirectError) Error() string {
	return "tsapi: cross-origin redirect refused: " + e.From + " -> " + e.To
}

func (e *CrossOriginRedirectError) Unwrap() error { return ErrCrossOriginRedirect }

// origin is the normalized scheme/authority identity a credential is bound to.
//
// Normalization is deliberately strict, because every difference below is a
// distinct trust boundary:
//   - scheme and host are lowercased (case-insensitive per RFC 3986), so
//     API.Tailscale.COM matches api.tailscale.com;
//   - an omitted port is filled in from the scheme, so https://h and
//     https://h:443 match, but https://h:8443 does not;
//   - a scheme downgrade (https -> http) never matches, so a redirect can never
//     move a credential onto cleartext;
//   - userinfo is part of the identity here (it is NOT, per the web origin
//     concept) so a redirect that injects embedded credentials into an
//     otherwise-matching URL is treated as a different origin rather than
//     silently accepted.
type origin struct {
	scheme   string // lowercased
	hostPort string // lowercased host + explicit port
	userinfo string // url.Userinfo.String(), "" when absent
}

// originOf normalizes u into an origin. It never returns the path or query.
func originOf(u *url.URL) origin {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	o := origin{scheme: scheme, hostPort: net.JoinHostPort(strings.ToLower(u.Hostname()), port)}
	if u.User != nil {
		o.userinfo = u.User.String()
	}
	return o
}

// parseOrigin normalizes a configured base URL (e.g. Options.BaseURL) into an
// origin, rejecting anything without both a scheme and a host — an unbound
// credential must never fall back to "matches everything".
func parseOrigin(raw string) (origin, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return origin{}, err
	}
	if u.Scheme == "" || u.Host == "" {
		return origin{}, errors.New("tsapi: base URL must include a scheme and host")
	}
	return originOf(u), nil
}

func (o origin) equal(other origin) bool {
	return o.scheme == other.scheme && o.hostPort == other.hostPort && o.userinfo == other.userinfo
}

// String renders the origin for logs and errors. Embedded credentials are
// reported as present but never printed.
func (o origin) String() string {
	if o.scheme == "" && o.hostPort == "" {
		return "(unset)"
	}
	if o.userinfo != "" {
		return o.scheme + "://<redacted>@" + o.hostPort
	}
	return o.scheme + "://" + o.hostPort
}

// redirectPolicy returns the http.Client.CheckRedirect shared by EVERY
// credential-bearing client this package builds: the API-key client, the OAuth
// client-credentials client, and the workload-identity token-fetch client. It
// exists once so those paths cannot drift apart (#466, #467).
//
// The rule is a flat refusal, not an allowlist: a redirect is followed only
// when its target is the exact same origin the request started on. Go's
// http.Client already strips a cross-origin Authorization header for us, but
// that is not enough on its own — a custom transport can re-add it (that was
// #466), and a 307/308 replays the request BODY regardless, which is how a
// projected service-account JWT could be handed to a second origin (#467).
//
// logger may be nil. When set, a refusal is logged with the two origins and a
// stable class only — never the credential, the path/query, or userinfo.
func redirectPolicy(logger *slog.Logger) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		// Compare against the ORIGINAL request's origin, not the previous hop, so
		// a chain cannot walk the credential away one same-origin-looking step at
		// a time.
		from := originOf(via[0].URL)
		to := originOf(req.URL)
		if !from.equal(to) {
			err := &CrossOriginRedirectError{From: from.String(), To: to.String()}
			if logger != nil {
				logger.Error("refused a cross-origin redirect on a credential-bearing request",
					"error_class", errClassRedirectRefused,
					"from_origin", err.From,
					"to_origin", err.To,
					"endpoint", endpointLabel(via[0].URL.Path))
			}
			return err
		}
		if len(via) >= maxRedirects {
			return ErrTooManyRedirects
		}
		return nil
	}
}
