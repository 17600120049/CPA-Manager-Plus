package main

import (
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/router"
)

// PAH-S0 is enforced at the shared Manager process boundary, before the
// Legacy/Fresh selection. Fresh never mounts the Legacy http/router handler.
func (h *startupHTTPHandler) EnablePAHS0(enabled bool) {
	h.pahS0 = enabled
}

func (h *startupHTTPHandler) servePAHS0(w http.ResponseWriter, r *http.Request) bool {
	if !h.pahS0 || r == nil || r.URL == nil {
		return false
	}
	if r.URL.Path == "/internal/pah-s0/resource-guard" {
		w.Header().Set("Cache-Control", "no-store")
		if (h.ready.Load() || h.fresh.Load() != nil) &&
			r.Method == http.MethodGet && r.URL.EscapedPath() == r.URL.Path &&
			r.URL.RawQuery == "" && (r.RequestURI == "" || r.RequestURI == r.URL.Path) {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.NotFound(w, r)
		}
		return true
	}
	if router.PAHS0ResourceRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return true
	}
	return false
}
