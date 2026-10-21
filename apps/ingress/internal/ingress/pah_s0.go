package ingress

import (
	"net/http"
	"path"
	"strings"
)

// The public ingress must never expose this Manager-to-Ingress readiness proof.
const pahS0GuardPath = "/internal/pah-s0/resource-guard"
const pahS0ResourcePrefix = "/v0/resource/plugins"

// Unescape twice and normalize so malformed path aliases cannot escape into
// the Gateway under intermediary path rewriting.
func pahS0ResourceCandidate(value string) bool {
	if index := strings.IndexByte(value, '?'); index >= 0 {
		value = value[:index]
	}
	for _, candidate := range routeCandidates(value) {
		raw := strings.ToLower(strings.ReplaceAll(candidate, "\\", "/"))
		for _, normalized := range []string{raw, path.Clean(raw)} {
			if normalized == pahS0ResourcePrefix ||
				strings.HasPrefix(normalized, pahS0ResourcePrefix+"/") ||
				strings.HasPrefix(normalized, pahS0ResourcePrefix+"%") ||
				strings.HasPrefix(normalized, pahS0ResourcePrefix+";") {
				return true
			}
		}
	}
	return false
}

func canonicalPAHS0Resource(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p != pahS0ResourcePrefix && !strings.HasPrefix(p, pahS0ResourcePrefix+"/") {
		return false
	}
	if p == pahS0ResourcePrefix+"/" || strings.Contains(p, "//") ||
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

func classifyPAHS0Resource(r *http.Request) (Route, bool) {
	for _, value := range []string{r.URL.Path, r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI} {
		if pahS0ResourceCandidate(value) {
			if canonicalPAHS0Resource(r) {
				return RouteManager, true
			}
			return RouteDeny, true
		}
	}
	return RouteGateway, false
}

// classifyLegacyPluginCandidate recognizes all URI carriers before the old
// Gateway fallback. Unlike the P-S0 classifier this does not change normal
// ownership: it only requires a live Legacy authority on these requests.
func classifyLegacyPluginCandidate(r *http.Request) bool {
	for _, p := range []string{r.URL.Path, r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI} {
		if pahS0ResourceCandidate(p) {
			return true
		}
	}
	return false
}
