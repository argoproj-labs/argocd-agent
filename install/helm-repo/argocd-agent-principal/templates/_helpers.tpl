{{/*
Expand the name of the chart.
*/}}
{{- define "argocd-agent-principal.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "argocd-agent-principal.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "argocd-agent-principal.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Produce a Kubernetes-compliant name (<= 63 chars, the DNS label limit).

If the input already fits, it is returned unchanged. Otherwise it is
truncated to 54 chars and suffixed with an 8-char hash derived from the
full, untruncated input. This guarantees that two inputs which only differ
in their tail (e.g. "<base>-metrics" vs "<base>-healthz") still produce
distinct, deterministic names after truncation, instead of silently
colliding once the differentiating suffix is cut off.

Usage: {{ include "argocd-agent-principal.safeName" "some-long-candidate-name" }}
*/}}
{{- define "argocd-agent-principal.safeName" -}}
{{- $name := . -}}
{{- if le (len $name) 63 -}}
{{- $name -}}
{{- else -}}
{{- $hash := $name | sha256sum | trunc 8 -}}
{{- printf "%s-%s" ($name | trunc 54 | trimSuffix "-") $hash -}}
{{- end -}}
{{- end }}

{{/*
Create default image tag. Defaults to chart appVersion if not set.
*/}}
{{- define "argocd-agent-principal.defaultTag" -}}
{{- default .Chart.AppVersion .Values.image.tag }}
{{- end -}}

{{/*
Expand the namespace of the release.
Defaults to release namespace if namespaceOverride is not set.
*/}}
{{- define "argocd-agent-principal.namespace" -}}
{{- default .Release.Namespace .Values.namespaceOverride | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Common labels
*/}}
{{- define "argocd-agent-principal.labels" -}}
helm.sh/chart: {{ include "argocd-agent-principal.chart" . }}
{{ include "argocd-agent-principal.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
NOTE: spec.selector.matchLabels is immutable on Deployments. Changing any
value emitted here after the initial install (e.g. by setting nameOverride)
requires deleting and reinstalling the release.
*/}}
{{- define "argocd-agent-principal.selectorLabels" -}}
app.kubernetes.io/name: {{ include "argocd-agent-principal.name" . }}
app.kubernetes.io/part-of: argocd-agent
app.kubernetes.io/component: principal
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "argocd-agent-principal.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Resource name helpers
*/}}
{{- define "argocd-agent-principal.configMapName" -}}
{{- include "argocd-agent-principal.safeName" (printf "%s-params" (include "argocd-agent-principal.fullname" .)) }}
{{- end }}

{{- define "argocd-agent-principal.serviceName" -}}
{{- include "argocd-agent-principal.safeName" (include "argocd-agent-principal.fullname" .) }}
{{- end }}

{{- define "argocd-agent-principal.metricsServiceName" -}}
{{- include "argocd-agent-principal.safeName" (printf "%s-metrics" (include "argocd-agent-principal.fullname" .)) }}
{{- end }}

{{- define "argocd-agent-principal.healthzServiceName" -}}
{{- include "argocd-agent-principal.safeName" (printf "%s-healthz" (include "argocd-agent-principal.fullname" .)) }}
{{- end }}

{{- define "argocd-agent-principal.redisProxyServiceName" -}}
argocd-agent-redis-proxy
{{- end }}

{{- define "argocd-agent-principal.resourceProxyServiceName" -}}
argocd-agent-resource-proxy
{{- end }}

{{- define "argocd-agent-principal.serviceMonitorName" -}}
{{- include "argocd-agent-principal.safeName" (printf "%s-servicemonitor" (include "argocd-agent-principal.fullname" .)) }}
{{- end }}

{{- define "argocd-agent-principal.clusterRoleName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{- define "argocd-agent-principal.roleName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{- define "argocd-agent-principal.clusterRoleBindingName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{- define "argocd-agent-principal.roleBindingName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{- define "argocd-agent-principal.userpassSecretName" -}}
{{- .Values.principal.userpass.secretName }}
{{- end }}

{{- define "argocd-agent-principal.testResourceName" -}}
{{- include "argocd-agent-principal.safeName" (printf "%s-test" (include "argocd-agent-principal.fullname" .)) }}
{{- end }}
