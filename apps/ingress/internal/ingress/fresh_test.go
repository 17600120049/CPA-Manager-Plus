package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFreshPrivateProjectionFencesForeignOwnerAndRetainsCommit(t *testing.T) {
	f := &FreshRoutes{}
	h := f.ControlHandler(func() (string, error) { return "internal-secret", nil })
	s := FreshRouteState{InstallationID: "install", Generation: "gen", OperationID: "fresh-op", Revision: 1, Target: "/private-control"}
	put := func(state FreshRouteState, auth string) int {
		data, _ := json.Marshal(state)
		r := httptest.NewRequest("PUT", FreshRoutingControlPath, bytes.NewReader(data))
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if put(s, "Bearer wrong") != 401 || f.Active() {
		t.Fatal("unauthorized apply")
	}
	if put(s, "Bearer internal-secret") != 200 || !f.Matches(s.Target+"/api/v2/operation") || f.Matches(s.Target+"-alias") {
		t.Fatal("matching checkpoint failed")
	}
	foreign := s
	foreign.OperationID = "new-prefix-operation"
	if put(foreign, "Bearer internal-secret") != 409 {
		t.Fatal("foreign owner accepted")
	}
	foreign = s
	foreign.Generation = "new-gen"
	if put(foreign, "Bearer internal-secret") != 409 {
		t.Fatal("foreign generation accepted")
	}
	foreign = s
	foreign.Target = "/other"
	if put(foreign, "Bearer internal-secret") != 409 {
		t.Fatal("foreign target accepted")
	}
	s.Committed = true
	if put(s, "Bearer internal-secret") != 200 {
		t.Fatal("commit acknowledgement rejected")
	}
	s.Committed = false
	if put(s, "Bearer internal-secret") != 409 {
		t.Fatal("retired state reopened")
	}
	for _, target := range []string{"/", "/v1", "/.well-known/cpamp-bootstrap/nested", "/health", "/other/../control", "/control%2falias"} {
		empty := &FreshRoutes{}
		bad := s
		bad.Target = target
		data, _ := json.Marshal(bad)
		r := httptest.NewRequest("PUT", FreshRoutingControlPath, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer internal-secret")
		w := httptest.NewRecorder()
		empty.ControlHandler(func() (string, error) { return "internal-secret", nil }).ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(target, w.Code)
		}
	}
}

func TestFreshPublicGatewayPreservesDefaultUntilFreshProjectionAndFixedNamespaceCaptured(t *testing.T) {
	var managerHits, gatewayHits int
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { managerHits++; w.WriteHeader(404) }))
	defer manager.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gatewayHits++; w.WriteHeader(200) }))
	defer gateway.Close()
	m, _ := url.Parse(manager.URL)
	g, _ := url.Parse(gateway.URL)
	routes := &FreshRoutes{}
	h := NewFreshHandler(routes, m, g, log.New(io.Discard, "", 0))
	for _, p := range []string{FixedBootstrapDiscoveryPath, FixedBootstrapDiscoveryPath + "/ui", FixedBootstrapDiscoveryPath + "/assets/a-AbCd1234.js", FixedBootstrapDiscoveryPath + "/api/v2/admin/commit", FixedBootstrapDiscoveryPath + "/unknown", FixedBootstrapDiscoveryPath + "%2funknown", FixedBootstrapDiscoveryPath + "%252funknown"} {
		r := httptest.NewRequest("POST", p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 || w.Header().Get("X-CPAMP-Route-Owner") != "manager" {
			t.Fatal(p, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer admin-or-management-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("X-CPAMP-Route-Owner") != "gateway" || gatewayHits != 1 {
		t.Fatal("default Gateway fallback regressed without Fresh routing ownership")
	}
	// Once the Fresh operation publishes its routing projection, product
	// initialization and a running CPA still do not prove data-plane client
	// authentication. Keep Gateway closed until a later qualified consumer.
	routes.state = FreshRouteState{InstallationID: "i", Generation: "g", OperationID: "op", Revision: 1, Target: "/private", Committed: true}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || gatewayHits != 1 {
		t.Fatal("Fresh routing ownership activated Gateway")
	}
	if managerHits != 7 {
		t.Fatal(managerHits)
	}
}
