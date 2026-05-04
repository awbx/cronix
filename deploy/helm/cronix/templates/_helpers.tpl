{{- define "cronix.name" -}}
{{- default .Chart.Name -}}
{{- end -}}

{{- define "cronix.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "cronix.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cronix.labels" -}}
app.kubernetes.io/name: {{ include "cronix.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
cronix.dev/managed: "true"
{{- end -}}
