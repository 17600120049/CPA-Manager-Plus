package ingress

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestPAHS0ResourceRouteOwner(t *testing.T) {
	legacy := NewTransitionalClassifier()
	c := NewPAHS0Classifier(legacy.control)
	cases := []struct {
		method, target string
		want Route
	}{
		{"GET", "/v0/resource/plugins", RouteManager},
		{"HEAD", "/v0/resource/plugins/demo/logo.png", RouteManager},
		{"POST", "/v0/resource/plugins/demo/page", RouteDeny},
		{"GET", "/v0/resource/plugins/", RouteDeny},
		{"GET", "/v0/resource/plugins//demo", RouteDeny},
		{"GET", "/v0/resource/plugins/../demo", RouteDeny},
		{"GET", "/v0/resource/plugins%2fdemo", RouteDeny},
		{"GET", "/v0/resource/plugins%252fdemo", RouteDeny},
		{"GET", "/v0/resource%2fplugins/demo", RouteDeny},
		{"GET", "/v0/resource/plugins\\demo", RouteDeny},
		{"GET", "/V0/RESOURCE/PLUGINS/demo", RouteDeny},
		{"GET", "/v0/resource/plugins;param", RouteDeny},
		{"GET", "/v0/resource/plugins-other", RouteGateway},
		{"GET", "/v0/management/plugins", RouteManager},
		{"GET", "/v1/models", RouteGateway},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			if got := c.ClassifyRequest(r); got != tc.want {
				t.Fatalf("route=%d want=%d", got, tc.want)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/v0/resource/plugins/demo", nil)
	request.RequestURI = "/v0/resource/plugins%2fdemo"
	if got := c.ClassifyRequest(request); got != RouteDeny {
		t.Fatalf("RequestURI disagreement routed as %d", got)
	}
	for _, target := range []string{pahS0GuardPath, "/internal%2fpah-s0/resource-guard", "/internal%252fpah-s0/resource-guard"} {
		if got := c.ClassifyRequest(httptest.NewRequest("GET", target, nil)); got != RouteDeny {
			t.Fatalf("private probe leaked through Ingress: %s", target)
		}
	}
}

func TestPAHS0IngressProbeFailureCannotReachManagerOrGateway(t *testing.T) {
	var managerHits, gatewayHits atomic.Int32
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		managerHits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer manager.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayHits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer gateway.Close()
	m, _ := url.Parse(manager.URL)
	g, _ := url.Parse(gateway.URL)
	h := NewFreshHandler(&FreshRoutes{}, m, g, log.New(io.Discard, "", 0), func(context.Context) bool { return true })
	var ready atomic.Bool
	h.EnablePAHS0(func(context.Context) bool { return ready.Load() })
	for _, target := range []string{"/v0/resource/plugins/demo", "/v0/resource/plugins%2fdemo"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != http.StatusNotFound || managerHits.Load() != 0 || gatewayHits.Load() != 0 {
			t.Fatalf("unready request reached backend: %s status=%d", target, w.Code)
		}
	}
	ready.Store(true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("HEAD", "/v0/resource/plugins/demo", nil))
	if w.Code != http.StatusNotFound || managerHits.Load() != 1 || gatewayHits.Load() != 0 {
		t.Fatalf("canonical request not owned by Manager: %d", w.Code)
	}
	ready.Store(false) // Manager-side guard lost during a single-side restart.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v0/resource/plugins/demo", nil))
	if w.Code != http.StatusNotFound || managerHits.Load() != 1 || gatewayHits.Load() != 0 {
		t.Fatalf("restart bypass: %d", w.Code)
	}
}

func TestOrdinaryLegacyResourceFallbackSurvivesManagerOutage(t *testing.T) {
	var gatewayHits atomic.Int32
	manager := httptest.NewServer(http.NotFoundHandler())
	defer manager.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gatewayHits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer gateway.Close()
	m, _ := url.Parse(manager.URL)
	g, _ := url.Parse(gateway.URL)
	// The caller has already received its positive Legacy observation. The
	// current Manager may be temporarily unavailable: ordinary deployments
	// retain the historical passthrough for resource URLs.
	h := NewFreshHandler(&FreshRoutes{}, m, g, log.New(io.Discard, "", 0),
		func(context.Context) bool { return true })
	for _, target := range []string{
		"/v0/resource/plugins/logo.png", "/v0/resource/plugins%2fpage",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusTeapot {
			t.Fatalf("legacy plugin resource %s regressed: %d", target, w.Code)
		}
	}
	if got := gatewayHits.Load(); got != 2 {
		t.Fatalf("ordinary Legacy gateway passthrough missing: %d", got)
	}
}
