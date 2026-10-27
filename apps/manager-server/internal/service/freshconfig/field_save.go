package freshconfig

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// SaveField performs exactly one bounded write of one allowlisted field. The
// Manager journal entry is durable before Runtime reserves the operation, and no
// step is ever replayed: an unknown result stays unknown until it is reconciled.
func (s *Service) SaveField(ctx context.Context, path string, q FieldRequest) (freshstore.ConfigOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ValidateFieldRequest(path, q); err != nil {
		return freshstore.ConfigOperation{}, err
	}
	value, err := fieldValue(path, q.Value)
	if err != nil {
		return freshstore.ConfigOperation{}, ErrInvalid
	}
	r, management, err := s.fieldOwner(ctx, path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if r.ConfigInvalid[path] {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	journal := fieldJournal(r, path)
	if journal != nil {
		if old, ok := journal.Operations[q.RequestID]; ok {
			// Tombstone forever: an existing request ID is never a new write.
			if !reflect.DeepEqual(old.Value, value) || expectedOf(old) != q.Expected {
				return old, cpa.ErrSourceConflict
			}
			return old, nil
		}
		if latest, ok := journal.Operations[journal.Latest]; ok && latest.State == "unknown" {
			return freshstore.ConfigOperation{}, cpa.ErrApplyUnknown
		}
	}
	// The managed profile has no Runtime journal to observe: its authority is the
	// committed installation itself, so the private source read is not part of
	// this path. saveManaged re-reads the scalar with the same credential.
	if s.managedProfile() {
		return s.saveManaged(ctx, path, q, value, r, management)
	}
	source, _, _, err := s.wire.ReadConfigField(ctx, management, path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if fieldExpected(source) != q.Expected ||
		source.Generation != strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10) ||
		source.State != "persisted" || !source.Installed || !knownSource(r, source) {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	if s.h1 != nil {
		return s.saveH1(ctx, path, q, value, r, management, source)
	}
	return s.saveDirect(ctx, path, q, value, r, management, source)
}

func expectedOf(o freshstore.ConfigOperation) Expected {
	return Expected{SourceID: o.SourceID, Generation: o.Generation, Revision: o.Revision, SHA256: o.SHA256}
}

func newOperation(path, requestID string, value any, source cpa.ConfigFieldSource) freshstore.ConfigOperation {
	return freshstore.ConfigOperation{
		RequestID:  requestID,
		Field:      path,
		Value:      value,
		SourceID:   source.SourceID,
		Generation: source.Generation,
		Revision:   source.Revision,
		SHA256:     source.SHA256,
		State:      "unknown",
	}
}

func (s *Service) saveDirect(ctx context.Context, path string, q FieldRequest, value any, r freshstore.Record, management string, source cpa.ConfigFieldSource) (freshstore.ConfigOperation, error) {
	operation := newOperation(path, q.RequestID, value, source)
	if err := s.appendOperation(ctx, r.Operation.Identity, r.Operation.Revision, path, operation); err != nil {
		return operation, err
	}
	// Durable Manager intent precedes the Runtime reservation; every later step
	// is reconciled from that original request ID, never retried.
	if err := s.wire.BeginConfigField(ctx, source, q.RequestID, path, value); err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	current, currentManagement, err := s.fieldOwner(ctx, path)
	if err != nil || currentManagement != management || current.Operation.Identity != r.Operation.Identity ||
		current.Operation.Strategy.TargetGeneration != r.Operation.Strategy.TargetGeneration {
		return operation, cpa.ErrApplyUnknown
	}
	if err = s.wire.PutConfigField(ctx, source, q.RequestID, management, path, value); err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	return s.reconcileDirect(ctx, path, operation)
}

func (s *Service) saveH1(ctx context.Context, path string, q FieldRequest, value any, r freshstore.Record, management string, source cpa.ConfigFieldSource) (freshstore.ConfigOperation, error) {
	if !source.H1 || !source.HandoffValid || source.ParentID != "" || s.transport == nil || s.h1 == nil {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	intent := cpa.H1Intent{RequestID: q.RequestID, Domain: "config", Mode: "config-field", Path: path, Value: value}
	parent, err := s.h1.Claim(ctx, s.transport, intent)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	operation := newOperation(path, q.RequestID, value, source)
	operation.ParentID = parent.ID
	operation.ClaimRevision = parent.ClaimRevision
	operation.IntentDigest = cpa.H1Terminal(intent)
	operation.Attempted = true
	if err = s.appendOperation(ctx, r.Operation.Identity, r.Operation.Revision, path, operation); err != nil {
		return operation, err
	}
	step, err := s.h1.Begin(ctx, s.transport, operation.ParentID, 0)
	if err != nil {
		return operation, err
	}
	current, currentManagement, err := s.fieldOwner(ctx, path)
	if err != nil || currentManagement != management || current.Operation.Identity != r.Operation.Identity ||
		current.Operation.Strategy.TargetGeneration != r.Operation.Strategy.TargetGeneration {
		return operation, cpa.ErrApplyUnknown
	}
	if err = s.wire.WriteH1ConfigField(ctx, operation.ParentID, step, management, path, value); err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	// Only a complete successful response becomes durable HTTP200 evidence.
	before := operation
	operation.HTTP200 = true
	operation.BeginRevision = step.BeginRevision
	saveCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.updateOperation(saveCtx, path, before, operation); err != nil {
		return before, err
	}
	return s.reconcileH1(ctx, path, operation)
}

// appendOperation records the original intent under its own field journal. An
// existing unknown operation or a replay of an existing request ID is refused.
// saveManaged writes one allowlisted field on a manager-owned journal: durable
// Manager intent, exactly one authenticated CPA PUT, then readback. There is no
// host reservation, so the readback is the only effectiveness evidence.
func (s *Service) saveManaged(ctx context.Context, path string, q FieldRequest, value any, r freshstore.Record, management string) (freshstore.ConfigOperation, error) {
	source := managedSource(r, path)
	presence, current, err := s.wire.ReadConfigFieldValue(ctx, management, path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if presence == "unknown" {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	if fieldExpected(source) != q.Expected {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	_ = current
	operation := newOperation(path, q.RequestID, value, source)
	if err = s.appendOperation(ctx, r.Operation.Identity, r.Operation.Revision, path, operation); err != nil {
		return operation, err
	}
	if err = s.wire.WriteConfigFieldValue(ctx, management, path, value); err != nil {
		if errors.Is(err, cpa.ErrWriteRejected) {
			// A definite refusal: nothing landed, so the original is terminal and
			// the field is writable again without a replay.
			return s.failManagedOperation(ctx, path, operation, "write_rejected")
		}
		return operation, cpa.ErrApplyUnknown
	}
	return s.reconcileManaged(ctx, path, operation)
}

// failManagedOperation records a definite terminal failure of the ORIGINAL
// operation. It never repeats the PUT: a failure is evidence, not a retry.
func (s *Service) failManagedOperation(ctx context.Context, path string, operation freshstore.ConfigOperation, code string) (freshstore.ConfigOperation, error) {
	next := operation
	next.State, next.Code = "failed", code
	if err := s.updateOperation(ctx, path, operation, next); err != nil {
		return operation, err
	}
	return next, ErrWriteFailed
}

// reconcileManaged resolves an unknown managed write by reading the scalar back:
// an identical value is the persisted original, and a different value proves the
// write never landed, which is a terminal failure. It never repeats the PUT.
func (s *Service) reconcileManaged(ctx context.Context, path string, operation freshstore.ConfigOperation) (freshstore.ConfigOperation, error) {
	if operation.State != "unknown" {
		return operation, nil
	}
	_, management, err := s.fieldOwner(ctx, path)
	if err != nil {
		return operation, err
	}
	presence, value, err := s.wire.ReadConfigFieldValue(ctx, management, path)
	if err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	if presence != "present" || !reflect.DeepEqual(value, operation.Value) {
		return s.failManagedOperation(ctx, path, operation, "write_not_applied")
	}
	next := operation
	next.State = "persisted"
	if err = s.updateOperation(ctx, path, operation, next); err != nil {
		return operation, err
	}
	return next, nil
}

func (s *Service) appendOperation(ctx context.Context, identity bootstrap.FreshIdentity, revision uint64, path string, operation freshstore.ConfigOperation) error {
	_, err := s.store.Update(ctx, identity, revision, func(v *freshstore.Record) error {
		if v.Config == nil {
			v.Config = map[string]*freshstore.FieldJournal{}
		}
		journal := v.Config[path]
		if journal == nil {
			journal = &freshstore.FieldJournal{Operations: map[string]freshstore.ConfigOperation{}}
			v.Config[path] = journal
		}
		if journal.Operations == nil {
			journal.Operations = map[string]freshstore.ConfigOperation{}
		}
		if _, exists := journal.Operations[operation.RequestID]; exists {
			return freshstore.ErrConflict
		}
		if latest, ok := journal.Operations[journal.Latest]; ok && latest.State == "unknown" {
			return freshstore.ErrConflict
		}
		journal.Operations[operation.RequestID] = operation
		journal.Latest = operation.RequestID
		return nil
	})
	return err
}

func (s *Service) updateOperation(ctx context.Context, path string, before, next freshstore.ConfigOperation) error {
	r, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(v *freshstore.Record) error {
		journal := v.Config[path]
		if journal == nil || journal.Latest != before.RequestID || journal.Operations[before.RequestID] != before {
			return freshstore.ErrConflict
		}
		journal.Operations[before.RequestID] = next
		return nil
	})
	return err
}

func (s *Service) reconcileDirect(ctx context.Context, path string, operation freshstore.ConfigOperation) (freshstore.ConfigOperation, error) {
	source := cpa.ConfigFieldSource{SourceID: operation.SourceID, Generation: operation.Generation}
	v, err := s.wire.ReconcileConfigField(ctx, source, operation.RequestID, path, operation.Value)
	if err != nil {
		return operation, err
	}
	operation.State = v.OperationState
	if operation.State != "persisted" && operation.State != "recovered" {
		return operation, cpa.ErrApplyUnknown
	}
	r, err := s.store.Load(ctx)
	if err != nil {
		return operation, err
	}
	_, err = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(v *freshstore.Record) error {
		journal := v.Config[path]
		if journal == nil || journal.Latest != operation.RequestID {
			return freshstore.ErrConflict
		}
		journal.Operations[operation.RequestID] = operation
		return nil
	})
	if err != nil {
		operation.State = "unknown"
	}
	return operation, err
}

func (s *Service) reconcileH1(ctx context.Context, path string, operation freshstore.ConfigOperation) (freshstore.ConfigOperation, error) {
	if s.transport == nil || s.h1 == nil || operation.ParentID != "c:"+operation.RequestID ||
		!operation.Attempted || !operation.HTTP200 {
		return operation, cpa.ErrApplyUnknown
	}
	if _, _, err := s.fieldOwner(ctx, path); err != nil {
		return operation, err
	}
	if operation.State == "unknown" {
		p, err := s.h1.ParentRecord(ctx, s.transport, operation.ParentID)
		if err != nil {
			return operation, err
		}
		if p == nil {
			return operation, cpa.ErrApplyUnknown
		}
		intentPath, intentValue, ok := cpa.H1IntentField(p.Intent)
		if !ok || intentPath != path || !reflect.DeepEqual(intentValue, operation.Value) ||
			p.SourceID != operation.SourceID || p.Generation != operation.Generation ||
			p.ClaimSHA256 != operation.SHA256 || p.ClaimRevision != operation.ClaimRevision ||
			cpa.H1Terminal(p.Intent) != operation.IntentDigest || p.Intent.RequestID != operation.RequestID {
			return operation, cpa.ErrSourceConflict
		}
		receipt, err := s.h1.Commit(ctx, s.transport, operation.ParentID, 0)
		if err != nil {
			return operation, err
		}
		if receipt.ID != operation.ParentID+":0:"+cpa.H1IntentStepScope(p.Intent) ||
			receipt.BeginRevision != operation.BeginRevision || receipt.Receipt == "" {
			return operation, cpa.ErrApplyUnknown
		}
		next := operation
		next.State = "persisted"
		next.Receipt = receipt.Receipt
		if err = s.updateOperation(ctx, path, operation, next); err != nil {
			return operation, err
		}
		operation = next
	}
	if operation.State != "persisted" || operation.Receipt == "" {
		return operation, cpa.ErrApplyUnknown
	}
	return operation, s.h1.Finish(ctx, s.transport, operation.ParentID, cpa.H1Terminal(operation))
}
