package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
)

// Read-only startup does not grant a Bootstrap API or let aliases escape into
// the next route owner, even when the browser loads the wizard shell.
func TestReadOnlyStartupWizardHasNoMutationSurface(t *testing.T) {
	h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
	h.readOnlyShell = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("read-only shell"))
	})
	request := func(method, path string, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != want {
			t.Fatalf("%s %s: status %d, wanted %d", method, path, response.Code, want)
		}
		if want == http.StatusOK && !strings.Contains(response.Body.String(), "read-only shell") {
			t.Fatalf("shell missing for %s", path)
		}
	}
	ui := bootstrap.FixedBootstrapUIPath
	assets := bootstrap.FixedBootstrapAssetsPath + "index-AbCd1234.js"
	request(http.MethodGet, ui, http.StatusNotFound)
	h.projectionMu.Lock()
	h.readOnlyDiscovery = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h.projectionMu.Unlock()
	request(http.MethodGet, ui, http.StatusOK)
	request(http.MethodHead, assets, http.StatusOK)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		request(method, ui, http.StatusNotFound)
	}
	for _, path := range []string{
		bootstrap.FixedBootstrapAPIPath + "claim/exchange",
		bootstrap.FixedBootstrapAPIPath + "admin/commit",
		bootstrap.FixedBootstrapDiscoveryPath + "/ui/extra",
		bootstrap.FixedBootstrapDiscoveryPath + "/assets/../api/v2/claim/exchange",
		bootstrap.FixedBootstrapDiscoveryPath + "/ui%2Fbad",
	} {
		request(http.MethodGet, path, http.StatusNotFound)
	}
	request(http.MethodGet, "/v1/models", http.StatusNotFound)
	h.BeginWritableStartup()
	request(http.MethodGet, ui, http.StatusNotFound)
	h.SetReady(http.NotFoundHandler())
	request(http.MethodGet, ui, http.StatusNotFound)
	h.SetCompleted()
	request(http.MethodGet, ui, http.StatusNotFound)
}
