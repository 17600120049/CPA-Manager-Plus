package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
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
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return false }
	denied := a3Post(t, app, op.Revision, source.URL, "private-source-secret")
	if denied.Code != http.StatusServiceUnavailable || sourceCalls.Load() != 0 { t.Fatal("untrusted Host accessed CPA", denied.Code) }
	still, _ := app.current(t.Context())
	if still.Revision != op.Revision || still.Strategy.Strategy != "" { t.Fatal("denied path persisted a checkpoint") }
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return true }
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
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return true }
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
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return true }
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
		IssuedAtMS: now.Add(-time.Second).UnixMilli(), ExpiresAtMS: now.Add(time.Minute).UnixMilli(),
		ExpectedRevision: 3, IntentDigest: freshExternalIntentDigest("fresh-private-run-123", 3, "http://cpa.private:8317", "private-source-secret")}
	if !p.matchesIntent(op, 3, "http://cpa.private:8317", "private-source-secret", now) { t.Fatal("valid bound private grant denied") }
	if p.matchesIntent(op, 3, "http://foreign.private:8317", "private-source-secret", now) ||
		p.matchesIntent(op, 3, "http://cpa.private:8317", "different-key", now) ||
		p.matchesIntent(op, 4, "http://cpa.private:8317", "private-source-secret", now) {
		t.Fatal("Host permit admitted a different original intent")
	}
	other := op
	other.Identity.Generation = "rotated"
	if p.valid(other, now) { t.Fatal("foreign generation admitted") }
	p.ExpiresAtMS = now.Add(-time.Second).UnixMilli()
	if p.valid(op, now) { t.Fatal("expired permit admitted") }
	p.ExpiresAtMS = now.Add(time.Minute).UnixMilli()
	p.Scope = "private-s1"
	if p.valid(op, now) { t.Fatal("S1 permit was accepted as External") }
}

// A preflight refusal must leave the entire durable Fresh record untouched.
// In particular a malformed Management Key cannot occupy a one-shot A3 slot.
func TestA3StaticPreflightDeniedWithoutReservation(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return true }
	before, err := app.store.Load(t.Context())
	if err != nil { t.Fatal(err) }
	for _, item := range []struct{ url, key string }{
		{"http://cpa.private:8317", ""},
		{"http://cpa.private:8317", "invalid key with space"},
		{"http://cpa.private:8317", "bad\nkey"},
		{"http://cpa.private:8317/%25", "private-source-secret"},
	} {
		got := a3Post(t, app, before.Operation.Revision, item.url, item.key)
		if got.Code != http.StatusConflict { t.Fatalf("preflight %q accepted: %d", item.url, got.Code) }
		after, err := app.store.Load(t.Context())
		if err != nil || !reflect.DeepEqual(before, after) { t.Fatal("preflight persisted an attempt", err) }
	}
	app.strategy = freshstrategy.New(freshstrategy.Options{Protector: nil})
	got := a3Post(t, app, before.Operation.Revision, "http://cpa.private:8317", "private-source-secret")
	if got.Code != http.StatusConflict { t.Fatal("unqualified consumer reserved A3", got.Code) }
	after, err := app.store.Load(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) { t.Fatal("unqualified path mutated record", err) }
}

// The original Host-owned permit binds the exact request, independent of the
// browser and network arrival order. A delayed first POST can still claim the
// slot; a fresh browser's different key or source must be refused before CAS.
func TestA3HostOriginalIntentRejectsReplacement(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	var reads atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	defer source.Close()
	op, _ := app.current(t.Context())
	originalKey := "original-private-key"
	digest := freshExternalIntentDigest("private-host-run-123", op.Revision, source.URL, originalKey)
	app.externalPermit = func(current bootstrap.FreshOperation, revision uint64, baseURL, key string) bool {
		return current.Identity == op.Identity && revision == op.Revision &&
			freshExternalIntentDigest("private-host-run-123", revision, baseURL, key) == digest
	}
	for _, altered := range []struct { url, key string }{{source.URL, "replacement-key"}, {"http://127.0.0.1:9", originalKey}} {
		response := a3Post(t, app, op.Revision, altered.url, altered.key)
		if response.Code != http.StatusServiceUnavailable { t.Fatal("replacement intent admitted", response.Code) }
		still, _ := app.current(t.Context())
		if still.Revision != op.Revision || still.Strategy.Strategy != "" || reads.Load() != 0 {
			t.Fatal("replacement intent consumed the original grant")
		}
	}
	accepted := a3Post(t, app, op.Revision, source.URL, originalKey)
	if accepted.Code != http.StatusOK || reads.Load() != 1 { t.Fatal("original request refused", accepted.Code) }
}

func TestA3PermitRevocationAtBothCASBoundaries(t *testing.T) {
	for _, revokeAt := range []int{2, 5} {
		t.Run(strconv.Itoa(revokeAt), func(t *testing.T) {
			app, closeApp := newA3TestApp(t)
			defer closeApp()
			var checks atomic.Int32
			app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool {
				return checks.Add(1) < int32(revokeAt)
			}
			var reads atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
			}))
			defer source.Close()
			op, _ := app.current(t.Context())
			response := a3Post(t, app, op.Revision, source.URL, "private-key")
			if response.Code != http.StatusServiceUnavailable { t.Fatal("revoked CAS admitted", response.Code, response.Body.String()) }
			result, err := app.store.Load(t.Context())
			if err != nil || result.Connection != nil || result.Operation.Strategy.Stage == "committed" {
				t.Fatal("revoked grant persisted a connection", err)
			}
			if revokeAt == 2 && (result.Operation.Revision != op.Revision || reads.Load() != 0) {
				t.Fatal("first CAS rejection persisted or called source")
			}
			if revokeAt == 5 && (result.Operation.Strategy.Stage != "validated" || reads.Load() != 1) {
				t.Fatal("second CAS rejection did not preserve STOP fence", result.Operation.Strategy, reads.Load())
			}
		})
	}
}

func TestA3ValidatedFencePersistsAfterSQLiteRestart(t *testing.T) {
	ctx := t.Context()
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil { t.Fatal(err) }
	app := newFreshApplication(opened, config.Config{}, nil)
	app.strategy = freshstrategy.New(freshstrategy.Options{Protector: opened.Protector,
		Qualification: bootstrap.CPAQualification{ManagementGeneration: "v8", ArtifactQualified: true, RequiredProfileQualified: true}})
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return true }
	op := opened.Record.Operation
	_, err = app.store.Update(ctx, op.Identity, op.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: op.Identity.OperationID, Revision: 1, Target: "/control", Stage: bootstrap.PrefixCommitted, Committed: true}
		return nil
	})
	if err != nil { t.Fatal(err) }
	op, _ = app.current(ctx)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer source.Close()
	response := a3Post(t, app, op.Revision, source.URL, "private-source-secret")
	if response.Code == http.StatusOK { t.Fatal("failed source read was accepted") }
	staged, _ := app.current(ctx)
	if staged.Strategy.Stage != "validated" { t.Fatal("no durable fence") }
	if err := opened.Close(); err != nil { t.Fatal(err) }
	opened, err = fresh.Open(ctx, paths, fresh.OpenOptions{})
	if err != nil { t.Fatal(err) }
	defer opened.Close()
	app = newFreshApplication(opened, config.Config{}, nil)
	app.externalPermit = func(bootstrap.FreshOperation, uint64, string, string) bool { return true }
	response = a3Post(t, app, staged.Revision, source.URL, "replacement-key")
	if response.Code != http.StatusConflict { t.Fatal("restart allowed a second POST", response.Code) }
	result, err := app.store.Load(ctx)
	if err != nil || result.Operation.Revision != staged.Revision || result.Connection != nil { t.Fatal("restart fence lost", err) }
}
