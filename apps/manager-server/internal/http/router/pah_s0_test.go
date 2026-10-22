package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
)

func TestPAHS0ManagerPreUpstreamDeny(t *testing.T) {
	h := New(&app.Context{Config: config.Config{PAHS0Enabled: true}})
	for _, method := range []string{"GET", "HEAD", "POST", "OPTIONS"} {
		for _, target := range []string{
			"/v0/resource/plugins/demo/index.html",
			"/v0/resource/plugins/demo/image.png",
			"/v0/resource/plugins%2fdemo/index.html",
			"/v0/resource/plugins%252fdemo",
			"/v0/resource/plugins/../demo",
		} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
			if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
				t.Fatalf("%s %s status=%d location=%q", method, target, w.Code, w.Header().Get("Location"))
			}
		}
	}
	ready := httptest.NewRecorder()
	h.ServeHTTP(ready, httptest.NewRequest("GET", "/internal/pah-s0/resource-guard", nil))
	if ready.Code != http.StatusNoContent || ready.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Manager guard not active: %d", ready.Code)
	}
	disabled := New(&app.Context{Config: config.Config{}})
	w := httptest.NewRecorder()
	disabled.ServeHTTP(w, httptest.NewRequest("GET", "/internal/pah-s0/resource-guard", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled Manager attested guard: %d", w.Code)
	}
}
