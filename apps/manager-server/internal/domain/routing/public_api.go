package routing

import "net/http"

// PublicAPIHandler is a mount contract, not a production activation. It only
// delegates when a verified-effective snapshot owns the canonical
// <controlBasePath>/api/v2 namespace; absent or stale authority remains a
// generic fail-closed 404.
type PublicAPIHandler struct {
	Snapshot RoutingSnapshot
	Active   http.Handler
}

func (h PublicAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	base, ok := h.Snapshot.CanonicalPublicAPIBase()
	if !ok || r == nil || r.URL == nil || !CanonicalRequestPath(r.URL.Path) ||
		(r.URL.RawPath != "" && r.URL.EscapedPath() != r.URL.Path) ||
		!isSegmentPath(r.URL.Path, base) {
		writeGenericNotFound(w)
		return
	}
	if h.Active == nil {
		writeGenericNotFound(w)
		return
	}
	h.Active.ServeHTTP(w, r)
}
