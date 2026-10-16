package routing

import (
	"net/http"
	"strings"
)

// ExactBrandResource is deliberately limited to two immutable release assets.
// Unlike discovery, encoded/normalized variants and other methods receive no
// exception: they continue through the ordinary ownership classifier.
func ExactBrandResource(r *http.Request) bool {
	if r == nil || r.URL == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	if r.URL.Path != "/favicon.ico" && r.URL.Path != "/apple-touch-icon.png" {
		return false
	}
	if r.URL.EscapedPath() != r.URL.Path {
		return false
	}
	return r.RequestURI == "" || strings.SplitN(r.RequestURI, "?", 2)[0] == r.URL.Path
}

func ProtectBrandResources(next, brand http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ExactBrandResource(r) {
			brand.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
