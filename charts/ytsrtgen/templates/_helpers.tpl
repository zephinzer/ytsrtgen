{{- define "ytsrtgen.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "ytsrtgen.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ytsrtgen.labels" -}}
app.kubernetes.io/name: {{ include "ytsrtgen.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "ytsrtgen.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ytsrtgen.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
