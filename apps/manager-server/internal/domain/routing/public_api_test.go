package routing

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicAPIHandlerRemainsInactiveWithoutVerifiedAuthority(t *testing.T) {
	for _, snapshot := range []RoutingSnapshot{
		{DesiredControlBasePath: "/desired", State: SnapshotDesiredOnly, Revision: 2},
		{VerifiedEffectiveControlBasePath: "/old", State: SnapshotStale, Revision: 2},
		{State: SnapshotUnknown},
		{State: SnapshotUnavailable},
	} {
		handler := PublicAPIHandler{Snapshot: snapshot, Active: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})}
		req := httptest.NewRequest(http.MethodGet, "http://example.test/control/api/v2/models", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("inactive snapshot status = %d, want 404", res.Code)
		}
	}
}

func TestPublicAPIHandlerDelegatesOnlyCanonicalVerifiedNamespace(t *testing.T) {
	handler := PublicAPIHandler{
		Snapshot: verifiedSnapshot(),
		Active: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/control-old/api/v2/models" {
				t.Fatalf("unexpected delegated path %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusTeapot)
		}),
	}
	for _, target := range []string{
		"/control-old/api/v2/models",
		"/api/v2/models",
		"/control-new/api/v2/models",
		"/control-old/api/v2/",
		"/control-old/api/v2%2fmodels",
		"/control-old/api/v2ish/models",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example.test"+target, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if target == "/control-old/api/v2/models" {
			if res.Code != http.StatusTeapot {
				t.Fatalf("canonical path status = %d, want delegated response", res.Code)
			}
		} else if res.Code != http.StatusNotFound {
			t.Fatalf("ambiguous/non-canonical path %q status = %d, want 404", target, res.Code)
		}
	}
}
