package freshstore

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

// newAdoptionAttemptStore creates one admitted Fresh record whose staged
// adoption checkpoint has already committed the protected snapshot. When
// commitDestination is set, the same record also publishes its destination: the
// connection whose digest is the checkpoint's DestinationConnectionIdentity and
// the protected management key. Both shapes are valid records; they differ only
// in whether the attempt that staged the destination already succeeded.
func newAdoptionAttemptStore(t *testing.T, commitDestination bool) (Store, bootstrap.FreshIdentity, Record) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	id := bootstrap.FreshIdentity{AdmissionID: "a", InstallationID: "i", Generation: "g", DatabasePath: path, OperationID: "adoption-attempt-operation"}
	admin, _ := security.NewAdminCredential("isolated-fresh-adopt-admin-key", "host")
	store := New(db)
	record, err := store.Create(t.Context(), id, &admin)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, snapshot := adoptionCheckpointFixture(t, adoptionCheckpointBaseURL)
	record, err = store.Update(t.Context(), id, record.Operation.Revision, func(r *Record) error {
		r.Operation.Strategy = checkpoint
		r.AdoptionCiphertext = snapshot
		if commitDestination {
			r.Operation.Strategy.Stage = "committed"
			r.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: adoptionCheckpointBaseURL, ManagementKey: snapshot}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prepare adoption record: %v", err)
	}
	return store, id, record
}

// TestAdoptionCommittedAttemptRefusesTheMonotonicAdvance isolates the committed
// clause of the adoption store guard. The same +1 write is admissible on a
// staged checkpoint: it is monotonic and within MaxAdoptionDestinationRetry, so
// only "a committed attempt may not move" can refuse it on a committed record.
func TestAdoptionCommittedAttemptRefusesTheMonotonicAdvance(t *testing.T) {
	stagedStore, stagedID, staged := newAdoptionAttemptStore(t, false)
	advanced, err := stagedStore.Update(t.Context(), stagedID, staged.Operation.Revision, func(r *Record) error {
		r.Operation.Strategy.DestinationAttempt++
		return nil
	})
	if err != nil || advanced.Operation.Strategy.DestinationAttempt != 1 || advanced.Operation.Strategy.Stage != "staged" {
		t.Fatalf("staged control could not advance the attempt: %+v %v", advanced.Operation.Strategy, err)
	}
	if advanced.Operation.Revision != staged.Operation.Revision+1 {
		t.Fatalf("staged advance revision = %d", advanced.Operation.Revision)
	}

	committedStore, committedID, committed := newAdoptionAttemptStore(t, true)
	if committed.Operation.Strategy.Stage != "committed" || committed.Operation.Strategy.DestinationAttempt != 0 || committed.Connection == nil {
		t.Fatalf("committed fixture is not a committed adoption: %+v", committed.Operation.Strategy)
	}
	if _, err = committedStore.Update(t.Context(), committedID, committed.Operation.Revision, func(r *Record) error {
		r.Operation.Strategy.DestinationAttempt++
		return nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("committed adoption advanced past its successful attempt: %v", err)
	}
	reloaded, err := committedStore.Load(t.Context())
	if err != nil {
		t.Fatalf("committed record did not load after the refusal: %v", err)
	}
	if reloaded.Operation.Revision != committed.Operation.Revision || reloaded.Operation.Strategy.DestinationAttempt != 0 || reloaded.Operation.Strategy.Stage != "committed" {
		t.Fatalf("refused advance mutated the committed record: revision=%d %+v", reloaded.Operation.Revision, reloaded.Operation.Strategy)
	}
}
