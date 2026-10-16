package fullweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/routing"
)

func TestNonRootPathnameStaticAPIAndFailureOwnership(t *testing.T) {
	snapshot := routing.RoutingSnapshot{State: routing.SnapshotVerifiedEffective, VerifiedEffectiveControlBasePath: "/phase4-test", Revision: 1}
	apiPath, _ := snapshot.CanonicalPublicAPIPath("technical-probe")
	h := Handler{Snapshot: snapshot, Dist: fstest.MapFS{
		"index.html":                {Data: []byte(`<html><script src="./assets/index-AbCd1234.js"></script><script type="application/json">__CPAMP_ROUTING_CONTEXT__</script></html>`)},
		"assets/index-AbCd1234.js":  {Data: []byte(`console.log('technical carrier')`)},
		"assets/index-AbCd1234.css": {Data: []byte(`body{color:black}`)},
	}, API: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != apiPath {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"technical":true}`))
	})}
	for _, item := range []struct {
		path, mime, cache string
		status            int
	}{
		{"/phase4-test/technical-route", "text/html", "no-cache", 200},
		{"/phase4-test/technical-route", "text/html", "no-cache", 200}, // direct refresh
		{"/phase4-test", "text/html", "no-cache", 200},
		{"/phase4-test/", "text/html", "no-cache", 200},
		{"/phase4-test/assets/index-AbCd1234.js", "javascript", "immutable", 200},
		{"/phase4-test/assets/index-AbCd1234.css", "text/css", "immutable", 200},
		{apiPath, "application/json", "no-cache", 200},
		{"/phase4-test/api/v2/missing", "application/json", "no-cache", 404},
		{"/phase4-test/api/v1/missing", "text/plain", "no-cache", 404},
		{"/phase4-test/api", "text/plain", "no-cache", 404},
		{"/phase4-test/api//v2/test", "text/plain", "no-cache", 404},
		{"/phase4-test/api%2fv2/test", "text/plain", "no-cache", 404},
		{"/phase4-test/assets/missing-AbCd1234.js", "text/plain", "no-cache", 404},
		{"/phase4-test/assets/missing.png", "text/plain", "no-cache", 404},
		{"/phase4-test/assets", "text/plain", "no-cache", 404},
		{"/phase4-test/missing.svg", "text/plain", "no-cache", 404},
		{"/phase4-test//technical-route", "text/plain", "no-cache", 404},
		{"/phase4-test/technical-route/", "text/plain", "no-cache", 404},
		{"/phase4-test/../v1/models", "text/plain", "no-cache", 404},
		{"/phase4-test-lookalike/technical-route", "text/plain", "no-cache", 404},
	} {
		t.Run(item.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", item.path, nil)
			r.Header.Set("Accept", "text/html") // API/asset failure must survive HTML Accept
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != item.status || !strings.Contains(w.Header().Get("Content-Type"), item.mime) || !strings.Contains(w.Header().Get("Cache-Control"), item.cache) {
				t.Fatalf("response %d %v %s", w.Code, w.Header(), w.Body.String())
			}
			if w.Header().Get("Location") != "" {
				t.Fatal("unexpected redirect")
			}
			if item.status == 404 && strings.Contains(w.Body.String(), "<html>") {
				t.Fatal("SPA swallowed failure")
			}
			if item.mime == "text/html" {
				if !strings.Contains(w.Body.String(), `src="/phase4-test/assets/index-AbCd1234.js"`) || !strings.Contains(w.Body.String(), `"verifiedEffectiveControlBasePath":"/phase4-test"`) {
					t.Fatal(w.Body.String())
				}
			}
		})
	}
	r := httptest.NewRequest("GET", "/phase4-test/technical-route", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("non-navigation received HTML")
	}
}

func TestStaticMountRequiresVerifiedAuthority(t *testing.T) {
	for _, state := range []routing.SnapshotState{routing.SnapshotUnknown, routing.SnapshotUnavailable, routing.SnapshotDesiredOnly, routing.SnapshotStale, routing.SnapshotVerifiedEffective} {
		h := Handler{Snapshot: routing.RoutingSnapshot{State: state, DesiredControlBasePath: "/phase4-test"}}
		r := httptest.NewRequest("GET", "/phase4-test/technical-route", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("state %s activated static", state)
		}
	}
}
