{{/*
Expand the name of the chart.
*/}}
{{- define "argocd-agent-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "argocd-agent-agent.fullname" -}}
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
Base name for Helm-created resources, derived from the release.
*/}}
{{- define "argocd-agent-agent.agentBaseName" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-agent-helm" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Produce a Kubernetes-compliant name (<= 63 chars, the DNS label limit).

If the input already fits, it is returned unchanged. Otherwise it is
truncated to 54 chars and suffixed with an 8-char hash derived from the
full, untruncated input. This guarantees that two inputs which only differ
in their tail (e.g. "<base>-metrics" vs "<base>-healthz") still produce
distinct, deterministic names after truncation, instead of silently
colliding once the differentiating suffix is cut off.

Usage: {{ include "argocd-agent-agent.safeName" "some-long-candidate-name" }}
*/}}
{{- define "argocd-agent-agent.safeName" -}}
{{- $name := . -}}
{{- if le (len $name) 63 -}}
{{- $name -}}
{{- else -}}
{{- $hash := $name | sha256sum | trunc 8 -}}
{{- printf "%s-%s" ($name | trunc 54 | trimSuffix "-") $hash -}}
{{- end -}}
{{- end }}

{{/*
Helper to append a suffix to the resource base name.
Usage: {{ include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "metrics") }}
*/}}
{{- define "argocd-agent-agent.resourceName" -}}
{{- $root := .root -}}
{{- $suffix := .suffix | default "" -}}
{{- $base := include "argocd-agent-agent.agentBaseName" $root -}}
{{- if $suffix }}
{{- include "argocd-agent-agent.safeName" (printf "%s-%s" $base $suffix) }}
{{- else }}
{{- include "argocd-agent-agent.safeName" $base }}
{{- end }}
{{- end }}

{{/*
Name for the agent deployment.
*/}}
{{- define "argocd-agent-agent.agentDeploymentName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "") }}
{{- end }}

{{/*
Common resource-specific helpers.
*/}}
{{- define "argocd-agent-agent.paramsConfigMapName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "params") }}
{{- end }}

{{- define "argocd-agent-agent.metricsServiceName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "metrics") }}
{{- end }}

{{- define "argocd-agent-agent.healthzServiceName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "healthz") }}
{{- end }}

{{- define "argocd-agent-agent.roleName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "role") }}
{{- end }}

{{- define "argocd-agent-agent.roleBindingName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "rolebinding") }}
{{- end }}

{{- define "argocd-agent-agent.clusterRoleName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "clusterrole") }}
{{- end }}

{{- define "argocd-agent-agent.clusterRoleBindingName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "clusterrolebinding") }}
{{- end }}

{{/*
Name for resources used exclusively by Helm tests.
*/}}
{{- define "argocd-agent-agent.testResourceName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "test") }}
{{- end }}

{{/*
Create default image tag. Uses appVersion as the default, which can be
overridden by setting image.tag in values.yaml. This follows the same
pattern as the official argo-cd Helm chart (argo-cd.defaultTag).
*/}}
{{- define "argocd-agent-agent.defaultTag" -}}
{{- default .Chart.AppVersion .Values.image.tag }}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "argocd-agent-agent.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "argocd-agent-agent.labels" -}}
helm.sh/chart: {{ include "argocd-agent-agent.chart" . }}
{{ include "argocd-agent-agent.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
NOTE: `spec.selector.matchLabels` is immutable on Deployments. Changing any
value emitted here after the initial install (e.g. by setting
`.Values.nameOverride`) requires deleting and reinstalling the release.
*/}}
{{- define "argocd-agent-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "argocd-agent-agent.name" . }}
app.kubernetes.io/part-of: argocd-agent
app.kubernetes.io/component: agent
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "argocd-agent-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "sa") }}
{{- end }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name for the agent service monitor.
*/}}
{{- define "argocd-agent-agent.agentServiceMonitorName" -}}
{{- include "argocd-agent-agent.resourceName" (dict "root" . "suffix" "servicemonitor") }}
{{- end }}

{{/*
Expand the namespace of the release.
*/}}
{{- define "argocd-agent-agent.namespace" -}}
{{- default .Release.Namespace .Values.namespaceOverride | trunc 63 | trimSuffix "-" -}}
{{- end }}
