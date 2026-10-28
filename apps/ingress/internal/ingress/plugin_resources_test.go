package ingress

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Plugin resources must be owned by the Manager, never forwarded to the model
// Gateway: the Manager is the only component that can rewrite the response into
// an opaque-origin sandbox document.
func TestPluginResourceOwnedByManager(t *testing.T) {
	classifier := NewClassifier(ControlMatcherFunc(func(path string) bool { return false }))
	cases := []struct {
		name   string
		method string
		target string
		want   Route
	}{
		{"plugin page", http.MethodGet, "/v0/resource/plugins/demo/index.html", RouteManager},
		{"plugin asset", http.MethodGet, "/v0/resource/plugins/demo/assets/app.js", RouteManager},
		{"plugin head", http.MethodHead, "/v0/resource/plugins/demo/index.html", RouteManager},
		{"plugin with query", http.MethodGet, "/v0/resource/plugins/demo/index.html?v=7", RouteManager},
		// A method that could not have come from a document load is denied rather
		// than forwarded; the transport edge will not prove the method upstream.
		{"plugin post", http.MethodPost, "/v0/resource/plugins/demo/index.html", RouteDeny},
		{"plugin put", http.MethodPut, "/v0/resource/plugins/demo/index.html", RouteDeny},
		// Malformed aliases must not become Manager or Gateway traffic.
		{"encoded separator", http.MethodGet, "/v0/resource/plugins%2fdemo/index.html", RouteDeny},
		{"dot segment", http.MethodGet, "/v0/resource/plugins/../demo/index.html", RouteDeny},
		{"doubled separator", http.MethodGet, "/v0/resource/plugins//demo/index.html", RouteDeny},
		{"trailing separator", http.MethodGet, "/v0/resource/plugins/", RouteDeny},
		{"semicolon parameter", http.MethodGet, "/v0/resource/plugins;x/demo", RouteDeny},
		// Ordinary Gateway traffic is unchanged.
		{"chat", http.MethodPost, "/v1/chat/completions", RouteGateway},
		{"models", http.MethodGet, "/v1/models", RouteGateway},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.target, nil)
			if got := classifier.ClassifyRequest(request); got != testCase.want {
				t.Fatalf("route = %v, want %v", got, testCase.want)
			}
		})
	}
}

// A path-only query carries no method proof, so it must never resolve to
// Manager ownership even when the Control matcher would accept the prefix.
func TestPluginResourcePathOnlyIsDenied(t *testing.T) {
	classifier := NewClassifier(ControlMatcherFunc(func(path string) bool { return true }))
	for _, target := range []string{"/v0/resource/plugins/demo/index.html", "/V0/RESOURCE/PLUGINS/demo/index.html"} {
		if got := classifier.Classify(target); got != RouteDeny {
			t.Fatalf("Classify(%q) = %v, want RouteDeny", target, got)
		}
	}
}

// End to end through the transport handler: the Manager upstream receives the
// plugin resource and the Gateway upstream never does.
func TestPluginResourceNeverReachesGateway(t *testing.T) {
	var managerHits, gatewayHits int
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		managerHits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("sandbox-document"))
	}))
	defer manager.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	managerTarget, err := url.Parse(manager.URL)
	if err != nil {
		t.Fatal(err)
	}
	gatewayTarget, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		NewClassifier(ControlMatcherFunc(func(path string) bool { return false })),
		managerTarget, gatewayTarget, log.New(io.Discard, "", 0),
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v0/resource/plugins/demo/index.html", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", recorder.Code, recorder.Body.String())
	}
	if managerHits != 1 {
		t.Fatalf("manager hits = %d, want 1", managerHits)
	}
	if gatewayHits != 0 {
		t.Fatalf("plugin resource reached the gateway %d times", gatewayHits)
	}
	if got := recorder.Header().Get("X-CPAMP-Route-Owner"); got != "manager" {
		t.Fatalf("route owner = %q", got)
	}
}
