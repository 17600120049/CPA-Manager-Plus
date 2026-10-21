package freshkeyapply

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

const KeyPermitPath = "/run/cpamp-s1/key-operation.json"

var ErrLastKey = errors.New("cannot revoke last effective key")

// A distinct Host grant for exactly one step of one durable operation. Host must
// exclude/drain every other config/file/reload writer before issuing this file.
// It contains no credential and cannot be supplied through the public API.
type KeyPermit struct {
	OperationID  string `json:"operationId"`
	Step         string `json:"step"`
	RunID        string `json:"runId"`
	Generation   string `json:"generation"`
	SourceSHA256 string `json:"sourceSHA256"`
	IssuedAtMS   int64  `json:"issuedAtMs"`
	ExpiresAtMS  int64  `json:"expiresAtMs"`
}

func loadKeyPermit() (KeyPermit, error) {
	var p KeyPermit
	err := readHostPermit(KeyPermitPath, &p)
	return p, err
}

type keyWire interface {
	KeySnapshot(context.Context, string) (cpa.KeySnapshot, error)
	WriteKeySet(context.Context, string, []string) error
	SampleKeySet(context.Context, []string, []string) (bool, error)
}
type OperationResult struct {
	Operation    *repo.Operation `json:"operation"`
	RuntimeState string          `json:"runtimeState"`
	ExpiresAtMS  int64           `json:"expiresAtMs"`
	NextStep     string          `json:"nextStep"`
}

func (s *Service) KeyOperationsEnabled() bool { return s != nil && s.keyOperations }

func (s *Service) installedKey(ctx context.Context) (string, error) {
	o, err := s.repo.LatestOperation(ctx)
	if err != nil {
		return "", err
	}
	if o != nil {
		if !s.keyOperations || o.Outcome != "committed" {
			return "", ErrUnavailable
		}
		a, e := s.repo.InitialInstall(ctx)
		if e != nil {
			return "", e
		}
		if a == nil || o.RunID != a.RunID || o.Generation != a.Generation || o.ProviderSeedSHA256 != a.ProviderSeedSHA256 {
			return "", ErrGeneration
		}
		return o.KeeperID, nil
	}
	a, err := s.repo.InitialInstall(ctx)
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", ErrUnavailable
	}
	return a.KeyID, nil
}

// Prepare persists an immutable selection before any upstream write. Missing
// step authority never causes an attempted mutation or a new operation retry.
func (s *Service) Prepare(ctx context.Context, id, target, keeper, mode string, generation uint64) (OperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := OperationResult{RuntimeState: "effect_unknown"}
	if !s.KeyOperationsEnabled() {
		return result, ErrUnavailable
	}
	for _, v := range []string{id, target, keeper} {
		if !freshkeys.ValidRequestID(v) {
			return result, freshkeys.ErrRequest
		}
	}
	if target == keeper || (mode != "revoke" && mode != "replace") {
		return result, freshkeys.ErrRequest
	}
	if previous, err := s.repo.Operation(ctx, id); err != nil {
		return result, err
	} else if previous != nil {
		if previous.TargetID != target || previous.KeeperID != keeper || previous.Mode != mode {
			return result, repo.ErrConflict
		}
		return s.checkOperation(ctx, previous, generation)
	}
	p, err := s.operationTarget(ctx, generation)
	if err != nil {
		return result, err
	}
	installed, err := s.installedKey(ctx)
	if err != nil || installed != target && (mode != "revoke" || installed != keeper) {
		return result, repo.ErrConflict
	}
	w, ok := s.wire.(keyWire)
	if !ok {
		return result, ErrUnavailable
	}
	err = s.keyPair(ctx, target, keeper, false, func(old, next string) error {
		snap, e := w.KeySnapshot(ctx, p.ManagementKey)
		if e != nil {
			return e
		}
		if mode == "revoke" && len(snap.Keys) == 1 {
			return ErrLastKey
		}
		want := []string{old}
		if mode == "revoke" {
			want = append(want, next)
		}
		if !sameKeys(snap.Keys, want) {
			return cpa.ErrSourceConflict
		}
		ok, e := w.SampleKeySet(ctx, want, nil)
		if e != nil {
			return e
		}
		if !ok {
			return cpa.ErrSourceConflict
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
	o := repo.Operation{ID: id, TargetID: target, KeeperID: keeper, Mode: mode, RunID: p.RunID, Generation: p.Generation, ProviderSeedSHA256: p.S2B.Binding(), Revision: 1, Outcome: "pending", Steps: steps, CreatedAtMS: time.Now().UnixMilli()}
	if err = s.repo.BeginOperation(ctx, o); err != nil {
		return result, err
	}
	return s.checkOperation(ctx, &o, generation)
}

func (s *Service) operationTarget(ctx context.Context, generation uint64) (Permit, error) {
	p, err := s.permit()
	if err != nil {
		return p, err
	}
	if p.Generation != strconv.FormatUint(generation, 10) {
		return p, ErrGeneration
	}
	if _, err = s.target(ctx, p); err != nil {
		return p, err
	}
	a, err := s.repo.InitialInstall(ctx)
	if err != nil {
		return p, err
	}
	if a == nil || a.RunID != p.RunID || a.Generation != p.Generation || a.ProviderSeedSHA256 != p.S2B.Binding() {
		return p, ErrGeneration
	}
	return p, nil
}
func (s *Service) keyPair(ctx context.Context, target, keeper string, retained bool, consume func(string, string) error) error {
	read := s.keys.WithStagedSecret
	if retained {
		read = s.keys.WithRetainedSecret
	}
	return read(ctx, target, func(old string) error {
		return s.keys.WithStagedSecret(ctx, keeper, func(next string) error {
			if old == next {
				return cpa.ErrSourceConflict
			}
			return consume(old, next)
		})
	})
}
func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, v := range a {
		if !slices.Contains(b, v) {
			return false
		}
	}
	return true
}

func (s *Service) CheckOperation(ctx context.Context, id string, generation uint64) (OperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.KeyOperationsEnabled() {
		return OperationResult{}, ErrUnavailable
	}
	var o *repo.Operation
	var err error
	if id == "latest" {
		o, err = s.repo.LatestOperation(ctx)
	} else if !freshkeys.ValidRequestID(id) {
		return OperationResult{}, freshkeys.ErrRequest
	} else {
		o, err = s.repo.Operation(ctx, id)
	}
	if err != nil {
		return OperationResult{}, err
	}
	if o == nil {
		return OperationResult{RuntimeState: "effect_unknown"}, nil
	}
	return s.checkOperation(ctx, o, generation)
}

func (s *Service) checkOperation(ctx context.Context, o *repo.Operation, generation uint64) (OperationResult, error) {
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
			if o.RunID != latest.RunID || o.Generation != latest.Generation || o.ProviderSeedSHA256 != latest.ProviderSeedSHA256 || o.Generation != strconv.FormatUint(generation, 10) {
				return r, ErrGeneration
			}
			// Historical committed operations are durable records, not live
			// effectiveness claims. Their former keeper may now be revoked.
			return r, nil
		}
	}
	p, err := s.operationTarget(ctx, generation)
	if err != nil {
		return r, err
	}
	if o.RunID != p.RunID || o.Generation != p.Generation || o.ProviderSeedSHA256 != p.S2B.Binding() {
		return r, ErrGeneration
	}
	w, ok := s.wire.(keyWire)
	if !ok {
		return r, ErrUnavailable
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
			want, reject := []string{next}, []string{old}
			if step.Name == "add" {
				want, reject = []string{old, next}, nil
			}
			// Require multiple spaced live sweeps; a config save is never an ack.
			for n := 0; n < 3; n++ {
				snap, e := w.KeySnapshot(ctx, p.ManagementKey)
				if e != nil {
					return e
				}
				if !sameKeys(snap.Keys, want) {
					return cpa.ErrApplyUnknown
				}
				ok, e := w.SampleKeySet(ctx, want, reject)
				if e != nil {
					return e
				}
				if !ok {
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
			_, e := s.target(ctx, p)
			if e != nil {
				return e
			}
			snap, e := w.KeySnapshot(ctx, p.ManagementKey)
			if e != nil || !sameKeys(snap.Keys, want) {
				return cpa.ErrApplyUnknown
			}
			return nil
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
	if r.ExpiresAtMS > p.ExpiresAtMS {
		r.ExpiresAtMS = p.ExpiresAtMS
	}
	return r, nil
}

func (s *Service) ContinueOperation(ctx context.Context, id string, generation uint64) (OperationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := OperationResult{RuntimeState: "effect_unknown"}
	if !s.KeyOperationsEnabled() || !freshkeys.ValidRequestID(id) {
		return r, ErrUnavailable
	}
	o, err := s.repo.Operation(ctx, id)
	if err != nil {
		return r, err
	}
	if o == nil {
		return r, freshkeys.ErrNotFound
	}
	// Always reconcile first. An attempted unknown step is never sent again.
	r, err = s.checkOperation(ctx, o, generation)
	if err != nil {
		return r, err
	}
	latest, err := s.repo.LatestOperation(ctx)
	if err != nil || latest == nil || latest.ID != id {
		return r, repo.ErrConflict
	}
	if o.Outcome == "committed" {
		return r, nil
	}
	index := -1
	for i, step := range o.Steps {
		if step.State == "unattempted" {
			index = i
			break
		}
	}
	if index < 0 {
		return r, cpa.ErrApplyUnknown
	}
	p, err := s.operationTarget(ctx, generation)
	if err != nil {
		return r, err
	}
	grant, err := s.keyPermit()
	if err != nil {
		return r, ErrUnavailable
	}
	now := time.Now().UnixMilli()
	if grant.OperationID != id || grant.Step != o.Steps[index].Name || grant.RunID != o.RunID || grant.Generation != o.Generation || grant.IssuedAtMS < s.startedAtMS || grant.IssuedAtMS < o.CreatedAtMS || grant.IssuedAtMS > now || grant.ExpiresAtMS <= now || grant.ExpiresAtMS-grant.IssuedAtMS > 60000 || !identity.IsValidSHA256Hex(grant.SourceSHA256) {
		return r, ErrUnavailable
	}
	w, ok := s.wire.(keyWire)
	if !ok {
		return r, ErrUnavailable
	}
	err = s.keyPair(ctx, o.TargetID, o.KeeperID, false, func(old, next string) error {
		before, after := []string{old, next}, []string{next}
		if grant.Step == "add" {
			before, after = []string{old}, []string{old, next}
		}
		snap, e := w.KeySnapshot(ctx, p.ManagementKey)
		if e != nil {
			return e
		}
		if snap.SourceSHA256 != grant.SourceSHA256 || !sameKeys(snap.Keys, before) {
			return cpa.ErrSourceConflict
		}
		ok, e := w.SampleKeySet(ctx, before, nil)
		if e != nil {
			return e
		}
		if !ok {
			return cpa.ErrSourceConflict
		}
		current, e := s.keyPermit()
		if e != nil || current != grant {
			return ErrUnavailable
		}
		if _, e = s.target(ctx, p); e != nil {
			return e
		}
		snap, e = w.KeySnapshot(ctx, p.ManagementKey)
		if e != nil || snap.SourceSHA256 != grant.SourceSHA256 || !sameKeys(snap.Keys, before) {
			return cpa.ErrSourceConflict
		}
		if time.Now().UnixMilli() >= grant.ExpiresAtMS {
			return ErrUnavailable
		}
		o.Steps[index].State = "attempted"
		o.Outcome = "unknown"
		if e = s.repo.SaveOperation(ctx, *o); e != nil {
			return e
		}
		o.Revision++
		// Recheck authority after intent, immediately before the single PUT.
		current, e = s.keyPermit()
		if e != nil || current != grant || time.Now().UnixMilli() >= grant.ExpiresAtMS {
			return ErrUnavailable
		}
		if _, e = s.target(ctx, p); e != nil {
			return e
		}
		writeErr := w.WriteKeySet(ctx, p.ManagementKey, after)
		o.Steps[index].Returned = true
		// Use a bounded independent context to retain the attempt on client loss.
		saveCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e = s.repo.SaveOperation(saveCtx, *o); e != nil {
			return e
		}
		o.Revision++
		return writeErr
	})
	if err != nil {
		return OperationResult{Operation: o, RuntimeState: "effect_unknown"}, err
	}
	return s.checkOperation(ctx, o, generation)
}
