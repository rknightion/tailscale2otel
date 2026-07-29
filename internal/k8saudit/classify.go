package k8saudit

import "strings"

// AttrOther is the sink value for anything outside an admit-set. Every
// normalizer funnels unknown input here so a client-controlled string can never
// mint a new metric series.
const AttrOther = "other"

// validCommandClasses is the closed vocabulary for the command_class label.
var validCommandClasses = map[string]bool{
	"interactive_shell": true,
	"recon":             true,
	"package_mgmt":      true,
	"net_tool":          true,
	"file_transfer":     true,
	"credential_read":   true,
	"none":              true,
	AttrOther:           true,
}

// commandPatterns maps a class to the tokens that imply it. Order matters:
// the first class whose token appears wins, and the ordering below is by
// investigative interest — a command that both reads a token and starts a shell
// is more interesting as credential_read.
var commandPatterns = []struct {
	class  string
	tokens []string
}{
	{"credential_read", []string{"serviceaccount/token", "/var/run/secrets", "id_rsa", ".kube/config", "printenv", "/proc/self/environ"}},
	{"recon", []string{"whoami", "uname", "hostname", "ifconfig", "ip a", "netstat", "ps aux", "mount", "env", " id;", " id ", ";id"}},
	{"package_mgmt", []string{"apt-get", "apt ", "apk ", "yum ", "dnf ", "pip install", "npm install", "gem install"}},
	{"net_tool", []string{"curl", "wget", "nc ", "ncat", "nmap", "ssh ", "dig ", "nslookup"}},
	{"file_transfer", []string{"tar ", "scp ", "rsync", "base64", "dd "}},
	{"interactive_shell", []string{"bash", "/bin/sh", "ash", "zsh", "/bin/dash"}},
}

// shellBinaries are argv[0] values that mean "a shell was started" when nothing
// more specific matched.
var shellBinaries = map[string]bool{
	"sh": true, "bash": true, "ash": true, "zsh": true, "dash": true, "fish": true,
}

// ClassifyCommand reduces an exec argv to one bounded class. It deliberately
// never returns the command text: the text is a log attribute, the class is the
// metric label, and only the class is safe to put in a series.
func ClassifyCommand(argv []string) string {
	if len(argv) == 0 {
		return "none"
	}
	joined := strings.ToLower(strings.Join(argv, " "))
	for _, p := range commandPatterns {
		for _, tok := range p.tokens {
			if strings.Contains(joined, tok) {
				return p.class
			}
		}
	}
	base := argv[0]
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if shellBinaries[strings.ToLower(base)] {
		return "interactive_shell"
	}
	return AttrOther
}

// ExecCommand pulls the argv out of the exec query parameters. It reads
// queryParameters rather than Request.Path on purpose: Path carries the whole
// query string and must never be touched for emission.
func ExecCommand(o Object) []string {
	if o.Event.Request.QueryParameters == nil {
		return nil
	}
	return o.Event.Request.QueryParameters["command"]
}

var validVerbs = map[string]bool{
	"get": true, "list": true, "watch": true, "create": true, "update": true,
	"patch": true, "delete": true, "deletecollection": true, "proxy": true,
	"connect": true, "head": true, "post": true, "put": true, "options": true,
}

// NormalizeVerb bounds the Kubernetes verb. For non-resource requests upstream
// puts the lowercased HTTP method here, which is why the HTTP verbs are in the
// admit-set alongside the Kubernetes ones.
func NormalizeVerb(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "none"
	}
	if validVerbs[v] {
		return v
	}
	return AttrOther
}

// validResources is the built-in Kubernetes resource vocabulary worth labeling.
// A CRD's plural is deliberately NOT admitted: cluster operators install
// arbitrary CRDs, so admitting them would make the label unbounded.
var validResources = map[string]bool{
	"pods": true, "services": true, "endpoints": true, "namespaces": true,
	"nodes": true, "events": true, "secrets": true, "configmaps": true,
	"serviceaccounts": true, "persistentvolumes": true, "persistentvolumeclaims": true,
	"deployments": true, "replicasets": true, "statefulsets": true, "daemonsets": true,
	"jobs": true, "cronjobs": true, "ingresses": true, "networkpolicies": true,
	"endpointslices": true, "customresourcedefinitions": true,
	"roles": true, "rolebindings": true, "clusterroles": true, "clusterrolebindings": true,
	"selfsubjectrulesreviews": true, "selfsubjectaccessreviews": true,
	"selfsubjectreviews": true, "subjectaccessreviews": true, "tokenreviews": true,
	"certificatesigningrequests": true, "leases": true, "podmetrics": true,
}

func NormalizeResource(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	if r == "" {
		return "none"
	}
	if validResources[r] {
		return r
	}
	return AttrOther
}

var validAPIGroups = map[string]bool{
	"apps": true, "batch": true, "authorization.k8s.io": true,
	"authentication.k8s.io": true, "rbac.authorization.k8s.io": true,
	"networking.k8s.io": true, "discovery.k8s.io": true, "metrics.k8s.io": true,
	"apiextensions.k8s.io": true, "storage.k8s.io": true, "policy": true,
	"coordination.k8s.io": true, "certificates.k8s.io": true, "autoscaling": true,
}

// NormalizeAPIGroup maps the empty group to "core" — the conventional name for
// the legacy /api group — and bounds everything else.
func NormalizeAPIGroup(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	if g == "" {
		return "core"
	}
	if validAPIGroups[g] {
		return g
	}
	return AttrOther
}

var validSubresources = map[string]bool{
	"exec": true, "attach": true, "portforward": true, "log": true, "status": true,
	"proxy": true, "binding": true, "eviction": true, "scale": true, "token": true,
	"finalize": true, "approval": true,
}

func NormalizeSubresource(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "none"
	}
	if validSubresources[s] {
		return s
	}
	return AttrOther
}

var validUserAgents = map[string]bool{
	"kubectl": true, "freelens": true, "lens": true, "node-fetch": true,
	"helm": true, "k9s": true, "argocd": true, "terraform": true,
	"kubernetes-client": true, "client-go": true, "python-requests": true,
	"go-http-client": true, "curl": true, "octant": true,
}

// NormalizeUserAgent reduces a full UA string to its product token. The version,
// platform and build hash are dropped: they are the unbounded part, and
// "kubectl/v1.36.2" vs "kubectl/v1.36.3" is not a distinction worth a series.
func NormalizeUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "unknown"
	}
	product := ua
	if i := strings.IndexAny(product, "/ "); i >= 0 {
		product = product[:i]
	}
	product = strings.ToLower(product)
	if validUserAgents[product] {
		return product
	}
	return AttrOther
}

// sensitiveResources are the resources whose mere READ is investigative signal.
var sensitiveResources = map[string]bool{
	"secrets": true, "serviceaccounts": true, "roles": true, "rolebindings": true,
	"clusterroles": true, "clusterrolebindings": true, "tokenreviews": true,
	"certificatesigningrequests": true,
}

// IsSensitive reports whether a resource read is worth its own counter.
func IsSensitive(resource string) bool {
	return sensitiveResources[strings.ToLower(strings.TrimSpace(resource))]
}
