package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestFreshClientKeysAdminPersistenceAndStops(t *testing.T) {
	ctx := t.Context()
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if opened != nil {
			_ = opened.Close()
		}
	}()
	app := newFreshApplication(opened, config.Config{}, nil)
	_, err = app.store.Update(ctx, opened.Record.Operation.Identity, opened.Record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: r.Operation.Identity.OperationID, Revision: 1, Target: "/control", Stage: bootstrap.PrefixCommitted, Committed: true}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, resource, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/control/api/v2/"+resource, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		// Exercise the real canonical Admin verifier. Ingress routing is covered
		// separately by the process smoke; this does not forge its verification.
		httpapi.InitialFullWebHandler("/control", http.HandlerFunc(app.canonicalAPI)).ServeHTTP(response, request)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("Full Web wrapper weakened API cache control")
		}
		if response.Body.String() != "" && strings.Contains(response.Body.String(), strategyTestAdmin) {
			t.Fatal("admin secret leaked")
		}
		return response
	}
	id := strings.Repeat("a", 32)
	body := `{"requestId":"` + id + `"}`
	for _, token := range []string{"", "wrong-admin"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			if got := call(method, "api-keys", token, body); got.Code != 401 {
				t.Fatalf("unauthorized %s: %d", method, got.Code)
			}
		}
		if got := call("GET", "api-keys/requests/"+id, token, ""); got.Code != 401 {
			t.Fatal("unauthorized lookup")
		}
	}
	created := call("POST", "api-keys", strategyTestAdmin, body)
	if created.Code != 201 {
		t.Fatalf("create %d", created.Code)
	}
	var result freshkeys.Result
	if json.Unmarshal(created.Body.Bytes(), &result) != nil || result.CreatedSecret == "" {
		t.Fatal("missing creation result")
	}
	for _, resource := range []string{"api-keys", "api-keys/requests/" + id} {
		got := call("GET", resource, strategyTestAdmin, "")
		if got.Code != 200 || strings.Contains(got.Body.String(), result.CreatedSecret) || !strings.Contains(got.Body.String(), string(result.Key.ID)) {
			t.Fatal("invalid protected read")
		}
	}
	replay := call("POST", "api-keys", strategyTestAdmin, body)
	if replay.Code != 200 || strings.Contains(replay.Body.String(), "createdSecret") {
		t.Fatal("replay disclosed secret")
	}
	for _, resource := range []string{"api-keys", "api-keys/" + string(result.Key.ID), "api-keys/" + string(result.Key.ID) + "/replace", "api-keys/" + string(result.Key.ID) + "/reveal"} {
		if got := call("DELETE", resource, strategyTestAdmin, ""); got.Code != 405 {
			t.Fatal("dangerous delete enabled")
		}
		if resource != "api-keys" && call("POST", resource, strategyTestAdmin, body).Code != 405 {
			t.Fatal("unsupported mutation enabled")
		}
	}
	if got := call("GET", "api-keys/requests/"+strings.Repeat("b", 32), strategyTestAdmin, ""); got.Code != 404 {
		t.Fatal("unknown operation misreported")
	}
	for _, invalid := range []string{`{}`, `{"requestId":"bad"}`, body + ` {}`, `{"requestId":"` + id + `","secret":"forbidden"}`} {
		if call("POST", "api-keys", strategyTestAdmin, invalid).Code != 400 {
			t.Fatal("malformed create accepted")
		}
	}
	before, _ := app.store.Load(ctx)
	if err = opened.Close(); err != nil {
		t.Fatal(err)
	}
	opened = nil
	opened, err = fresh.Open(ctx, paths, fresh.OpenOptions{AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	app = newFreshApplication(opened, config.Config{}, nil)
	after, _ := app.store.Load(ctx)
	if after.Operation.Revision != before.Operation.Revision || !after.Operation.GatewayPending {
		t.Fatal("creation changed Gateway or bootstrap authority")
	}
	list := call("GET", "api-keys", strategyTestAdmin, "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), string(result.Key.ID)) || strings.Contains(list.Body.String(), result.CreatedSecret) {
		t.Fatal("restart lost redacted result")
	}
	for _, strategy := range []bootstrap.CPAStrategy{bootstrap.ConnectExternal, bootstrap.AdoptExisting} {
		blocked := after.Operation
		blocked.Strategy.Strategy = strategy
		response := httptest.NewRecorder()
		app.clientKeys(response, httptest.NewRequest("POST", "/control/api/v2/api-keys", strings.NewReader(body)), blocked, "api-keys")
		if response.Code != 409 {
			t.Fatal("non-New scope enabled")
		}
	}
}
