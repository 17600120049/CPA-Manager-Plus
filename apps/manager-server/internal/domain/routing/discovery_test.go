package routing

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryHandlerActiveOwnsMethods(t *testing.T) {
	active := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := DiscoveryHandler{
		Lifecycle: func() DiscoveryLifecycle { return DiscoveryActive },
		Active:    active,
	}
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "TRACE"} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "http://example.test"+BootstrapDiscoveryPath, nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if method == http.MethodGet && res.Code != http.StatusTeapot {
				t.Fatalf("GET status = %d, want delegated response", res.Code)
			}
			if method == http.MethodOptions && res.Code != http.StatusNoContent {
				t.Fatalf("OPTIONS status = %d, want 204", res.Code)
			}
			if method != http.MethodGet && method != http.MethodOptions && res.Code == http.StatusTeapot {
				t.Fatal("unsupported method reached active GET handler")
			}
		})
	}
}

func TestDiscoveryHandlerCompletedIsGenericForEveryMethod(t *testing.T) {
	handler := DiscoveryHandler{Lifecycle: func() DiscoveryLifecycle { return DiscoveryCompleted }}
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "TRACE"} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "http://example.test"+BootstrapDiscoveryPath, nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want generic 404", res.Code)
			}
			if got := res.Header().Get("Location"); got != "" {
				t.Fatalf("completed discovery redirected to %q", got)
			}
			body, _ := io.ReadAll(res.Result().Body)
			for _, forbidden := range []string{"controlBasePath", "api/v2", "completed", "cpamp"} {
				if strings.Contains(strings.ToLower(string(body)), strings.ToLower(forbidden)) {
					t.Fatalf("completed body leaked %q: %q", forbidden, body)
				}
			}
		})
	}
}

func TestProtectFixedDiscoveryRejectsRequestTargetAliasesBeforeServeMux(t *testing.T) {
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusTeapot)
	})
	discovery := DiscoveryHandler{Lifecycle: func() DiscoveryLifecycle { return DiscoveryActive }}
	handler := ProtectFixedDiscovery(next, discovery)
	for _, target := range []string{
		BootstrapDiscoveryPath + "/",
		"/.well-known//cpamp-bootstrap",
		"/.well-known/./cpamp-bootstrap",
		"/.well-known/../.well-known/cpamp-bootstrap",
		"/.well-known%2Fcpamp-bootstrap",
		"/.well-known%252Fcpamp-bootstrap",
		"/%2Ewell-known/cpamp-bootstrap",
		"/%252Ewell-known/cpamp-bootstrap",
		"/.well-known\\cpamp-bootstrap",
	} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.test"+target, nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d, want 404", target, res.Code)
			}
			if res.Header().Get("Location") != "" {
				t.Fatalf("%s redirected", target)
			}
		})
	}
	if called != 0 {
		t.Fatalf("ambiguous discovery candidates reached next handler %d times", called)
	}
}

func TestProtectFixedDiscoveryLeavesExplicitGatewayPathsAlone(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	discovery := DiscoveryHandler{Lifecycle: func() DiscoveryLifecycle { return DiscoveryActive }}
	handler := ProtectFixedDiscovery(next, discovery)
	for _, target := range []string{"/v1/responses", "/.well-known/cpamp-bootstrapper", "/models"} {
		req := httptest.NewRequest(http.MethodPost, "http://example.test"+target, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusTeapot {
			t.Fatalf("%s status = %d, want next handler", target, res.Code)
		}
	}
}

func TestCanonicalDiscoveryRequestRejectsRawPathMismatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.test"+BootstrapDiscoveryPath, nil)
	req.URL.RawPath = "/.well-known%2Fcpamp-bootstrap"
	if canonicalDiscoveryRequest(req) {
		t.Fatal("raw path alias accepted")
	}
}
