package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

// installReadinessRuntime models the Supervisor: Start publishes the spawn, and
// the readiness observation keeps reporting starting until the child has
// answered on its listener for readyAfter observations.
type installReadinessRuntime struct {
	runtimeservice.RuntimeClient
	mu           sync.Mutex
	status       model.RuntimeObservedStatus
	spawned      bool
	observations int
	readyAfter   int
	starts       []model.RuntimeMutationRequest
	stages       []model.RuntimeDestinationRequest
	startState   model.RuntimeOperationState
	startErr     error
}

func (r *installReadinessRuntime) observationsSeen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observations
}

// ready reports the parent-side view the fake CPA listener uses.
func (r *installReadinessRuntime) ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.spawned && r.observations > r.readyAfter
}

func (r *installReadinessRuntime) Status(context.Context) (model.RuntimeObservedStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	observed := r.status
	if !r.spawned {
		observed.State = model.RuntimeStateOffline
		return observed, nil
	}
	r.observations++
	if r.observations <= r.readyAfter {
		observed.State = model.RuntimeStateStarting
		return observed, nil
	}
	observed.State = model.RuntimeStateReady
	return observed, nil
}

func (r *installReadinessRuntime) Start(_ context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, request)
	if r.startErr != nil {
		return model.RuntimeOperationResult{}, r.startErr
	}
	r.spawned = true
	state := r.startState
	if state == "" {
		state = model.RuntimeOperationSucceeded
	}
	return model.RuntimeOperationResult{
		OperationID: request.OperationID, OperationType: model.RuntimeOperationStart,
		RuntimeIdentity: request.ExpectedRuntimeIdentity, RuntimeGeneration: request.ExpectedRuntimeGeneration,
		State: state,
	}, nil
}

func (r *installReadinessRuntime) StageDestination(_ context.Context, request model.RuntimeDestinationRequest) (model.RuntimeOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, request)
	return model.RuntimeOperationResult{
		OperationID: request.OperationID, OperationType: model.RuntimeOperationStageDestination,
		RuntimeIdentity: request.ExpectedRuntimeIdentity, RuntimeGeneration: request.ExpectedRuntimeGeneration,
		State: model.RuntimeOperationSucceeded,
	}, nil
}

func (r *installReadinessRuntime) stagedKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stages) == 0 {
		return ""
	}
	return r.stages[len(r.stages)-1].ManagementKey
}

// installReadinessCPA is one synthetic destination CPA. It answers the
// authenticated management read only once its own Runtime reports ready, which
// is exactly the window the install verification used to race.
func installReadinessCPA(t *testing.T, runtime *installReadinessRuntime) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !runtime.ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		key := runtime.stagedKey()
		if r.URL.Path != "/v8/management/config" || key == "" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config-version":8,"server":{"port":8317},"oauth":{"auth-dir":"auth"},"access":{"api-keys":[]}}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func installReadinessStrategy(t *testing.T, client runtimeservice.RuntimeClient, baseURL string) (*freshstrategy.Service, bootstrap.FreshOperation) {
	t.Helper()
	dir := t.TempDir()
	paths := legacypreflight.DefaultPaths(filepath.Join(dir, "manager.sqlite"))
	paths.Config = filepath.Join(dir, "config.json")
	opened, err := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	artifact := model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64))
	return freshstrategy.New(freshstrategy.Options{
		Runtime: client, Protector: opened.Protector, DestinationBaseURL: baseURL,
		QualifiedArtifactID: artifact,
		Qualification:       bootstrap.CPAQualification{ManagementGeneration: "v8", ArtifactQualified: true, RequiredProfileQualified: true},
	}), opened.Record.Operation
}

// A Supervisor Start result only publishes the spawn. The default managed
// install verifies its staged destination by reading CPA immediately after
// Start, so a first attempt used to fail with
// managed_destination_verification_required and leave the wizard unable to
// continue. Verification now waits for the Supervisor's own readiness signal.
func TestManagedInstallVerificationWaitsForRuntimeReadiness(t *testing.T) {
	runtime := &installReadinessRuntime{
		status: model.RuntimeObservedStatus{
			Identity: "runtime-readiness", Generation: 3, ProtocolVersion: "v1", State: model.RuntimeStateOffline,
			Capabilities:          model.RuntimeCapabilities{model.RuntimeCapabilityStart, model.RuntimeCapabilityStageDestination},
			ActiveGatewayArtifact: &model.ActiveGatewayArtifact{Engine: "cpa", ArtifactID: model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64))},
		},
		// The child needs several readiness observations before its listener
		// answers, which is exactly what a spawned CPA looks like.
		readyAfter: 4,
	}
	baseURL := installReadinessCPA(t, runtime)
	service, op := installReadinessStrategy(t, freshReadyStartRuntime(runtime), baseURL)
	request := freshstrategy.Request{
		Strategy:                  bootstrap.NewManaged,
		ExpectedRuntimeIdentity:   runtime.status.Identity,
		ExpectedRuntimeGeneration: runtime.status.Generation,
		ExpectedArtifactID:        runtime.status.ActiveGatewayArtifact.ArtifactID,
	}
	staged, err := service.Execute(t.Context(), op, request)
	if err != nil || staged.Checkpoint.Stage != "staged" || staged.AdoptionCiphertext == "" {
		t.Fatalf("staged intent: %+v %v", staged, err)
	}
	op.Strategy = staged.Checkpoint
	request.AdoptionCiphertext = staged.AdoptionCiphertext
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil {
		t.Fatal("first install attempt failed against a starting CPA:", err)
	}
	if committed.Checkpoint.Stage != "committed" || committed.Connection == nil {
		t.Fatalf("committed checkpoint = %+v", committed)
	}
	if runtime.observationsSeen() <= runtime.readyAfter {
		t.Fatal("verification did not wait for the readiness observation")
	}
	if len(runtime.starts) != 1 || runtime.starts[0].OperationID != string(op.Identity.OperationID)+":managed-destination:start" {
		t.Fatalf("start operations = %+v", runtime.starts)
	}
}

// A readiness timeout is not a failed spawn and not a lost write: Start keeps
// its exact Supervisor result, the bounded wait ends, and the strategy reports
// the typed blocker the wizard may resume.
func TestReadinessBoundKeepsTheSpawnResultAndTheResumableBlocker(t *testing.T) {
	runtime := &installReadinessRuntime{
		status: model.RuntimeObservedStatus{
			Identity: "runtime-readiness", Generation: 3, ProtocolVersion: "v1", State: model.RuntimeStateOffline,
			Capabilities:          model.RuntimeCapabilities{model.RuntimeCapabilityStart, model.RuntimeCapabilityStageDestination},
			ActiveGatewayArtifact: &model.ActiveGatewayArtifact{Engine: "cpa", ArtifactID: model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64))},
		},
		readyAfter: 1 << 20,
	}
	baseURL := installReadinessCPA(t, runtime)
	client := &freshReadyStartClient{RuntimeClient: runtime, destination: runtime, budget: 40 * time.Millisecond, interval: 5 * time.Millisecond}
	service, op := installReadinessStrategy(t, client, baseURL)
	request := freshstrategy.Request{
		Strategy:                  bootstrap.NewManaged,
		ExpectedRuntimeIdentity:   runtime.status.Identity,
		ExpectedRuntimeGeneration: runtime.status.Generation,
		ExpectedArtifactID:        runtime.status.ActiveGatewayArtifact.ArtifactID,
	}
	staged, err := service.Execute(t.Context(), op, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := func() (freshstrategy.Result, error) {
		op.Strategy = staged.Checkpoint
		request.AdoptionCiphertext = staged.AdoptionCiphertext
		return service.Execute(t.Context(), op, request)
	}()
	var blocker *freshstrategy.Blocker
	if !errors.As(err, &blocker) || blocker.Code != "managed_destination_verification_required" {
		t.Fatalf("blocker = %+v %v", blocker, err)
	}
	if result.Connection != nil || result.Checkpoint.Stage != "staged" {
		t.Fatalf("unverified checkpoint = %+v", result.Checkpoint)
	}
	// The spawn is still durable evidence, so the Supervisor operation stays
	// exactly as Start reported it and the retry replays the same destination.
	if len(runtime.starts) != 1 || runtime.starts[0].OperationID != string(op.Identity.OperationID)+":managed-destination:start" {
		t.Fatalf("start operations = %+v", runtime.starts)
	}
}

func TestReadinessGateOnlyWaitsForASpawnedRuntime(t *testing.T) {
	runtime := &installReadinessRuntime{}
	client := &freshReadyStartClient{RuntimeClient: runtime, destination: runtime, budget: 40 * time.Millisecond, interval: 5 * time.Millisecond}
	request := model.RuntimeMutationRequest{OperationID: "start", ExpectedRuntimeIdentity: "runtime-readiness", ExpectedRuntimeGeneration: 3}
	runtime.startState = model.RuntimeOperationFailed
	result, err := client.Start(t.Context(), request)
	if err != nil || result.State != model.RuntimeOperationFailed {
		t.Fatalf("failed spawn = %+v %v", result, err)
	}
	if runtime.observationsSeen() != 0 {
		t.Fatal("a failed spawn waited for readiness")
	}
	runtime.startState = ""
	runtime.startErr = errors.New("Supervisor unreachable")
	if _, err = client.Start(t.Context(), request); err == nil {
		t.Fatal("transport failure was hidden")
	}
	if runtime.observationsSeen() != 0 {
		t.Fatal("a transport failure waited for readiness")
	}
	// The install consumer still needs the destination capability, and a client
	// without it keeps its exact behaviour.
	if _, ok := freshReadyStartRuntime(runtime).(runtimeservice.DestinationClient); !ok {
		t.Fatal("readiness gate hid the destination capability")
	}
	external := &freshRuntimeTestClient{}
	if got := freshReadyStartRuntime(external); got != runtimeservice.RuntimeClient(external) {
		t.Fatal("an observation-only client was wrapped")
	}
	if freshReadyStartRuntime(nil) != nil {
		t.Fatal("a nil client was wrapped")
	}
}

// An already ready Runtime answers the first observation, so the gate adds no
// latency to the ordinary path.
func TestReadinessGateReturnsOnTheFirstReadyObservation(t *testing.T) {
	runtime := &installReadinessRuntime{
		status: model.RuntimeObservedStatus{Identity: "runtime-readiness", Generation: 3, ProtocolVersion: "v1", State: model.RuntimeStateReady},
	}
	client := &freshReadyStartClient{RuntimeClient: runtime, destination: runtime, budget: freshRuntimeReadinessBudget, interval: freshRuntimeReadinessInterval}
	started := time.Now()
	if _, err := client.Start(context.Background(), model.RuntimeMutationRequest{OperationID: "start", ExpectedRuntimeIdentity: "runtime-readiness", ExpectedRuntimeGeneration: 3}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatal("a ready Runtime still waited:", elapsed)
	}
	if runtime.observationsSeen() != 1 {
		t.Fatalf("readiness observations = %d", runtime.observationsSeen())
	}
	// A different instance's readiness never satisfies this Start.
	other := &installReadinessRuntime{status: model.RuntimeObservedStatus{Identity: "foreign-runtime", Generation: 3, ProtocolVersion: "v1", State: model.RuntimeStateReady}}
	started = time.Now()
	if _, err := (&freshReadyStartClient{RuntimeClient: other, destination: other, budget: 30 * time.Millisecond, interval: 5 * time.Millisecond}).Start(context.Background(), model.RuntimeMutationRequest{OperationID: "start", ExpectedRuntimeIdentity: "runtime-readiness", ExpectedRuntimeGeneration: 3}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatal("a foreign Runtime instance was accepted as ready")
	}
}
