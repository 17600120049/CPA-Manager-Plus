package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
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

// This test is explicitly invoked after the Web bundle is staged into the
// Manager embed. It checks the real packaged shell over HTTP, not a stub.
func TestReadOnlyStartupWizardPackagedTransport(t *testing.T) {
	if os.Getenv("CPAMP_FULLWEB_PACKAGED_TEST") != "1" {
		t.Skip("requires a staged Full Web bundle")
	}
	mode := bootstrap.UnsupportedUpgrade
	h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
	h.projectionMu.Lock()
	h.readOnlyDiscovery = bootstrapdiscovery.Handler{Discovery: bootstrap.PublicDiscovery{
		ContractVersion: bootstrap.PublicDiscoveryContractVersion,
		Status: bootstrap.PublicUnsupportedUpgrade,
		Mode: &mode,
		AuthenticationRequired: true,
		AuthEntry: bootstrap.AuthEntryAdminSession,
		Reason: bootstrap.ReasonQualificationUnavailable,
	}}
	h.projectionMu.Unlock()
	server := httptest.NewServer(h)
	defer server.Close()

	request := func(method, path string, want int) (http.Header, string) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s: status=%d want=%d body=%q", method, path, response.StatusCode, want, body)
		}
		return response.Header, string(body)
	}
	_, discovery := request(http.MethodGet, bootstrap.FixedBootstrapDiscoveryPath, http.StatusOK)
	if !strings.Contains(discovery, `"status":"unsupported_upgrade"`) || !strings.Contains(discovery, `"auth_entry":"admin_session"`) {
		t.Fatalf("unexpected read-only classification: %s", discovery)
	}
	headers, page := request(http.MethodGet, bootstrap.FixedBootstrapUIPath, http.StatusOK)
	if !strings.Contains(headers.Get("Content-Type"), "text/html") ||
		headers.Get("Cache-Control") != "no-store" ||
		strings.Contains(page, "__CPAMP_ROUTING_CONTEXT__") {
		t.Fatal("packaged bootstrap page missing or unbounded")
	}
	refs := regexp.MustCompile(`(?:src|href)="(/\.well-known/cpamp-bootstrap/assets/[^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(refs) == 0 {
		t.Fatal("packaged bootstrap page has no hashed entry assets")
	}
	for _, match := range refs {
		headers, body := request(http.MethodGet, match[1], http.StatusOK)
		if len(body) == 0 || strings.Contains(headers.Get("Content-Type"), "text/html") || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid bounded asset %s", match[1])
		}
		_, headBody := request(http.MethodHead, match[1], http.StatusOK)
		if headBody != "" {
			t.Fatalf("HEAD returned asset body: %s", match[1])
		}
	}
	for _, path := range []string{
		bootstrap.FixedBootstrapAPIPath + "claim/exchange",
		bootstrap.FixedBootstrapAPIPath + "admin/commit",
		bootstrap.FixedBootstrapAPIPath + "strategy/apply",
		"/v1/models",
	} {
		request(http.MethodPost, path, http.StatusNotFound)
	}
	request(http.MethodPost, bootstrap.FixedBootstrapUIPath, http.StatusNotFound)
	request(http.MethodGet, bootstrap.FixedBootstrapDiscoveryPath+"/ui/extra", http.StatusNotFound)
	h.BeginWritableStartup()
	request(http.MethodGet, bootstrap.FixedBootstrapUIPath, http.StatusNotFound)
	_, unavailable := request(http.MethodGet, bootstrap.FixedBootstrapDiscoveryPath, http.StatusServiceUnavailable)
	if !strings.Contains(unavailable, `"status":"unavailable"`) {
		t.Fatalf("stale classification survived writable startup: %s", unavailable)
	}
	h.SetReady(http.NotFoundHandler())
	request(http.MethodGet, bootstrap.FixedBootstrapUIPath, http.StatusNotFound)
	h.SetCompleted()
	request(http.MethodGet, bootstrap.FixedBootstrapDiscoveryPath, http.StatusNotFound)
}
