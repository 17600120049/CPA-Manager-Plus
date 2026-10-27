package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/cpaprocess"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/journal"
)

// stageFixture stages one destination and returns the Start request a Manager
// would derive for it.
func stageFixture(t *testing.T) (*prepareFixture, DestinationRequest, string, StartRequest) {
	t.Helper()
	f, r, root := destinationFixture(t)
	if _, err := f.starter.StageDestination(t.Context(), r); err != nil {
		t.Fatalf("stage: %v", err)
	}
	start := StartRequest{
		OperationID:               r.OperationID + ":start",
		ExpectedRuntimeIdentity:   r.ExpectedRuntimeIdentity,
		ExpectedRuntimeGeneration: r.ExpectedRuntimeGeneration,
		ExpectedActiveArtifactID:  r.ExpectedActiveArtifactID,
	}
	return f, r, root, start
}

// failSpawn makes the next spawn fail without publishing a running child, which
// is the only Start outcome that may be retired by a later attempt.
func failSpawn(f *prepareFixture) {
	f.child.onStart = func(context.Context, cpaprocess.StartSpec) error {
		return errors.New("spawn refused")
	}
}

func readStage(t *testing.T, root string) DestinationRequest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "stage.json"))
	if err != nil {
		t.Fatalf("read stage snapshot: %v", err)
	}
	var request DestinationRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("decode stage snapshot: %v", err)
	}
	return request
}

// A Manager that observed a terminal Start failure must be able to complete the
// install. The Supervisor cannot resume the failed operation, so it admits the
// identical destination under the next operation ID and retires, rather than
// deletes or orphans, the attempt it replaced.
func TestDestinationStageRetrySupersedesOnlyTerminalAttemptFailure(t *testing.T) {
	f, r, root, start := stageFixture(t)
	failSpawn(f)
	if _, err := f.starter.Start(t.Context(), start); !errors.Is(err, ErrExecutionFailed) {
		t.Fatalf("failed spawn was not reported: %v", err)
	}
	f.child.onStart = nil
	if f.child.starts != 1 || f.child.stops != 0 {
		t.Fatalf("failed spawn counts = %d/%d", f.child.starts, f.child.stops)
	}
	retried := r
	retried.OperationID = r.OperationID + ":attempt-1"
	staged, err := f.starter.StageDestination(t.Context(), retried)
	if err != nil || staged.State != journal.StateSucceeded || staged.OperationID != retried.OperationID {
		t.Fatalf("retry over a terminal start failure: %+v %v", staged, err)
	}
	if got := readStage(t, root); got.OperationID != retried.OperationID || got.ManagementKey != r.ManagementKey || got.SourceIdentity != r.SourceIdentity {
		t.Fatalf("live destination after retry: %+v", got)
	}
	retired := f.starter.destination.retiredDestination(r.OperationID)
	if got := readStage(t, retired); got.OperationID != r.OperationID {
		t.Fatalf("superseded destination was deleted: %+v", got)
	}
	if f.child.starts != 1 || f.child.stops != 0 {
		t.Fatal("retired attempt was killed or respawned by the stage retry")
	}
	// The retried stage is itself idempotent and publishes nothing twice.
	if replay, err := f.starter.StageDestination(t.Context(), retried); err != nil || !reflect.DeepEqual(replay, staged) {
		t.Fatalf("retry replay: %+v %v", replay, err)
	}
	// A third attempt needs its own terminal failure evidence.
	third := r
	third.OperationID = r.OperationID + ":attempt-2"
	if _, err := f.starter.StageDestination(t.Context(), third); !errors.Is(err, journal.ErrOperationStateConflict) {
		t.Fatalf("attempt without terminal failure evidence was admitted: %v", err)
	}
	if got := readStage(t, root); got.OperationID != retried.OperationID {
		t.Fatal("refused attempt replaced the live destination")
	}
}

// A successful attempt is never superseded: only terminal failure evidence
// permits a new operation ID, and different destination content is never a
// retry of the same destination.
func TestDestinationStageRetryRequiresTerminalFailureEvidence(t *testing.T) {
	f, r, root, start := stageFixture(t)
	if _, err := f.starter.Start(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if _, err := f.starter.Stop(t.Context(), StopRequest{OperationID: "stop-after-start", ExpectedRuntimeIdentity: r.ExpectedRuntimeIdentity, ExpectedRuntimeGeneration: r.ExpectedRuntimeGeneration}); err != nil {
		t.Fatal(err)
	}
	retried := r
	retried.OperationID = r.OperationID + ":attempt-1"
	if _, err := f.starter.StageDestination(t.Context(), retried); !errors.Is(err, journal.ErrOperationStateConflict) {
		t.Fatalf("successfully started attempt was superseded: %v", err)
	}
	changed := retried
	changed.ManagementKey = strings.Repeat("f", 64)
	if _, err := f.starter.StageDestination(t.Context(), changed); !errors.Is(err, journal.ErrOperationIDConflict) {
		t.Fatalf("different destination was admitted as a retry: %v", err)
	}
	retyped := retried
	retyped.SourceIdentity = "external:sha256:" + strings.Repeat("c", 64)
	if _, err := f.starter.StageDestination(t.Context(), retyped); !errors.Is(err, journal.ErrOperationIDConflict) {
		t.Fatalf("different source was admitted as a retry: %v", err)
	}
	if got := readStage(t, root); got.OperationID != r.OperationID {
		t.Fatal("refused attempt replaced the live destination")
	}
}

// A child spawned before its terminal evidence was durable is never respawned,
// killed or displaced. Its operation stays running, so no retry may claim the
// destination while that child still owns it.
func TestDestinationSpawnBeforeTerminalEvidenceIsNeverRetriedOrOrphaned(t *testing.T) {
	f, r, root, start := stageFixture(t)
	f.journal.failAt = "complete"
	if _, err := f.starter.Start(t.Context(), start); !errors.Is(err, ErrExecutionFailed) {
		t.Fatalf("missing terminal evidence reported success: %v", err)
	}
	f.journal.failAt = ""
	if f.child.starts != 1 {
		t.Fatalf("spawn count after failed journal completion = %d", f.child.starts)
	}
	replayed, err := f.starter.Start(t.Context(), start)
	if err != nil || replayed.State != journal.StateRunning || f.child.starts != 1 {
		t.Fatalf("replay of the spawned operation: %+v spawns=%d %v", replayed, f.child.starts, err)
	}
	retried := r
	retried.OperationID = r.OperationID + ":attempt-1"
	if _, err := f.starter.StageDestination(t.Context(), retried); !errors.Is(err, journal.ErrOperationStateConflict) {
		t.Fatalf("running attempt was displaced: %v", err)
	}
	if got := readStage(t, root); got.OperationID != r.OperationID {
		t.Fatal("running attempt lost its destination")
	}
	if _, err := f.starter.Stop(t.Context(), StopRequest{OperationID: "stop-uncertain", ExpectedRuntimeIdentity: r.ExpectedRuntimeIdentity, ExpectedRuntimeGeneration: r.ExpectedRuntimeGeneration}); err != nil {
		t.Fatal(err)
	}
	if f.child.stops != 1 {
		t.Fatalf("confirmed stop count = %d", f.child.stops)
	}
	// An exited child does not make an uncertain operation retryable; that stays
	// a manual-intervention decision.
	if _, err := f.starter.StageDestination(t.Context(), retried); !errors.Is(err, journal.ErrOperationStateConflict) {
		t.Fatalf("uncertain attempt was retried: %v", err)
	}
}

// A retry that cannot publish leaves the superseded destination live and
// complete, and the next attempt may still publish once the obstruction clears.
func TestDestinationRetryPublishFailureKeepsThePreviousDestination(t *testing.T) {
	f, r, root, start := stageFixture(t)
	failSpawn(f)
	if _, err := f.starter.Start(t.Context(), start); !errors.Is(err, ErrExecutionFailed) {
		t.Fatal("failed spawn was not reported")
	}
	f.child.onStart = nil
	retired := f.starter.destination.retiredDestination(r.OperationID)
	if err := os.Mkdir(retired, 0o700); err != nil {
		t.Fatal(err)
	}
	retried := r
	retried.OperationID = r.OperationID + ":attempt-1"
	if _, err := f.starter.StageDestination(t.Context(), retried); !errors.Is(err, ErrExecutionFailed) {
		t.Fatalf("obstructed publish was not reported: %v", err)
	}
	failed, err := f.journal.Store.Get(t.Context(), "runtime-01", retried.OperationID)
	if err != nil || failed.State != journal.StateFailed || failed.FailureCode != "destination_stage_failed" {
		t.Fatalf("failed retry evidence: %+v %v", failed, err)
	}
	if got := readStage(t, root); got.OperationID != r.OperationID {
		t.Fatal("failed retry displaced the live destination")
	}
	if f.child.starts != 1 || f.child.stops != 0 {
		t.Fatal("failed publish touched the child")
	}
	if err := os.Remove(retired); err != nil {
		t.Fatal(err)
	}
	next := r
	next.OperationID = r.OperationID + ":attempt-2"
	if _, err := f.starter.StageDestination(t.Context(), next); err != nil {
		t.Fatalf("retry after the obstruction cleared: %v", err)
	}
	if got := readStage(t, root); got.OperationID != next.OperationID {
		t.Fatalf("live destination after recovery: %+v", got)
	}
	if got := readStage(t, retired); got.OperationID != r.OperationID {
		t.Fatalf("superseded destination after recovery: %+v", got)
	}
}

// A stage that fails before it publishes anything is retryable under a new
// operation ID; the Supervisor never resumes the failed operation itself.
func TestDestinationStageFailureBeforePublishIsRetriedUnderANewOperation(t *testing.T) {
	f, r, root := destinationFixture(t)
	request := newManagedRequest(r)
	parent := filepath.Dir(root)
	// An untrusted destination parent makes the publish fail before anything is
	// written; the failure is durable and the destination stays absent.
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := f.starter.StageDestination(t.Context(), request); !errors.Is(err, ErrExecutionFailed) {
		t.Fatalf("untrusted parent published a destination: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("failed stage published a destination")
	}
	failed, err := f.journal.Store.Get(t.Context(), "runtime-01", request.OperationID)
	if err != nil || failed.State != journal.StateFailed || failed.FailureCode != "destination_stage_failed" {
		t.Fatalf("failed stage evidence: %+v %v", failed, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	// The Supervisor never resumes a terminal operation: the same ID only
	// replays the recorded failure, which is exactly what the Manager observes.
	if replayed, err := f.starter.StageDestination(t.Context(), request); err != nil || replayed.State != journal.StateFailed {
		t.Fatalf("terminal stage evidence: %+v %v", replayed, err)
	}
	// A stage that published nothing is retryable by the next attempt.
	retried := request
	retried.OperationID = request.OperationID + ":attempt-1"
	if _, err := f.starter.StageDestination(t.Context(), retried); err != nil {
		t.Fatalf("retry after a stage that published nothing: %v", err)
	}
	if got := readStage(t, root); got.OperationID != retried.OperationID {
		t.Fatalf("live destination after retry: %+v", got)
	}
	if _, err := os.Stat(f.starter.destination.retiredDestination(request.OperationID)); !os.IsNotExist(err) {
		t.Fatal("a destination that never published was retired")
	}
}

// recordDestinationOperation writes the exact durable evidence the Supervisor
// itself would record for one destination operation. It constructs the states a
// filesystem fault or a crash leaves behind, which no successful call produces:
// an empty state leaves the operation running.
func recordDestinationOperation(t *testing.T, f *prepareFixture, intent journal.Intent, state journal.State, code string) {
	t.Helper()
	if _, created, err := f.journal.Store.Begin(t.Context(), f.starter.authority, intent); err != nil || !created {
		t.Fatalf("begin %s: created=%v err=%v", intent.OperationID, created, err)
	}
	if _, err := f.journal.Store.MarkRunning(t.Context(), f.starter.authority.RuntimeIdentity, intent.OperationID); err != nil {
		t.Fatalf("mark running %s: %v", intent.OperationID, err)
	}
	if state == "" {
		return
	}
	if _, err := f.journal.Store.Complete(t.Context(), f.starter.authority.RuntimeIdentity, intent.OperationID, state, code); err != nil {
		t.Fatalf("complete %s: %v", intent.OperationID, err)
	}
}

// destinationStartIntent is the journal identity of the Start a Manager derives
// for one stage request.
func destinationStartIntent(r DestinationRequest, operationID string) journal.Intent {
	return journal.Intent{
		OperationID:               operationID,
		OperationType:             "start",
		ExpectedRuntimeIdentity:   r.ExpectedRuntimeIdentity,
		ExpectedRuntimeGeneration: r.ExpectedRuntimeGeneration,
		RequestFingerprint:        sha256.Sum256([]byte(`runtime.start/v1:{"expectedActiveArtifactId":"` + string(r.ExpectedActiveArtifactID) + `"}`)),
	}
}

// failedStageWithLiveRoot reproduces the one state a stage can leave when its
// final directory sync fails after the rename: the destination is published and
// live, while the operation is terminally failed.
func failedStageWithLiveRoot(t *testing.T, f *prepareFixture, r DestinationRequest, root string) {
	t.Helper()
	if err := f.starter.destination.publish(r, ""); err != nil {
		t.Fatalf("publish live root: %v", err)
	}
	recordDestinationOperation(t, f, destinationIntent(f.starter.authority, r), journal.StateFailed, "destination_stage_failed")
	if got := readStage(t, root); got.OperationID != r.OperationID {
		t.Fatalf("live root after failed stage: %+v", got)
	}
}

func managedStartRequest(r DestinationRequest) StartRequest {
	return StartRequest{
		OperationID:               r.OperationID + ":start",
		ExpectedRuntimeIdentity:   r.ExpectedRuntimeIdentity,
		ExpectedRuntimeGeneration: r.ExpectedRuntimeGeneration,
		ExpectedActiveArtifactID:  r.ExpectedActiveArtifactID,
	}
}

// A stage can publish its live root and still be recorded as failed. Start must
// never launch from such a destination, and a retry may only retire that root
// because no Start evidence exists at all, which is what proves no child owns it.
func TestDestinationFailedStageNeverStarts(t *testing.T) {
	f, r, root := destinationFixture(t)
	failedStageWithLiveRoot(t, f, r, root)
	start := managedStartRequest(r)
	if _, err := f.starter.Start(t.Context(), start); !errors.Is(err, journal.ErrOperationStateConflict) {
		t.Fatalf("start from a failed stage: %v", err)
	}
	if f.child.starts != 0 {
		t.Fatal("spawned from a destination its stage never admitted")
	}
	if _, err := f.journal.Store.Get(t.Context(), "runtime-01", start.OperationID); !errors.Is(err, journal.ErrOperationNotFound) {
		t.Fatalf("refused start recorded evidence: %v", err)
	}
	// No Start exists for the attempt, so the next attempt may publish and
	// retire the live root instead of leaving the install uncompletable.
	retried := r
	retried.OperationID = r.OperationID + ":attempt-1"
	staged, err := f.starter.StageDestination(t.Context(), retried)
	if err != nil || staged.State != journal.StateSucceeded {
		t.Fatalf("retry after a failed stage without Start evidence: %+v %v", staged, err)
	}
	if got := readStage(t, root); got.OperationID != retried.OperationID {
		t.Fatalf("live destination after retry: %+v", got)
	}
	if got := readStage(t, f.starter.destination.retiredDestination(r.OperationID)); got.OperationID != r.OperationID {
		t.Fatalf("superseded root was deleted: %+v", got)
	}
	if f.child.starts != 0 || f.child.stops != 0 {
		t.Fatal("retry after a failed stage touched the child")
	}
}

// A failed stage whose Start is accepted, running or succeeded may own a child,
// so no retry may rename that destination aside.
func TestDestinationFailedStageWithStartEvidenceIsNotSuperseded(t *testing.T) {
	for name, state := range map[string]journal.State{"running start": "", "succeeded start": journal.StateSucceeded} {
		t.Run(name, func(t *testing.T) {
			f, r, root := destinationFixture(t)
			failedStageWithLiveRoot(t, f, r, root)
			recordDestinationOperation(t, f, destinationStartIntent(r, r.OperationID+":start"), state, "")
			retried := r
			retried.OperationID = r.OperationID + ":attempt-1"
			if _, err := f.starter.StageDestination(t.Context(), retried); !errors.Is(err, journal.ErrOperationStateConflict) {
				t.Fatalf("failed stage with %s was superseded: %v", name, err)
			}
			if got := readStage(t, root); got.OperationID != r.OperationID {
				t.Fatal("refused retry displaced the live destination")
			}
			if _, err := os.Stat(f.starter.destination.retiredDestination(r.OperationID)); !os.IsNotExist(err) {
				t.Fatal("refused retry retired the live destination")
			}
			if f.child.starts != 0 || f.child.stops != 0 {
				t.Fatal("refused retry touched the child")
			}
		})
	}
}

// A stage that failed after publishing is still retryable once its Start is
// terminally failed: that is the only evidence that no child was launched.
func TestDestinationFailedStageWithFailedStartIsRetriedUnderANewOperation(t *testing.T) {
	f, r, root := destinationFixture(t)
	failedStageWithLiveRoot(t, f, r, root)
	recordDestinationOperation(t, f, destinationStartIntent(r, r.OperationID+":start"), journal.StateFailed, "process_start_failed")
	retried := r
	retried.OperationID = r.OperationID + ":attempt-1"
	staged, err := f.starter.StageDestination(t.Context(), retried)
	if err != nil || staged.State != journal.StateSucceeded {
		t.Fatalf("retry after a failed stage and failed start: %+v %v", staged, err)
	}
	if got := readStage(t, root); got.OperationID != retried.OperationID {
		t.Fatalf("live destination after retry: %+v", got)
	}
	if got := readStage(t, f.starter.destination.retiredDestination(r.OperationID)); got.OperationID != r.OperationID {
		t.Fatalf("superseded root was deleted: %+v", got)
	}
	// The retried destination is startable because its own stage succeeded.
	if _, err := f.starter.Start(t.Context(), managedStartRequest(retried)); err != nil || f.child.starts != 1 {
		t.Fatalf("start after the retried stage: %v spawns=%d", err, f.child.starts)
	}
}
