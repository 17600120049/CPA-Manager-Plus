package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestPrivateChatConsumerRequiresOptInLoopbackAndCommittedFreshTarget(t *testing.T) {
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	t.Setenv("CPAMP_PRIVATE_S1_PERMIT", freshkeyapply.PermitPath)
	app := newFreshApplication(opened, config.Config{RuntimeURL: "http://127.0.0.1:9081", CPAUpstreamURL: "http://127.0.0.1:8317"}, nil)
	for _, test := range []struct {
		opt, remote, method, target, body string
		want                              int
	}{
		{"", "127.0.0.1:30000", "POST", privateChatAdmissionPath, `{}`, 404},
		{"1", "172.19.0.8:30000", "POST", privateChatAdmissionPath, `{}`, 404},
		{"1", "127.0.0.1:30000", "GET", privateChatAdmissionPath, `{}`, 404},
		{"1", "127.0.0.1:30000", "POST", privateChatAdmissionPath + "?x=1", `{}`, 404},
		{"1", "127.0.0.1:30000", "POST", privateChatAdmissionPath, `{} {}`, 503},
		{"1", "127.0.0.1:30000", "POST", privateChatAdmissionPath, `{"requestBinding":"` + strings.Repeat("a", 64) + `"}`, 503},
	} {
		t.Setenv("CPAMP_PRIVATE_S2A_CHAT", test.opt)
		r := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
		r.RemoteAddr = test.remote
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 73))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != test.want || strings.Contains(w.Body.String(), strings.Repeat("s", 73)) || strings.Contains(w.Body.String(), "expiresAtMs") {
			t.Fatal(test, w.Code, "credential or verdict exposure")
		}
	}
}
