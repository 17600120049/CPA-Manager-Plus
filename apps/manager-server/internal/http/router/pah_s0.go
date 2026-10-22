package router

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

const pahS0ResourcePrefix = "/v0/resource/plugins"

// Intercept before ServeMux normalization, OPTIONS routing, and CPAResource.
// Ingress and Manager are separate modules; no shared route-owner registry.
func pahS0DenyResources(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r != nil && r.URL != nil && PAHS0ResourceRequest(r) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PAHS0ResourceRequest applies the same conservative raw-path deny predicate
// to Legacy and Fresh Manager handlers in the private profile.
func PAHS0ResourceRequest(r *http.Request) bool {
	for _, initial := range []string{r.URL.Path, r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI} {
		if index := strings.IndexByte(initial, '?'); index >= 0 {
			initial = initial[:index]
		}
		current := initial
		for pass := 0; pass < 3; pass++ {
			raw := strings.ToLower(strings.ReplaceAll(current, "\\", "/"))
			for _, candidate := range []string{raw, path.Clean(raw)} {
				if candidate == pahS0ResourcePrefix ||
					strings.HasPrefix(candidate, pahS0ResourcePrefix+"/") ||
					strings.HasPrefix(candidate, pahS0ResourcePrefix+"%") ||
					strings.HasPrefix(candidate, pahS0ResourcePrefix+";") {
					return true
				}
			}
			decoded, err := url.PathUnescape(current)
			if err != nil || decoded == current {
				break
			}
			current = decoded
		}
	}
	return false
}
