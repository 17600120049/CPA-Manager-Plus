// Package freshconfig owns the private CPA configuration write loop for every
// allowlisted field. The existing Fresh CAS persists intent; Runtime owns the
// reservation, the raw-file recovery bytes and the receipt.
package freshconfig

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"sync"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

var ErrInvalid = errors.New("invalid config field request")
var ErrNotFound = errors.New("config field operation not found")

// Expected is the exact private source the caller observed. A write is refused
// unless the same source is still current, so no field is written twice.
type Expected struct {
	SourceID   string `json:"sourceId"`
	Generation string `json:"generation"`
	Revision   uint64 `json:"revision"`
	SHA256     string `json:"sha256"`
}

// FieldRequest is one single-field write of an allowlisted scalar.
type FieldRequest struct {
	RequestID string          `json:"requestId"`
	Value     json.RawMessage `json:"value"`
	Expected  Expected        `json:"expected"`
}

// FieldView is the read model for one field. It carries no YAML and no secret.
type FieldView struct {
	Field                string                      `json:"field"`
	Presence             string                      `json:"presence"`
	Value                any                         `json:"value"`
	Source               string                      `json:"source"`
	Authority            string                      `json:"authority"`
	Editable             bool                        `json:"editable"`
	Reason               string                      `json:"reason"`
	Persisted            string                      `json:"persisted"`
	Effective            string                      `json:"effective"`
	ConcurrentProtection string                      `json:"concurrentProtection"`
	Expected             *Expected                   `json:"expected,omitempty"`
	Operation            *freshstore.ConfigOperation `json:"operation,omitempty"`
}

// fieldWire is the transport surface for allowlisted config fields. Transport
// implements it with the private Runtime journal; tests supply fakes.
type fieldWire interface {
	ConfigFieldSource(context.Context) (cpa.ConfigFieldSource, error)
	ReadConfigField(context.Context, string, string) (cpa.ConfigFieldSource, string, any, error)
	BeginConfigField(context.Context, cpa.ConfigFieldSource, string, string, any) error
	PutConfigField(context.Context, cpa.ConfigFieldSource, string, string, string, any) error
	WriteH1ConfigField(context.Context, string, cpa.H1Step, string, string, any) error
	ReconcileConfigField(context.Context, cpa.ConfigFieldSource, string, string, any) (cpa.ConfigFieldSource, error)
	ReadConfigFieldValue(context.Context, string, string) (string, any, error)
	WriteConfigFieldValue(context.Context, string, string, any) error
	HasRuntimeJournal() bool
}

// Service serializes every field write in this process. Runtime remains the
// cross-process arbiter: this mutex never pretends to be the Host lease.
type Service struct {
	mu        sync.Mutex
	store     freshstore.Store
	wire      fieldWire
	transport *cpa.Transport
	h1        *cpa.H1Coordinator
	target    func(context.Context, uint64) (string, error)
}

// New builds the service over the Fresh CAS store and the private management
// authority. The authority is the only source of a management credential.
func New(st freshstore.Store, target func(context.Context, uint64) (string, error)) *Service {
	transport := cpa.NewTransport()
	return &Service{store: st, wire: transport, transport: transport, target: target}
}

// EnableH1 switches the service onto the Host H1 handoff journal.
func (s *Service) EnableH1(h *cpa.H1Coordinator) {
	s.h1 = h
	if s.transport != nil {
		s.transport.UseH1(h)
	}
}

func fieldValue(path string, raw json.RawMessage) (any, error) {
	return cpa.ParseConfigFieldValue(path, raw)
}

// ValidateFieldRequest rejects everything the allowlist or the wire contract
// does not permit before any journal, reservation or PUT happens.
func ValidateFieldRequest(path string, q FieldRequest) error {
	if _, ok := cpa.LookupConfigField(path); !ok {
		return ErrInvalid
	}
	if !freshkeys.ValidRequestID(q.RequestID) || len(q.Value) == 0 || q.Expected.Generation == "" ||
		q.Expected.Revision == 0 || (len(q.Expected.SHA256) != 0 && len(q.Expected.SHA256) != 64) {
		return ErrInvalid
	}
	if _, err := fieldValue(path, q.Value); err != nil {
		return ErrInvalid
	}
	return nil
}

func fieldExpected(source cpa.ConfigFieldSource) Expected {
	return Expected{SourceID: source.SourceID, Generation: source.Generation, Revision: source.Revision, SHA256: source.SHA256}
}

func emptyField(path string) FieldView {
	return FieldView{Field: path, Presence: "unknown", Source: "private-embedded-cpa-v8.0.13", Authority: "host-runtime", Reason: "private_source_unavailable", Persisted: "unknown", Effective: "unknown", ConcurrentProtection: "host-exclusive-runtime-reservation"}
}

func fieldJournal(r freshstore.Record, path string) *freshstore.FieldJournal {
	return r.Config[path]
}

func fieldOperation(r freshstore.Record, path string) *freshstore.ConfigOperation {
	journal := fieldJournal(r, path)
	if journal == nil {
		return nil
	}
	operation, ok := journal.Operations[journal.Latest]
	if !ok {
		return nil
	}
	return &operation
}

// knownSource proves the private source carries only our own allowed writes. A
// completed operation of another allowlisted field is equally known: each field
// owns its own journal entry.
func knownSource(r freshstore.Record, source cpa.ConfigFieldSource) bool {
	if source.OperationScope == "initial-key" || source.OperationID == "" {
		return true
	}
	if source.OperationPath == "" {
		return false
	}
	journal := r.Config[source.OperationPath]
	if journal == nil {
		return false
	}
	id := cpa.ConfigFieldRequestID(source.OperationID)
	for requestID, operation := range journal.Operations {
		if requestID == id && operation.SourceID == source.SourceID && operation.Generation == source.Generation &&
			reflect.DeepEqual(operation.Value, source.OperationValue) {
			return true
		}
	}
	return false
}

// fieldOwner proves the private profile and returns the management authority for
// exactly the committed Fresh target generation.
func (s *Service) fieldOwner(ctx context.Context, path string) (freshstore.Record, string, error) {
	if _, ok := cpa.LookupConfigField(path); !ok {
		return freshstore.Record{}, "", ErrInvalid
	}
	r, err := s.store.Load(ctx)
	if err != nil {
		return r, "", err
	}
	op := r.Operation
	if s.target == nil || op.Strategy.Stage != "committed" || op.Strategy.Strategy != bootstrap.NewManaged ||
		op.Strategy.TargetGeneration == 0 {
		return r, "", cpa.ErrProofUnavailable
	}
	// The private attested profile must name its exact runtime artifact; a managed
	// install writes through the install-derived ConfigTarget instead.
	if op.Strategy.TargetIdentity == cpa.PrivateKeyRuntime && op.Strategy.TargetArtifactID != cpa.PrivateKeyArtifact {
		return r, "", cpa.ErrProofUnavailable
	}
	management, err := s.target(ctx, op.Strategy.TargetGeneration)
	return r, management, err
}

// readOwner is fieldOwner plus the isolated-journal check used by reads, where a
// damaged journal must lock only its own field instead of failing the profile.
func (s *Service) readOwner(ctx context.Context, path string) (freshstore.Record, string, error) {
	r, management, err := s.fieldOwner(ctx, path)
	if err != nil {
		return r, "", err
	}
	if r.ConfigInvalid[path] {
		return r, "", cpa.ErrSourceConflict
	}
	return r, management, nil
}

// managedSource is the Manager-owned journal view. It carries an installation
// identity and the committed target generation, never a runtime reservation.
func managedSource(r freshstore.Record) cpa.ConfigFieldSource {
	return cpa.ConfigFieldSource{
		SourceID:   r.Operation.Identity.InstallationID,
		Generation: strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10),
		Revision:   r.Operation.Revision,
		State:      "persisted",
		Installed:  true,
	}
}

// ReadField reports presence, value and editability for one allowlisted field.
func (s *Service) ReadField(ctx context.Context, path string) (FieldView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := emptyField(path)
	if _, ok := cpa.LookupConfigField(path); !ok {
		return v, ErrInvalid
	}
	r, management, err := s.readOwner(ctx, path)
	if err != nil {
		if r.ConfigInvalid[path] {
			v.Reason = "configuration_journal_invalid"
		}
		if errors.Is(err, cpa.ErrWriterIsolation) {
			v.Reason = "config_key_writer_not_isolated"
		}
		return v, nil
	}
	var source cpa.ConfigFieldSource
	var presence string
	var value any
	if s.wire.HasRuntimeJournal() {
		source, presence, value, err = s.wire.ReadConfigField(ctx, management, path)
		if err != nil {
			return v, err
		}
		if source.Generation != strconv.FormatUint(r.Operation.Strategy.TargetGeneration, 10) {
			return v, cpa.ErrSourceConflict
		}
	} else {
		// Managed profile: the Manager journal is the source, CPA is only read.
		source = managedSource(r)
		presence, value, err = s.wire.ReadConfigFieldValue(ctx, management, path)
		if err != nil {
			return v, err
		}
	}
	v.Presence, v.Value, v.Persisted = presence, value, source.State
	expected := fieldExpected(source)
	v.Expected = &expected
	v.Editable = source.Installed && source.State == "persisted" && knownSource(r, source)
	if source.H1 {
		v.Editable = v.Editable && source.HandoffValid && source.ParentID == ""
	}
	if !s.wire.HasRuntimeJournal() {
		v.Editable = true
	}
	if operation := fieldOperation(r, path); operation != nil {
		v.Operation = operation
		if operation.State == "unknown" {
			v.Editable = false
		}
	}
	v.Reason = ""
	if !v.Editable {
		v.Reason = "original_operation_or_source_requires_reconciliation"
	}
	return v, nil
}

// OperationField returns the original operation of one field. Recovery is
// explicit: an unknown operation is never replayed, only re-observed.
func (s *Service) OperationField(ctx context.Context, path, id string, reconcile bool) (freshstore.ConfigOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := cpa.LookupConfigField(path); !ok || !freshkeys.ValidRequestID(id) {
		return freshstore.ConfigOperation{}, ErrInvalid
	}
	if _, _, err := s.readOwner(ctx, path); err != nil {
		return freshstore.ConfigOperation{}, err
	}
	operation := s.journalOperation(ctx, path, id)
	if operation == nil {
		return freshstore.ConfigOperation{}, ErrNotFound
	}
	return s.reconcileOperation(ctx, path, *operation, reconcile)
}

// Operation resolves a request ID across every allowlisted field journal.
func (s *Service) Operation(ctx context.Context, id string, reconcile bool) (freshstore.ConfigOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !freshkeys.ValidRequestID(id) {
		return freshstore.ConfigOperation{}, ErrInvalid
	}
	r, err := s.store.Load(ctx)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if r.Operation.Strategy.Stage != "committed" || s.target == nil {
		return freshstore.ConfigOperation{}, cpa.ErrProofUnavailable
	}
	for _, path := range cpa.ConfigFieldPaths() {
		if r.ConfigInvalid[path] {
			continue
		}
		journal := fieldJournal(r, path)
		if journal == nil {
			continue
		}
		if operation, ok := journal.Operations[id]; ok {
			if _, _, err := s.readOwner(ctx, path); err != nil {
				return freshstore.ConfigOperation{}, err
			}
			return s.reconcileOperation(ctx, path, operation, reconcile)
		}
	}
	return freshstore.ConfigOperation{}, ErrNotFound
}

func (s *Service) journalOperation(ctx context.Context, path, id string) *freshstore.ConfigOperation {
	r, err := s.store.Load(ctx)
	if err != nil {
		return nil
	}
	journal := fieldJournal(r, path)
	if journal == nil {
		return nil
	}
	operation, ok := journal.Operations[id]
	if !ok {
		return nil
	}
	return &operation
}

func (s *Service) reconcileOperation(ctx context.Context, path string, operation freshstore.ConfigOperation, reconcile bool) (freshstore.ConfigOperation, error) {
	if !reconcile || operation.State != "unknown" {
		return operation, nil
	}
	if s.h1 != nil {
		return s.reconcileH1(ctx, path, operation)
	}
	if !s.wire.HasRuntimeJournal() {
		return s.reconcileManaged(ctx, path, operation)
	}
	return s.reconcileDirect(ctx, path, operation)
}
