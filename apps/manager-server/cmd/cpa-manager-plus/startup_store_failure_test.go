package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestStoreOpenFailureKeepsSafeStartupListener(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite")
	writeOwnedProvisioningConfig(t, dir)
	db, err := sqliterepo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const adminKey = "cpamp_Startup_Migration_Test_7391_Strong_Key"
	credential, err := security.NewAdminCredential(adminKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into settings(key,value,updated_at_ms) values('admin_credential_v1',?,1)`, string(credentialJSON)); err != nil {
		t.Fatal(err)
	}
	// A structurally native source passes admission, but its unsupported
	// derived format makes the real Store.Open fail before migration writes.
	// Include untrusted text to prove it never reaches diagnostics or logs.
	const privateFormat = "future-PRIVATE-SCHEMA-CONTENT"
	if _, err := db.Exec(`update settings set value = ? where key = 'usage_monitoring_model_format_version'`, privateFormat); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := security.LoadOrCreateDataKey("", filepath.Join(dir, "data.key")); err != nil {
		t.Fatal(err)
	}
	paths := legacypreflight.DefaultPaths(path)
	paths.Config = filepath.Join(dir, "config.json")
	admission, err := legacypreflight.AdmitStartup(context.Background(), paths, legacypreflight.Options{})
	if err != nil {
		t.Fatalf("fixture must pass real writable admission: %v", err)
	}
	if err := admission.Close(); err != nil {
		t.Fatal(err)
	}
	const runtimeToken = "runtime-test-private-token"
	runtimeTokenPath := filepath.Join(dir, "runtime.token")
	if err := os.WriteFile(runtimeTokenPath, []byte(runtimeToken), 0600); err != nil {
		t.Fatal(err)
	}
	before := managerSourceSnapshot(t, dir)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	env := ownedPreflightEnvironment(dir, path)
	for i, entry := range env {
		switch {
		case strings.HasPrefix(entry, "CPAMP_RUNTIME_URL="):
			env[i] = "CPAMP_RUNTIME_URL=" + upstream.URL
		case strings.HasPrefix(entry, "CPAMP_RUNTIME_TOKEN_FILE="):
			env[i] = "CPAMP_RUNTIME_TOKEN_FILE=" + runtimeTokenPath
		}
	}
	process := startManagerServerProcessWithEnvironment(t, env)
	t.Cleanup(func() { _ = process.cmd.Process.Kill() })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !strings.Contains(process.logs.String(), "keeping safe not-ready diagnostics available") {
		select {
		case err := <-process.done:
			t.Fatalf("Store.Open failure terminated the pre-bound listener: %v\n%s", err, process.logs.String())
		case <-deadline.C:
			t.Fatalf("did not reach the Store.Open failure wait path:\n%s", process.logs.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Match the existing contract exactly; the Store error is not public data.
	wantHealth := httptest.NewRecorder()
	bootstrapdiscovery.NotReadyHandler{}.ServeHTTP(wantHealth, httptest.NewRequest(http.MethodGet, "/health", nil))
	for range 2 {
		status, body := waitForManagerResponse(t, process.addr, http.MethodGet, "/health")
		if status != wantHealth.Code || body != wantHealth.Body.String() {
			t.Fatalf("unsafe health response: status=%d body=%s", status, body)
		}
		status, body = waitForManagerResponse(t, process.addr, http.MethodGet, bootstrapdiscovery.Path)
		var discovery map[string]any
		if err := json.Unmarshal([]byte(body), &discovery); err != nil || status != http.StatusServiceUnavailable || discovery["status"] != "unavailable" || discovery["mode"] != nil {
			t.Fatalf("failed Store republished source authority: status=%d body=%s err=%v", status, body, err)
		}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/usage-service/info"},
		{http.MethodPost, "/usage-service/config"},
		{http.MethodPost, bootstrap.FixedBootstrapAPIPath + "claim/exchange"},
		{http.MethodPost, bootstrap.FixedBootstrapAPIPath + "admin/commit"},
		{http.MethodGet, "/v0/management/config"},
		{http.MethodGet, "/v0/management/usage"},
		{http.MethodGet, "/debug/pprof/"},
	} {
		for _, credential := range []string{"", "Bearer " + adminKey} {
			request, err := http.NewRequest(route.method, "http://"+process.addr+route.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", credential)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("failed Store exposed %s %s: %d", route.method, route.path, response.StatusCode)
			}
		}
	}
	// Acquire while the diagnostic process is still alive, not just on exit.
	lock, err := processlock.AcquireExisting(path)
	if err != nil {
		t.Fatalf("failed Store retained database admission: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	// SQLite may create and remove transient WAL sidecars while opening; only
	// the parent directory mtime changes. All source files must stay identical.
	delete(before, dir)
	after := managerSourceSnapshot(t, dir)
	delete(after, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed Store changed source/config/key/ownership evidence:\nbefore=%v\nafter=%v", before, after)
	}
	process.stop(t)
	if calls.Load() != 0 {
		t.Fatalf("failed Store contacted Runtime: %d requests", calls.Load())
	}
	for _, forbidden := range []string{privateFormat, adminKey, runtimeToken, dir, "administrator authority loaded", "starting background workers", "Fresh"} {
		if strings.Contains(process.logs.String(), forbidden) {
			t.Fatalf("failed Store continued startup or disclosed private data (%s):\n%s", forbidden, process.logs.String())
		}
	}
	if connection, err := net.DialTimeout("tcp", process.addr, time.Second); err == nil {
		_ = connection.Close()
		t.Fatal("diagnostic listener survived process shutdown")
	}
}

func TestReadOnlyStartupCancellationClosesListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: newStartupHTTPHandler(bootstrapdiscovery.Unavailable()), ReadHeaderTimeout: time.Second}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go serveHTTPServer(server, listener, cancel, result)
	done := make(chan struct{})
	go func() {
		waitForReadOnlyStartup(ctx, server, result)
		close(done)
	}()
	status, _ := waitForManagerResponse(t, listener.Addr().String(), http.MethodGet, "/health")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("health status=%d", status)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic wait ignored cancellation")
	}
	if connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		_ = connection.Close()
		t.Fatal("cancelled diagnostic listener still accepts connections")
	}
}
