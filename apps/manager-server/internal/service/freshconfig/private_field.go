package freshconfig

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// The private H1/direct profile keeps the write contract it shipped with: one
// leaf per operation, reserved either by the Runtime Host (H1) or by the private
// runtime journal (direct), and journaled per field. Its scope is frozen at the
// six leaves C2 delivered; the page reads the rest and never writes it.
//
// This is deliberately NOT the group PATCH path. The Host protocol has no
// multi-leaf instruction and no delete, so a private save is one "set" of one
// private leaf, and its evidence lives in the pre-cutover field journal that
// legacy_field.go already reconciles.

// ErrWriteFailed is a definite, terminal per-field failure: the original
// operation is known not to have landed, its journal entry names why, and the
// field is writable again without ever resending that operation.
var ErrWriteFailed = errors.New("config field write did not land")

// privateOwner proves the private profile this process was constructed for and
// returns the management authority of exactly the committed target generation.
// A record that does not name the attested private runtime is never a write path.
func (s *Service) privateOwner(ctx context.Context, path string) (freshstore.Record, string, error) {
	if _, ok := cpa.LookupPrivateConfigLeaf(path); !ok {
		return freshstore.Record{}, "", ErrInvalid
	}
	r, err := s.store.Load(ctx)
	if err != nil {
		return r, "", err
	}
	op := r.Operation
	if s.target == nil || op.Strategy.Stage != "committed" || op.Strategy.TargetGeneration == 0 ||
		op.Strategy.TargetIdentity != cpa.PrivateKeyRuntime || op.Strategy.TargetArtifactID != cpa.PrivateKeyArtifact {
		return r, "", cpa.ErrProofUnavailable
	}
	management, err := s.target(ctx, op.Strategy.TargetGeneration)
	if err != nil {
		return r, "", err
	}
	if management == "" {
		return r, "", cpa.ErrProofUnavailable
	}
	return r, management, nil
}

// knownSource proves the private source carries only our own allowed writes. A
// completed operation of another allowlisted field is equally known: each field
// owns its own journal entry.
func knownSource(r freshstore.Record, source cpa.ConfigFieldSource) bool {
	if source.OperationScope == "initial-key" || source.OperationID == "" {
		return true
	}
	if source.OperationPath == "" {
		// A Key step on the H1 profile names no field; its ready-and-free handoff
		// already proves the source.
		return source.H1 && source.HandoffValid && source.ParentID == ""
	}
	journal := r.Config[source.OperationPath]
	if journal == nil {
		return false
	}
	// The last write may be an H1 step ID ("c:<requestId>:<step>:<scope>"); it
	// still names the same journal request as the direct profile.
	id := cpa.ConfigFieldRequestID(source.OperationID)
	for requestID, operation := range journal.Operations {
		if requestID == id && operation.SourceID == source.SourceID && operation.Generation == source.Generation &&
			canonicalEqual(operation.Value, source.OperationValue) {
			return true
		}
	}
	return false
}

// privateFieldSave performs exactly one bounded write of one private leaf. The
// Manager journal entry is durable before the Host reserves the operation, and
// no step is ever replayed.
func (s *Service) privateFieldSave(ctx context.Context, path string, value any, requestID string) (freshstore.ConfigOperation, error) {
	r, management, err := s.privateOwner(ctx, path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if r.ConfigInvalid[path] {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	if journal := r.Config[path]; journal != nil {
		if old, exists := journal.Operations[requestID]; exists {
			// Tombstone forever: an existing request ID is never a new write.
			if !canonicalEqual(old.Value, value) {
				return old, cpa.ErrSourceConflict
			}
			return old, nil
		}
		if latest, exists := journal.Operations[journal.Latest]; exists && latest.State == "unknown" {
			return freshstore.ConfigOperation{}, cpa.ErrApplyUnknown
		}
	}
	source, _, _, err := s.wire.ReadConfigField(ctx, management, path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if source.Generation != strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10) {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	if source.State != "persisted" || !source.Installed || !knownSource(r, source) {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	if s.h1 != nil {
		return s.saveH1(ctx, path, value, requestID, r, management, source)
	}
	return s.saveDirect(ctx, path, value, requestID, r, management, source)
}

func newFieldOperation(path, requestID string, value any, source cpa.ConfigFieldSource) freshstore.ConfigOperation {
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

// latestPrivateOperation returns the newest operation of a private group in the
// group-operation shape, so the page sees persistent evidence after a reload.
// Each private group holds leaves of its own journal entries; the newest one is
// the group's latest save.
func latestPrivateOperation(r freshstore.Record, group cpa.ConfigGroup) *freshstore.ConfigGroupOperation {
	var newest *freshstore.ConfigGroupOperation
	for _, path := range group.Leaves {
		entry := r.Config[path]
		if entry == nil || r.ConfigInvalid[path] {
			continue
		}
		operation, ok := entry.Operations[entry.Latest]
		if !ok {
			continue
		}
		view := legacyView(operation)
		view.Group, view.Prefix = group.ID, group.Prefix
		if newest == nil || operation.State == "unknown" {
			newest = &view
		}
		if operation.State == "unknown" {
			// An unresolved original is the one the page must reconcile, and only
			// one can be outstanding per group because no new save is accepted.
			return newest
		}
	}
	return newest
}

// savePrivateGroup applies the private profile's own save shape: exactly one
// "set" of one private leaf, verified against the group baseline the page read,
// written through the Host reservation protocol. "Use default" is refused because
// the H1 protocol has one field per operation and no delete.
func (s *Service) savePrivateGroup(ctx context.Context, r freshstore.Record, auth authority, groupID string, edits []freshstore.ConfigGroupEdit, q GroupRequest) (freshstore.ConfigGroupOperation, error) {
	group, ok := cpa.LookupPrivateConfigGroup(groupID)
	if !ok {
		return freshstore.ConfigGroupOperation{}, ErrReadOnly
	}
	if len(edits) != 1 || edits[0].Action != "set" {
		// One leaf per operation is the Host contract, not a preference: a second
		// edit could not be reserved. A "default" edit has no Host instruction.
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	edit := edits[0]
	if !slices.Contains(group.Leaves, edit.Path) {
		// The group's public leaf list is wider than the private subset; a leaf
		// the Host does not write is not part of this profile's save.
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	// A replay of an existing request ID is the ORIGINAL operation, so it is
	// answered before anything about the current document is compared: the
	// original already moved the baseline this save would be measured against.
	if journal := r.Config[edit.Path]; journal != nil {
		if old, exists := journal.Operations[q.RequestID]; exists {
			if !canonicalEqual(old.Value, edit.Value) {
				return privateGroupView(old, groupID, q.Expected.Baseline), cpa.ErrSourceConflict
			}
			return privateGroupView(old, groupID, q.Expected.Baseline), nil
		}
	}
	// Any unresolved leaf of this group blocks the whole group: reconciling it
	// reads the document, so a newer write would let it observe a foreign value.
	for _, path := range group.Leaves {
		entry := r.Config[path]
		if entry == nil {
			continue
		}
		if operation, ok := entry.Operations[entry.Latest]; ok && operation.State == "unknown" && path != edit.Path {
			return freshstore.ConfigGroupOperation{}, cpa.ErrApplyUnknown
		}
	}
	if err := cpa.ValidatePrivateConfigLeafValue(edit.Path, edit.Value); err != nil {
		if errors.Is(err, cpa.ErrPrivateConfigOutOfRange) {
			// The Host would refuse this after the reservation; the page shows it
			// on the field instead.
			return freshstore.ConfigGroupOperation{}, Rejection{Code: "invalid_config", Field: edit.Path, Message: "value outside the private profile range"}
		}
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	document, err := auth.wire.ReadConfigDocument(ctx, auth.secret)
	if err != nil {
		return freshstore.ConfigGroupOperation{}, err
	}
	if groupBaseline(document, group) != q.Expected.Baseline {
		return freshstore.ConfigGroupOperation{}, cpa.ErrSourceConflict
	}
	operation, err := s.privateFieldSave(ctx, edit.Path, edit.Value, q.RequestID)
	view := privateGroupView(operation, groupID, q.Expected.Baseline)
	if err != nil {
		return view, err
	}
	return view, nil
}

// privateGroupView presents one private field operation in the group-operation
// shape the page already reads, so a private save needs no second contract.
func privateGroupView(operation freshstore.ConfigOperation, groupID, baseline string) freshstore.ConfigGroupOperation {
	view := legacyView(operation)
	view.Group = groupID
	view.Prefix = groupID
	if baseline != "" {
		view.Baseline = baseline
	}
	return view
}

// saveDirect reserves the original operation in the private runtime journal and
// sends exactly one non-replayable PUT of one scalar.
func (s *Service) saveDirect(ctx context.Context, path string, value any, requestID string, r freshstore.Record, management string, source cpa.ConfigFieldSource) (freshstore.ConfigOperation, error) {
	operation := newFieldOperation(path, requestID, value, source)
	if err := s.appendFieldOperation(ctx, r.Operation.Identity, r.Operation.Revision, path, operation); err != nil {
		return operation, err
	}
	// Durable Manager intent precedes the Runtime reservation; every later step
	// is reconciled from that original request ID, never retried.
	if err := s.wire.BeginConfigField(ctx, source, requestID, path, value); err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	current, currentManagement, err := s.privateOwner(ctx, path)
	if err != nil || currentManagement != management || current.Operation.Identity != r.Operation.Identity ||
		current.Operation.Strategy.TargetGeneration != r.Operation.Strategy.TargetGeneration {
		return operation, cpa.ErrApplyUnknown
	}
	if err = s.wire.PutConfigField(ctx, source, requestID, management, path, value); err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	return s.reconcileDirect(ctx, path, operation)
}

// saveH1 claims the Runtime handoff, reserves its single step and writes it. The
// intent is re-read from Runtime inside the transport, never trusted from local
// state.
func (s *Service) saveH1(ctx context.Context, path string, value any, requestID string, r freshstore.Record, management string, source cpa.ConfigFieldSource) (freshstore.ConfigOperation, error) {
	if !source.H1 || !source.HandoffValid || source.ParentID != "" || s.transport == nil || s.h1 == nil {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	intent := cpa.H1Intent{RequestID: requestID, Domain: "config", Mode: "config-field", Path: path, Value: value}
	parent, err := s.h1.Claim(ctx, s.transport, intent)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	operation := newFieldOperation(path, requestID, value, source)
	operation.ParentID = parent.ID
	operation.ClaimRevision = parent.ClaimRevision
	operation.IntentDigest = cpa.H1Terminal(intent)
	operation.Attempted = true
	if err = s.appendFieldOperation(ctx, r.Operation.Identity, r.Operation.Revision, path, operation); err != nil {
		return operation, err
	}
	step, err := s.h1.Begin(ctx, s.transport, operation.ParentID, 0)
	if err != nil {
		return operation, err
	}
	current, currentManagement, err := s.privateOwner(ctx, path)
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
	if err = s.updateFieldOperation(ctx, before, operation); err != nil {
		return before, err
	}
	return s.reconcileH1(ctx, path, r, operation)
}

// reconcileDirect resolves an unknown direct operation from the Runtime record
// of the ORIGINAL request. It never repeats the PUT.
func (s *Service) reconcileDirect(ctx context.Context, path string, operation freshstore.ConfigOperation) (freshstore.ConfigOperation, error) {
	source := cpa.ConfigFieldSource{SourceID: operation.SourceID, Generation: operation.Generation}
	v, err := s.wire.ReconcileConfigField(ctx, source, operation.RequestID, path, operation.Value)
	if err != nil {
		return operation, err
	}
	if v.OperationState != "persisted" && v.OperationState != "recovered" {
		return operation, cpa.ErrApplyUnknown
	}
	next := operation
	next.State = v.OperationState
	if err = s.updateFieldOperation(ctx, operation, next); err != nil {
		return operation, err
	}
	return next, nil
}

// reconcileH1 proves the original H1 parent still carries exactly this intent
// before committing its receipt. It shares the single field-journal resolver
// with the pre-cutover entries, which use the same journal.
func (s *Service) reconcileH1(ctx context.Context, path string, r freshstore.Record, operation freshstore.ConfigOperation) (freshstore.ConfigOperation, error) {
	if s.transport == nil || s.h1 == nil {
		return operation, cpa.ErrApplyUnknown
	}
	current, _, err := s.privateOwner(ctx, path)
	if err != nil {
		return operation, err
	}
	// The caller's record predates the journal append of this operation; resolve
	// against the record the append produced.
	return s.reconcileFieldOperationH1(ctx, current, operation)
}

// appendFieldOperation records the original intent under its own field journal.
// An existing unknown operation or a replay of an existing request ID is refused.
func (s *Service) appendFieldOperation(ctx context.Context, identity bootstrap.FreshIdentity, revision uint64, path string, operation freshstore.ConfigOperation) error {
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

// updateFieldOperation advances one original operation of one field journal.
func (s *Service) updateFieldOperation(ctx context.Context, before, next freshstore.ConfigOperation) error {
	r, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	return s.updateFieldJournal(ctx, r, before, next)
}
