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

func TestRouterDiscoverySeamKeepsAllMethodsManagerOwned(t *testing.T) {
	active := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := NewWithBootstrapDiscovery(
		&app.Context{},
		func() routingdomain.DiscoveryLifecycle { return routingdomain.DiscoveryActive },
		active,
	)
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "TRACE"} {
		req := httptest.NewRequest(method, "http://example.test"+routingdomain.BootstrapDiscoveryPath, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		switch method {
		case http.MethodGet:
			if res.Code != http.StatusTeapot {
				t.Fatalf("GET status = %d, want delegated Manager response", res.Code)
			}
		case http.MethodOptions:
			if res.Code != http.StatusNoContent {
				t.Fatalf("OPTIONS status = %d, want 204", res.Code)
			}
		default:
			if res.Code == http.StatusTeapot {
				t.Fatalf("%s reached active GET handler", method)
			}
		}
	}
}

func TestRouterCompletedDiscoveryIsGeneric404ForAllMethods(t *testing.T) {
	handler := NewWithBootstrapDiscovery(
		&app.Context{},
		func() routingdomain.DiscoveryLifecycle { return routingdomain.DiscoveryCompleted },
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	)
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "http://example.test"+routingdomain.BootstrapDiscoveryPath, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound || res.Header().Get("Location") != "" {
			t.Fatalf("%s completed response = %d, Location=%q", method, res.Code, res.Header().Get("Location"))
		}
	}
}
