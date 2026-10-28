package ingress

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		// The plugin resource namespace is read-only upstream, so a write is
		// denied rather than forwarded to a 404. Codex Invite's POST lives on a
		// management route, not under the resource prefix.
		{"plugin post", http.MethodPost, "/v0/resource/plugins/demo/index.html", RouteDeny},
		{"plugin put", http.MethodPut, "/v0/resource/plugins/demo/index.html", RouteDeny},
		{"plugin patch", http.MethodPatch, "/v0/resource/plugins/demo/index.html", RouteDeny},
		{"plugin delete", http.MethodDelete, "/v0/resource/plugins/demo/index.html", RouteDeny},
		{"codex invite resource post", http.MethodPost, "/v0/resource/plugins/codex-invite/invite", RouteDeny},
		{"plugin trace", http.MethodTrace, "/v0/resource/plugins/demo/index.html", RouteDeny},
		{"plugin options", http.MethodOptions, "/v0/resource/plugins/demo/index.html", RouteDeny},
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

// A write under the resource prefix is denied at the transport rather than
// forwarded, because CPA serves that namespace read-only. Nothing reaches either
// upstream, so a plugin cannot use the prefix to reach the Gateway by method.
func TestPluginResourceWriteIsDeniedAtTransport(t *testing.T) {
	var managerHits, gatewayHits int
	for _, target := range []string{
		"/v0/resource/plugins/isolation-probe/probe",
		"/v0/resource/plugins/codex-invite/invite",
	} {
		manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			managerHits++
			w.WriteHeader(http.StatusOK)
		}))
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gatewayHits++
			w.WriteHeader(http.StatusOK)
		}))
		managerTarget, _ := url.Parse(manager.URL)
		gatewayTarget, _ := url.Parse(gateway.URL)
		handler := NewHandler(
			NewClassifier(ControlMatcherFunc(func(path string) bool { return false })),
			managerTarget, gatewayTarget, log.New(io.Discard, "", 0),
		)
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		// The transport edge answers a denial with 404 rather than a status that
		// would confirm which paths exist.
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404", target, recorder.Code)
		}
		manager.Close()
		gateway.Close()
	}
	if managerHits != 0 || gatewayHits != 0 {
		t.Fatalf("a denied write reached an upstream (manager=%d gateway=%d)", managerHits, gatewayHits)
	}
}

// A plugin write with a malformed shape is still a denial: the write contract
// widens the method set, not the path shape.
func TestPluginResourceWriteWithMalformedPathIsDenied(t *testing.T) {
	classifier := NewClassifier(ControlMatcherFunc(func(path string) bool { return false }))
	cases := []string{
		"/v0/resource/plugins%2fdemo/invite",
		"/v0/resource/plugins//demo/invite",
		"/v0/resource/plugins/../demo/invite",
	}
	for _, target := range cases {
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader("{}"))
		if got := classifier.ClassifyRequest(request); got != RouteDeny {
			t.Fatalf("POST %q route = %v, want RouteDeny", target, got)
		}
	}
}
