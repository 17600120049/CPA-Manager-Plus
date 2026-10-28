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

// canonicalPluginResource reports whether the request is a bare, unescaped
// GET/HEAD for one plugin resource. Every other shape (write method, trailing
// separator, empty segment, encoded separator, dot segment, parameter) is a
// denial instead of Gateway traffic.
//
// The method set is read-only because CPA's resource namespace is: a
// pluginapi.ResourceRoute is the plugin's own page ("the management UI menu
// label for this GET resource"), and pluginhost serves it only for GET. A
// plugin that needs to accept input posts to a management route instead - the
// official Codex Invite plugin registers
// {POST /codex-invite/invite} alongside its GET resource - so a write never
// belongs under this prefix. Denying it here keeps the transport from carrying
// a request CPA would answer with 404 anyway.
func canonicalPluginResource(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
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
