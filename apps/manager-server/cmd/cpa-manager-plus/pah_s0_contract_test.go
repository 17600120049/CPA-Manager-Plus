package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
)

// A fake actively serving plugin resource path distinguishes a genuine
// pre-upstream denial from a harmless 404 produced by CPA plugins.enabled=false.
// This is a source-layer witness; actual Host/browser evidence remains STOP.
func TestPAHS0ActivePluginResourceNeverReachesUpstream(t *testing.T) {
	var hits atomic.Int32
	h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
	h.EnablePAHS0(true)
	h.SetFreshHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/v0/resource/plugins/demo/index.html" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<script>pluginExecutor()</script>"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		for _, target := range []string{
			"/v0/resource/plugins/demo/index.html",
			"/v0/resource/plugins/demo/logo.png",
			"/v0/resource/plugins%2fdemo/index.html",
			"/v0/resource/plugins/../demo",
		} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, target, nil)
			r.Header.Set("Authorization", "Bearer fake-Admin")
			h.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound || w.Body.String() == "<script>pluginExecutor()</script>" {
				t.Fatalf("%s %s leaked executable response, status=%d", method, target, w.Code)
			}
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("plugin resource called upstream %d times", got)
	}
	for _, target := range []string{"/health", "/v0/management/config", "/v1/models"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("normal route %s regressed: %d", target, w.Code)
		}
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("normal routes failed to reach Manager: %d", got)
	}
}
