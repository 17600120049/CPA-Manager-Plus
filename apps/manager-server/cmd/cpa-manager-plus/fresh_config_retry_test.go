package main

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestRetryPublicAdminInputAndDefaultOff(t *testing.T) {
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, e := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if e != nil {
		t.Fatal(e)
	}
	defer opened.Close()
	a := newFreshApplication(opened, config.Config{}, nil)
	_, e = a.store.Update(t.Context(), opened.Record.Operation.Identity, opened.Record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: r.Operation.Identity.OperationID, Revision: 1, Target: "/control", Stage: bootstrap.PrefixCommitted, Committed: true}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, key, body string) int {
		r := httptest.NewRequest(method, "/control/api/v2/gateway/retry/"+path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		a.canonicalAPI(w, r)
		if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), strategyTestAdmin) {
			t.Fatal("unsafe response")
		}
		return w.Code
	}
	id := strings.Repeat("a", 32)
	ex := `{"sourceId":"isolated-source-1234","generation":"7","revision":2,"sha256":"` + strings.Repeat("b", 64) + `"}`
	for _, key := range []string{"", "wrong", "expired-session"} {
		for _, v := range []struct{ method, path, body string }{{"GET", "request-retry", ""}, {"PUT", "request-retry", `{}`}, {"GET", "operations/" + id, ""}, {"POST", "operations/" + id + "/reconcile", `{}`}} {
			if code := call(v.method, v.path, key, v.body); code != 401 {
				t.Fatal(code)
			}
		}
	}
	for _, value := range []string{`-1`, `11`, `0.0`, `1.5`, `"0"`, `null`, `true`, `[]`, `{}`} {
		if code := call("PUT", "request-retry", strategyTestAdmin, `{"requestId":"`+id+`","value":`+value+`,"expected":`+ex+`}`); code != 400 {
			t.Fatal(value, code)
		}
	}
	for _, body := range []string{`null`, `[]`, `{}`, `{"value":0,"value":1}`, `{"Value":0}`, `{"requestId":"` + id + `","value":0,"expected":` + ex + `,"yaml":"no"}`, `{"requestId":"` + id + `","value":0,"expected":` + ex + `} {}`} {
		if code := call("PUT", "request-retry", strategyTestAdmin, body); code != 400 {
			t.Fatal(body, code)
		}
	}
	for _, n := range []string{"0", "10"} {
		if code := call("PUT", "request-retry", strategyTestAdmin, `{"requestId":"`+id+`","value":`+n+`,"expected":`+ex+`}`); code != 409 {
			t.Fatal("default enabled", code)
		}
	}
	if call("GET", "request-retry", strategyTestAdmin, "") != 409 {
		t.Fatal("default configured")
	}
	if call("POST", "request-retry", strategyTestAdmin, `{}`) != 405 {
		t.Fatal("method guard")
	}
}
