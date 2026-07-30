package k8saudit

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation: name, unit, instrument, description, and the
// attribute keys carried. The emit sites (processor.go) reference these
// descriptors so a description/unit cannot drift from what is documented; the
// doc generator (tools/metricscatalog, via internal/catalog) renders them into
// docs/metrics.md, and catalog_test.go asserts the closed high-cardinality
// exclusion these declarations promise.
//
// IMPORTANT: the source schema carries no response status, latency, or byte
// count (see the plan's Evidence Base). Every metric below counts ATTEMPTS —
// requests made, sessions started — never outcomes. Do not describe any of
// them as counting successes, failures, denials, or errors.
const groupK8sAudit = "Kubernetes audit"

// Attribute keys. Every value here is frozen — other packages in this feature
// (the processor, and later the app-layer wiring) code against these exact
// strings, so renaming one is a breaking change across the feature.
const (
	// Metric-safe attributes: each is bounded to a closed admit-set by
	// classify.go before it ever reaches a metric label.
	attrVerb         = "tailscale.k8s.verb"
	attrResource     = "tailscale.k8s.resource"
	attrSubresource  = "tailscale.k8s.subresource"
	attrAPIGroup     = "tailscale.k8s.api_group"
	attrNamespace    = "tailscale.k8s.namespace"
	attrUserAgent    = "tailscale.k8s.user_agent"
	attrCommandClass = "tailscale.k8s.command_class"
	attrSessionType  = "tailscale.k8s.session_type"
	// attrUser is bounded by tailnet size (a real identity, not client-supplied
	// free text), unlike the other client-controlled fields in this package.
	attrUser     = "tailscale.k8s.user"
	attrRecorder = "tailscale.k8s.recorder"

	// Log-only attributes: high cardinality (object names, selectors, raw
	// command text, node identifiers) or otherwise unsafe for a metric label.
	// TestNoHighCardinalityAttributeOnAnyMetric guards these off Catalog().
	attrObjectName    = "tailscale.k8s.object_name"
	attrPath          = "tailscale.k8s.path"
	attrLabelSelector = "tailscale.k8s.label_selector"
	attrFieldSelector = "tailscale.k8s.field_selector"
	// attrCommand is redactable via pii_filter; the bounded attrCommandClass
	// ships regardless and is never redacted.
	attrCommand   = "tailscale.k8s.command"
	attrPod       = "tailscale.k8s.pod"
	attrContainer = "tailscale.k8s.container"
	attrSrcNode   = "tailscale.k8s.src_node"
	attrSrcNodeID = "tailscale.k8s.src_node_id"
)

var (
	docAPIRequests = metricdoc.Metric{
		Name:        "tailscale.k8s.api.requests",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Kubernetes API requests proxied through the Tailscale API-server proxy, by verb, resource, subresource, API group, namespace, user agent, user, and recorder node. Counts attempts only — the source schema carries no response status.",
		Attributes:  []string{attrVerb, attrResource, attrSubresource, attrAPIGroup, attrNamespace, attrUserAgent, attrUser, attrRecorder},
		Group:       groupK8sAudit,
	}

	docSensitiveReads = metricdoc.Metric{
		Name:        "tailscale.k8s.api.sensitive_reads",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Read (get/list/watch) requests against sensitive resources — secrets, service accounts, RBAC objects, token/CSR reviews — by resource, namespace, user, and user agent. A read being attempted, not that it succeeded or returned data.",
		Attributes:  []string{attrResource, attrNamespace, attrUser, attrUserAgent},
		Group:       groupK8sAudit,
	}

	docExecSessions = metricdoc.Metric{
		Name:        "tailscale.k8s.api.exec_sessions",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Interactive exec/attach/portforward requests against a pod, by namespace, bounded command class, session type, and user. command_class summarizes what the command does (interactive_shell, recon, package_mgmt, ...); the raw command text is a log attribute only.",
		Attributes:  []string{attrNamespace, attrCommandClass, attrSessionType, attrUser},
		Group:       groupK8sAudit,
	}

	docMutations = metricdoc.Metric{
		Name:        "tailscale.k8s.api.mutations",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Mutating Kubernetes API requests (create/update/patch/delete/deletecollection) proxied through Tailscale, by verb, resource, namespace, and user. Counts the request being made, not that the mutation was admitted or persisted.",
		Attributes:  []string{attrVerb, attrResource, attrNamespace, attrUser},
		Group:       groupK8sAudit,
	}

	docRBACProbes = metricdoc.Metric{
		Name:        "tailscale.k8s.api.rbac_probes",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "SelfSubjectAccessReview/SelfSubjectRulesReview/SubjectAccessReview/TokenReview requests — a client asking what it or another identity is allowed to do — by resource, namespace, and user. A probe being made, not its answer.",
		Attributes:  []string{attrResource, attrNamespace, attrUser},
		Group:       groupK8sAudit,
	}

	docSessionStarted = metricdoc.Metric{
		Name:        "tailscale.k8s.session.started",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Recorded terminal sessions started against a Kubernetes pod (from the tsrecorder .cast header), by session type, namespace, bounded command class, and user. Session completeness cannot be observed — there is no documented end-of-recording signal — so this counts starts only.",
		Attributes:  []string{attrSessionType, attrNamespace, attrCommandClass, attrUser},
		Group:       groupK8sAudit,
	}

	docSchemaDrift = metricdoc.Metric{
		Name:        "tailscale.k8s.schema_drift",
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Kubernetes-audit schema vocabulary observations, by field and whether its value is known to this collector version. The feature is explicitly beta and unversioned upstream, so an unexpected event type or enum value increments this rather than being silently dropped or guessed at.",
		Attributes:  []string{"field", "status"},
		Group:       groupK8sAudit,
	}

	docAPIRequestLog = metricdoc.LogEvent{
		Name:        "tailscale.k8s.api_request",
		Severity:    "INFO",
		Description: "Per proxied Kubernetes API request: verb, target resource/object, namespace, requesting user and node, user agent, and (when the request is an exec/attach/portforward) the classified and, unless redacted, raw command. tailscale.k8s.path is the query-free Kubernetes request path — the raw request path/query string is never emitted.",
		Attributes: []string{
			attrVerb, attrResource, attrSubresource, attrAPIGroup, attrNamespace,
			attrObjectName, attrPath, attrLabelSelector, attrFieldSelector,
			attrUserAgent, attrUser, attrSrcNode, attrSrcNodeID, attrRecorder,
			attrCommand, attrCommandClass, attrPod, attrContainer,
		},
		Group: groupK8sAudit,
	}

	docSessionLog = metricdoc.LogEvent{
		Name:        "tailscale.k8s.session",
		Severity:    "INFO",
		Description: "Per recorded terminal session start against a Kubernetes pod: session type, target pod/container/namespace, the classified and, unless redacted, raw launch command, requesting user and node, and the recorder node. Session completeness is not observable, so this fires once at session start only.",
		Attributes: []string{
			attrSessionType, attrNamespace, attrPod, attrContainer,
			attrCommand, attrCommandClass, attrUser, attrSrcNode, attrRecorder,
		},
		Group: groupK8sAudit,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docAPIRequests, docSensitiveReads, docExecSessions, docMutations,
		docRBACProbes, docSessionStarted, docSchemaDrift,
	}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docAPIRequestLog, docSessionLog}
}
