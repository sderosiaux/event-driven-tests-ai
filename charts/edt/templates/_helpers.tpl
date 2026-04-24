{{/*
Expand chart and release names. Follow the Helm default-name conventions so
resources stay under the 63-character DNS-1123 limit.
*/}}
{{- define "edt.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "edt.fullname" -}}
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

{{- define "edt.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "edt.labels" -}}
helm.sh/chart: {{ include "edt.chart" . }}
{{ include "edt.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "edt.selectorLabels" -}}
app.kubernetes.io/name: {{ include "edt.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "edt.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{- define "edt.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "edt.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "edt.adminSecretName" -}}
{{- printf "%s-admin-token" (include "edt.fullname" .) -}}
{{- end -}}

{{- define "edt.controlPlaneURL" -}}
{{- printf "http://%s-controlplane:%d" (include "edt.fullname" .) (.Values.controlPlane.service.port | int) -}}
{{- end -}}
