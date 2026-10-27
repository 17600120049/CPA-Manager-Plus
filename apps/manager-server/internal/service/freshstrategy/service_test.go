package freshstrategy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

func operation() bootstrap.FreshOperation {
	return bootstrap.FreshOperation{Version: 1, Identity: bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: "installation", Generation: "generation", DatabasePath: "/private/manager.sqlite", OperationID: "fresh-operation-123"}, Revision: 1, AdminInitialized: true, GatewayPending: true}
}

func qualified() bootstrap.CPAQualification {
	return bootstrap.CPAQualification{ManagementGeneration: "v8", ArtifactQualified: true, RequiredProfileQualified: true}
}

type managedRuntime struct {
	runtimeservice.RuntimeClient
	status model.RuntimeObservedStatus
	starts []model.RuntimeMutationRequest
}

func (r *managedRuntime) Status(context.Context) (model.RuntimeObservedStatus, error) {
	return r.status, nil
}
func (r *managedRuntime) Start(_ context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	r.starts = append(r.starts, request)
	return model.RuntimeOperationResult{OperationID: request.OperationID, OperationType: model.RuntimeOperationStart, RuntimeIdentity: r.status.Identity, RuntimeGeneration: r.status.Generation, State: model.RuntimeOperationSucceeded}, nil
}

func TestManagedUnqualifiedOrForeignTargetCannotStart(t *testing.T) {
	service := New(Options{})
	if _, err := service.Execute(context.Background(), operation(), Request{Strategy: bootstrap.NewManaged}); err == nil {
		t.Fatal("unqualified artifact accepted")
	}
	op := operation()
	op.Strategy.Strategy = bootstrap.ConnectExternal
	if _, err := service.Execute(context.Background(), op, Request{Strategy: bootstrap.NewManaged}); err == nil {
		t.Fatal("foreign strategy owner accepted")
	}
	op = operation()
	op.AdminInitialized = false
	if _, err := service.Execute(context.Background(), op, Request{Strategy: bootstrap.NewManaged}); err == nil {
		t.Fatal("uninitialized admin accepted")
	}
}

func TestManagedReplayRejectsChangedRequestForEveryDurableStage(t *testing.T) {
	artifact := model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64))
	runtime := &managedRuntime{status: model.RuntimeObservedStatus{Identity: "target-one", Generation: 3, ProtocolVersion: "v1", State: model.RuntimeStateReady, Capabilities: model.RuntimeCapabilities{model.RuntimeCapabilityStart}, ActiveGatewayArtifact: &model.ActiveGatewayArtifact{Engine: "cpa", ArtifactID: artifact}}}
	service := New(Options{Runtime: runtime, Qualification: qualified(), QualifiedArtifactID: artifact})
	for _, stage := range []string{"staged", "starting", "committed"} {
		op := operation()
		op.Strategy = bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.NewManaged, Stage: stage, TargetIdentity: "target-one", TargetGeneration: 3, TargetArtifactID: string(artifact)}
		for _, request := range []Request{
			{Strategy: bootstrap.NewManaged},
			{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "foreign", ExpectedRuntimeGeneration: 3, ExpectedArtifactID: artifact},
			{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "target-one", ExpectedRuntimeGeneration: 4, ExpectedArtifactID: artifact},
			{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "target-one", ExpectedRuntimeGeneration: 3, ExpectedArtifactID: model.RuntimeArtifactID("sha256:" + strings.Repeat("b", 64))},
		} {
			_, err := service.Execute(t.Context(), op, request)
			var blocker *Blocker
			if !errors.As(err, &blocker) || blocker.Code != "managed_target_identity_mismatch" || len(runtime.starts) != 0 {
				t.Fatalf("%s replay bypassed exact request identity: %v", stage, err)
			}
		}
	}
}

func TestExternalStoresOnlyEncryptedManagementSecretAndKeepsGatewayPending(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v8/management/config" {
			t.Errorf("unsafe source access")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	defer server.Close()
	protector, _ := security.NewProtector([]byte("task-specific-data-key"))
	service := New(Options{Protector: protector, Qualification: qualified()})
	request := Request{Strategy: bootstrap.ConnectExternal, BaseURL: server.URL, ManagementKey: "private-management-secret"}
	result, err := service.Execute(context.Background(), operation(), request)
	if err != nil || result.Checkpoint.Stage != "committed" || !result.GatewayPending || result.RuntimeMode != model.RuntimeModeExternal || !security.IsProtected(result.Connection.ManagementKeyCiphertext) {
		t.Fatalf("external: %+v %v", result, err)
	}
	secret, err := protector.UnprotectString(result.Connection.ManagementKeyCiphertext)
	if err != nil || secret != request.ManagementKey {
		t.Fatal("encrypted handoff invalid")
	}
	public, _ := json.Marshal(result)
	if strings.Contains(string(public), "private-management") || strings.Contains(string(public), "enc:v1:") {
		t.Fatal("connection leaked to public result")
	}
	op := operation()
	op.Strategy = result.Checkpoint
	retry, err := service.Execute(context.Background(), op, request)
	if err != nil || retry.Checkpoint.TargetIdentity != result.Checkpoint.TargetIdentity || calls.Load() != 2 {
		t.Fatal("external retry changed target")
	}
	if _, err := New(Options{Protector: protector}).Execute(context.Background(), operation(), request); err == nil {
		t.Fatal("safe read elevated to qualified profile")
	}
	if calls.Load() != 2 {
		t.Fatal("unqualified External touched source")
	}
}

func TestAdoptionPreservesSourceAndExplicitlyBlocksUnsupportedReplay(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v8/management/config" {
			t.Errorf("adoption touched source lifecycle/history/queue")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]},"future-resource":{"secret":"never-return"}}`))
	}))
	defer server.Close()
	result, err := New(Options{Qualification: qualified()}).Execute(context.Background(), operation(), Request{Strategy: bootstrap.AdoptExisting, BaseURL: server.URL, ManagementKey: "private", AdoptSourceClass: bootstrap.AdoptNetworkExisting})
	var blocker *Blocker
	if !errors.As(err, &blocker) || blocker.Code != "adoption_destination_staging_required" || !result.SourcePreserved || result.Checkpoint.Stage == "committed" || calls.Load() != 0 {
		t.Fatalf("adoption: %+v %v", result, err)
	}
	if !strings.Contains(strings.Join(blocker.Unsupported, ","), "cpa_native_destination_staging_required") {
		t.Fatal("adoption blocker lost the accepted staging prerequisite")
	}
	if _, err := New(Options{}).Execute(context.Background(), operation(), Request{Strategy: bootstrap.AdoptExisting, BaseURL: server.URL, ManagementKey: "private", AdoptSourceClass: bootstrap.AdoptNetworkExisting}); err == nil || calls.Load() != 0 {
		t.Fatal("unqualified adoption touched source")
	}
	public, _ := json.Marshal(result)
	if strings.Contains(string(public), "never-return") {
		t.Fatal("source secrets escaped")
	}
	// Source remains usable and receives no stop/delete/replace or usage Pop.
	response, err := http.Get(server.URL + "/v8/management/config")
	if err != nil || response.StatusCode != 200 {
		t.Fatal("source unavailable after failed adoption")
	}
	_ = response.Body.Close()
}

// managedDestinationRuntime is a Runtime that owns one default destination: the
// stage request carries the destination snapshot and Start adopts it. It also
// records every request so a replay can be proven side-effect free.
type managedDestinationRuntime struct {
	runtimeservice.RuntimeClient
	status     model.RuntimeObservedStatus
	starts     []model.RuntimeMutationRequest
	stages     []model.RuntimeDestinationRequest
	startState model.RuntimeOperationState
	stageState model.RuntimeOperationState
	stageErr   error
}

func (r *managedDestinationRuntime) Status(context.Context) (model.RuntimeObservedStatus, error) {
	return r.status, nil
}

func (r *managedDestinationRuntime) Start(_ context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	r.starts = append(r.starts, request)
	state := r.startState
	if state == "" {
		state = model.RuntimeOperationSucceeded
	}
	return model.RuntimeOperationResult{OperationID: request.OperationID, OperationType: model.RuntimeOperationStart, RuntimeIdentity: r.status.Identity, RuntimeGeneration: r.status.Generation, State: state}, nil
}

func (r *managedDestinationRuntime) StageDestination(_ context.Context, request model.RuntimeDestinationRequest) (model.RuntimeOperationResult, error) {
	r.stages = append(r.stages, request)
	if r.stageErr != nil {
		return model.RuntimeOperationResult{}, r.stageErr
	}
	state := r.stageState
	if state == "" {
		state = model.RuntimeOperationSucceeded
	}
	return model.RuntimeOperationResult{OperationID: request.OperationID, OperationType: model.RuntimeOperationStageDestination, RuntimeIdentity: r.status.Identity, RuntimeGeneration: r.status.Generation, State: state}, nil
}

// stagedManagementKey is the credential the Supervisor wrote to the destination.
func (r *managedDestinationRuntime) stagedManagementKey() string {
	if len(r.stages) == 0 {
		return ""
	}
	return r.stages[0].ManagementKey
}

// managedDestinationFixture stages one synthetic destination CPA which only
// answers authenticated reads of its staged management credential.
func managedDestinationFixture(t *testing.T) (*managedDestinationRuntime, string, *security.Protector, model.RuntimeArtifactID) {
	t.Helper()
	artifact := model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64))
	runtime := &managedDestinationRuntime{status: model.RuntimeObservedStatus{Identity: "target-one", Generation: 3, ProtocolVersion: "v1", State: model.RuntimeStateReady,
		Capabilities:          model.RuntimeCapabilities{model.RuntimeCapabilityStart, model.RuntimeCapabilityStageDestination},
		ActiveGatewayArtifact: &model.ActiveGatewayArtifact{Engine: "cpa", ArtifactID: artifact}}}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		key := runtime.stagedManagementKey()
		mu.Unlock()
		if r.URL.Path != "/v8/management/config" || key == "" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	t.Cleanup(server.Close)
	protector, err := security.NewProtector([]byte("fresh-managed-destination-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return runtime, server.URL, protector, artifact
}

func blockedWith(err error, code string) bool {
	var blocker *Blocker
	return errors.As(err, &blocker) && blocker.Code == code
}

// A default install must never persist a start-only checkpoint: it would have no
// credential and no destination, so every later write and the Gateway stay shut.
func TestManagedNeverFallsBackToAStartOnlyCheckpoint(t *testing.T) {
	runtime, baseURL, protector, artifact := managedDestinationFixture(t)
	request := Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "target-one", ExpectedRuntimeGeneration: 3, ExpectedArtifactID: artifact}
	for name, options := range map[string]Options{
		"no destination": {Runtime: runtime, Protector: protector, Qualification: qualified(), QualifiedArtifactID: artifact},
		"no storage":     {Runtime: runtime, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: artifact},
	} {
		result, err := New(options).Execute(t.Context(), operation(), request)
		if !blockedWith(err, "managed_destination_connection_unavailable") || result.Checkpoint.Strategy != "" || result.AdoptionCiphertext != "" || len(runtime.stages) != 0 || len(runtime.starts) != 0 {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
	}
	// A resumed checkpoint without its protected snapshot cannot start either.
	service := New(Options{Runtime: runtime, Protector: protector, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: artifact})
	staged, err := service.Execute(t.Context(), operation(), request)
	if err != nil || staged.AdoptionCiphertext == "" {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	op := operation()
	op.Strategy = staged.Checkpoint
	if result, err := service.Execute(t.Context(), op, request); !blockedWith(err, "managed_destination_snapshot_unavailable") || len(runtime.stages) != 0 || len(runtime.starts) != 0 {
		t.Fatalf("resume without snapshot: %+v %v", result, err)
	}
}

// The complete default destination loop through the durable checkpoint: stage,
// start, authenticated read-back, commit, replay and generation fence.
func TestManagedRequiresDurableExactIntentAndReusesTypedRuntime(t *testing.T) {
	runtime, baseURL, protector, artifact := managedDestinationFixture(t)
	other := model.RuntimeArtifactID("sha256:" + strings.Repeat("b", 64))
	request := Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "target-one", ExpectedRuntimeGeneration: 3, ExpectedArtifactID: artifact}
	op := operation()
	if _, err := New(Options{Runtime: runtime, Protector: protector, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: other}).Execute(t.Context(), op, request); err == nil || len(runtime.stages) != 0 {
		t.Fatal("generic qualification promoted a different exact artifact")
	}
	service := New(Options{Runtime: runtime, Protector: protector, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: artifact})
	staged, err := service.Execute(t.Context(), op, request)
	if err != nil || staged.Checkpoint.Stage != "staged" || len(runtime.stages) != 0 || len(runtime.starts) != 0 || !staged.GatewayPending || staged.Connection != nil {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	op.Strategy = staged.Checkpoint // simulates the caller's durable CAS checkpoint
	request.AdoptionCiphertext = staged.AdoptionCiphertext
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil || committed.Checkpoint.Stage != "committed" || len(runtime.stages) != 1 || len(runtime.starts) != 1 ||
		runtime.starts[0].OperationID != runtime.stages[0].OperationID+":start" || !committed.GatewayPending || committed.Connection == nil {
		t.Fatalf("commit: %+v %v", committed, err)
	}
	if secret, err := protector.UnprotectString(committed.Connection.ManagementKeyCiphertext); err != nil || secret != runtime.stagedManagementKey() {
		t.Fatal("committed connection is not the staged credential")
	}
	if runtime.stages[0].SourceClass != "new_managed" || runtime.stages[0].SourceIdentity != ManagedDestinationIdentity(string(op.Identity.OperationID)) || len(runtime.stages[0].APIKeys) != 0 {
		t.Fatalf("destination request: %+v", runtime.stages[0])
	}
	if committed.Checkpoint.DestinationConnectionIdentity != ManagedDestinationIdentity(baseURL) {
		t.Fatal("checkpoint is not bound to the destination")
	}
	// Lost reply replays the same durable destination operation; no second owner.
	op.Strategy = committed.Checkpoint
	if _, err = service.Execute(t.Context(), op, request); err != nil || len(runtime.stages) != 1 || len(runtime.starts) != 1 {
		t.Fatal("committed retry restarted the destination", err)
	}
	runtime.status.Generation++
	if _, err = service.Execute(t.Context(), op, request); !blockedWith(err, "managed_target_identity_mismatch") {
		t.Fatal("accepted foreign runtime generation", err)
	}
}
func TestManagedMigrationRequiresStoppedRuntimeAndIsJournaled(t *testing.T) {
	runtime, baseURL, protector, artifact := managedDestinationFixture(t)
	service := New(Options{Runtime: runtime, Protector: protector, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: artifact})
	op := operation()
	op.ProductComplete = true
	op.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: op.Identity.OperationID, Revision: 1, Target: "/console", Stage: bootstrap.PrefixCommitted, Committed: true}
	op.Strategy = bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.NewManaged, Stage: "committed", TargetIdentity: string(runtime.status.Identity), TargetGeneration: uint64(runtime.status.Generation), TargetArtifactID: string(artifact)}
	request := Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: runtime.status.Identity, ExpectedRuntimeGeneration: runtime.status.Generation, ExpectedArtifactID: artifact}
	if _, err := service.PrepareManagedMigration(t.Context(), op, request); !blockedWith(err, "managed_migration_requires_stopped") || len(runtime.stages) != 0 {
		t.Fatalf("running legacy destination was not refused: %v", err)
	}
	runtime.status.State = model.RuntimeStateOffline
	prepared, err := service.PrepareManagedMigration(t.Context(), op, request)
	if err != nil || prepared.AdoptionCiphertext == "" || prepared.Checkpoint.Stage != "committed" || prepared.Checkpoint.DestinationConnectionIdentity != "" || len(runtime.stages) != 0 {
		t.Fatalf("prepare migration: %+v %v", prepared, err)
	}
	op.Strategy = prepared.Checkpoint
	request.AdoptionCiphertext = prepared.AdoptionCiphertext
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil || committed.Checkpoint.Stage != "committed" || committed.Checkpoint.DestinationConnectionIdentity == "" || committed.Connection == nil || len(runtime.stages) != 1 || len(runtime.starts) != 1 {
		t.Fatalf("execute migration: %+v %v", committed, err)
	}
	op.Strategy = committed.Checkpoint
	if _, err = service.Execute(t.Context(), op, request); err != nil || len(runtime.stages) != 1 || len(runtime.starts) != 1 {
		t.Fatalf("migration replay was not idempotent: %v", err)
	}
	if runtime.stages[0].OperationID != string(op.Identity.OperationID)+":managed-migration-destination" {
		t.Fatalf("migration used the install operation id: %q", runtime.stages[0].OperationID)
	}
}

func TestManagedMigrationRefusesGatewayAuthPresence(t *testing.T) {
	runtime, baseURL, protector, artifact := managedDestinationFixture(t)
	runtime.status.State = model.RuntimeStateOffline
	runtime.stageErr = &runtimeservice.ProtocolError{Code: runtimeservice.ProtocolErrorCode("gateway_auth_present"), HTTPStatus: 409}
	service := New(Options{Runtime: runtime, Protector: protector, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: artifact})
	op := operation()
	op.ProductComplete = true
	op.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: op.Identity.OperationID, Revision: 1, Target: "/console", Stage: bootstrap.PrefixCommitted, Committed: true}
	op.Strategy = bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.NewManaged, Stage: "committed", TargetIdentity: string(runtime.status.Identity), TargetGeneration: uint64(runtime.status.Generation), TargetArtifactID: string(artifact)}
	request := Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: runtime.status.Identity, ExpectedRuntimeGeneration: runtime.status.Generation, ExpectedArtifactID: artifact}
	prepared, err := service.PrepareManagedMigration(t.Context(), op, request)
	if err != nil {
		t.Fatal(err)
	}
	op.Strategy = prepared.Checkpoint
	request.AdoptionCiphertext = prepared.AdoptionCiphertext
	if _, err = service.Execute(t.Context(), op, request); !blockedWith(err, "managed_gateway_auth_present") {
		t.Fatalf("non-empty Gateway auth directory was not refused: %v", err)
	}
	if len(runtime.starts) != 0 {
		t.Fatal("migration started Runtime after auth refusal")
	}
}
