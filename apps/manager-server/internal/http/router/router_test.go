package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	routingdomain "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/routing"
)

func TestRouterCapturesFixedDiscoveryBeforeMuxNormalization(t *testing.T) {
	handler := New(&app.Context{})
	for _, target := range []string{
		routingdomain.BootstrapDiscoveryPath,
		routingdomain.BootstrapDiscoveryPath + "/",
		"/.well-known//cpamp-bootstrap",
		"/.well-known%2Fcpamp-bootstrap",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example.test"+target, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want generic 404", target, res.Code)
		}
		if res.Header().Get("Location") != "" {
			t.Fatalf("%s redirected", target)
		}
	}
}

func TestLegacyRouterExplicitModeAcknowledgement(t *testing.T) {
	h := New(&app.Context{})
	for _, tc := range []struct {
		method, target string
		code           int
	}{
		{"GET", "/internal/routing/fresh-authority", 204},
		{"PUT", "/internal/routing/fresh-authority", 405},
		{"GET", "/internal/routing/fresh-authority?legacy=true", 404},
		{"GET", "/internal/routing/fresh%2dauthority", 404},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, nil))
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc, w.Code, w.Header())
		}
	}
}

// A plugin resource answers its own CORS contract: the proxy sets exactly one
// Access-Control-Allow-Origin on the rewritten sandbox response. Wrapping the
// same route in WithCORS as well put a second value on the response, and a
// browser refuses any response carrying two, so the sandboxed plugin document
// could not read its own resource. The route must not write CORS itself.
func TestPluginResourceRouteLeavesCORSHeadersToTheProxy(t *testing.T) {
	appCtx := &app.Context{}
	appCtx.Config.CORSOrigins = []string{"*"}
	handler := New(appCtx)

	// No deployment or admin session exists here, so the handler refuses well
	// before any upstream call. That is the point: whatever CORS header is
	// present came from the router, not from a proxied CPA response.
	req := httptest.NewRequest(http.MethodGet, "http://example.test/v0/resource/plugins/isolation-probe/probe", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Dest", "iframe")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if got := recorder.Header().Values("Access-Control-Allow-Origin"); len(got) != 0 {
		t.Fatalf("router wrote Access-Control-Allow-Origin for a plugin resource: %v", got)
	}

	// A normal admin route keeps the deployment's CORS answer.
	adminReq := httptest.NewRequest(http.MethodGet, "http://example.test/status", nil)
	adminRecorder := httptest.NewRecorder()
	handler.ServeHTTP(adminRecorder, adminReq)
	if got := adminRecorder.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "*" {
		t.Fatalf("admin route CORS = %v, want [\"*\"]", got)
	}
}
