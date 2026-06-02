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

{{/* The rendered config.yaml: explicit override, else built from values. */}}
{{- define "tailscale2otel.config" -}}
{{- if .Values.config -}}
{{ .Values.config | toYaml }}
{{- else -}}
log_level: info
tailscale:
  tailnet: {{ .Values.tailscale.tailnet | quote }}
  auth:
    method: {{ .Values.tailscale.authMethod | quote }}
    oauth:
      client_id: "${TS_OAUTH_CLIENT_ID}"
      client_secret: "${TS_OAUTH_CLIENT_SECRET}"
      scopes: ["all:read"]
    apikey: "${TS_API_KEY}"
otlp:
  protocol: {{ .Values.otlp.protocol | quote }}
  endpoint: {{ .Values.otlp.endpoint | quote }}
  grafana_cloud:
    instance_id: "${GC_INSTANCE_ID}"
    token: "${GC_OTLP_TOKEN}"
  metric_interval: {{ .Values.otlp.metricInterval }}
self_observability:
  enabled: {{ .Values.selfObservability.enabled }}
{{- end -}}
{{- end -}}
