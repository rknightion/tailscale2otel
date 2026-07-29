// Processor is the single emission path for tsrecorder Kubernetes-audit
// objects: it converts a decoded Object (Task 1) into bounded OTEL metrics
// (attribute values drawn only from classify.go's Normalize*/Classify*
// functions, Task 2) plus one enriched log record, and converts a decoded
// CastHeader (Task 3) into its own session-start signal.
package k8saudit

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

const schemaDriftWarningLimit = 128

// Processor converts decoded k8saudit objects into OTEL metrics and log
// records. It is safe for concurrent use.
type Processor struct {
	logger          *slog.Logger
	now             func() time.Time
	emitCommandText bool

	mu                  sync.Mutex
	unknownSchemaValues map[string]struct{}
}

// Option configures a Processor at construction time.
type Option func(*Processor)

// WithLogger attaches the logger used for bounded schema-drift warnings. A nil
// logger (the default) suppresses those warnings while retaining the
// schema_drift metric itself.
func WithLogger(logger *slog.Logger) Option {
	return func(p *Processor) { p.logger = logger }
}

// WithClock overrides the time source used for each log record's
// ObservedTimestamp (when this process saw the object), as distinct from its
// Timestamp (when the request/session actually happened, taken from the
// object itself). Intended for deterministic tests; nil retains time.Now.
func WithClock(now func() time.Time) Option {
	return func(p *Processor) {
		if now != nil {
			p.now = now
		}
	}
}

// WithEmitCommandText controls whether the raw exec/session command text is
// attached to log records. Defaults to true — Product Decision #2 in the plan
// ships raw command text by default and makes it redactable via the
// command_text pii_filter category (wired one layer up, at the app/config
// boundary that owns *Processor construction) rather than off by default. The
// bounded command_class attribute is NEVER affected by this option: it is not
// PII and must survive redaction.
func WithEmitCommandText(emit bool) Option {
	return func(p *Processor) { p.emitCommandText = emit }
}

// NewProcessor returns a k8saudit Processor. With no options, command text is
// emitted (see WithEmitCommandText) and no schema-drift warnings are logged.
func NewProcessor(opts ...Option) *Processor {
	p := &Processor{
		now:                 time.Now,
		emitCommandText:     true,
		unknownSchemaValues: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// rbacProbeResources is the closed set of "what am I allowed to do" resources
// that make a request an RBAC probe rather than an ordinary read. Every member
// is also present in classify.go's validResources, so NormalizeResource never
// maps one of these away before this check runs.
var rbacProbeResources = map[string]bool{
	"selfsubjectrulesreviews":  true,
	"selfsubjectaccessreviews": true,
	"selfsubjectreviews":       true,
	"subjectaccessreviews":     true,
}

// readVerbs are the verbs that make a sensitive-resource request a read worth
// its own counter (as opposed to a write, which is already covered by
// mutations).
var readVerbs = map[string]bool{"get": true, "list": true, "watch": true}

// mutationVerbs are the verbs that make a request a mutation.
var mutationVerbs = map[string]bool{
	"create": true, "update": true, "patch": true, "delete": true, "deletecollection": true,
}

// execSubresources are the subresources that make a request an interactive
// exec session, as opposed to an ordinary API call against a pod.
var execSubresources = map[string]bool{"exec": true, "attach": true, "portforward": true}

// Process converts a single decoded Object into OTEL signals: the
// tailscale.k8s.api.requests counter (always), zero or more of
// sensitive_reads / mutations / rbac_probes / exec_sessions, and exactly one
// tailscale.k8s.api_request log record. An Object whose event type is not
// EventTypeKubernetesAPIRequest is schema drift, not a request to guess at —
// it is counted and dropped without touching any request-shaped signal.
func (p *Processor) Process(o Object, e telemetry.Emitter) {
	if o.Event.Type != EventTypeKubernetesAPIRequest {
		p.observeSchemaDrift("type", o.Event.Type, e)
		return
	}

	k := o.Event.Kubernetes

	verb := NormalizeVerb(k.Verb)
	resource := NormalizeResource(k.Resource)
	subresource := NormalizeSubresource(k.Subresource)
	apiGroup := NormalizeAPIGroup(k.APIGroup)
	userAgent := NormalizeUserAgent(o.Event.UserAgent)
	// Namespace and user are deliberately NOT run through a classify.go
	// normalizer: neither has one (there is no NormalizeNamespace, and a real
	// tailnet identity is not a client-controlled string to bound the way a
	// verb/resource/user agent is — see attrUser's doc comment in catalog.go).
	// Namespace count is bounded by the cluster the operator runs, not by an
	// arbitrary remote client; this mirrors catalog.go's own grouping of
	// attrNamespace and attrUser as the two exceptions to the "normalized
	// admit-set" rule for this package's metric attributes.
	namespace := k.Namespace
	user := o.Event.Source.NodeUser
	recorder := o.Event.Destination.Node

	metricAttrs := telemetry.Attrs{
		attrVerb:        verb,
		attrResource:    resource,
		attrSubresource: subresource,
		attrAPIGroup:    apiGroup,
		attrNamespace:   namespace,
		attrUserAgent:   userAgent,
		attrUser:        user,
		attrRecorder:    recorder,
	}
	e.Counter(docAPIRequests.Name, docAPIRequests.Unit, docAPIRequests.Description, 1, metricAttrs)

	if IsSensitive(resource) && readVerbs[verb] {
		e.Counter(docSensitiveReads.Name, docSensitiveReads.Unit, docSensitiveReads.Description, 1, telemetry.Attrs{
			attrResource:  resource,
			attrNamespace: namespace,
			attrUser:      user,
			attrUserAgent: userAgent,
		})
	}

	if mutationVerbs[verb] {
		e.Counter(docMutations.Name, docMutations.Unit, docMutations.Description, 1, telemetry.Attrs{
			attrVerb:      verb,
			attrResource:  resource,
			attrNamespace: namespace,
			attrUser:      user,
		})
	}

	if rbacProbeResources[resource] {
		e.Counter(docRBACProbes.Name, docRBACProbes.Unit, docRBACProbes.Description, 1, telemetry.Attrs{
			attrResource:  resource,
			attrNamespace: namespace,
			attrUser:      user,
		})
	}

	var commandClass string
	if execSubresources[subresource] {
		commandClass = ClassifyCommand(ExecCommand(o))
		e.Counter(docExecSessions.Name, docExecSessions.Unit, docExecSessions.Description, 1, telemetry.Attrs{
			attrNamespace:    namespace,
			attrCommandClass: commandClass,
			attrSessionType:  subresource,
			attrUser:         user,
		})
	} else {
		// Keep the log's command_class attribute meaningful even for a
		// non-exec request (e.g. a plain "get pods" call): "none" rather than
		// leaving it unset lets a query group by command_class without a
		// separate null-handling case.
		commandClass = ClassifyCommand(nil)
	}

	// Pod/container are only meaningful when the target is a pod. Deriving
	// them from Kubernetes.Name/queryParameters (rather than parsing Path)
	// keeps this off the one field this package must never touch for
	// emission — see the comment on attrPath below.
	var pod, container string
	if strings.EqualFold(resource, "pods") {
		pod = k.Name
	}
	if cs := o.Event.Request.QueryParameters["container"]; len(cs) > 0 {
		container = strings.Join(cs, ",")
	}

	attrs := telemetry.Attrs{
		attrVerb:          verb,
		attrResource:      resource,
		attrSubresource:   subresource,
		attrAPIGroup:      apiGroup,
		attrNamespace:     namespace,
		attrObjectName:    k.Name,
		attrLabelSelector: k.LabelSelector,
		attrFieldSelector: k.FieldSelector,
		attrUserAgent:     userAgent,
		attrUser:          user,
		attrSrcNode:       o.Event.Source.Node,
		attrSrcNodeID:     o.Event.Source.NodeID,
		attrRecorder:      recorder,
		attrCommandClass:  commandClass,
	}
	// SECURITY: attrPath MUST be sourced from Kubernetes.Path, which is the
	// query-free request path. o.Event.Request.Path carries the FULL query
	// string, including URL-encoded exec command text
	// (".../exec?command=sh&command=-c&command=..."), and must NEVER be
	// emitted on any signal. Do not "simplify" this by reusing Request.Path.
	attrs[attrPath] = k.Path
	if pod != "" {
		attrs[attrPod] = pod
	}
	if container != "" {
		attrs[attrContainer] = container
	}
	if p.emitCommandText {
		if argv := ExecCommand(o); len(argv) > 0 {
			attrs[attrCommand] = strings.Join(argv, " ")
		}
	}

	e.LogEvent(telemetry.Event{
		Name:              docAPIRequestLog.Name,
		Body:              requestSummary(verb, resource, subresource, namespace),
		Severity:          telemetry.SeverityInfo,
		Timestamp:         EventTimestamp(o),
		ObservedTimestamp: p.now(),
		Attrs:             attrs,
	})
}

// ProcessSession converts a decoded .cast header into a session-start signal:
// the tailscale.k8s.session.started counter plus one tailscale.k8s.session log
// record. Session completeness is never observable (see cast.go's package
// doc), so this fires exactly once, at session start.
func (p *Processor) ProcessSession(h CastHeader, e telemetry.Emitter) {
	var namespace, pod, container string
	sessionType := "none"
	if h.Kubernetes != nil {
		namespace = h.Kubernetes.Namespace
		pod = h.Kubernetes.PodName
		container = h.Kubernetes.Container
		// SessionType shares its vocabulary with subresource (exec/attach/
		// portforward), so NormalizeSubresource bounds it without needing a
		// separate normalizer.
		sessionType = NormalizeSubresource(h.Kubernetes.SessionType)
	}

	argv := strings.Fields(h.Command)
	commandClass := ClassifyCommand(argv)
	user := h.SrcNodeUser

	e.Counter(docSessionStarted.Name, docSessionStarted.Unit, docSessionStarted.Description, 1, telemetry.Attrs{
		attrSessionType:  sessionType,
		attrNamespace:    namespace,
		attrCommandClass: commandClass,
		attrUser:         user,
	})

	attrs := telemetry.Attrs{
		attrSessionType:  sessionType,
		attrNamespace:    namespace,
		attrCommandClass: commandClass,
		attrUser:         user,
		attrSrcNode:      h.SrcNode,
		// The recorder comes from the header's dstNode, which upstream's
		// CastHeader does not declare but every real header carries (see
		// cast.go). Without it a session log could not be tied back to the
		// recording that holds it.
		attrRecorder: h.DstNode,
	}
	if pod != "" {
		attrs[attrPod] = pod
	}
	if container != "" {
		attrs[attrContainer] = container
	}
	if p.emitCommandText && len(argv) > 0 {
		attrs[attrCommand] = h.Command
	}

	e.LogEvent(telemetry.Event{
		Name:              docSessionLog.Name,
		Body:              sessionSummary(sessionType, namespace),
		Severity:          telemetry.SeverityInfo,
		Timestamp:         time.Unix(h.Timestamp, 0).UTC(),
		ObservedTimestamp: p.now(),
		Attrs:             attrs,
	})
}

// observeSchemaDrift records that field carried a value outside this
// package's understanding of the tsrecorder wire schema (today, only
// event.type is checked — see Process). It logs at most once per unique
// (field, value) pair, and only ever logs a digest of the value, never the
// value itself, mirroring internal/audit's schema-drift warning.
func (p *Processor) observeSchemaDrift(field, value string, e telemetry.Emitter) {
	e.Counter(docSchemaDrift.Name, docSchemaDrift.Unit, docSchemaDrift.Description, 1, telemetry.Attrs{
		"field":  field,
		"status": "unknown",
	})
	p.warnUnknownSchemaValue(field, value)
}

func (p *Processor) warnUnknownSchemaValue(field, value string) {
	if p.logger == nil {
		return
	}
	key := field + "\x00" + value
	p.mu.Lock()
	if len(p.unknownSchemaValues) >= schemaDriftWarningLimit {
		p.mu.Unlock()
		return
	}
	if _, seen := p.unknownSchemaValues[key]; seen {
		p.mu.Unlock()
		return
	}
	p.unknownSchemaValues[key] = struct{}{}
	p.mu.Unlock()

	p.logger.Warn("unrecognized k8saudit schema value", "field", field, "digest", schemaValueDigest(value))
}

// schemaValueDigest turns an unknown wire value into a short, non-reversible
// fingerprint so the same value can be recognized across log lines without
// ever being logged in the clear.
func schemaValueDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:6])
}

// requestSummary is the human-readable log body for an API-request record. It
// carries only bounded/derived fields (never the raw path, object name, or
// command text) so the body itself cannot leak anything the attribute
// contract forbids.
func requestSummary(verb, resource, subresource, namespace string) string {
	target := resource
	if subresource != "" && subresource != "none" {
		target = resource + "/" + subresource
	}
	if namespace != "" {
		return verb + " " + target + " in " + namespace
	}
	return verb + " " + target
}

// sessionSummary is the human-readable log body for a session-start record.
func sessionSummary(sessionType, namespace string) string {
	if namespace != "" {
		return sessionType + " session started in " + namespace
	}
	return sessionType + " session started"
}
