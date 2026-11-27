{{/*
Common labels
*/}}
{{- define "ainsel.labels" -}}
app.kubernetes.io/part-of: ainsel
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{/*
Image reference for a project-owned component image.

  {{ include "ainsel.image" (dict "root" . "repo" .Values.hub.image.repository "tag" .Values.hub.image.tag) }}
  {{ include "ainsel.image" (dict "root" . "repo" ... "tag" ... "digest" ...) }}

`global.imageRegistry` is the prefix every project image is pulled from, so a
mirror or a fork's own registry is one value instead of eight. Two escape
hatches keep it from getting in the way:

  * A repository whose first path segment looks like a registry host (it
    contains "." or ":", or is "localhost") is used verbatim — the same rule
    the agent operator's image-digest resolver uses, so chart and operator
    agree on what a full reference looks like.
  * `global.imageRegistry: ""` renders repositories as-is, which is what a
    plain Docker Hub layout needs.

An empty tag follows the chart's appVersion. The release pipeline publishes
every component image as `:<appVersion>` next to the chart, so the defaults
always name images that exist; a values file sets a tag only to pin a build.

Never use this for third-party images (postgres): they are not published by
this project and must keep their own repository. The upstream images render
`.Values.<comp>.image.repository` directly.
*/}}
{{- define "ainsel.image" -}}
{{- $root := .root -}}
{{- $repo := .repo | default "" -}}
{{- $global := $root.Values.global | default dict -}}
{{- $registry := $global.imageRegistry | default "" -}}
{{- if and $registry $repo -}}
{{-   $first := splitList "/" $repo | first -}}
{{-   $looksLikeRegistry := or (contains "." $first) (contains ":" $first) (eq $first "localhost") -}}
{{-   if and (not $looksLikeRegistry) (not (hasPrefix (printf "%s/" $registry) $repo)) -}}
{{-     $repo = printf "%s/%s" $registry $repo -}}
{{-   end -}}
{{- end -}}
{{- $ref := $repo -}}
{{- with (.tag | default $root.Chart.AppVersion) }}{{ $ref = printf "%s:%s" $ref . }}{{ end -}}
{{- with .digest }}{{ $ref = printf "%s@%s" $ref . }}{{ end -}}
{{- $ref -}}
{{- end }}

{{/*
Local mode: nothing is externally exposed — no hub ingress, no frontend
ingress (or frontend disabled), no MCP ingress (or MCP disabled), and no
OIDC issuer configured. In that situation the hub may safely run without
auth middleware; the chart then auto-enables HUB_ALLOW_INSECURE_NO_AUTH so
a port-forward-only install works with zero auth configuration. Note:
WebhookConnector CRs still get their own ingress via the gateway operator —
keep local mode truly local and do not create connectors on an exposed
cluster while relying on it.
*/}}
{{- define "ainsel.localMode" -}}
{{- $uiExposed := and .Values.frontend.enabled .Values.frontend.ingress.enabled -}}
{{- $mcpExposed := and .Values.mcp.enabled .Values.mcp.ingress.enabled -}}
{{- if and (not .Values.auth.oidcIssuer) (not .Values.hub.ingress.enabled) (not $uiExposed) (not $mcpExposed) -}}
true
{{- end -}}
{{- end -}}

{{/*
Whether an ingress className refers to ingress-nginx. An empty className is
treated as nginx for backwards compatibility with the chart defaults. The
nginx-specific regex paths and rewrite annotations are only rendered when
this returns "true"; other controllers get literal Prefix paths instead.
Usage:
  {{- $isNginx := eq (include "ainsel.isNginx" .Values.hub.ingress.className) "true" -}}
*/}}
{{- define "ainsel.isNginx" -}}
{{- if has (trim (lower (toString (default "nginx" .)))) (list "nginx" "kubernetes.io/ingress-nginx") -}}true{{- end -}}
{{- end -}}

{{/*
Build the annotation map for an Ingress, keeping nginx.ingress.kubernetes.io/*
annotations only when the controller is ingress-nginx so other controllers
(Traefik, HAProxy, cloud LBs) never receive rules they cannot interpret.
"nginxOnly" entries are the chart's own hardcoded nginx annotations (added
only for nginx); "extra" entries come from user values and are filtered the
same way.
Usage:
  {{- $annotations := fromYaml (include "ainsel.ingressAnnotations" (dict "isNginx" $isNginx "nginxOnly" (dict "nginx.ingress.kubernetes.io/use-regex" "true") "extra" .Values.hub.ingress.annotations)) -}}
*/}}
{{- define "ainsel.ingressAnnotations" -}}
{{- $isNginx := .isNginx -}}
{{- $annotations := dict -}}
{{- if $isNginx -}}
{{- range $key, $value := (default (dict) .nginxOnly) -}}
{{- $_ := set $annotations $key $value -}}
{{- end -}}
{{- end -}}
{{- range $key, $value := (default (dict) .extra) -}}
{{- if or $isNginx (not (hasPrefix "nginx.ingress.kubernetes.io/" $key)) -}}
{{- $_ := set $annotations $key $value -}}
{{- end -}}
{{- end -}}
{{- toYaml $annotations -}}
{{- end -}}

{{/* Roles claim; empty lets the services default to `groups`. */}}
{{- define "ainsel.oidcRolesClaim" -}}
{{- if .Values.auth.oidcRolesClaim -}}
{{- .Values.auth.oidcRolesClaim -}}
{{- else if .Values.auth.oidcProjectId -}}
urn:zitadel:iam:org:project:roles
{{- end -}}
{{- end -}}

{{/* Shared provider scopes for browser and MCP clients. */}}
{{- define "ainsel.oidcScopes" -}}
{{- if .Values.auth.oidcScopes -}}
{{- .Values.auth.oidcScopes -}}
{{- else if .Values.auth.oidcProjectId -}}
openid profile email urn:zitadel:iam:org:project:id:{{ .Values.auth.oidcProjectId }}:aud
{{- else -}}
openid profile email offline_access
{{- end -}}
{{- end -}}

{{/*
Named domain presets for agentEgress.proxy.presets. Each preset is a list of
domains in Squid dstdomain notation: an entry matches that domain AND every
subdomain of it, with or without a leading dot — there is no exact-match form,
so a preset may only list domains whose whole subtree is acceptable. Chart-side
constants — adding a preset is one map entry here; values switch presets on by
name.
*/}}
{{- define "ainsel.egressPresets" -}}
github:
  - github.com
  - api.github.com
  - codeload.github.com
  - githubusercontent.com
  - objects.githubusercontent.com
  - raw.githubusercontent.com
npm:
  - registry.npmjs.org
  - registry.yarnpkg.com
goproxy:
  - proxy.golang.org
  - sum.golang.org
pypi:
  - pypi.org
  - files.pythonhosted.org
crates:
  - crates.io
  - static.crates.io
  - index.crates.io
{{- end -}}

{{/*
The merged agent-egress allow-list: preset domains (agentEgress.proxy.presets,
resolved against "ainsel.egressPresets"; unknown names fail the render) plus
the free-form agentEgress.proxy.allowedDomains, deduplicated and sorted so the
rendered ConfigMap is stable. Takes the root context.
Usage: {{ include "ainsel.egressAllowedDomains" . }}
*/}}
{{- define "ainsel.egressAllowedDomains" -}}
{{- $presets := fromYaml (include "ainsel.egressPresets" .) -}}
{{- $domains := list -}}
{{- range $name := (default (list) .Values.agentEgress.proxy.presets) -}}
{{- $known := get $presets $name -}}
{{- if not $known -}}
{{- fail (printf "agentEgress.proxy.presets: unknown preset %q (known: %s)" $name (keys $presets | sortAlpha | join ", ")) -}}
{{- end -}}
{{- $domains = concat $domains $known -}}
{{- end -}}
{{- $domains = concat $domains (default (list) .Values.agentEgress.proxy.allowedDomains) -}}
{{- $domains | uniq | sortAlpha | join "\n" -}}
{{- end -}}

{{/*
The NO_PROXY list injected as EGRESS_NO_PROXY on the agent operator (the
operator forwards it to agent pods and MCP sidecars as NO_PROXY). Always
contains the hub service, the namespace's svc domains, the cluster DNS suffix
and the proxy service itself, plus the user's agentEgress.proxy.noProxy
entries. Comma-separated. Takes the root context.
*/}}
{{- define "ainsel.egressNoProxy" -}}
{{- $ns := .Values.namespace -}}
{{- $noProxy := list "agent-egress-proxy" (printf "agent-egress-proxy.%s" $ns) (printf "agent-egress-proxy.%s.svc.cluster.local" $ns) (printf "hub.%s.svc.cluster.local" $ns) (printf "%s.svc.cluster.local" $ns) "cluster.local" -}}
{{- range $entry := (default (list) .Values.agentEgress.proxy.noProxy) -}}
{{- $noProxy = concat $noProxy (splitList "," (replace " " "" $entry)) -}}
{{- end -}}
{{- $noProxy | compact | uniq | join "," -}}
{{- end -}}

