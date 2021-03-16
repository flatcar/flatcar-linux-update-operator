{{/*
Expand the name of the chart.
*/}}
{{- define "flatcar-linux-update-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "flatcar-linux-update-operator.fullname" -}}
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
Create fully qualified app name for the operator component.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
*/}}
{{- define "operator.fullname" -}}
{{- include "flatcar-linux-update-operator.fullname" . | trunc 54 }}-operator
{{- end }}

{{/*
Create fully qualified app name for the operator component.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
*/}}
{{- define "agent.fullname" -}}
{{- include "flatcar-linux-update-operator.fullname" . | trunc 56 }}-agent
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "flatcar-linux-update-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "flatcar-linux-update-operator.labels" -}}
helm.sh/chart: {{ include "flatcar-linux-update-operator.chart" . }}
{{ include "flatcar-linux-update-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "flatcar-linux-update-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "flatcar-linux-update-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use for the operator component
*/}}
{{- define "operator.serviceAccountName" -}}
{{- if .Values.operator.serviceAccount.create }}
{{- if .Values.operator.serviceAccount.name }}
{{ .Values.operator.serviceAccount.name }}
{{- else }}
{{- include "flatcar-linux-update-operator.fullname" . }}-operator
{{- end }}
{{- else }}
{{- default "default" .Values.operator.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the service account to use for the agent component
*/}}
{{- define "agent.serviceAccountName" -}}
{{- if .Values.agent.serviceAccount.create }}
{{- if .Values.agent.serviceAccount.name }}
{{ .Values.agent.serviceAccount.name }}
{{- else }}
{{- include "flatcar-linux-update-operator.fullname" .}}-agent
{{- end }}
{{- else }}
{{- default "default" .Values.agent.serviceAccount.name }}
{{- end }}
{{- end }}
