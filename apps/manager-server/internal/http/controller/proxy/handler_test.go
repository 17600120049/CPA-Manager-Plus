package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestAPIKeysManagementRequiresCPAMPAdminBeforeProxy(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "admin.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	credential, err := security.NewAdminCredential("cpamp-admin-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAdminCredential(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{App: &app.Context{AdminAuthService: adminauth.New(config.Config{}, st)}}
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		req := httptest.NewRequest(method, "/v0/management/api-keys", nil)
		req.Header.Set("Authorization", "Bearer caller-cpa-key")
		response := httptest.NewRecorder()
		handler.Management(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", method, response.Code)
		}
	}
}

// The plugin resource namespace is read-only upstream, so the Manager refuses a
// write here rather than forwarding it to a 404. Refusing locally keeps the
// reason visible and stops an unauthenticated caller from making the Manager
// reach CPA at all.
func TestCPAResourceRefusesWrites(t *testing.T) {
	handler := &Handler{}
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/v0/resource/plugins/demo/probe", strings.NewReader("{}"))
		handler.CPAResource(recorder, request)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, recorder.Code)
		}
		// The guard runs before any dependency is touched, which is why a zero
		// Handler is safe here and why CPA is never contacted.
	}
}
