package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"os"
	"path/filepath"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
)

func TestPAHS0FreshStartupHandlerDeniesResourcesAndAttestsOnlyAfterReadiness(t *testing.T) {
	h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
	h.EnablePAHS0(true)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
		for _, target := range []string{
			"/v0/resource/plugins/demo/page.html",
			"/v0/resource/plugins/demo/image.png",
			"/v0/resource/plugins%2fdemo",
			"/v0/resource/plugins%252fdemo",
			"/v0/resource/plugins/../demo",
		} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
			if w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" ||
				w.Header().Get("Location") != "" {
				t.Fatalf("%s %s: status=%d", method, target, w.Code)
			}
		}
	}
	preReady := httptest.NewRecorder()
	h.ServeHTTP(preReady, httptest.NewRequest(http.MethodGet, "/internal/pah-s0/resource-guard", nil))
	if preReady.Code != http.StatusNotFound {
		t.Fatalf("unready Manager attested guard: %d", preReady.Code)
	}
	// Fresh selects this target instead of the Legacy router; P-S0 remains
	// an outer security layer regardless of which application is active.
	h.SetFreshHandler(http.NotFoundHandler())
	for _, tc := range []struct {
		method, target string
		want           int
	}{
		{http.MethodGet, "/internal/pah-s0/resource-guard", http.StatusNoContent},
		{http.MethodHead, "/internal/pah-s0/resource-guard", http.StatusNotFound},
		{http.MethodGet, "/internal/pah-s0/resource-guard?x=1", http.StatusNotFound},
		{http.MethodGet, "/internal/pah-s0/resource%2dguard", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, nil))
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status=%d want %d", tc.method, tc.target, w.Code, tc.want)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v0/resource/plugins/demo", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("ready Fresh Manager forwarded resource: %d", w.Code)
	}
}

func TestPAHS0AdmitsOnlyFreshOrMissingStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite")
	if err := validatePAHS0FreshSource(path); err != nil {
		t.Fatalf("Fresh New missing store rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePAHS0FreshSource(path); err == nil {
		t.Fatal("existing Legacy or unknown store admitted to P-S0")
	}
	if err := os.WriteFile(path+".manager.lock", []byte("unverified"), 0600); err != nil {
		t.Fatal(err)
	}
	// This only admits the marker-shaped path; fresh.Open still must
	// validate authority and metadata before enabling the private guard.
	if err := validatePAHS0FreshSource(path); err != nil {
		t.Fatalf("Fresh marker blocked before strict fresh.Open: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := validatePAHS0FreshSource(path); err == nil {
		t.Fatal("marker without database admitted")
	}
}
