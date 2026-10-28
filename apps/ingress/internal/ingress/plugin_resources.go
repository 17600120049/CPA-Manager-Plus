package ingress

import (
	"net/http"
	"path"
	"strings"
)

// PluginResourcesPathPrefix is the CPA-provided plugin resource namespace. The
// Manager serves it because every response must be rewritten into an
// opaque-origin sandbox document; forwarded to the model Gateway it would
// simply be unreachable.
const PluginResourcesPathPrefix = "/v0/resource/plugins"

// pluginResourceCandidate unescapes twice and normalizes so malformed path
// aliases cannot reach the Gateway under intermediary path rewriting.
func pluginResourceCandidate(value string) bool {
	if index := strings.IndexByte(value, '?'); index >= 0 {
		value = value[:index]
	}
	for _, candidate := range routeCandidates(value) {
		raw := strings.ToLower(strings.ReplaceAll(candidate, "\\", "/"))
		for _, normalized := range []string{raw, path.Clean(raw)} {
			if normalized == PluginResourcesPathPrefix ||
				strings.HasPrefix(normalized, PluginResourcesPathPrefix+"/") ||
				strings.HasPrefix(normalized, PluginResourcesPathPrefix+"%") ||
				strings.HasPrefix(normalized, PluginResourcesPathPrefix+";") {
				return true
			}
		}
	}
	return false
}

// pluginResourceWriteMethods are the methods the Manager's plugin resource
// contract accepts. Plugin pages are served with `sandbox allow-scripts
// allow-forms` and use their own namespace for their own endpoints - the
// official Codex Invite page posts to
// /v0/resource/plugins/codex-invite/invite - so a method veto here would break
// a working plugin on Full Docker while the same request reaches the Manager
// directly in a Manager-only deployment. The isolation is delivered by the
// Manager: Fetch Metadata, the opaque-origin sandbox CSP, response-header
// confinement and redirect confinement apply to writes exactly as to reads.
var pluginResourceWriteMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodHead:   true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// canonicalPluginResource reports whether the request is a bare, unescaped
// plugin resource request in a method the Manager serves. Every other shape
// (other method, trailing separator, empty segment, encoded separator, dot
// segment, parameter) is a denial instead of Gateway traffic.
func canonicalPluginResource(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	// The canonical plugin resource path is shape-checked below; the method
	// check here mirrors the Manager's accepted set exactly.
	if !pluginResourceWriteMethods[r.Method] {
		return false
	}
	p := r.URL.Path
	if p != PluginResourcesPathPrefix && !strings.HasPrefix(p, PluginResourcesPathPrefix+"/") {
		return false
	}
	if p == PluginResourcesPathPrefix+"/" || strings.Contains(p, "//") ||
		strings.ContainsAny(p, "\\%;") || path.Clean(p) != p {
		return false
	}
	if r.URL.EscapedPath() != p || (r.URL.RawPath != "" && r.URL.RawPath != p) {
		return false
	}
	if r.RequestURI != "" && strings.SplitN(r.RequestURI, "?", 2)[0] != p {
		return false
	}
	return true
}

// classifyPluginResource resolves plugin resource ownership: a canonical
// resource belongs to the Manager, a plugin-shaped non-canonical request is
// denied outright.
func classifyPluginResource(r *http.Request) (Route, bool) {
	for _, value := range []string{r.URL.Path, r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI} {
		if pluginResourceCandidate(value) {
			if canonicalPluginResource(r) {
				return RouteManager, true
			}
			return RouteDeny, true
		}
	}
	return RouteGateway, false
}
