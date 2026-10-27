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

func TestConfigFieldPublicAdminInputAndDefaultOff(t *testing.T) {
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
		r := httptest.NewRequest(method, "/control/api/v2/gateway/config/"+path, bytes.NewBufferString(body))
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
	sse := "requests/streaming/keepalive-seconds"
	headers := "requests/passthrough-headers"
	ex := `{"sourceId":"isolated-source-1234","generation":"7","revision":2,"sha256":"` + strings.Repeat("b", 64) + `"}`
	for _, key := range []string{"", "wrong", "expired-session"} {
		for _, v := range []struct{ method, path, body string }{{"GET", sse, ""}, {"PUT", sse, `{}`}, {"GET", "operations/" + id, ""}, {"POST", "operations/" + id + "/reconcile", `{}`}} {
			if code := call(v.method, v.path, key, v.body); code != 401 {
				t.Fatal(code)
			}
		}
	}
	// Out-of-range, wrong type and non-scalar values never reach the writer.
	for _, tc := range []struct{ path, value string }{
		{sse, `-1`}, {sse, `301`}, {sse, `1.5`}, {sse, `"60"`}, {sse, `null`}, {sse, `true`}, {sse, `[]`}, {sse, `{}`},
		{"requests/streaming/bootstrap-retries", `11`}, {"requests/nonstream-keepalive-interval", `301`}, {headers, `1`}, {headers, `"true"`},
	} {
		body := `{"requestId":"` + id + `","value":` + tc.value + `,"expected":` + ex + `}`
		if code := call("PUT", tc.path, strategyTestAdmin, body); code != 400 {
			t.Fatal(tc.path, tc.value, code)
		}
	}
	for _, body := range []string{`null`, `[]`, `{}`, `{"value":0,"value":1}`, `{"Value":0}`, `{"requestId":"` + id + `","value":60,"expected":` + ex + `,"yaml":"no"}`, `{"requestId":"` + id + `","value":60,"expected":` + ex + `} {}`, `{"requestId":"` + id + `","value":60,"expected":{"sourceId":"isolated-source-1234"}}`} {
		if code := call("PUT", sse, strategyTestAdmin, body); code != 400 {
			t.Fatal(body, code)
		}
	}
	// A field outside the allowlist is a 404 and never reaches CPA.
	for _, path := range []string{"requests/proxy-url", "routing/strategy", "requests/streaming", "requests/streaming/unknown"} {
		if code := call("GET", path, strategyTestAdmin, ""); code != 404 {
			t.Fatal(path, code)
		}
	}
	for _, value := range []string{"0", "300"} {
		body := `{"requestId":"` + id + `","value":` + value + `,"expected":` + ex + `}`
		if code := call("PUT", sse, strategyTestAdmin, body); code != 409 {
			t.Fatal("default enabled", value, code)
		}
	}
	if call("GET", sse, strategyTestAdmin, "") != 409 {
		t.Fatal("default configured")
	}
	// A profile without a writer answers 409 for every operation route before any
	// per-request validation; the id shape is checked inside the service.
	for _, operation := range []string{"operations/" + id, "operations/" + strings.Repeat("a", 31)} {
		if call("GET", operation, strategyTestAdmin, "") != 409 {
			t.Fatal("default operation read enabled", operation)
		}
	}
	if call("POST", "operations/"+id, strategyTestAdmin, `{}`) != 405 {
		t.Fatal("method guard")
	}
	if call("POST", sse, strategyTestAdmin, `{}`) != 405 {
		t.Fatal("field method guard")
	}
}
