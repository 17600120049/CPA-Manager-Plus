package freshconfig

import (
	"context"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

func (s *Service) EnableH1(h *cpa.H1Coordinator) { s.h1 = h; s.wire.(*cpa.Transport).UseH1(h) }
func (s *Service) saveH1(ctx context.Context, q Request) (freshstore.RetryOperation, error) {
	r, m, e := s.owner(ctx)
	if e != nil {
		return freshstore.RetryOperation{}, e
	}
	if r.Retry != nil {
		if old, ok := r.Retry.Operations[q.RequestID]; ok {
			if old.Value != *q.Value || (Expected{old.SourceID, old.Generation, old.Revision, old.SHA256}) != q.Expected {
				return old, cpa.ErrSourceConflict
			}
			return old, nil
		}
	}
	t := s.wire.(*cpa.Transport)
	source, e := t.RetrySource(ctx)
	if e != nil {
		return freshstore.RetryOperation{}, e
	}
	if expected(source) != q.Expected || !source.H1 || !source.HandoffValid || source.ParentID != "" || source.State != "persisted" || !source.Installed {
		return freshstore.RetryOperation{}, cpa.ErrSourceConflict
	}
	i := cpa.H1Intent{RequestID: q.RequestID, Domain: "config", Mode: "retry", Retry: *q.Value}
	parent, e := s.h1.Claim(ctx, t, i)
	if e != nil {
		return freshstore.RetryOperation{}, e
	}
	o := freshstore.RetryOperation{RequestID: q.RequestID, Value: *q.Value, SourceID: source.SourceID, Generation: source.Generation, Revision: source.Revision, SHA256: source.SHA256, State: "unknown", ParentID: parent.ID, ClaimRevision: parent.ClaimRevision, IntentDigest: cpa.H1Terminal(i), Attempted: true}
	_, e = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(v *freshstore.Record) error {
		if v.Retry == nil {
			v.Retry = &freshstore.RetryJournal{Operations: map[string]freshstore.RetryOperation{}}
		}
		if _, ok := v.Retry.Operations[o.RequestID]; ok {
			return freshstore.ErrConflict
		}
		if v.Retry.Latest != "" && v.Retry.Operations[v.Retry.Latest].State == "unknown" {
			return freshstore.ErrConflict
		}
		v.Retry.Latest = o.RequestID
		v.Retry.Operations[o.RequestID] = o
		return nil
	})
	if e != nil {
		return o, e
	}
	step, e := s.h1.Begin(ctx, t, o.ParentID, 0)
	if e != nil {
		return o, e
	}
	current, management, e := s.owner(ctx)
	if e != nil || management != m || current.Operation.Identity != r.Operation.Identity || current.Operation.Strategy.TargetGeneration != r.Operation.Strategy.TargetGeneration {
		return o, cpa.ErrApplyUnknown
	}
	e = t.WriteH1Retry(ctx, o.ParentID, step, m, o.Value)
	if e != nil {
		return o, cpa.ErrApplyUnknown
	}
	// Only a complete successful response becomes durable HTTP200 evidence.
	before := o
	o.HTTP200 = true
	o.BeginRevision = step.BeginRevision
	saveCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = s.saveH1Operation(saveCtx, before, o); e != nil {
		return before, e
	}
	return s.reconcileH1(ctx, o)
}
func (s *Service) saveH1Operation(ctx context.Context, old, next freshstore.RetryOperation) error {
	r, e := s.store.Load(ctx)
	if e != nil {
		return e
	}
	_, e = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(v *freshstore.Record) error {
		if v.Retry == nil || v.Retry.Latest != old.RequestID || v.Retry.Operations[old.RequestID] != old {
			return freshstore.ErrConflict
		}
		v.Retry.Operations[old.RequestID] = next
		return nil
	})
	return e
}
func (s *Service) reconcileH1(ctx context.Context, o freshstore.RetryOperation) (freshstore.RetryOperation, error) {
	if o.ParentID != "c:"+o.RequestID || !o.Attempted || !o.HTTP200 {
		return o, cpa.ErrApplyUnknown
	}
	if _, _, e := s.owner(ctx); e != nil {
		return o, e
	}
	t := s.wire.(*cpa.Transport)
	if o.State == "unknown" {
		v, e := s.h1.Parent(ctx, t, o.ParentID)
		if e != nil {
			return o, e
		}
		p := v.H1.Parent
		if p.SourceID != o.SourceID || p.Generation != o.Generation || p.ClaimSHA256 != o.SHA256 || p.ClaimRevision != o.ClaimRevision || cpa.H1Terminal(p.Intent) != o.IntentDigest || p.Intent.RequestID != o.RequestID || p.Intent.Domain != "config" || p.Intent.Retry != o.Value {
			return o, cpa.ErrSourceConflict
		}
		receipt, e := s.h1.Commit(ctx, t, o.ParentID, 0)
		if e != nil {
			return o, e
		}
		if receipt.ID != o.ParentID+":0:request-retry" || receipt.BeginRevision != o.BeginRevision || receipt.Receipt == "" {
			return o, cpa.ErrApplyUnknown
		}
		next := o
		next.State = "persisted"
		next.Receipt = receipt.Receipt
		if e = s.saveH1Operation(ctx, o, next); e != nil {
			return o, e
		}
		o = next
	}
	if o.State != "persisted" || o.Receipt == "" {
		return o, cpa.ErrApplyUnknown
	}
	return o, s.h1.Finish(ctx, t, o.ParentID, cpa.H1Terminal(o))
}
