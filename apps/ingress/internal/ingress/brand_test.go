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

func TestStaticAPIFailuresNeverFallThroughToGateway(t *testing.T) {
	managerCalls, gatewayCalls := 0, 0
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		managerCalls++
		w.Header().Set("X-Test-Owner", "manager")
		http.NotFound(w, r)
	}))
	defer manager.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayCalls++
		w.Header().Set("X-Test-Owner", "gateway")
		w.WriteHeader(204)
	}))
	defer gateway.Close()
	managerURL, _ := url.Parse(manager.URL)
	gatewayURL, _ := url.Parse(gateway.URL)
	// Non-production carrier of the accepted segment-prefix matcher contract.
	segment := func(prefix string) ControlMatcherFunc {
		return func(p string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }
	}
	h := NewHandler(NewOwnershipClassifier(segment("/phase4-test"), segment("/retired")), managerURL, gatewayURL, log.New(io.Discard, "", 0))
	for _, p := range []string{"/phase4-test/technical-route", "/phase4-test/assets/missing-AbCd1234.js", "/phase4-test/missing.svg", "/phase4-test/api/v2/missing", "/phase4-test/api//v2/probe", "/phase4-test%2fapi/v2/probe", "/favicon.ico", "/apple-touch-icon.png"} {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("Authorization", "Bearer synthetic-full-web-admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 || w.Header().Get("X-Test-Owner") != "manager" || gatewayCalls != 0 {
			t.Fatalf("%s %d %v", p, w.Code, w.Header())
		}
	}
	before := managerCalls
	for _, p := range []string{"/retired/api/v2/probe", "/retired%2fapi/v2/probe"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 || gatewayCalls != 0 || managerCalls != before {
			t.Fatal("retired namespace forwarded")
		}
	}
	for _, p := range []string{"/v1/models", "/favicon.ico-lookalike", "/%66avicon.ico"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 204 || w.Header().Get("X-Test-Owner") != "gateway" {
			t.Fatal("normal Gateway ownership changed")
		}
	}
}

func TestBrandExceptionsAreExactAndMethodAware(t *testing.T) {
	for _, active := range []bool{false, true} {
		c := NewOwnershipClassifier(ControlMatcherFunc(func(string) bool { return active }), nil)
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "OPTIONS"} {
			for _, p := range []string{"/favicon.ico", "/apple-touch-icon.png", "/%66avicon.ico", "/favicon%2eico", "/favicon.ico/subpath", "/favicon.ico/", "/favicon.ico-lookalike", "/apple-touch-icon.png%2fsubpath", "/x/../favicon.ico"} {
				r := httptest.NewRequest(method, p, nil)
				want := RouteGateway
				if active || (method == "GET" || method == "HEAD") && (p == "/favicon.ico" || p == "/apple-touch-icon.png") {
					want = RouteManager
				}
				if got := c.ClassifyRequest(r); got != want {
					t.Fatalf("%s %s got %d want %d", method, p, got, want)
				}
			}
		}
	}
	// A negative brand variant still observes normal retired-Control denial.
	c := NewOwnershipClassifier(ControlMatcherFunc(func(string) bool { return false }), ControlMatcherFunc(func(string) bool { return true }))
	if c.ClassifyRequest(httptest.NewRequest(http.MethodPost, "/favicon.ico", nil)) != RouteDeny {
		t.Fatal("normal retired owner bypassed")
	}
}
