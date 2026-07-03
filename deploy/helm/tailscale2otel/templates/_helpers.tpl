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
(e.g. --set config.log_level=debug) keep working. Secrets contain no values here —
they are injected exclusively via TS2OTEL_* environment variables from the envFrom
Secret, which override the corresponding fields in this file at runtime.
*/}}
{{- define "tailscale2otel.config" -}}
{{ .Values.config | toYaml }}
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
