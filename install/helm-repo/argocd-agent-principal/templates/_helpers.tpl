{{/*
Expand the name of the chart.
*/}}
{{- define "argocd-agent-principal.name" -}}
{{- default .Chart.Name .Values.global.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "argocd-agent-principal.fullname" -}}
{{- if .Values.global.fullnameOverride }}
{{- .Values.global.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.global.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}


{{/*
Common labels
*/}}
{{- define "argocd-agent-principal.labels" -}}
{{ include "argocd-agent-principal.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: argocd-agent
app.kubernetes.io/component: principal
{{- with .Values.labels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "argocd-agent-principal.selectorLabels" -}}
app.kubernetes.io/name: {{ include "argocd-agent-principal.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "argocd-agent-principal.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "argocd-agent-principal.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the config map
*/}}
{{- define "argocd-agent-principal.configMapName" -}}
{{- printf "%s-params" (include "argocd-agent-principal.fullname" .) }}
{{- end }}

{{/*
Create the name of the main service
*/}}
{{- define "argocd-agent-principal.serviceName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{/*
Create the name of the metrics service
*/}}
{{- define "argocd-agent-principal.metricsServiceName" -}}
{{- printf "%s-metrics" (include "argocd-agent-principal.fullname" .) }}
{{- end }}

{{/*
Create the name of the healthz service
*/}}
{{- define "argocd-agent-principal.healthzServiceName" -}}
{{- printf "%s-healthz" (include "argocd-agent-principal.fullname" .) }}
{{- end }}

{{/*
Create the name of the cluster role
*/}}
{{- define "argocd-agent-principal.clusterRoleName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{/*
Create the name of the role
*/}}
{{- define "argocd-agent-principal.roleName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{/*
Create the name of the cluster role binding
*/}}
{{- define "argocd-agent-principal.clusterRoleBindingName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}

{{/*
Create the name of the role binding
*/}}
{{- define "argocd-agent-principal.roleBindingName" -}}
{{- include "argocd-agent-principal.fullname" . }}
{{- end }}


{{/*
Create the name of the userpass secret
*/}}
{{- define "argocd-agent-principal.userpassSecretName" -}}
{{- printf "%s-userpass" (include "argocd-agent-principal.fullname" .) }}
{{- end }}


{{/*
Common annotations
*/}}
{{- define "argocd-agent-principal.annotations" -}}
{{- with .Values.annotations }}
{{ toYaml . }}
{{- end }}
{{- end }}