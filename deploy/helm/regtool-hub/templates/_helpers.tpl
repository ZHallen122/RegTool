{{/*
The chart name, overridable so two releases in one namespace can be told apart.
*/}}
{{- define "regtool-hub.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The name every object is prefixed with. A release already named after the chart
is not repeated, so `helm install regtool-hub` yields `regtool-hub` rather than
`regtool-hub-regtool-hub`.
*/}}
{{- define "regtool-hub.fullname" -}}
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

{{- define "regtool-hub.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "regtool-hub.labels" -}}
helm.sh/chart: {{ include "regtool-hub.chart" . }}
{{ include "regtool-hub.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: regtool
{{- end }}

{{/*
Selector labels are the subset that must never change for a release: they are
immutable on a Deployment's selector.
*/}}
{{- define "regtool-hub.selectorLabels" -}}
app.kubernetes.io/name: {{ include "regtool-hub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "regtool-hub.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "regtool-hub.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The image reference, defaulting the tag to the chart's appVersion.
*/}}
{{- define "regtool-hub.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
Whether an inline sources.json was given, which is what turns the ConfigMap and
the --sources flag on.
*/}}
{{- define "regtool-hub.hasSources" -}}
{{- if .Values.hub.sources }}true{{- end }}
{{- end }}

{{/*
The name of the PVC the pod mounts at /data: the existing claim if one was
named, otherwise the one this chart creates.
*/}}
{{- define "regtool-hub.pvcName" -}}
{{- default (printf "%s-data" (include "regtool-hub.fullname" .)) .Values.persistence.existingClaim }}
{{- end }}
