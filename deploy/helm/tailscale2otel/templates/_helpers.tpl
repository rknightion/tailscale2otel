{{- define "tailscale2otel.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "tailscale2otel.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "tailscale2otel.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "tailscale2otel.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "tailscale2otel.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tailscale2otel.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
The rendered container image reference (#349). Digest wins deterministically over a
tag: when image.digest is set, image.tag and .Chart.AppVersion are IGNORED entirely —
the rendered reference is always `repository@digest`, NEVER `repository:tag@digest`.

The pattern check is a SECOND layer, not the only one: values.schema.json's
`@schema pattern:^sha256:[a-f0-9]{64}$` annotation on image.digest rejects a
malformed digest for any schema-validated install (helm install/upgrade/template,
which validate by default) before this template is ever reached. This `fail`
exists for the input that check cannot see: `helm template
--skip-schema-validation`, or any other schema-less path that reaches templating
directly. Both layers must independently reject the same bad input, since neither
implies the other runs.
*/}}
{{- define "tailscale2otel.image" -}}
{{- if .Values.image.digest -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" .Values.image.digest) -}}
{{- fail (printf "image.digest must match ^sha256:[a-f0-9]{64}$ (got %q): not a valid immutable image digest. Fix the value, or clear image.digest to pin by image.tag instead." .Values.image.digest) -}}
{{- end -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}
{{- end -}}

{{/*
Probe timing/threshold guards (#350). Same two-layer split as
terminationGracePeriodSeconds (#332):

  - values.schema.json's `minimum`/`enum` annotations catch a NUMERIC value
    outside range (periodSeconds: 0, successThreshold: 2 on liveness/startup) and
    fire FIRST on any schema-validated install, so this helper never sees those.
  - This helper is the second layer. It catches inputs the schema does NOT
    reject: `null` (Helm/OpenAPIv3 `minimum`/`enum` do not fire on a null value —
    `--set probes.liveness.periodSeconds=null` passes schema validation and
    reaches the template as an empty value, which `int` below would otherwise
    silently coerce to 0), and any schema-less path
    (`--skip-schema-validation`) that reaches templating with no schema check
    at all.

Args: (dict "path" <dotted values path for messages, e.g. "probes.liveness">
       "block" <the probe values map, e.g. .Values.probes.liveness>
       "requireSuccessOne" <bool — true for liveness/startup, false for readiness>)
*/}}
{{- define "tailscale2otel.validateProbeBlock" -}}
{{- $path := .path -}}
{{- $b := .block -}}
{{- if lt (int $b.initialDelaySeconds) 0 -}}
{{- fail (printf "%s.initialDelaySeconds must be at least 0 (got %v)" $path $b.initialDelaySeconds) -}}
{{- end -}}
{{- if lt (int $b.periodSeconds) 1 -}}
{{- fail (printf "%s.periodSeconds must be at least 1 (got %v): Kubernetes rejects a probe period below 1 second" $path $b.periodSeconds) -}}
{{- end -}}
{{- if lt (int $b.timeoutSeconds) 1 -}}
{{- fail (printf "%s.timeoutSeconds must be at least 1 (got %v): Kubernetes rejects a probe timeout below 1 second" $path $b.timeoutSeconds) -}}
{{- end -}}
{{- if lt (int $b.failureThreshold) 1 -}}
{{- fail (printf "%s.failureThreshold must be at least 1 (got %v)" $path $b.failureThreshold) -}}
{{- end -}}
{{- if .requireSuccessOne -}}
{{- if ne (int $b.successThreshold) 1 -}}
{{- fail (printf "%s.successThreshold must be 1 (got %v): Kubernetes rejects any other value for a liveness or startup probe" $path $b.successThreshold) -}}
{{- end -}}
{{- else -}}
{{- if lt (int $b.successThreshold) 1 -}}
{{- fail (printf "%s.successThreshold must be at least 1 (got %v)" $path $b.successThreshold) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "tailscale2otel.validateProbes" -}}
{{- if .Values.probes.liveness.enabled -}}
{{- include "tailscale2otel.validateProbeBlock" (dict "path" "probes.liveness" "block" .Values.probes.liveness "requireSuccessOne" true) -}}
{{- end -}}
{{- if .Values.probes.readiness.enabled -}}
{{- include "tailscale2otel.validateProbeBlock" (dict "path" "probes.readiness" "block" .Values.probes.readiness "requireSuccessOne" false) -}}
{{- end -}}
{{- if .Values.probes.startup.enabled -}}
{{- include "tailscale2otel.validateProbeBlock" (dict "path" "probes.startup" "block" .Values.probes.startup "requireSuccessOne" true) -}}
{{- end -}}
{{- end -}}

{{- define "tailscale2otel.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "tailscale2otel.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "tailscale2otel.secretName" -}}
{{- if .Values.existingSecret -}}
{{- .Values.existingSecret -}}
{{- else -}}
{{- include "tailscale2otel.fullname" . -}}
{{- end -}}
{{- end -}}

{{/*
The rendered config.yaml. Always sourced from .Values.config so there is a single
source of truth and no chart<->config drift. The full default config map lives in
values.yaml under `config:`; Helm deep-merges maps, so single-key overrides
(e.g. --set config.log_level=debug) keep working. The intended path is to leave the
credential fields empty here and inject them as TS2OTEL_* environment variables from
the envFrom Secret, which override the corresponding fields in this file at runtime.
A credential set inline anyway does NOT land in a ConfigMap: see
tailscale2otel.configStoresSecret below.
*/}}
{{- define "tailscale2otel.config" -}}
{{ .Values.config | toYaml }}
{{- end -}}

{{/*
Name of the Secret holding the rendered config.yaml when the config is stored in a
Secret rather than the ConfigMap. Unchanged from the multi-tailnet behaviour that
predates the credential auto-routing, so existing multi-tailnet installs keep their
object name.
*/}}
{{- define "tailscale2otel.configSecretName" -}}
{{- printf "%s-config" (include "tailscale2otel.fullname" .) -}}
{{- end -}}

{{/*
The authoritative list of credential-bearing keys inside .Values.config, one dotted
path per line. A non-empty value at ANY of these moves the whole rendered config.yaml
out of the ConfigMap and into a Secret (SEC-07 / #470): a ConfigMap is readable by
anyone with namespace `get configmaps`, which in practice is granted far more widely
than `get secrets`.

Deliberately NOT listed:
  * identity halves of credential pairs — tailscale.auth.oauth.client_id,
    tailscale.auth.workload_identity.client_id, otlp.grafana_cloud.instance_id,
    profiling.pyroscope.basic_auth_user. They are identifiers, not secrets, and
    listing them would push credential-free installs off the ConfigMap path.
  * every *_file key (client_secret_file, apikey_file, token_file, secret_file,
    basic_auth_password_file, object-store access_key_id_file /
    secret_access_key_file / session_token_file, tls cert/key/ca files) — those
    are PATHS to material mounted from elsewhere, so the config file itself
    stays credential-free.
otlp.headers IS listed (as a whole map): raw OTLP headers are where an Authorization
header goes, so any non-empty value there is treated as a credential.

This list is GUARDED: internal/config's TestChartCredentialPathsCoversEverySecretField
derives every Secret-typed field from the Config struct and fails when one is missing
here (or when an entry here names a field that no longer exists). It has to be, because
the list was hand-maintained and had silently fallen four entries behind — the GeoIP
download license key and all three k8s_audit object-store credentials were being
rendered into a plainly readable ConfigMap. Adding a Secret field to Config now fails
that test until this list is updated.
Structural carriers that are lists — config.tailnets[] and
config.collectors.node_metrics.targets[] — are handled separately below.
*/}}
{{- define "tailscale2otel.credentialPaths" -}}
headscale.api_key
tailscale.auth.oauth.client_secret
tailscale.auth.apikey
otlp.grafana_cloud.token
otlp.headers
otlp.metrics.headers
otlp.logs.headers
otlp.traces.headers
collectors.flowlogs.objectstore.access_key_id
collectors.flowlogs.objectstore.secret_access_key
collectors.flowlogs.objectstore.session_token
collectors.auditlogs.objectstore.access_key_id
collectors.auditlogs.objectstore.secret_access_key
collectors.auditlogs.objectstore.session_token
collectors.k8s_audit.objectstore.access_key_id
collectors.k8s_audit.objectstore.secret_access_key
collectors.k8s_audit.objectstore.session_token
streaming.token
webhook.secret
prometheus.auth.token
admin.auth.token
profiling.pyroscope.basic_auth_password
profiling.pyroscope.headers
grafana_annotations.token
enrichment.geoip.download.license_key
{{- end -}}

{{/*
Which credential-bearing keys are actually set inline under `config:`, as a
comma-separated list of dotted paths (empty when none). KEY NAMES ONLY — the values
are never included, so this is safe to put in a `fail` message.
*/}}
{{- define "tailscale2otel.inlineCredentialKeys" -}}
{{- $found := list -}}
{{- range $path := splitList "\n" (include "tailscale2otel.credentialPaths" .) -}}
  {{- $p := trim $path -}}
  {{- if $p -}}
    {{- $node := $.Values.config -}}
    {{- range $seg := splitList "." $p -}}
      {{- if kindIs "map" $node -}}
        {{- $node = index $node $seg -}}
      {{- else -}}
        {{- $node = "" -}}
      {{- end -}}
    {{- end -}}
    {{- if $node -}}
      {{- $found = append $found (printf "config.%s" $p) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- /* Multi-tailnet entries carry their own inline auth; the list is file-only
       (no TS2OTEL_* env path exists for it), so any entry is credential-bearing. */ -}}
{{- if $.Values.config.tailnets -}}
  {{- $found = append $found "config.tailnets[]" -}}
{{- end -}}
{{- /* Receiver route lists contain per-route credentials and must be inspected
       explicitly because dotted-path traversal cannot cross list elements. */ -}}
{{- range $r := ($.Values.config.streaming.routes | default list) -}}
  {{- if and (kindIs "map" $r) (index $r "token") -}}
    {{- $found = append $found "config.streaming.routes[].token" -}}
  {{- end -}}
{{- end -}}
{{- range $r := ($.Values.config.webhook.routes | default list) -}}
  {{- if and (kindIs "map" $r) (index $r "secret") -}}
    {{- $found = append $found "config.webhook.routes[].secret" -}}
  {{- end -}}
{{- end -}}
{{- /* Node-metrics scrape targets may carry a per-target bearer token or headers. */ -}}
{{- $nm := $.Values.config.collectors -}}
{{- if kindIs "map" $nm -}}{{- $nm = index $nm "node_metrics" -}}{{- end -}}
{{- if kindIs "map" $nm -}}
  {{- range $t := (index $nm "targets" | default list) -}}
    {{- if kindIs "map" $t -}}
      {{- if index $t "bearer_token" -}}
        {{- $found = append $found "config.collectors.node_metrics.targets[].bearer_token" -}}
      {{- end -}}
      {{- if index $t "headers" -}}
        {{- $found = append $found "config.collectors.node_metrics.targets[].headers" -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- join ", " (uniq $found) -}}
{{- end -}}

{{/*
Whether the rendered config.yaml is stored in a Secret ("true") or a ConfigMap ("").
Every template that touches the config object routes through this, so the decision —
and the `fail` guards — are made in exactly one place.

configStorage.mode:
  auto      (default) Secret when any credential-bearing key is set inline, else ConfigMap.
  secret    always a Secret.
  configmap always a ConfigMap — REFUSED (helm template fails) when a credential is
            set inline, because that is the one combination that cannot be made safe.
*/}}
{{- define "tailscale2otel.configStoresSecret" -}}
{{- $mode := "auto" -}}
{{- if and .Values.configStorage (kindIs "map" .Values.configStorage) .Values.configStorage.mode -}}
  {{- $mode = .Values.configStorage.mode | toString -}}
{{- end -}}
{{- $keys := include "tailscale2otel.inlineCredentialKeys" . -}}
{{- if eq $mode "secret" -}}
true
{{- else if eq $mode "auto" -}}
{{- if $keys -}}true{{- end -}}
{{- else if eq $mode "configmap" -}}
{{- if $keys -}}
{{- fail (printf "configStorage.mode=configmap, but credential-bearing keys are set inline under `config:` (%s). A ConfigMap is readable by anyone holding `get configmaps` in this namespace, which is routinely granted far more widely than `get secrets`, so this chart refuses to write credentials there. Fix by ONE of: (1) drop configStorage.mode (the default `auto` stores this config in Secret %s instead, no other change needed); (2) clear those keys and inject them as TS2OTEL_* env vars via `secret:` or `existingSecret`; (3) point the matching *_file key at a mounted Secret. See the chart README, 'Credentials and the rendered config'." $keys (include "tailscale2otel.configSecretName" .)) -}}
{{- end -}}
{{- else -}}
{{- fail (printf "configStorage.mode must be one of auto|secret|configmap, got %q" $mode) -}}
{{- end -}}
{{- end -}}

{{/*
Compute the GOMEMLIMIT env value. An explicit goRuntime.memLimit always wins;
otherwise default to ~90% of resources.limits.memory (mirrors the docker-compose
GOMEMLIMIT backstop: mem_limit 256m -> GOMEMLIMIT 230MiB), falling back to unset
when the memory limit is absent or in a unit we don't compute (only the binary
Mi/Gi suffixes Kubernetes/this chart's default use are handled).
*/}}
{{- define "tailscale2otel.gomemlimit" -}}
{{- if .Values.goRuntime.memLimit -}}
{{- .Values.goRuntime.memLimit -}}
{{- else if .Values.resources.limits.memory -}}
{{- $mem := .Values.resources.limits.memory | toString -}}
{{- if regexMatch "^[0-9]+(Mi|Gi)$" $mem -}}
{{- $num := regexFind "^[0-9]+" $mem | atoi -}}
{{- $unit := regexFind "(Mi|Gi)$" $mem -}}
{{- $mib := $num -}}
{{- if eq $unit "Gi" -}}
{{- $mib = mul $num 1024 -}}
{{- end -}}
{{- $scaled := div (mul $mib 9) 10 -}}
{{- printf "%dMiB" $scaled -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Reject an extraEnv entry that would silently shadow something the chart owns (#348).

WHY THIS FAILS RATHER THAN WARNS. Kubernetes resolves `env` AFTER `envFrom`, so
an extraEnv entry naming a key the chart injects from its Secret wins — quietly.
A credential the operator believes is coming from the chart's Secret (or from
their existingSecret) would be replaced by an unrelated value, and nothing in
the rendered manifest looks wrong. The same applies to GOGC/GOMEMLIMIT, which
the chart sets from goRuntime: two entries with the same name in one `env` list
is last-wins, so the operator's value would appear to work while the ordering
that makes it work is an implementation detail of this template.

The reserved set is DERIVED, not hardcoded: it is exactly the names this chart
renders — the keys of `secret:` (skipped when existingSecret is in use, since
the chart renders no Secret then and cannot know its keys) plus GOGC/GOMEMLIMIT
while goRuntime is setting them. Clear the chart's own value to take a name
over, e.g. goRuntime.gogc: "".

Never echo a VALUE here — only names. This message is rendered into terminal
output and CI logs.
*/}}
{{- define "tailscale2otel.validateExtraEnv" -}}
{{- $reserved := dict -}}
{{- if not .Values.existingSecret -}}
  {{- range $k, $v := (.Values.secret | default dict) -}}
    {{- $_ := set $reserved $k "the chart's `secret:` map (injected via envFrom)" -}}
  {{- end -}}
{{- end -}}
{{- if .Values.goRuntime.gogc -}}
  {{- $_ := set $reserved "GOGC" "goRuntime.gogc" -}}
{{- end -}}
{{- if .Values.workloadIdentity.enabled -}}
  {{- $_ := set $reserved "TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__ID_TOKEN_FILE" "workloadIdentity.enabled (the projected token path)" -}}
{{- end -}}
{{- if include "tailscale2otel.gomemlimit" . -}}
  {{- $_ := set $reserved "GOMEMLIMIT" "goRuntime.memLimit (or computed from resources.limits.memory)" -}}
{{- end -}}
{{- $seen := dict -}}
{{- range $i, $e := (.Values.extraEnv | default list) -}}
  {{- $name := $e.name | default "" -}}
  {{- if not $name -}}
    {{- fail (printf "extraEnv[%d] has no `name`; every entry must be a Kubernetes EnvVar with a name" $i) -}}
  {{- end -}}
  {{- if hasKey $seen $name -}}
    {{- fail (printf "extraEnv has two entries named %q; a duplicate silently last-wins inside one env list, so this is rejected rather than resolved" $name) -}}
  {{- end -}}
  {{- $_ := set $seen $name true -}}
  {{- if hasKey $reserved $name -}}
    {{- fail (printf "extraEnv[%d] name %q is already set by %s. Kubernetes resolves `env` after `envFrom`, so this entry would SILENTLY override it. Remove the entry, or clear the chart's own value to take the name over." $i $name (index $reserved $name)) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
Reject an extraContainers list that would render an invalid Pod.

Kubernetes requires container names to be unique within a pod and rejects the
Deployment at admission, so this only moves the failure from `kubectl apply` to
`helm template` — but it names the offending index and the reason, which the API
server's message does not. The collision with the exporter's own container is the
one worth catching early: a sidecar called "tailscale2otel" reads as a deliberate
override of the exporter and is silently nothing of the sort.
*/}}
{{- define "tailscale2otel.validateExtraContainers" -}}
{{- $seen := dict "tailscale2otel" "the exporter's own container" -}}
{{- range $i, $c := (.Values.extraContainers | default list) -}}
  {{- $name := $c.name | default "" -}}
  {{- if not $name -}}
    {{- fail (printf "extraContainers[%d] has no `name`; every entry must be a Kubernetes Container with a name" $i) -}}
  {{- end -}}
  {{- if not ($c.image | default "") -}}
    {{- fail (printf "extraContainers[%d] (%s) has no `image`; the chart supplies no default for a sidecar" $i $name) -}}
  {{- end -}}
  {{- if hasKey $seen $name -}}
    {{- fail (printf "extraContainers[%d] is named %q, which collides with %s. Container names must be unique within a pod." $i $name (index $seen $name)) -}}
  {{- end -}}
  {{- $_ := set $seen $name (printf "extraContainers[%d]" $i) -}}
{{- end -}}
{{- end -}}

{{/*
The port number from a `host:port` listen address (#344).

`splitList ":" | last` also copes with an IPv6 bind such as "[::1]:9091",
because the port is always the final colon-separated field.
*/}}
{{- define "tailscale2otel.listenPort" -}}
{{- . | splitList ":" | last | int -}}
{{- end -}}

{{/*
Per-listener Service safety gate (#344).

deploy/CLAUDE.md's standing rule for this chart: a Service may "never map a
receiver port without its credential". Publishing a listener is the one action
that turns a local-only misconfiguration into a network-reachable one, so the
two failure modes below are rejected at render time rather than warned about:

  1. A Service for a listener that is switched OFF publishes a port nothing
     serves, and hides the actual mistake behind a plausible-looking Service.
  2. A Service for a listener with NO credential publishes it to the whole
     cluster. The specifics are not hypothetical: `prometheus` serves every
     series unauthenticated when its token is empty, and `webhook` fails OPEN —
     an empty secret skips HMAC verification entirely.

A *_file credential counts. Requiring the inline form would push operators back
toward putting credentials on the command line, which #341 just removed.

Args: (dict "ctx" $ "name" "prometheus" "enabled" <bool> "cred" <string> "credFile" <string>)
*/}}
{{- define "tailscale2otel.requireListenerCredential" -}}
{{- $name := .name -}}
{{- if not .enabled -}}
{{- fail (printf "service.%s.enabled requires config.%s.enabled: a Service for a disabled listener publishes a port nothing is serving" $name $name) -}}
{{- end -}}
{{- if and (not .cred) (not .credFile) -}}
{{- fail (printf "service.%s.enabled requires a credential on that listener (%s). Publishing it with no credential exposes it to the whole cluster; set the value or its _file sibling, or leave the Service disabled and reach the pod directly." $name .credKey) -}}
{{- end -}}
{{- end -}}

{{/*
Ingress safety gate for the two RECEIVER listeners, streaming and webhook,
opt-in per #346. Admin and Prometheus never get an Ingress at ANY value — see
templates/ingress.yaml and deploy/CLAUDE.md's "No Kubernetes Service" note.
They are introspection/scrape surfaces; publishing either to the internet is
never the right default, so the chart does not offer that as a one-line
mistake.

Four failure modes rejected at render time, same "fail don't warn" posture as
tailscale2otel.requireListenerCredential:

  1. No backing Service: an Ingress rule with nothing behind it is a silently
     broken route, not a working one with a warning.
  2. No credential on the listener (delegated to requireListenerCredential):
     publishing an unauthenticated receiver to the internet is the exact
     failure mode this feature exists to prevent.
  3. No host: a host-less Ingress rule is a catch-all that swallows traffic
     for unrelated apps sharing the same controller.
  4. tls.enabled (the default) with neither a secretName nor an annotation
     (e.g. cert-manager.io/cluster-issuer): the rendered Ingress would claim
     TLS with nothing configured to terminate it.

Args: (dict "name" <listener> "ingress" <.Values.ingress.<listener>>
       "serviceEnabled" <bool> "enabled" <config.<listener>.enabled>
       "cred" <string> "credFile" <string> "credKey" <string>)
*/}}
{{- define "tailscale2otel.validateIngress" -}}
{{- $name := .name -}}
{{- $ing := .ingress -}}
{{- if not .serviceEnabled -}}
{{- fail (printf "ingress.%s.enabled requires service.%s.enabled: an Ingress with no backing Service is a silently broken route" $name $name) -}}
{{- end -}}
{{/*
Transitively unreachable TODAY, and kept on purpose. The guard above already
requires the Service, and service.yaml runs this same check on the same values
key — so with the current shape the message an operator sees for a missing
credential comes from service.yaml, not from here. This call is the thing that
keeps "no credential, no exposure" true if that precondition is ever relaxed.
Do not delete it as dead code without re-checking who enforces the credential.
*/}}
{{- include "tailscale2otel.requireListenerCredential" (dict "name" $name "enabled" .enabled "cred" .cred "credFile" .credFile "credKey" .credKey) -}}
{{- if not $ing.host -}}
{{- fail (printf "ingress.%s.host must be set: a host-less Ingress rule is a catch-all that will swallow traffic for unrelated apps on the same controller" $name) -}}
{{- end -}}
{{- if $ing.tls.enabled -}}
{{- if and (not $ing.tls.secretName) (not $ing.annotations) -}}
{{- fail (printf "ingress.%s.tls.enabled is true but neither ingress.%s.tls.secretName nor ingress.%s.annotations (e.g. cert-manager.io/cluster-issuer) is set: the rendered Ingress would claim TLS with nothing to terminate it. Set one of those, or set ingress.%s.tls.enabled: false only if a mesh terminates TLS for you upstream — Tailscale webhooks will not deliver to a plaintext endpoint." $name $name $name $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Gateway API HTTPRoute safety gate, same scope and reasoning as
tailscale2otel.validateIngress above (#346): streaming and webhook only, never
admin or Prometheus. The extra failure mode here is an HTTPRoute with no
parentRefs, which Gateway API accepts syntactically but which attaches to
nothing.

Args: (dict "name" <listener> "gateway" <.Values.gateway.<listener>>
       "serviceEnabled" <bool> "enabled" <config.<listener>.enabled>
       "cred" <string> "credFile" <string> "credKey" <string>)
*/}}
{{- define "tailscale2otel.validateGateway" -}}
{{- $name := .name -}}
{{- $gw := .gateway -}}
{{- if not .serviceEnabled -}}
{{- fail (printf "gateway.%s.enabled requires service.%s.enabled: an HTTPRoute with no backing Service is a silently broken route" $name $name) -}}
{{- end -}}
{{- include "tailscale2otel.requireListenerCredential" (dict "name" $name "enabled" .enabled "cred" .cred "credFile" .credFile "credKey" .credKey) -}}
{{- if not $gw.parentRefs -}}
{{- fail (printf "gateway.%s.enabled requires at least one entry in gateway.%s.parentRefs naming the Gateway to attach to" $name $name) -}}
{{- end -}}
{{- end -}}

{{/*
Whether config.yaml comes from a resource the OPERATOR manages (#347).

GitOps, ExternalSecrets and SOPS users produce the config outside Helm. Before
this, the only way to mount it was a generic extraVolumes override that fought
the chart's own config volume, or putting multi-tailnet credentials into Helm
values where `helm get values` exposes them.

When either is set the chart renders NO config object and the entire `config:`
tree is inert. That is deliberate and total: a partially-applied config would be
worse than none, because `helm template` would show values the pod never sees.
*/}}
{{- define "tailscale2otel.usesExternalConfig" -}}
{{- if or .Values.existingConfigMap .Values.existingConfigSecret -}}true{{- end -}}
{{- end -}}

{{/*
Validate the external-config combination (#347). Fails rather than picking one.
*/}}
{{- define "tailscale2otel.validateExternalConfig" -}}
{{- if and .Values.existingConfigMap .Values.existingConfigSecret -}}
{{- fail "existingConfigMap and existingConfigSecret are mutually exclusive: set exactly one, since only one object can back the config volume" -}}
{{- end -}}
{{- if and (include "tailscale2otel.usesExternalConfig" .) (not .Values.existingConfigKey) -}}
{{- fail "existingConfigKey must name the key holding the config YAML inside your ConfigMap/Secret (it is mounted as that filename)" -}}
{{- end -}}
{{- end -}}

{{/*
The projected WIF token path, and the guards around it (#343).

Tailscale's workload-identity audience is `api.tailscale.com/<client id>`. Getting
it wrong does not fail at render or at startup — the token is simply rejected when
first exchanged, which surfaces as an auth error that looks like a bad client. So
the audience is DERIVED from the configured client_id by default rather than being
another string to keep in sync by hand.
*/}}
{{- define "tailscale2otel.wifTokenPath" -}}
{{- printf "%s/%s" (trimSuffix "/" .Values.workloadIdentity.mountPath) .Values.workloadIdentity.fileName -}}
{{- end -}}

{{- define "tailscale2otel.wifAudience" -}}
{{- with .Values.workloadIdentity.audience -}}
{{- . -}}
{{- else -}}
{{- printf "api.tailscale.com/%s" .Values.config.tailscale.auth.workload_identity.client_id -}}
{{- end -}}
{{- end -}}

{{- define "tailscale2otel.validateWorkloadIdentity" -}}
{{- if .Values.workloadIdentity.enabled -}}
  {{- if ne .Values.config.tailscale.auth.method "workload_identity" -}}
    {{- fail (printf "workloadIdentity.enabled requires config.tailscale.auth.method: workload_identity (got %q). The projected token would be mounted and never used." .Values.config.tailscale.auth.method) -}}
  {{- end -}}
  {{- if not .Values.config.tailscale.auth.workload_identity.client_id -}}
    {{- fail "workloadIdentity.enabled requires config.tailscale.auth.workload_identity.client_id: it is also what the default token audience (api.tailscale.com/<client id>) is derived from, and an unscoped or wrongly-scoped token is rejected only at first use" -}}
  {{- end -}}
  {{- if not .Values.workloadIdentity.fileName -}}
    {{- fail "workloadIdentity.fileName must not be empty; it is the projected token's filename" -}}
  {{- end -}}
  {{- if lt (int .Values.workloadIdentity.expirationSeconds) 600 -}}
    {{- fail (printf "workloadIdentity.expirationSeconds must be at least 600 (got %v): Kubernetes silently clamps shorter requests, so a smaller value here would misdescribe the real token lifetime" .Values.workloadIdentity.expirationSeconds) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
tailscale2otel.prometheusListenIsLoopback — keep the chart's reachability
classification aligned with internal/listenaddr. Helm has no net.ParseIP, so
normalize every textual spelling of IPv4, IPv6, and IPv4-mapped loopback that
the runtime accepts. The helper emits "true" for loopback and nothing otherwise.
*/}}
{{- define "tailscale2otel.prometheusListenIsLoopback" -}}
{{- $listen := lower (trim (toString .)) -}}
{{- $host := "" -}}
{{- if hasPrefix "[" $listen -}}
  {{- $host = trimAll "[]" (regexFind "^\\[[^]]+\\]" $listen) -}}
{{- else -}}
  {{- $host = regexReplaceAll ":[0-9]+$" $listen "" -}}
{{- end -}}
{{- if or (eq $host "localhost") (or (regexMatch "^127(?:\\.[0-9]{1,3}){3}$" $host) (or (regexMatch "^[0:]*1$" $host) (or (regexMatch "^[0:]*ffff:127(?:\\.[0-9]{1,3}){3}$" $host) (regexMatch "^[0:]*ffff:7f[0-9a-f]{2}:[0-9a-f]{1,4}$" $host)))) -}}true{{- end -}}
{{- end -}}

{{/*
tailscale2otel.prometheusTokenConfigured — resolve token posture across config,
the chart-managed secret, and resources Helm cannot inspect. Opaque sources must
declare whether they provide the token; guessing either way can publish an
unauthenticated Service or render a monitor that receives 401 forever.
*/}}
{{- define "tailscale2otel.prometheusTokenConfigured" -}}
{{- $auth := .Values.config.prometheus.auth | default dict -}}
{{- $configToken := "" -}}
{{- $configTokenFile := "" -}}
{{- if not (or .Values.existingConfigMap .Values.existingConfigSecret) -}}
  {{- $configToken = $auth.token | default "" -}}
  {{- $configTokenFile = $auth.token_file | default "" -}}
{{- end -}}
{{- $chartSecretToken := "" -}}
{{- if not .Values.existingSecret -}}
  {{- $chartSecretToken = index (.Values.secret | default dict) "TS2OTEL_PROMETHEUS__AUTH__TOKEN" | default "" -}}
{{- end -}}
{{- $opaque := or .Values.existingSecret (or (gt (len (.Values.extraEnvFrom | default list)) 0) (or .Values.existingConfigMap .Values.existingConfigSecret)) -}}
{{- $external := .Values.metrics.externalPrometheusToken | default "auto" -}}
{{- if and $opaque (eq $external "auto") -}}
{{- fail "a remote Prometheus consumer is enabled while existingSecret, extraEnvFrom, existingConfigMap, or existingConfigSecret can override its auth posture. Helm cannot inspect that source; set metrics.externalPrometheusToken=present when it supplies TS2OTEL_PROMETHEUS__AUTH__TOKEN (and configure the monitor bearerTokenSecret), or =absent to assert that it does not." -}}
{{- end -}}
{{- if and (not $opaque) (ne $external "auto") -}}
{{- fail "metrics.externalPrometheusToken is present/absent but no opaque existingSecret, extraEnvFrom, existingConfigMap, or existingConfigSecret is configured; leave it at auto." -}}
{{- end -}}
{{- if or $configToken (or $configTokenFile (or $chartSecretToken (eq $external "present"))) -}}true{{- end -}}
{{- end -}}

{{/*
tailscale2otel.validateMetricsExposure — a remote consumer must not target a
/metrics listener the app will refuse or that is reachable only inside its pod.

config.prometheus.listen defaults to "127.0.0.1:2112", a loopback bind. Since #315 the app
answers /metrics there with 403 whenever no token is set and the exposure is not
acknowledged, because /metrics carries every series it produces — device names,
flow endpoints, audit identities. A PodMonitor or ServiceMonitor aimed at that
listener scrapes a 403 forever and shows up as a target reporting "no data",
which reads as a broken exporter rather than a chart misconfiguration.

Two remote-scrape postures are legitimate: set a token (with the
matching bearerTokenSecret, which the monitor templates already require),
or acknowledge the exposure for in-cluster scraping behind a NetworkPolicy.
Loopback stays the safe default for same-host scraping, but a monitor pod cannot
reach another pod's loopback and the chart must reject that silent no-data state.
*/}}
{{- define "tailscale2otel.validateMetricsExposure" -}}
{{- $p := .Values.config.prometheus -}}
{{- $auth := $p.auth | default dict -}}
{{- $listen := $p.listen | default "127.0.0.1:2112" -}}
{{- $loopback := eq (include "tailscale2otel.prometheusListenIsLoopback" $listen) "true" -}}
{{- $tokenConfigured := eq (include "tailscale2otel.prometheusTokenConfigured" .) "true" -}}
{{- if $loopback -}}
{{- fail (printf "a remote metrics consumer cannot scrape config.prometheus.listen (%s) because it is loopback-only. Set a pod-reachable bind such as :2112, then set config.prometheus.auth.token/config.prometheus.auth.token_file or config.prometheus.auth.allow_unauthenticated=true." $listen) -}}
{{- end -}}
{{- if and (not $tokenConfigured) (not $auth.allow_unauthenticated) (not $loopback) -}}
{{- fail (printf "a remote metrics consumer is enabled but config.prometheus.listen (%s) is a network-reachable bind with no config.prometheus.auth.token or config.prometheus.auth.token_file: the app REFUSES /metrics there with HTTP 403, so the scrape target would report no data forever and look like a broken exporter. Set a token (plus the monitor's bearerTokenSecret when applicable), or set config.prometheus.auth.allow_unauthenticated=true to acknowledge scraping it unauthenticated behind a NetworkPolicy." $listen) -}}
{{- end -}}
{{- end -}}
