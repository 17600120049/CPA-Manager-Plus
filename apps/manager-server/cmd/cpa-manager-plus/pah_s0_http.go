package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/router"
)

// Only a missing database or a pre-admitted Fresh database may use P-S0.
func validatePAHS0FreshSource(database string) error {
	if freshMarkerPresent(database) {
		if _, err := os.Lstat(database); err != nil {
			return fmt.Errorf("Fresh marker without readable database: %w", err)
		}
		return nil // Strict marker and database validation happens in fresh.Open.
	}
	if _, err := os.Lstat(database); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect P-S0 source: %w", err)
	}
	return errors.New("P-S0 cannot open Legacy or unknown existing Manager store")
}

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
		if h.fresh.Load() != nil &&
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
