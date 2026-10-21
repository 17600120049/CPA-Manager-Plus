package main

import (
	"bytes"
	"context"
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
	for _, kind := range []string{"external", "adopt"} {
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
			} else {
				checkpoint, ciphertext = pahS0Adoption(t, opened)
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
			admitted, err := serveExistingFresh(ctx, config.Config{PAHS0Enabled: true}, paths, h)
			if admitted != nil { _ = admitted.Close() }
			if err == nil { t.Fatal("forbidden persisted source attached as P-S0") }
			if h.fresh.Load() != nil { t.Fatal("forbidden source published a Fresh handler") }
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/pah-s0/resource-guard", nil))
			if w.Code != http.StatusNotFound { t.Fatalf("unadmitted Manager attested resource guard: %d", w.Code) }
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
		w.WriteHeader(http.StatusOK)
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
		if w.Code != http.StatusConflict {
			t.Fatalf("strategy %s returned %d: %s", strategy, w.Code, w.Body.String())
		}
		after, err := app.store.Load(ctx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("P-S0 strategy %s mutated durable Fresh CAS: %v", strategy, err)
		}
	}
	if upstream.Load() != 0 { t.Fatalf("external CPA contacted before P-S0 deny: %d", upstream.Load()) }
	if rec.Operation.Routing.Target != "/control" { t.Fatal("test fixture lost actual routing") }
}
