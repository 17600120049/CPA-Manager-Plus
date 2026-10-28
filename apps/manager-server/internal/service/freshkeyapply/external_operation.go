package freshkeyapply

import (
	"context"
	"slices"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// externalPrepare journals one revoke/replace intent for the user's CPA after
// proving, by read-back, that the CPA is in exactly the state the intent starts
// from. No step is written here: the journal is the durable record a lost reply is
// reconciled against.
func (s *Service) externalPrepare(ctx context.Context, id, target, keeper, mode string, generation uint64) (OperationResult, error) {
	result := OperationResult{RuntimeState: "effect_unknown"}
	if target == keeper || (mode != "revoke" && mode != "replace") {
		return result, freshkeys.ErrRequest
	}
	install, err := s.externalTarget(ctx, generation)
	if err != nil {
		return result, err
	}
	w := s.externalKeyWire(install)
	if w == nil {
		return result, ErrExternalUnavailable
	}
	err = s.keyPair(ctx, target, keeper, false, func(old, next string) error {
		snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
		if err != nil {
			return err
		}
		// The user's CPA is administered by the user, so it may carry credentials
		// this Manager never staged. The step acts on exactly one of them: the
		// credential it removes must be carried, and that is the whole condition.
		if old == next || !slices.Contains(snapshot.Keys, old) {
			return cpa.ErrSourceConflict
		}
		// A replace keeps a credential on the CPA; a revoke of the only one would
		// leave it accepting unauthenticated clients, which is the user's decision
		// and never a step of this Manager.
		if mode == "revoke" && len(snapshot.Keys) <= 1 {
			return ErrLastKey
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	steps := []repo.KeyStep{{Name: "revoke", State: "unattempted"}}
	if mode == "replace" {
		steps = append([]repo.KeyStep{{Name: "add", State: "unattempted"}}, steps...)
	}
	o := repo.Operation{
		ID: id, TargetID: target, KeeperID: keeper, Mode: mode,
		// The journal records the digest-derived generation, not the checkpoint
		// number: that is the value a later step is bound to, and it changes as
		// soon as the user edits the configuration this Manager last observed.
		RunID: install.Identity, Generation: install.Generation(),
		Revision: 1, Outcome: "pending", Steps: steps, CreatedAtMS: time.Now().UnixMilli(),
	}
	if err = s.repo.BeginOperation(ctx, o); err != nil {
		return result, err
	}
	return s.externalCheckOperation(ctx, &o, generation)
}

// externalCheckOperation reconciles one journaled External operation by reading
// the user's CPA. It never writes: an attempted step is resolved by what the CPA
// now carries, and an unattempted step is only named.
func (s *Service) externalCheckOperation(ctx context.Context, o *repo.Operation, generation uint64) (OperationResult, error) {
	r := OperationResult{Operation: o, RuntimeState: "effect_unknown"}
	if o.Outcome == "committed" {
		latest, err := s.repo.LatestOperation(ctx)
		if err != nil {
			return r, err
		}
		if latest == nil {
			return r, repo.ErrConflict
		}
		if latest.ID != o.ID {
			// A historical committed operation is a durable record, not a live
			// effectiveness claim: its former keeper may since have been revoked.
			// It is only reported for the connection and generation it was
			// journaled against.
			if o.RunID != latest.RunID || o.Generation != latest.Generation {
				return r, ErrGeneration
			}
			return r, nil
		}
	}
	install, err := s.externalTarget(ctx, generation)
	if err != nil {
		return r, err
	}
	if o.RunID != install.Identity || o.Generation != install.Generation() {
		return r, ErrGeneration
	}
	w := s.externalKeyWire(install)
	if w == nil {
		return r, ErrExternalUnavailable
	}
	for i, step := range o.Steps {
		if step.State == "unattempted" {
			r.NextStep = step.Name
			return r, nil
		}
		if step.State == "observed" && i < len(o.Steps)-1 {
			continue
		}
		err = s.keyPair(ctx, o.TargetID, o.KeeperID, o.Outcome == "committed", func(old, next string) error {
			return s.awaitExternalKeySet(ctx, w, install, step.Name, old, next)
		})
		if err != nil {
			return r, err
		}
		if o.Outcome != "committed" {
			o.Steps[i].State = "observed"
			o.Outcome = "pending"
			if step.Name == "revoke" {
				o.Outcome = "committed"
				o.CompletedAtMS = time.Now().UnixMilli()
			}
			if err = s.repo.SaveOperation(ctx, *o); err != nil {
				return r, err
			}
			o.Revision++
		}
	}
	r.RuntimeState = "effective_observed"
	r.ExpiresAtMS = time.Now().Add(5 * time.Second).UnixMilli()
	return r, nil
}

// awaitExternalKeySet requires the stated membership to hold across spaced
// reads of the user's CPA. Membership is the fact this Manager owns: an External
// CPA may carry credentials it never staged, so a whole-list comparison would
// report a conflict for a correct write.
func (s *Service) awaitExternalKeySet(ctx context.Context, w externalWire, install ExternalInstall, step, old, next string) error {
	// The step's own fact: a revoke leaves the removed credential absent, and the
	// add step of a replace leaves the replacement carried.
	want, reject := []string{next}, []string{old}
	if step == "add" {
		want, reject = []string{next}, nil
	}
	for n := range 3 {
		snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
		if err != nil {
			return err
		}
		if !externalSatisfied(snapshot.Keys, want, reject) {
			return cpa.ErrApplyUnknown
		}
		if n < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
	if err != nil || !externalSatisfied(snapshot.Keys, want, reject) {
		return cpa.ErrApplyUnknown
	}
	return nil
}

// externalSatisfied reports the stated membership of one read-back: every wanted
// credential is carried and every rejected one is not.
func externalSatisfied(keys, want, reject []string) bool {
	for _, key := range want {
		if !slices.Contains(keys, key) {
			return false
		}
	}
	for _, key := range reject {
		if slices.Contains(keys, key) {
			return false
		}
	}
	return true
}

// externalContinue writes the next unattempted step of one journaled External
// operation. The step is journaled as attempted before the single request, so a
// lost reply is resolved by externalCheckOperation and never by resending.
func (s *Service) externalContinue(ctx context.Context, o *repo.Operation, index int, generation uint64) (OperationResult, error) {
	r := OperationResult{Operation: o, RuntimeState: "effect_unknown"}
	if o.Outcome == "committed" {
		return r, nil
	}
	install, err := s.externalTarget(ctx, generation)
	if err != nil {
		return r, err
	}
	if o.RunID != install.Identity || o.Generation != install.Generation() {
		return r, ErrGeneration
	}
	step := o.Steps[index].Name
	grant, err := s.keyPermit(ctx, o.ID, step)
	if err != nil {
		return r, ErrExternalUnavailable
	}
	now := time.Now().UnixMilli()
	if grant.OperationID != o.ID || grant.Step != step || grant.RunID != o.RunID || grant.Generation != o.Generation ||
		grant.IssuedAtMS < s.startedAtMS || grant.IssuedAtMS < o.CreatedAtMS || grant.IssuedAtMS > now ||
		grant.ExpiresAtMS <= now || grant.ExpiresAtMS-grant.IssuedAtMS > 60000 || !identity.IsValidSHA256Hex(grant.SourceSHA256) {
		return r, ErrExternalUnavailable
	}
	w := s.externalKeyWire(install)
	if w == nil {
		return r, ErrExternalUnavailable
	}
	err = s.keyPair(ctx, o.TargetID, o.KeeperID, false, func(old, next string) error {
		snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
		if err != nil {
			return err
		}
		if snapshot.SourceSHA256 != grant.SourceSHA256 {
			return cpa.ErrSourceConflict
		}
		if step == "revoke" && len(snapshot.Keys) <= 1 {
			return ErrLastKey
		}
		// A revoke removes the target and keeps the keeper; the add step of a
		// replace only requires both to be carried, since the user's CPA may
		// already hold the replacement.
		if !externalSatisfied(snapshot.Keys, []string{old}, nil) ||
			(step == "revoke" && len(snapshot.Keys) <= 1) {
			return cpa.ErrSourceConflict
		}
		if !s.keyGrantHeld(ctx, grant, o.ID, step) || time.Now().UnixMilli() >= grant.ExpiresAtMS {
			return ErrExternalUnavailable
		}
		o.Steps[index].State = "attempted"
		o.Outcome = "unknown"
		if err = s.repo.SaveOperation(ctx, *o); err != nil {
			return err
		}
		o.Revision++
		// The whole list is written because the CPA v8 route replaces it, but the
		// step is still exactly one membership change: an add introduces the keeper
		// and a revoke removes the target.
		after := slices.Clone(snapshot.Keys)
		if step == "add" {
			if !slices.Contains(after, next) {
				after = append(after, next)
			}
		} else {
			if len(after) <= 1 {
				return ErrLastKey
			}
			after = slices.DeleteFunc(after, func(key string) bool { return key == old })
		}
		writeErr := w.ExternalWriteKeySet(ctx, install.ManagementKey, after)
		o.Steps[index].Returned = true
		// Retain the attempt on a lost client: the journal, not the reply, is the
		// evidence a later reconcile reads.
		saveCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err = s.repo.SaveOperation(saveCtx, *o); err != nil {
			return err
		}
		o.Revision++
		return writeErr
	})
	if err != nil {
		return OperationResult{Operation: o, RuntimeState: "effect_unknown"}, err
	}
	return s.externalCheckOperation(ctx, o, generation)
}
