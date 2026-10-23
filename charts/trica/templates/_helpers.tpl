{{- define "trica.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "trica.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "trica.selectorLabels" -}}
app.kubernetes.io/name: {{ include "trica.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "trica.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "trica.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "trica.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "trica.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "trica.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ default .Chart.AppVersion .Values.image.tag }}
{{- end -}}
{{- end -}}

{{- define "trica.config" -}}
{{- $cfg := deepCopy .Values.config -}}
{{- $_ := set $cfg "http" (dict "addr" (printf ":%v" .Values.service.port)) -}}
{{- $_ := set $cfg "database" (dict "url_file" "/var/run/secrets/trica/database-url") -}}
{{- if .Values.transport.enabled -}}
{{- $t := deepCopy (default (dict) $cfg.transport) -}}
{{- $_ := set $t "addr" (printf ":%v" .Values.transport.port) -}}
{{- $_ := set $t "ca_dir" "/var/lib/trica/ca" -}}
{{- $_ := set $t "hosts" .Values.transport.hosts -}}
{{- $_ := set $cfg "transport" $t -}}
{{- end -}}
{{- if .Values.collect.enabled -}}
{{- $_ := set $cfg "collect" (dict "targets_file" "/etc/trica/collect/targets.yaml") -}}
{{- end -}}
{{- if .Values.deviceTypes.existingClaim -}}
{{- $_ := set $cfg "devicetypes" (dict "extra_dir" "/etc/trica/devicetypes") -}}
{{- end -}}
{{- if or .Values.theme.builtin .Values.theme.existingConfigMap -}}
{{- $w := deepCopy (default (dict) $cfg.web) -}}
{{- if .Values.theme.builtin -}}
{{- $_ := set $w "theme" (dict "builtin" .Values.theme.builtin) -}}
{{- else if .Values.theme.archiveKey -}}
{{- $_ := set $w "theme" (dict "path" (printf "/etc/trica/theme/%s" .Values.theme.archiveKey)) -}}
{{- else -}}
{{- $_ := set $w "theme" (dict "path" "/etc/trica/theme") -}}
{{- end -}}
{{- $_ := set $cfg "web" $w -}}
{{- end -}}
{{- if .Values.live.enabled -}}
{{- $_ := set $cfg "live" .Values.live.config -}}
{{- end -}}
{{- if .Values.schemaOverlays -}}
{{- $paths := list -}}
{{- range $name, $_ := .Values.schemaOverlays -}}
{{- $paths = append $paths (printf "/etc/trica/schema/%s" $name) -}}
{{- end -}}
{{- $_ := set $cfg "schema_overlays" $paths -}}
{{- end -}}
{{- toYaml $cfg -}}
{{- end -}}
