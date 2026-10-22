package freshconfig

import (
 "context"
 "errors"
 "strconv"
 "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
 "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
 "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// B3 shares H1 ownership but not B2's journal, value or receipt.
func (s *Service) ReadInterval(ctx context.Context) (View, error) {
 s.mu.Lock()
 defer s.mu.Unlock()
 v := empty()
 v.Field = "routing.retry.max-retry-interval"
 if s.h1 == nil { return v, nil }
 r, m, err := s.owner(ctx)
 if err != nil {
  if r.RetryInvalid || r.IntervalInvalid { v.Reason = "configuration_journal_invalid" }
  if errors.Is(err, cpa.ErrWriterIsolation) { v.Reason = "config_key_writer_not_isolated" }
  return v, nil
 }
 t := s.wire.(*cpa.Transport)
 source, presence, value, err := t.ReadInterval(ctx, m)
 if err != nil { return v, err }
 if source.Generation != strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10) { return v, cpa.ErrSourceConflict }
 v.Presence, v.Value, v.Persisted = presence, value, source.State
 ex := expected(source)
 v.Expected = &ex
 v.Editable = source.H1 && source.HandoffValid && source.ParentID == "" && source.Installed && source.State == "persisted"
 if r.Interval != nil {
  o := r.Interval.Operations[r.Interval.Latest]
  v.Operation = &o
  if o.State == "unknown" { v.Editable = false }
 }
 v.Reason = ""
 if !v.Editable { v.Reason = "original_operation_or_source_requires_reconciliation" }
 return v, nil
}

func (s *Service) SaveInterval(ctx context.Context, q Request) (freshstore.RetryOperation, error) {
 s.mu.Lock()
 defer s.mu.Unlock()
 if !freshkeys.ValidRequestID(q.RequestID) || q.Value == nil || *q.Value < 0 || *q.Value > 300 ||
  q.Expected.SourceID == "" || q.Expected.Generation == "" || q.Expected.Revision == 0 || len(q.Expected.SHA256) != 64 {
  return freshstore.RetryOperation{}, ErrInvalid
 }
 if s.h1 == nil { return freshstore.RetryOperation{}, cpa.ErrProofUnavailable }
 return s.saveIntervalH1(ctx, q)
}

func (s *Service) IntervalOperation(ctx context.Context, id string, reconcile bool) (freshstore.RetryOperation, error) {
 s.mu.Lock()
 defer s.mu.Unlock()
 if !freshkeys.ValidRequestID(id) { return freshstore.RetryOperation{}, ErrInvalid }
 r, err := s.store.Load(ctx)
 if err != nil { return freshstore.RetryOperation{}, err }
 if r.RetryInvalid || r.IntervalInvalid { return freshstore.RetryOperation{}, cpa.ErrSourceConflict }
 if r.Interval == nil { return freshstore.RetryOperation{}, ErrNotFound }
 o, ok := r.Interval.Operations[id]
 if !ok { return o, ErrNotFound }
 if !reconcile || o.State != "unknown" && o.State != "persisted" { return o, nil }
 if s.h1 == nil { return o, cpa.ErrProofUnavailable }
 return s.reconcileIntervalH1(ctx, o)
}
