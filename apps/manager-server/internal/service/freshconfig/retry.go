// Package freshconfig owns only the private B2 retry scalar operation. The
// existing Fresh CAS persists intent; Runtime owns reservation/recovery bytes.
package freshconfig

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

var ErrInvalid = errors.New("invalid retry request")
var ErrNotFound = errors.New("retry operation not found")

type Expected struct {
	SourceID   string `json:"sourceId"`
	Generation string `json:"generation"`
	Revision   uint64 `json:"revision"`
	SHA256     string `json:"sha256"`
}
type Request struct {
	RequestID string   `json:"requestId"`
	Value     *int     `json:"value"`
	Expected  Expected `json:"expected"`
}
type View struct {
	Field                string                     `json:"field"`
	Presence             string                     `json:"presence"`
	Value                *int                       `json:"value"`
	Source               string                     `json:"source"`
	Authority            string                     `json:"authority"`
	Editable             bool                       `json:"editable"`
	Reason               string                     `json:"reason"`
	Persisted            string                     `json:"persisted"`
	Effective            string                     `json:"effective"`
	ConcurrentProtection string                     `json:"concurrentProtection"`
	Expected             *Expected                  `json:"expected,omitempty"`
	Operation            *freshstore.RetryOperation `json:"operation,omitempty"`
}
type wire interface {
	ReadRetry(context.Context, string) (cpa.RetrySource, string, *int, error)
	RetrySource(context.Context) (cpa.RetrySource, error)
	BeginRetry(context.Context, cpa.RetrySource, string, int) error
	PutRetry(context.Context, cpa.RetrySource, string, string, int) error
	ReconcileRetry(context.Context, cpa.RetrySource, string, int) (cpa.RetrySource, error)
}
type Service struct {
	mu     sync.Mutex
	store  freshstore.Store
	wire   wire
	target func(context.Context, uint64) (string, error)
}

func New(st freshstore.Store, target func(context.Context, uint64) (string, error)) *Service {
	return &Service{store: st, wire: cpa.NewTransport(), target: target}
}
func empty() View {
	return View{Field: "routing.retry.request-retry", Presence: "unknown", Source: "private-embedded-cpa-v8.0.13", Authority: "host-runtime", Reason: "private_source_unavailable", Persisted: "unknown", Effective: "unknown", ConcurrentProtection: "host-exclusive-runtime-reservation"}
}
func expected(v cpa.RetrySource) Expected {
	return Expected{v.SourceID, v.Generation, v.Revision, v.SHA256}
}
func knownSource(r freshstore.Record, source cpa.RetrySource) bool {
	if source.OperationScope == "initial-key" {
		return true
	}
	if source.OperationScope != "request-retry" || r.Retry == nil {
		return false
	}
	for id, o := range r.Retry.Operations {
		if "retry-"+id == source.OperationID && o.SourceID == source.SourceID && o.Generation == source.Generation && o.Value == source.OperationRetry {
			return true
		}
	}
	return false
}
func (s *Service) owner(ctx context.Context) (freshstore.Record, string, error) {
	r, e := s.store.Load(ctx)
	if e != nil {
		return r, "", e
	}
	if r.RetryInvalid {
		return r, "", cpa.ErrSourceConflict
	}
	op := r.Operation
	if s.target == nil || op.Strategy.Stage != "committed" || op.Strategy.Strategy != bootstrap.NewManaged || op.Strategy.TargetIdentity != cpa.PrivateKeyRuntime || op.Strategy.TargetArtifactID != cpa.PrivateKeyArtifact {
		return r, "", cpa.ErrProofUnavailable
	}
	m, e := s.target(ctx, op.Strategy.TargetGeneration)
	return r, m, e
}
func (s *Service) Read(ctx context.Context) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := empty()
	r, m, e := s.owner(ctx)
	if e != nil {
		if r.RetryInvalid {
			v.Reason = "configuration_journal_invalid"
		}
		if errors.Is(e, cpa.ErrWriterIsolation) {
			v.Reason = "config_key_writer_not_isolated"
		}
		return v, nil
	}
	source, presence, value, e := s.wire.ReadRetry(ctx, m)
	if e != nil {
		return v, e
	}
	if source.Generation != strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10) {
		return v, cpa.ErrSourceConflict
	}
	v.Presence, v.Value, v.Persisted = presence, value, source.State
	ex := expected(source)
	v.Expected = &ex
	v.Editable = source.Installed && source.State == "persisted"
	if !knownSource(r, source) {
		v.Editable = false
	}
	if r.Retry != nil {
		o := r.Retry.Operations[r.Retry.Latest]
		v.Operation = &o
		if o.State == "unknown" {
			v.Editable = false
		}
	}
	v.Reason = ""
	if !v.Editable {
		v.Reason = "original_operation_or_source_requires_reconciliation"
	}
	return v, nil
}
func (s *Service) Save(ctx context.Context, q Request) (freshstore.RetryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !freshkeys.ValidRequestID(q.RequestID) || q.Value == nil || *q.Value < 0 || *q.Value > 10 || q.Expected.SourceID == "" || q.Expected.Generation == "" || q.Expected.Revision == 0 || len(q.Expected.SHA256) != 64 {
		return freshstore.RetryOperation{}, ErrInvalid
	}
	r, m, e := s.owner(ctx)
	if e != nil {
		return freshstore.RetryOperation{}, e
	}
	if r.Retry != nil {
		if old, ok := r.Retry.Operations[q.RequestID]; ok {
			if old.Value != *q.Value || (Expected{old.SourceID, old.Generation, old.Revision, old.SHA256}) != q.Expected {
				return old, cpa.ErrSourceConflict
			}
			return old, nil // Tombstone forever: no begin, PUT or new identity.
		}
		if r.Retry.Operations[r.Retry.Latest].State == "unknown" {
			return freshstore.RetryOperation{}, cpa.ErrApplyUnknown
		}
	}
	source, e := s.wire.RetrySource(ctx)
	if e != nil {
		return freshstore.RetryOperation{}, e
	}
	if expected(source) != q.Expected || source.Generation != strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10) || source.State != "persisted" || !source.Installed || !knownSource(r, source) {
		return freshstore.RetryOperation{}, cpa.ErrSourceConflict
	}
	o := freshstore.RetryOperation{RequestID: q.RequestID, Value: *q.Value, SourceID: source.SourceID, Generation: source.Generation, Revision: source.Revision, SHA256: source.SHA256, State: "unknown"}
	_, e = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(r *freshstore.Record) error {
		if r.Retry == nil {
			r.Retry = &freshstore.RetryJournal{Operations: map[string]freshstore.RetryOperation{}}
		}
		if _, ok := r.Retry.Operations[q.RequestID]; ok {
			return freshstore.ErrConflict
		}
		if r.Retry.Latest != "" && r.Retry.Operations[r.Retry.Latest].State == "unknown" {
			return freshstore.ErrConflict
		}
		r.Retry.Operations[q.RequestID] = o
		r.Retry.Latest = q.RequestID
		return nil
	})
	if e != nil {
		return o, e
	}
	// Durable Manager intent precedes Runtime begin; any interruption from here
	// is reconciled using this ID. Even a lost begin reply cannot retry a PUT.
	if e = s.wire.BeginRetry(ctx, source, "retry-"+q.RequestID, o.Value); e != nil {
		return o, cpa.ErrApplyUnknown
	}
	// Recheck original Host target/lease after both durable writes. owner pins
	// strategy/stage/runtime/artifact; reject any remaining binding or key drift.
	current, management, e := s.owner(ctx)
	if e != nil || management != m || current.Operation.Identity != r.Operation.Identity || current.Operation.Strategy.TargetGeneration != r.Operation.Strategy.TargetGeneration {
		return o, cpa.ErrApplyUnknown
	}
	if e = s.wire.PutRetry(ctx, source, "retry-"+q.RequestID, m, o.Value); e != nil {
		return o, cpa.ErrApplyUnknown
	}
	return s.reconcile(ctx, o)
}
func (s *Service) Operation(ctx context.Context, id string, reconcile bool) (freshstore.RetryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !freshkeys.ValidRequestID(id) {
		return freshstore.RetryOperation{}, ErrInvalid
	}
	r, e := s.store.Load(ctx)
	if e != nil {
		return freshstore.RetryOperation{}, e
	}
	if r.RetryInvalid {
		return freshstore.RetryOperation{}, cpa.ErrSourceConflict
	}
	if r.Retry == nil {
		return freshstore.RetryOperation{}, ErrNotFound
	}
	o, ok := r.Retry.Operations[id]
	if !ok {
		return o, ErrNotFound
	}
	if !reconcile || o.State != "unknown" {
		return o, nil
	}
	if _, _, e = s.owner(ctx); e != nil {
		return o, e
	}
	return s.reconcile(ctx, o)
}
func (s *Service) reconcile(ctx context.Context, o freshstore.RetryOperation) (freshstore.RetryOperation, error) {
	source := cpa.RetrySource{SourceID: o.SourceID, Generation: o.Generation}
	v, e := s.wire.ReconcileRetry(ctx, source, "retry-"+o.RequestID, o.Value)
	if e != nil {
		return o, e
	}
	r, e := s.store.Load(ctx)
	if e != nil {
		return o, e
	}
	state := v.OperationState
	if state != "persisted" && state != "recovered" {
		return o, cpa.ErrApplyUnknown
	}
	_, e = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(r *freshstore.Record) error {
		if r.Retry == nil || r.Retry.Latest != o.RequestID || r.Retry.Operations[o.RequestID] != o {
			return freshstore.ErrConflict
		}
		o.State = state
		r.Retry.Operations[o.RequestID] = o
		return nil
	})
	if e != nil {
		o.State = "unknown"
	}
	return o, e
}
