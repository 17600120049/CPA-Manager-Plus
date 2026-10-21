package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPAHS0FreshApplicationDeniesResourcesAndAttestsGuard(t *testing.T) {
	a := &freshApplication{pahS0: true}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
		for _, target := range []string{
			"/v0/resource/plugins/demo/page.html",
			"/v0/resource/plugins/demo/image.png",
			"/v0/resource/plugins%2fdemo",
			"/v0/resource/plugins%252fdemo",
			"/v0/resource/plugins/../demo",
		} {
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest(method, target, nil))
			if w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" ||
				w.Header().Get("Location") != "" {
				t.Fatalf("%s %s: status=%d", method, target, w.Code)
			}
		}
	}
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
		a.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, nil))
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status=%d want %d", tc.method, tc.target, w.Code, tc.want)
		}
	}
	// Missing Manager-side opt-in must not be mistaken for a resource guard.
	w := httptest.NewRecorder()
	(&freshApplication{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/pah-s0/resource-guard", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unprotected Fresh Manager affirmed guard: %d", w.Code)
	}
}
