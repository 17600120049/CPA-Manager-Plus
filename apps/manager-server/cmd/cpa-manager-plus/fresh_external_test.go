package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func newA3TestApp(t *testing.T) (*freshApplication, func()) {
	t.Helper()
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil { t.Fatal(err) }
	app := newFreshApplication(opened, config.Config{}, nil)
	app.strategy = freshstrategy.New(freshstrategy.Options{Protector: opened.Protector,
		Qualification: bootstrap.CPAQualification{ManagementGeneration: "v8", ArtifactQualified: true, RequiredProfileQualified: true}})
	op := opened.Record.Operation
	if _, err := app.store.Update(t.Context(), op.Identity, op.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: op.Identity.OperationID, Revision: 1, Target: "/control", Stage: bootstrap.PrefixCommitted, Committed: true}
		return nil
	}); err != nil { t.Fatal(err) }
	return app, func() { _ = opened.Close() }
}

func a3Post(t *testing.T, app *freshApplication, revision uint64, url, key string) *httptest.ResponseRecorder {
	t.Helper()
	op, err := app.current(context.Background())
	if err != nil { t.Fatal(err) }
	body, err := json.Marshal(struct {
		Identity bootstrap.FreshIdentity `json:"identity"`
		ExpectedRevision uint64 `json:"expectedRevision"`
		freshstrategy.Request
	}{op.Identity, revision, freshstrategy.Request{Strategy: bootstrap.ConnectExternal, BaseURL: url, ManagementKey: key}})
	if err != nil { t.Fatal(err) }
	request := httptest.NewRequest(http.MethodPost, "/control/api/v2/strategy", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
	response := httptest.NewRecorder()
	app.canonicalAPI(response, request)
	return response
}

func TestA3HostPermitDefaultDenyAndOneAttemptFence(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	var sourceCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v8/management/config" { t.Errorf("unexpected CPA source access") }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	defer source.Close()
	op, _ := app.current(t.Context())
	app.externalPermit = func(bootstrap.FreshOperation) bool { return false }
	denied := a3Post(t, app, op.Revision, source.URL, "private-source-secret")
	if denied.Code != http.StatusServiceUnavailable || sourceCalls.Load() != 0 { t.Fatal("untrusted Host accessed CPA", denied.Code) }
	still, _ := app.current(t.Context())
	if still.Revision != op.Revision || still.Strategy.Strategy != "" { t.Fatal("denied path persisted a checkpoint") }
	app.externalPermit = func(bootstrap.FreshOperation) bool { return true }
	accepted := a3Post(t, app, op.Revision, source.URL, "private-source-secret")
	if accepted.Code != http.StatusOK { t.Fatalf("A3 safe connection: %d %s", accepted.Code, accepted.Body.String()) }
	committed, _ := app.current(t.Context())
	if committed.Revision != op.Revision+2 || committed.Strategy.Strategy != bootstrap.ConnectExternal ||
		committed.Strategy.Stage != "committed" || !committed.GatewayPending || committed.ProductComplete {
		t.Fatal("A3 response failed checkpoint contract", committed)
	}
	record, err := app.store.Load(t.Context())
	if err != nil || record.Connection == nil || record.Connection.ManagementKey == "private-source-secret" { t.Fatal("A3 secret was not protected", err) }
	if sourceCalls.Load() != 1 { t.Fatal("incorrect CPA safe-read count") }
	replay := a3Post(t, app, committed.Revision, source.URL, "private-source-secret")
	if replay.Code != http.StatusConflict || sourceCalls.Load() != 1 { t.Fatal("committed A3 replay accessed CPA", replay.Code) }
	complete := httptest.NewRequest(http.MethodPost, "/control/api/v2/complete", nil)
	complete.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
	response := httptest.NewRecorder()
	app.canonicalAPI(response, complete)
	if response.Code != http.StatusConflict { t.Fatal("External became ProductComplete", response.Code) }
	after, _ := app.current(t.Context())
	if after.Revision != committed.Revision || after.ProductComplete { t.Fatal("External complete mutated state") }
}

func TestA3LostSourceResultNeverOpensSecondPost(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	var calls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer source.Close()
	app.externalPermit = func(bootstrap.FreshOperation) bool { return true }
	op, _ := app.current(t.Context())
	result := a3Post(t, app, op.Revision, source.URL, "private-source-secret")
	if result.Code == http.StatusOK { t.Fatal("source failure incorrectly confirmed saving") }
	fenced, _ := app.current(t.Context())
	if fenced.Strategy.Strategy != bootstrap.ConnectExternal || fenced.Strategy.Stage != "validated" || fenced.Revision != op.Revision+1 {
		t.Fatal("source failure cleared durable attempt fence", fenced)
	}
	result = a3Post(t, app, fenced.Revision, source.URL, "new-key")
	if result.Code != http.StatusConflict || calls.Load() != 1 { t.Fatal("unknown result retried source", result.Code, calls.Load()) }
}

func TestA3CASPreventsCompetingFirstPosts(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	app.externalPermit = func(bootstrap.FreshOperation) bool { return true }
	var reads atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	defer source.Close()
	op, _ := app.current(t.Context())
	var wg sync.WaitGroup
	results := make([]int, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = a3Post(t, app, op.Revision, source.URL, strings.Repeat("k", 24)).Code
		}(i)
	}
	wg.Wait()
	if !((results[0] == 200 && results[1] == 409) || (results[1] == 200 && results[0] == 409)) || reads.Load() != 1 {
		t.Fatal("A3 competing writes bypassed exclusive attempt", results, reads.Load())
	}
}

func TestA3PermitBoundToOneInstallation(t *testing.T) {
	now := time.Now()
	op := bootstrap.FreshOperation{Identity: bootstrap.FreshIdentity{AdmissionID: "admit", InstallationID: "install", Generation: "gen", DatabasePath: "/data/manager.sqlite", OperationID: "operation-123456789"}}
	p := freshExternalPermit{Scope: freshExternalPrivateProfile, RunID: "fresh-private-run-123",
		AdmissionID: op.Identity.AdmissionID, InstallationID: op.Identity.InstallationID, Generation: op.Identity.Generation,
		DatabasePath: op.Identity.DatabasePath, OperationID: string(op.Identity.OperationID),
		IssuedAtMS: now.Add(-time.Second).UnixMilli(), ExpiresAtMS: now.Add(time.Minute).UnixMilli()}
	if !p.valid(op, now) { t.Fatal("valid bound private grant denied") }
	other := op
	other.Identity.Generation = "rotated"
	if p.valid(other, now) { t.Fatal("foreign generation admitted") }
	p.ExpiresAtMS = now.Add(-time.Second).UnixMilli()
	if p.valid(op, now) { t.Fatal("expired permit admitted") }
	p.ExpiresAtMS = now.Add(time.Minute).UnixMilli()
	p.Scope = "private-s1"
	if p.valid(op, now) { t.Fatal("S1 permit was accepted as External") }
}
