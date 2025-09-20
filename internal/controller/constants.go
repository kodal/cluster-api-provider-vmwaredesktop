package controller

const metadataTemplate = `
instance-id: {{ .InstanceId }}
local-hostname: {{ .Hostname }}
{{- if .NodeIP }}
node-ip: {{ .NodeIP }}
{{- end }}
network:
  version: 2
  {{- if .Ethernets }}
  ethernets:
    {{- range $index, $element := .Ethernets }}
    {{ $element.Name }}:
      {{- template "ethernet" $element }}
    {{- end -}}
  {{- end -}}

{{- define "ethernet" -}}
      {{- if .Dhcp4 }}
      dhcp4: {{ .Dhcp4 }}
      {{- end -}}
      {{- if .Dhcp6 }}
      dhcp6: {{ .Dhcp6 }}
      {{- end -}}
      {{- if .Addresses }}
      addresses:
      {{- range .Addresses }}
        - {{ . }}
      {{- end -}}
      {{- end -}}
      {{- if .Routes }}
      routes:
      {{- range .Routes }}
        - to: {{ .To }}
          {{- if .Via }}
          via: {{ .Via }}
          {{- end -}}
      {{- end -}}
      {{- end -}}
      {{- if .Nameservers }}
      nameservers:
        addresses:
          {{- range .Nameservers }}
            - {{ . }}
          {{- end -}}
      {{- end -}}
      {{- if .Gateway4 }}
      gateway4: "{{ .Gateway4 }}"
      {{- end -}}
      {{- if .Gateway6 }}
      gateway6: "{{ .Gateway6 }}"
      {{- end -}}
{{- end -}}
`
