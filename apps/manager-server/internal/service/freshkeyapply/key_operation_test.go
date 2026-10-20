package freshkeyapply

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

type operationWire struct {
	fakeWire
	mu          sync.Mutex
	keys        []string
	live        []string
	writes      int
	writeErr    error
	skipWrite   bool
	noReload    bool
	beforeWrite func()
}

func (w *operationWire) KeySnapshot(context.Context, string) (cpa.KeySnapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return cpa.KeySnapshot{Keys: slices.Clone(w.keys), SourceSHA256: strings.Repeat("a", 64)}, nil
}
func (w *operationWire) WriteKeySet(_ context.Context, _ string, keys []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	if !w.skipWrite {
		w.keys = slices.Clone(keys)
		if !w.noReload {
			w.live = slices.Clone(keys)
		}
	}
	return w.writeErr
}
func (w *operationWire) SampleKeySet(_ context.Context, accepted, rejected []string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, k := range accepted {
		if !slices.Contains(w.live, k) {
			return false, nil
		}
	}
	for _, k := range rejected {
		if slices.Contains(w.live, k) {
			return false, nil
		}
	}
	return len(w.live) > 0, nil
}

func operationFixture(t *testing.T) (*Service, *operationWire, string, string, string, string) {
	t.Helper()
	t.Setenv("CPAMP_PRIVATE_KEY_OPERATIONS", "1")
	s, _, _, oldID := fixture(t)
	p, _ := s.permit()
	s.permit = func() (Permit, error) { return p, nil }
	if err := s.repo.ClaimInitialInstall(t.Context(), repo.InitialInstall{KeyID: oldID, RunID: p.RunID, Generation: p.Generation, AttemptedAtMS: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.keys.Create(t.Context(), strings.Repeat("c", 32))
	if err != nil {
		t.Fatal(err)
	}
	var old string
	if err = s.keys.WithStagedSecret(t.Context(), oldID, func(v string) error { old = v; return nil }); err != nil {
		t.Fatal(err)
	}
	w := &operationWire{fakeWire: fakeWire{configured: true, auth: true}, keys: []string{old}, live: []string{old}}
	s.wire = w
	return s, w, oldID, string(next.Key.ID), old, next.CreatedSecret
}
func grantStep(s *Service, id, step string) {
	p, _ := s.permit()
	grant := KeyPermit{OperationID: id, Step: step, RunID: p.RunID, Generation: p.Generation, SourceSHA256: strings.Repeat("a", 64), IssuedAtMS: time.Now().UnixMilli(), ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}
	s.keyPermit = func() (KeyPermit, error) { return grant, nil }
}
func mustPrepare(t *testing.T, s *Service, id, old, next, mode string) {
	t.Helper()
	if _, err := s.Prepare(t.Context(), id, old, next, mode, 7); err != nil {
		t.Fatal(err)
	}
}

func TestNecessaryReplacementAndCanonicalHistory(t *testing.T) {
	s, w, oldID, nextID, old, next := operationFixture(t)
	id := strings.Repeat("1", 32)
	mustPrepare(t, s, id, oldID, nextID, "replace")
	if _, err := s.Admit(t.Context(), old, 7, strings.Repeat("f", 64)); err == nil {
		t.Fatal("pending operation admitted")
	}
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, ErrUnavailable) {
		t.Fatal("old S1 permit authorized mutation", err)
	}
	grantStep(s, id, "add")
	r, err := s.ContinueOperation(t.Context(), id, 7)
	if err != nil || r.NextStep != "revoke" || !sameKeys(w.keys, []string{old, next}) {
		t.Fatal(r, err)
	}
	// A reused add permit cannot authorize the next destructive step.
	if _, err = s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, ErrUnavailable) || w.writes != 1 {
		t.Fatal("reused step grant", err)
	}
	grantStep(s, id, "revoke")
	r, err = s.ContinueOperation(t.Context(), id, 7)
	if err != nil || r.Operation.Outcome != "committed" || r.RuntimeState != "effective_observed" || !sameKeys(w.keys, []string{next}) {
		t.Fatal(r, err)
	}
	key, err := s.keys.Detail(t.Context(), oldID)
	if err != nil || key.Lifecycle != "superseded" {
		t.Fatal(key, err)
	}
	if ok, err := s.keys.MatchStagedSecret(t.Context(), old); err != nil || ok {
		t.Fatal("retired canonical credential matched", err)
	}
	if _, err = s.Admit(t.Context(), old, 7, strings.Repeat("f", 64)); err == nil {
		t.Fatal("old key admitted")
	}
	if _, err = s.Admit(t.Context(), next, 7, strings.Repeat("f", 64)); err != nil {
		t.Fatal("replacement not admitted", err)
	}
	if _, err = s.ContinueOperation(t.Context(), id, 7); err != nil || w.writes != 2 {
		t.Fatal("completed op repeated writes", err)
	}
	a, err := s.repo.InitialInstall(t.Context())
	if err != nil || a.KeyID != oldID {
		t.Fatal("S1 journal modified")
	}
	list, err := s.keys.List(t.Context())
	if err != nil || len(list) != 2 {
		t.Fatal("identity history discarded", err)
	}
}

func TestDirectRevokeLastKeyAndStagedKeeper(t *testing.T) {
	s, w, oldID, nextID, old, next := operationFixture(t)
	id := strings.Repeat("2", 32)
	if _, err := s.Prepare(t.Context(), id, oldID, nextID, "revoke", 7); !errors.Is(err, ErrLastKey) {
		t.Fatal(err)
	}
	w.keys = []string{old, next} // Configured, but keeper has not become effective.
	if _, err := s.Prepare(t.Context(), id, oldID, nextID, "revoke", 7); !errors.Is(err, cpa.ErrSourceConflict) {
		t.Fatal("staged-only keeper trusted", err)
	}
	w.live = slices.Clone(w.keys)
	mustPrepare(t, s, id, oldID, nextID, "revoke")
	grantStep(s, id, "revoke")
	r, err := s.ContinueOperation(t.Context(), id, 7)
	if err != nil || r.Operation.Outcome != "committed" || w.writes != 1 {
		t.Fatal(r, err)
	}
}

func TestLostReplyAndRestartResumeOnlyUnattemptedStep(t *testing.T) {
	s, w, oldID, nextID, _, _ := operationFixture(t)
	id := strings.Repeat("3", 32)
	mustPrepare(t, s, id, oldID, nextID, "replace")
	grantStep(s, id, "add")
	w.writeErr = cpa.ErrApplyUnknown
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, cpa.ErrApplyUnknown) {
		t.Fatal(err)
	}
	restarted := New(s.repo, s.keys, s.runtime)
	restarted.permit = s.permit
	restarted.wire = w
	r, err := restarted.CheckOperation(t.Context(), id, 7)
	if err != nil || r.NextStep != "revoke" || w.writes != 1 {
		t.Fatal(r, err)
	}
	oldGrant, _ := s.keyPermit()
	oldGrant.IssuedAtMS = restarted.startedAtMS - 1
	restarted.keyPermit = func() (KeyPermit, error) { return oldGrant, nil }
	if _, err = restarted.ContinueOperation(t.Context(), id, 7); !errors.Is(err, ErrUnavailable) {
		t.Fatal("old grant survived Manager restart", err)
	}
	grantStep(restarted, id, "revoke")
	w.writeErr = nil
	r, err = restarted.ContinueOperation(t.Context(), id, 7)
	if err != nil || r.Operation.Outcome != "committed" || w.writes != 2 {
		t.Fatal(r, err)
	}
}

func TestDirectRevokeEitherCanonicalTargetAndConflictingOperations(t *testing.T) {
	for _, retainOriginal := range []bool{false, true} {
		t.Run(strconv.FormatBool(retainOriginal), func(t *testing.T) {
			s, w, oldID, nextID, old, next := operationFixture(t)
			w.keys, w.live = []string{old, next}, []string{old, next}
			target, keeper := oldID, nextID
			if retainOriginal {
				target, keeper = nextID, oldID
			}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, id := range []string{strings.Repeat("1", 32), strings.Repeat("2", 32)} {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					_, err := s.Prepare(t.Context(), id, target, keeper, "revoke", 7)
					errs <- err
				}(id)
			}
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
				}
			}
			if successes != 1 {
				t.Fatal("double revoke admitted", successes)
			}
			if _, err := s.Prepare(t.Context(), strings.Repeat("3", 32), keeper, target, "replace", 7); err == nil {
				t.Fatal("replacement bypassed unresolved revoke")
			}
			if _, err := s.Apply(t.Context(), keeper, 7); !errors.Is(err, repo.ErrConflict) {
				t.Fatal("S1 reinstallation bypassed operation", err)
			}
			o, _ := s.repo.LatestOperation(t.Context())
			grantStep(s, o.ID, "revoke")
			r, err := s.ContinueOperation(t.Context(), o.ID, 7)
			if err != nil || r.Operation.Outcome != "committed" || w.writes != 1 || len(w.keys) != 1 {
				t.Fatal(r, err)
			}
		})
	}
}

func TestRuntimeGenerationChangeAfterPutRemainsUnknown(t *testing.T) {
	s, w, oldID, nextID, _, _ := operationFixture(t)
	id := strings.Repeat("d", 32)
	mustPrepare(t, s, id, oldID, nextID, "replace")
	grantStep(s, id, "add")
	runtime := s.runtime.(*observedRuntime)
	w.beforeWrite = func() { runtime.status.Generation++ }
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	if _, err := s.CheckOperation(t.Context(), id, 8); !errors.Is(err, ErrGeneration) {
		t.Fatal("journal rebound", err)
	}
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, ErrGeneration) || w.writes != 1 {
		t.Fatal("generation change retried", err)
	}
	o, _ := s.repo.Operation(t.Context(), id)
	if o.Outcome != "unknown" || o.Steps[0].State != "attempted" {
		t.Fatal("false observed state", o)
	}
}

func TestRevokedHistoryDoesNotBecomeNeverAppliedAfterLaterOperations(t *testing.T) {
	s, _, target, keeper, _, _ := operationFixture(t)
	for i := 0; i < 3; i++ {
		if i > 0 {
			created, _, err := s.keys.Create(t.Context(), strings.Repeat(strconv.Itoa(i), 32))
			if err != nil {
				t.Fatal(err)
			}
			keeper = string(created.Key.ID)
		}
		id := strings.Repeat(strconv.Itoa(i+3), 32)
		mustPrepare(t, s, id, target, keeper, "replace")
		for _, step := range []string{"add", "revoke"} {
			grantStep(s, id, step)
			if _, err := s.ContinueOperation(t.Context(), id, 7); err != nil {
				t.Fatal(err)
			}
		}
		target = keeper
	}
	keys, err := s.keys.List(t.Context())
	if err != nil || len(keys) != 4 {
		t.Fatal(keys, err)
	}
	for _, key := range keys {
		if key.RuntimeState != "effect_unknown" {
			t.Fatal("past applied key mislabeled", key)
		}
		if string(key.ID) != keeper && key.Lifecycle != "superseded" {
			t.Fatal("revoked history lost", key)
		}
	}
}

func TestUnknownAttemptNeverRetriesOrAllowsNewOperation(t *testing.T) {
	s, w, oldID, nextID, _, _ := operationFixture(t)
	id := strings.Repeat("4", 32)
	mustPrepare(t, s, id, oldID, nextID, "replace")
	grantStep(s, id, "add")
	w.skipWrite = true
	w.writeErr = cpa.ErrApplyUnknown
	_, _ = s.ContinueOperation(t.Context(), id, 7)
	for i := 0; i < 2; i++ {
		if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, cpa.ErrApplyUnknown) {
			t.Fatal(err)
		}
	}
	if _, err := s.Prepare(t.Context(), strings.Repeat("5", 32), oldID, nextID, "replace", 7); err == nil {
		t.Fatal("new op bypassed journal")
	}
	if w.writes != 1 {
		t.Fatal("unknown write retried")
	}
}

func TestReloadMustActuallyApplyBeforeOldKeyRemoval(t *testing.T) {
	s, w, oldID, nextID, _, _ := operationFixture(t)
	id := strings.Repeat("6", 32)
	mustPrepare(t, s, id, oldID, nextID, "replace")
	grantStep(s, id, "add")
	w.noReload = true
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, cpa.ErrApplyUnknown) {
		t.Fatal("readback claimed live effect", err)
	}
	grantStep(s, id, "revoke")
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, cpa.ErrApplyUnknown) || w.writes != 1 {
		t.Fatal("old removed before replacement effective", err)
	}
	w.live = slices.Clone(w.keys)
	w.noReload = false
	if _, err := s.ContinueOperation(t.Context(), id, 7); err != nil || w.writes != 2 {
		t.Fatal(err)
	}
}

func TestKeyOperationAuthorityAndGenerationFences(t *testing.T) {
	for _, scenario := range []string{"disabled", "generation", "operation", "step", "source", "expired", "future", "run", "old-manager-grant"} {
		t.Run(scenario, func(t *testing.T) {
			s, w, oldID, nextID, _, _ := operationFixture(t)
			id := strings.Repeat("7", 32)
			mustPrepare(t, s, id, oldID, nextID, "replace")
			grantStep(s, id, "add")
			p, _ := s.keyPermit()
			switch scenario {
			case "disabled":
				s.keyOperations = false
			case "generation":
				p.Generation = "8"
			case "operation":
				p.OperationID = strings.Repeat("8", 32)
			case "step":
				p.Step = "revoke"
			case "source":
				p.SourceSHA256 = strings.Repeat("b", 64)
			case "expired":
				p.ExpiresAtMS = time.Now().Add(-time.Second).UnixMilli()
			case "future":
				p.IssuedAtMS = time.Now().Add(time.Second).UnixMilli()
			case "run":
				p.RunID = "another-run"
			case "old-manager-grant":
				p.IssuedAtMS = s.startedAtMS - 1
			}
			s.keyPermit = func() (KeyPermit, error) { return p, nil }
			if _, err := s.ContinueOperation(t.Context(), id, 7); err == nil || w.writes != 0 {
				t.Fatal("unsafe grant wrote", err)
			}
		})
	}
}

func TestConcurrentRevocationReplacementAndStepClaims(t *testing.T) {
	s, w, oldID, nextID, _, _ := operationFixture(t)
	id := strings.Repeat("9", 32)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, opID := range []string{id, strings.Repeat("0", 32)} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.Prepare(t.Context(), id, oldID, nextID, "replace", 7)
			errs <- err
		}(opID)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("conflicting operations accepted", successes)
	}
	o, _ := s.repo.LatestOperation(t.Context())
	other := New(s.repo, s.keys, s.runtime)
	other.permit, other.wire = s.permit, w
	grantStep(s, o.ID, "add")
	other.keyPermit = s.keyPermit
	for _, consumer := range []*Service{s, other} {
		wg.Add(1)
		go func(consumer *Service) { defer wg.Done(); _, _ = consumer.ContinueOperation(t.Context(), o.ID, 7) }(consumer)
	}
	wg.Wait()
	if w.writes != 1 {
		t.Fatal("duplicate step write", w.writes)
	}
}

func TestGenerationChangeAfterIntentKeepsAttemptWithoutSending(t *testing.T) {
	s, w, oldID, nextID, _, _ := operationFixture(t)
	id := strings.Repeat("e", 32)
	mustPrepare(t, s, id, oldID, nextID, "replace")
	grantStep(s, id, "add")
	read := s.keyPermit
	calls := 0
	s.keyPermit = func() (KeyPermit, error) {
		calls++
		if calls == 3 {
			return KeyPermit{}, ErrUnavailable
		}
		return read()
	}
	if _, err := s.ContinueOperation(t.Context(), id, 7); err == nil || w.writes != 0 {
		t.Fatal(err)
	}
	o, _ := s.repo.Operation(t.Context(), id)
	if o.Steps[0].State != "attempted" {
		t.Fatal("intent lost")
	}
	if _, err := s.ContinueOperation(t.Context(), id, 7); !errors.Is(err, cpa.ErrApplyUnknown) || w.writes != 0 {
		t.Fatal("write-before-crash replayed", err)
	}
}
