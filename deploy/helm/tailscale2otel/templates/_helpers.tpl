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
(e.g. --set config.log_level=debug) keep working. Secrets stay as ${ENV}
placeholders here and are expanded at runtime from the envFrom Secret.
*/}}
{{- define "tailscale2otel.config" -}}
{{ .Values.config | toYaml }}
{{- end -}}
