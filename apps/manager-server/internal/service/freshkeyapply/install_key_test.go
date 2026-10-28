package freshkeyapply

import (
	"errors"
	"strings"
	"testing"

	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// The installation mints its Client Key and claims the single initial-install
// attempt before the destination exists. A resume replays both, so the strategy
// can never stage a second credential and the install route never installs a
// duplicate.
func TestEnsureInitialInstallIsIdempotentForOneInstallation(t *testing.T) {
	s, w, _, _ := fixture(t)
	ctx := t.Context()
	first, err := s.EnsureInitialInstall(ctx, "managed:operation-one", 7)
	if err != nil || first.ID == "" || first.RequestID == "" || !strings.HasPrefix(first.Secret, "sk-") {
		t.Fatalf("ensure install key: %+v %v", first, err)
	}
	again, err := s.EnsureInitialInstall(ctx, "managed:operation-one", 7)
	if err != nil || again != first {
		t.Fatalf("replay minted a second key: %+v %v", again, err)
	}
	attempt, err := s.repo.InitialInstall(ctx)
	if err != nil || attempt == nil || attempt.KeyID != string(first.ID) ||
		attempt.RunID != "managed:operation-one" || attempt.Generation != "7" {
		t.Fatalf("initial-install journal: %+v %v", attempt, err)
	}
	// A different run is a different installation: the journal never rebinds.
	if _, err = s.EnsureInitialInstall(ctx, "managed:operation-two", 7); !errors.Is(err, ErrGeneration) {
		t.Fatalf("journal rebound to another run: %v", err)
	}
	if _, err = s.EnsureInitialInstall(ctx, "managed:operation-one", 8); !errors.Is(err, ErrGeneration) {
		t.Fatalf("journal rebound to another generation: %v", err)
	}
	// It is a real canonical Key with the installation origin.
	keys, err := s.keys.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var installer *freshkeys.Key
	for i := range keys {
		if keys[i].Origin == freshkeys.OriginInstaller {
			installer = &keys[i]
		}
	}
	if installer == nil || installer.ID != first.ID || len(keys) != 2 {
		t.Fatalf("canonical projection: %+v", keys)
	}
	if w.calls != 0 {
		t.Fatal("recording the install key wrote to CPA")
	}
}

// The install-generated Key is already in the destination, so the existing
// initial-install route observes it instead of installing a second key. A
// workspace Key that owns the journal keeps the strict conflict.
func TestApplyObservesTheInstallKeyWithoutASecondInstall(t *testing.T) {
	s, w, _, _ := fixture(t)
	ctx := t.Context()
	key, err := s.EnsureInitialInstall(ctx, "cpamp-s1-test-run", 7)
	if err != nil {
		t.Fatal(err)
	}
	w.configured, w.auth = true, true
	state, err := s.Apply(ctx, string(key.ID), 7)
	if err != nil || !state.Attempted || !state.ManagerStaged || !state.CPAConfigured ||
		state.RuntimeState != "effective_observed" || state.InstallAvailable {
		t.Fatalf("install key state: %+v %v", state, err)
	}
	if w.calls != 0 {
		t.Fatal("install key was installed a second time")
	}
	// A workspace Key cannot claim the consumed install journal, and no write is
	// attempted for it.
	other, _, err := s.keys.Create(ctx, strings.Repeat("c", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, string(other.Key.ID), 7); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("workspace key consumed the install journal: %v", err)
	}
	if w.calls != 0 {
		t.Fatal("refused install wrote to CPA")
	}
}
