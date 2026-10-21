package routing

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

// The same fixture is consumed by the Full Web transport test. This probes
// the Manager's actual HTTP routing boundary, not an activated API backend.
// Full Web/API functional qualification remains separate.
func TestR1EmbeddedPublicAPITransportFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../../../tests/fixtures/r1-embedded-api-transport.json")
	if errors.Is(err, os.ErrNotExist) {
		// Packaged Docker smoke copies the same source fixture here.
		raw, err = os.ReadFile("release-embedded-api-transport.json")
	}
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		SchemaVersion int    `json:"schemaVersion"`
		Qualification string `json:"qualification"`
		Snapshot      struct {
			State    string `json:"state"`
			Base     string `json:"verifiedEffectiveControlBasePath"`
			Revision uint64 `json:"revision"`
		} `json:"snapshot"`
		APISuffix    string   `json:"apiSuffix"`
		Method       string   `json:"method"`
		ExpectedPath string   `json:"expectedPath"`
		DeniedPaths  []string `json:"deniedPaths"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.SchemaVersion != 1 ||
		f.Qualification != "SOURCE_TRANSPORT_CONTRACT_ONLY_NOT_COMPLETE_WEB_API_COMPATIBILITY" ||
		f.APISuffix != PublicAPIV2Suffix || f.Method != http.MethodGet {
		t.Fatal("R1 source transport fixture mismatch")
	}
	snapshot := RoutingSnapshot{
		State:                            SnapshotState(f.Snapshot.State),
		VerifiedEffectiveControlBasePath: f.Snapshot.Base,
		Revision:                         f.Snapshot.Revision,
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	called := 0
	handler := PublicAPIHandler{Snapshot: snapshot, Active: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != f.ExpectedPath || r.Method != f.Method {
			t.Fatalf("unexpected delegated request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	positive := httptest.NewRecorder()
	handler.ServeHTTP(positive, httptest.NewRequest(f.Method, "https://manager.example:18317"+f.ExpectedPath, nil))
	if positive.Code != http.StatusOK || positive.Body.String() != `{"ok":true}` || called != 1 {
		t.Fatalf("verified API response %d %q / delegated %d", positive.Code, positive.Body.String(), called)
	}
	for _, rejected := range f.DeniedPaths {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(f.Method, "https://manager.example:18317"+rejected, nil))
		if response.Code != http.StatusNotFound || called != 1 {
			t.Fatalf("API route leaked on %s: status %d delegated %d", rejected, response.Code, called)
		}
	}
}
