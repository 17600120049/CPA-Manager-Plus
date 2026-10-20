package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/privateconfig"
)

type configExecutor struct{ calls int }

func (e *configExecutor) PrivateConfiguration(context.Context, privateconfig.Request) (privateconfig.Result, error) {
	e.calls++
	return privateconfig.Result{}, privateconfig.ErrStop
}

func TestPrivateConfigurationDefaultOffAndAuthentication(t *testing.T) {
	executor := &configExecutor{}
	for _, tc := range []struct {
		name, token, path, body string
		enabled                 bool
		want                    int
	}{
		{"default-off", testRuntimeToken, privateconfig.Path, "{}", false, 404},
		{"absent-auth", "", privateconfig.Path, "{}", true, 401},
		{"wrong-auth", "wrong", privateconfig.Path, "{}", true, 401},
		{"query", testRuntimeToken, privateconfig.Path + "?x=1", "{}", true, 400},
		{"escaped", testRuntimeToken, "/v1/runtime/%70rivate-configuration", "{}", true, 400},
		{"unknown-field", testRuntimeToken, privateconfig.Path, `{"secret":"must-not-echo"}`, true, 400},
		{"trailing", testRuntimeToken, privateconfig.Path, "{} {}", true, 400},
		{"bounded", testRuntimeToken, privateconfig.Path, strings.Repeat(" ", 4097) + "{}", true, 400},
		{"host-stop", testRuntimeToken, privateconfig.Path, `{"action":"observe"}`, true, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{RuntimeIdentity: "runtime-01", RuntimeGeneration: 7, Token: testRuntimeToken}
			h, e := NewHandler(cfg)
			if e != nil {
				t.Fatal(e)
			}
			if tc.enabled {
				h = WithPrivateConfiguration(h, executor)
			}
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != tc.want {
				t.Fatalf("status %d want %d", out.Code, tc.want)
			}
			assertJSONHeaders(t, out)
			if strings.Contains(out.Body.String(), "must-not-echo") {
				t.Fatal("echoed rejected input")
			}
		})
	}
	if executor.calls != 1 {
		t.Fatal("unauthorized or malformed request reached authority", executor.calls)
	}
}
