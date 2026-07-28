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
Structural carriers that are lists — config.tailnets[] and
config.collectors.node_metrics.targets[] — are handled separately below.
*/}}
{{- define "tailscale2otel.credentialPaths" -}}
headscale.api_key
tailscale.auth.oauth.client_secret
tailscale.auth.apikey
otlp.grafana_cloud.token
otlp.headers
collectors.flowlogs.objectstore.access_key_id
collectors.flowlogs.objectstore.secret_access_key
collectors.flowlogs.objectstore.session_token
collectors.auditlogs.objectstore.access_key_id
collectors.auditlogs.objectstore.secret_access_key
collectors.auditlogs.objectstore.session_token
streaming.token
webhook.secret
prometheus.auth.token
admin.auth.token
profiling.pyroscope.basic_auth_password
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
