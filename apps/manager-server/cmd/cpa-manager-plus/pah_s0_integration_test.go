package main

import (
	"bytes"
	"errors"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func pahS0Adoption(t *testing.T, opened *fresh.Opened) (bootstrap.FreshStrategyCheckpoint, string) {
	t.Helper()
	snapshot, err := opened.Protector.ProtectString("disposable source")
	if err != nil { t.Fatal(err) }
	checkpoint := bootstrap.FreshStrategyCheckpoint{
		Strategy: bootstrap.AdoptExisting, Stage: "staged",
		TargetIdentity: "runtime-owned", TargetGeneration: 4,
		TargetArtifactID: "sha256:" + strings.Repeat("a", 64),
		SourceIdentity: "external:sha256:" + strings.Repeat("b", 64),
		SourceClass: bootstrap.AdoptNetworkExisting,
		DestinationConnectionIdentity: "external:sha256:" + strings.Repeat("c", 64),
		AdoptionResources: []bootstrap.ResourceRef{bootstrap.AdoptionRefClientAPIKeys, bootstrap.AdoptionRefCPAAuthFiles},
		AdoptionDecisions: []bootstrap.AdoptionResourceDecision{
			{ResourceRef: bootstrap.AdoptionRefClientAPIKeys, Kind: bootstrap.APIKeys,
			Scope: bootstrap.ClientAPIKeys, ReadEvidenceRef: "preflight:keys-read",
			ReplayEvidenceRef: "preflight:keys-replay", VerifyEvidenceRef: "preflight:keys-verify",
			Qualification: bootstrap.CapabilityQualified, Disposition: bootstrap.Migrate},
			{ResourceRef: bootstrap.AdoptionRefCPAAuthFiles, Kind: bootstrap.Credentials,
			Scope: bootstrap.CPAAuthFiles, Qualification: bootstrap.CapabilityPending,
			ManualAction: bootstrap.ReauthenticateAfterAdoption, Disposition: bootstrap.ManualAction},
		},
	}
	return checkpoint, snapshot
}

// Real SQLite/Fresh opening, not a helper predicate or synthetic JSON-only
// marker. A forbidden persisted operation never attaches the HTTP handler.
func TestPAHS0RealFreshReopenRejectsExternalAndAdoption(t *testing.T) {
	for _, kind := range []string{"external", "adopt", "managed"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
			paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
			opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
			if err != nil { t.Fatal(err) }
			op := opened.Record.Operation
			var checkpoint bootstrap.FreshStrategyCheckpoint
			var ciphertext string
			if kind == "external" {
				base := "http://source.invalid:8317"
				digest := sha256.Sum256([]byte(base))
				checkpoint = bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.ConnectExternal, Stage: "validated",
					TargetIdentity: "external:sha256:" + hex.EncodeToString(digest[:]), TargetGeneration: 1}
			} else if kind == "adopt" {
				checkpoint, ciphertext = pahS0Adoption(t, opened)
			} else {
				checkpoint = bootstrap.FreshStrategyCheckpoint{
					Strategy: bootstrap.NewManaged, Stage: "staged",
					TargetIdentity: "runtime-owned", TargetGeneration: 4,
					TargetArtifactID: "sha256:" + strings.Repeat("a", 64),
				}
			}
			if _, err := freshstore.New(opened.DB).Update(ctx, op.Identity, op.Revision, func(rec *freshstore.Record) error {
				rec.Operation.Strategy = checkpoint
				rec.AdoptionCiphertext = ciphertext
				return nil
			}); err != nil {
				_ = opened.Close()
				t.Fatalf("unable to persist valid forbidden checkpoint: %v", err)
			}
			if err := opened.Close(); err != nil { t.Fatal(err) }
			h := newStartupHTTPHandler(bootstrapdiscovery.Unavailable())
			h.EnablePAHS0(true)
			admitted, err := serveExistingFresh(ctx, config.Config{PAHS0Enabled: true, AdminKey: strategyTestAdmin}, paths, h)
			if kind == "managed" {
				if err != nil || admitted == nil || h.fresh.Load() == nil {
					t.Fatalf("authorized persisted NewManaged source not admitted: %v", err)
				}
			} else {
				var blocker *freshstrategy.Blocker
				if !errors.As(err, &blocker) || blocker.Code != "fresh_strategy_owner_conflict" {
					t.Fatalf("forbidden checkpoint should fail at exact PAH-S0 strategy check, got %v", err)
				}
				if admitted != nil || h.fresh.Load() != nil { t.Fatal("forbidden source published a Fresh handler") }
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/pah-s0/resource-guard", nil))
			want := http.StatusNotFound
			if kind == "managed" { want = http.StatusNoContent }
			if w.Code != want { t.Fatalf("PAH-S0 readiness %q: got %d want %d", kind, w.Code, want) }
			if admitted != nil { _ = admitted.Close() }
		})
	}
}

func TestPAHS0RealFreshHTTPRejectsExternalAndAdoptBeforeNetworkAndCAS(t *testing.T) {
	ctx := t.Context()
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil { t.Fatal(err) }
	defer opened.Close()
	var upstream atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v8/management/config" {
			t.Errorf("unexpected CPA read: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	defer source.Close()
	op := opened.Record.Operation
	rec, err := freshstore.New(opened.DB).Update(ctx, op.Identity, op.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{
			OperationID: r.Operation.Identity.OperationID,
			Revision: 1, Target: "/control",
			Stage: bootstrap.PrefixCommitted, Committed: true}
		return nil
	})
	if err != nil { t.Fatal(err) }
	app := newFreshApplication(opened, config.Config{PAHS0Enabled: true}, nil)
	app.strategy = freshstrategy.New(freshstrategy.Options{
		Protector: opened.Protector,
		Qualification: bootstrap.CPAQualification{
			ManagementGeneration: "v8", ArtifactQualified: true, RequiredProfileQualified: true,
		},
		QualifiedArtifactID: model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64)),
	})
	for _, strategy := range []bootstrap.CPAStrategy{bootstrap.ConnectExternal, bootstrap.AdoptExisting} {
		before, err := app.store.Load(ctx)
		if err != nil { t.Fatal(err) }
		body, err := json.Marshal(struct {
			Identity bootstrap.FreshIdentity `json:"identity"`
			ExpectedRevision uint64 `json:"expectedRevision"`
			freshstrategy.Request
		}{before.Operation.Identity, before.Operation.Revision, freshstrategy.Request{
			Strategy: strategy, BaseURL: source.URL, ManagementKey: "disposable-key"}})
		if err != nil { t.Fatal(err) }
		r := httptest.NewRequest(http.MethodPost, "/control/api/v2/strategy", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
		w := httptest.NewRecorder()
		app.canonicalAPI(w, r)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "fresh_strategy_owner_conflict") {
			t.Fatalf("strategy %s was not denied by PAH-S0 policy: %d: %s", strategy, w.Code, w.Body.String())
		}
		after, err := app.store.Load(ctx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("P-S0 strategy %s mutated durable Fresh CAS: %v", strategy, err)
		}
	}
	if upstream.Load() != 0 { t.Fatalf("external CPA contacted before P-S0 deny: %d", upstream.Load()) }
	// Positive contrast: with the *same* legitimate External input and
	// qualification, the underlying adapter actually reaches CPA. Therefore
	// the HTTP 409 above proves the PAH-S0 policy, not some other blocker.
	before, err := app.store.Load(ctx)
	if err != nil { t.Fatal(err) }
	result, err := app.strategy.Execute(ctx, before.Operation, freshstrategy.Request{
		Strategy: bootstrap.ConnectExternal, BaseURL: source.URL, ManagementKey: "disposable-key",
	})
	if err != nil || result.Connection == nil || upstream.Load() == 0 {
		t.Fatalf("positive External adapter control never accessed CPA: %v calls=%d", err, upstream.Load())
	}
	after, err := app.store.Load(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("direct adapter control accidentally wrote Fresh checkpoint", err)
	}
	if rec.Operation.Routing.Target != "/control" { t.Fatal("test fixture lost actual routing") }
}
