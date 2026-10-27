package freshstrategy

import (
	"errors"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// stagedManagedInstall drives one default managed install to its staged
// checkpoint and returns the checkpoint and protected snapshot the caller would
// have persisted.
func stagedManagedInstall(t *testing.T) (*managedDestinationRuntime, *Service, Request, bootstrap.FreshOperation, Result) {
	t.Helper()
	runtime, baseURL, protector, artifact := managedDestinationFixture(t)
	service := New(Options{Runtime: runtime, Protector: protector, DestinationBaseURL: baseURL, Qualification: qualified(), QualifiedArtifactID: artifact})
	request := Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "target-one", ExpectedRuntimeGeneration: 3, ExpectedArtifactID: artifact}
	op := operation()
	staged, err := service.Execute(t.Context(), op, request)
	if err != nil || staged.AdoptionCiphertext == "" || staged.Checkpoint.Stage != "staged" {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	op.Strategy = staged.Checkpoint
	request.AdoptionCiphertext = staged.AdoptionCiphertext
	return runtime, service, request, op, staged
}

// destinationRetried reports whether the strategy retired a destination attempt.
func destinationRetried(err error) bool {
	var retry *DestinationRetry
	return errors.As(err, &retry)
}

func destinationRetryOf(t *testing.T, err error, code string, attempt uint64) *DestinationRetry {
	t.Helper()
	var retry *DestinationRetry
	if !errors.As(err, &retry) || retry.Blocker.Code != code || retry.Checkpoint.DestinationAttempt != attempt {
		t.Fatalf("destination retry = %+v %v", retry, err)
	}
	// The browser still receives the typed blocker code.
	var blocker *Blocker
	if !errors.As(err, &blocker) || blocker.Code != code {
		t.Fatalf("blocker mapping lost: %v", err)
	}
	return retry
}

// A terminal stage failure is never resumed by the Supervisor, so the Manager
// must retire the attempt and re-issue the identical destination under the next
// operation ID. Replaying the failed ID forever would make the install
// uncompletable.
func TestManagedStageTerminalFailureRetiresTheAttempt(t *testing.T) {
	runtime, service, request, op, staged := stagedManagedInstall(t)
	runtime.stageState = model.RuntimeOperationFailed
	result, err := service.Execute(t.Context(), op, request)
	retry := destinationRetryOf(t, err, "managed_destination_stage_failed", 1)
	if result.Connection != nil || len(runtime.starts) != 0 {
		t.Fatal("terminal stage failure reported a connection or started Runtime")
	}
	// The retired checkpoint keeps the destination identity; only the attempt
	// index advances, so the retry cannot drift onto a different destination.
	if retry.Checkpoint.DestinationConnectionIdentity != staged.Checkpoint.DestinationConnectionIdentity || retry.Checkpoint.Stage != "staged" || retry.Checkpoint.TargetIdentity != staged.Checkpoint.TargetIdentity {
		t.Fatalf("retired checkpoint drifted: %+v", retry.Checkpoint)
	}
	op.Strategy = retry.Checkpoint
	runtime.stageState = ""
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil || committed.Checkpoint.Stage != "committed" || committed.Connection == nil {
		t.Fatalf("retry commit: %+v %v", committed, err)
	}
	if len(runtime.stages) != 2 {
		t.Fatalf("stage calls = %d", len(runtime.stages))
	}
	first, second := runtime.stages[0], runtime.stages[1]
	if first.OperationID != string(op.Identity.OperationID)+":managed-destination" || second.OperationID != first.OperationID+":attempt-1" {
		t.Fatalf("destination operation IDs = %q, %q", first.OperationID, second.OperationID)
	}
	if second.ManagementKey != first.ManagementKey || second.SourceIdentity != first.SourceIdentity || second.SourceClass != first.SourceClass {
		t.Fatal("retry changed the destination itself")
	}
	if len(runtime.starts) != 1 || runtime.starts[0].OperationID != second.OperationID+":start" {
		t.Fatalf("start operation = %+v", runtime.starts)
	}
	// A committed checkpoint replays without another attempt.
	op.Strategy = committed.Checkpoint
	if _, err := service.Execute(t.Context(), op, request); err != nil || len(runtime.stages) != 2 || len(runtime.starts) != 1 {
		t.Fatalf("committed replay: %v", err)
	}
}

// A terminal start failure is retryable in exactly the same way, and it also
// re-stages: the Supervisor only supersedes a destination whose whole attempt
// is terminally failed.
func TestManagedStartTerminalFailureRetiresTheAttempt(t *testing.T) {
	runtime, service, request, op, _ := stagedManagedInstall(t)
	runtime.startState = model.RuntimeOperationFailed
	result, err := service.Execute(t.Context(), op, request)
	retry := destinationRetryOf(t, err, "managed_target_start_failed", 1)
	if result.Connection != nil || len(runtime.starts) != 1 {
		t.Fatal("terminal start failure reported a connection")
	}
	op.Strategy = retry.Checkpoint
	runtime.startState = ""
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil || committed.Connection == nil || committed.Checkpoint.Stage != "committed" {
		t.Fatalf("retry commit: %+v %v", committed, err)
	}
	if len(runtime.stages) != 2 || runtime.stages[1].OperationID != runtime.stages[0].OperationID+":attempt-1" {
		t.Fatalf("retried stage IDs = %+v", runtime.stages)
	}
	if len(runtime.starts) != 2 || runtime.starts[1].OperationID != runtime.stages[1].OperationID+":start" || runtime.starts[1].OperationID == runtime.starts[0].OperationID {
		t.Fatalf("retried start IDs = %+v", runtime.starts)
	}
}

// Only terminal failure evidence may advance the attempt. An unresolved
// transport failure, an accepted stage or a running start keeps the same
// operation ID: a second attempt must never race one that may still be running.
func TestManagedUnresolvedDestinationKeepsTheAttempt(t *testing.T) {
	runtime, service, request, op, _ := stagedManagedInstall(t)
	runtime.stageErr = errors.New("destination transport unavailable")
	_, err := service.Execute(t.Context(), op, request)
	if !blockedWith(err, "managed_destination_stage_unresolved") || destinationRetried(err) {
		t.Fatalf("unresolved stage: %v", err)
	}
	runtime.stageErr = nil
	runtime.stageState = model.RuntimeOperationAccepted
	_, err = service.Execute(t.Context(), op, request)
	if !blockedWith(err, "managed_destination_stage_unresolved") || destinationRetried(err) {
		t.Fatalf("accepted stage: %v", err)
	}
	runtime.stageState = ""
	runtime.startState = model.RuntimeOperationRunning
	result, err := service.Execute(t.Context(), op, request)
	if err != nil || result.Checkpoint.Stage != "starting" || result.Checkpoint.DestinationAttempt != 0 {
		t.Fatalf("running start: %+v %v", result, err)
	}
	// The retry replays the exact same operations instead of forking a second
	// attempt.
	runtime.startState = ""
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil || committed.Connection == nil {
		t.Fatalf("resolved retry: %+v %v", committed, err)
	}
	stageIDs := map[string]bool{}
	for _, stage := range runtime.stages {
		stageIDs[stage.OperationID] = true
	}
	if len(stageIDs) != 1 || !stageIDs[string(op.Identity.OperationID)+":managed-destination"] {
		t.Fatalf("unresolved retries forked destination attempts: %+v", runtime.stages)
	}
	startIDs := map[string]bool{}
	for _, start := range runtime.starts {
		startIDs[start.OperationID] = true
	}
	if len(startIDs) != 1 || !startIDs[string(op.Identity.OperationID)+":managed-destination:start"] {
		t.Fatalf("unresolved retries forked start attempts: %+v", runtime.starts)
	}
}

// The legacy managed install migration retries with the same semantics: the
// retired attempt is durable, and the retry uses the next operation ID.
func TestManagedMigrationTerminalFailureRetiresTheAttempt(t *testing.T) {
	runtime, baseURL, protector, artifact := managedDestinationFixture(t)
	runtime.status.State = model.RuntimeStateOffline
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
	if prepared.Checkpoint.DestinationAttempt != 0 {
		t.Fatalf("prepared migration attempt = %d", prepared.Checkpoint.DestinationAttempt)
	}
	op.Strategy = prepared.Checkpoint
	request.AdoptionCiphertext = prepared.AdoptionCiphertext
	runtime.stageState = model.RuntimeOperationFailed
	if _, err = service.Execute(t.Context(), op, request); err == nil {
		t.Fatal("terminal migration stage failure was reported as success")
	}
	retry := destinationRetryOf(t, err, "managed_destination_stage_failed", 1)
	// The migration checkpoint keeps its committed stage and stays without a
	// connection, so the durable record remains a valid pending migration.
	if retry.Checkpoint.Stage != "committed" || retry.Checkpoint.DestinationConnectionIdentity != "" {
		t.Fatalf("retired migration checkpoint: %+v", retry.Checkpoint)
	}
	op.Strategy = retry.Checkpoint
	runtime.stageState = ""
	committed, err := service.Execute(t.Context(), op, request)
	if err != nil || committed.Connection == nil || committed.Checkpoint.DestinationConnectionIdentity == "" {
		t.Fatalf("migration retry commit: %+v %v", committed, err)
	}
	if len(runtime.stages) != 2 || runtime.stages[0].OperationID != string(op.Identity.OperationID)+":managed-migration-destination" ||
		runtime.stages[1].OperationID != string(op.Identity.OperationID)+":managed-migration-destination:attempt-1" {
		t.Fatalf("migration stage IDs = %+v", runtime.stages)
	}
	if len(runtime.starts) != 1 || runtime.starts[0].OperationID != runtime.stages[1].OperationID+":start" {
		t.Fatalf("migration start IDs = %+v", runtime.starts)
	}
}

func TestDestinationOperationIDStaysWithinTheRuntimeLimit(t *testing.T) {
	if id, err := destinationOperationID("fresh:managed-destination", 0); err != nil || id != "fresh:managed-destination" {
		t.Fatalf("attempt zero = %q %v", id, err)
	}
	if id, err := destinationOperationID("fresh:managed-destination", 12); err != nil || id != "fresh:managed-destination:attempt-12" {
		t.Fatalf("retried attempt = %q %v", id, err)
	}
	if _, err := destinationOperationID(strings.Repeat("x", maxRuntimeOperationIDBytes), 1); err == nil {
		t.Fatal("attempt suffix exceeded the Runtime limit")
	}
}
