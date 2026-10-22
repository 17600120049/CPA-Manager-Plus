package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func assertFreshHealth(t *testing.T, handler http.Handler, want int) {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != want {
		t.Fatalf("health status = %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != (want == http.StatusOK) || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("health contract: %s %v", w.Body.String(), w.Header())
	}
	if want == http.StatusOK {
		if len(body) != 2 || body["service"] != "cpa-manager-plus" {
			t.Fatalf("Manager health contract: %s", w.Body.String())
		}
	} else if len(body) != 1 {
		t.Fatalf("not-ready health disclosed state: %s", w.Body.String())
	}
}

func TestFreshHealthPreservesManagerContractAfterTakeover(t *testing.T) {
	h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
	assertFreshHealth(t, h, http.StatusServiceUnavailable)
	dir := t.TempDir()
	paths := legacypreflight.DefaultPaths(filepath.Join(dir, "manager.sqlite"))
	paths.Config = filepath.Join(dir, "config.json")
	opened, err := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	app := newFreshApplication(opened, config.Config{}, nil)
	h.SetFreshHandler(app)
	assertFreshHealth(t, h, http.StatusOK)
	// Manager readiness does not initialize Admin or complete the installation.
	op, err := app.current(t.Context())
	if err != nil || op.AdminInitialized || op.ProductComplete || !op.GatewayPending {
		t.Fatalf("health changed the Fresh checkpoint: %+v %v", op, err)
	}
	for _, method := range []string{http.MethodPost, http.MethodHead, http.MethodOptions} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/health", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s health = %d", method, w.Code)
		}
	}
	if _, err := app.authority.Issue(t.Context()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		bootstrap.FixedBootstrapDiscoveryPath:            http.StatusOK,
		bootstrap.FixedBootstrapAPIPath + "operation":    http.StatusUnauthorized,
		bootstrap.FixedBootstrapAPIPath + "admin/commit": http.StatusMethodNotAllowed,
		"/health/":              http.StatusNotFound,
		"/healthz":              http.StatusNotFound,
		"/v0/management/config": http.StatusNotFound,
		"/v1/models":            http.StatusNotFound,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Fatalf("%s = %d, want %d", path, w.Code, want)
		}
	}
}

func TestFreshHealthFailsClosedOnUnavailableStore(t *testing.T) {
	for _, state := range []string{"missing operation", "malformed operation", "closed database"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			paths := legacypreflight.DefaultPaths(filepath.Join(dir, "manager.sqlite"))
			paths.Config = filepath.Join(dir, "config.json")
			opened, err := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
			h.SetFreshHandler(newFreshApplication(opened, config.Config{}, nil))
			assertFreshHealth(t, h, http.StatusOK)
			switch state {
			case "missing operation":
				_, err = opened.DB.Exec("DELETE FROM settings WHERE key='fresh_operation_v1'")
			case "malformed operation":
				_, err = opened.DB.Exec("UPDATE settings SET value='{}' WHERE key='fresh_operation_v1'")
			case "closed database":
				err = opened.DB.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			assertFreshHealth(t, h, http.StatusServiceUnavailable)
		})
	}
}
