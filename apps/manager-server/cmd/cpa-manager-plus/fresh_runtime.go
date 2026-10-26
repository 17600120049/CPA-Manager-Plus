package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

const freshRuntimeJournalKey = "fresh_runtime_operations_v1"

var freshRuntimeRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var freshRuntimeOperationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_:/-]{1,192}$`)

type freshRuntimeJournal struct {
	db       *sql.DB
	mu       sync.Mutex
	last     *freshRuntimeStatusResponse
	lastTime time.Time
}

type freshRuntimeOperation struct {
	RequestID         string    `json:"requestId"`
	Action            string    `json:"action"`
	OperationID       string    `json:"operationId"`
	State             string    `json:"state"`
	RuntimeIdentity   string    `json:"runtimeIdentity,omitempty"`
	RuntimeGeneration uint64    `json:"runtimeGeneration,omitempty"`
	FailureCode       string    `json:"failureCode,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type freshRuntimeJournalData struct {
	Operations map[string]freshRuntimeOperation `json:"operations"`
}

func newFreshRuntimeJournal(db *sql.DB) *freshRuntimeJournal { return &freshRuntimeJournal{db: db} }

func (j *freshRuntimeJournal) load(ctx context.Context) (freshRuntimeJournalData, error) {
	data := freshRuntimeJournalData{Operations: map[string]freshRuntimeOperation{}}
	if j == nil || j.db == nil {
		return data, nil
	}
	var raw string
	err := j.db.QueryRowContext(ctx, `select value from settings where key = ?`, freshRuntimeJournalKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil || data.Operations == nil {
		return freshRuntimeJournalData{}, errors.New("runtime operation journal is unavailable")
	}
	return data, nil
}

func (j *freshRuntimeJournal) save(ctx context.Context, data freshRuntimeJournalData) error {
	if j == nil || j.db == nil {
		return errors.New("runtime operation journal is unavailable")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value, updated_at_ms=excluded.updated_at_ms`, freshRuntimeJournalKey, string(raw), time.Now().UnixMilli())
	return err
}

func (j *freshRuntimeJournal) setLast(status *freshRuntimeStatusResponse, observedAt time.Time) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.last = status
	j.lastTime = observedAt
}

func (j *freshRuntimeJournal) lastStatus() (*freshRuntimeStatusResponse, time.Time) {
	if j == nil {
		return nil, time.Time{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last == nil {
		return nil, time.Time{}
	}
	copy := *j.last
	return &copy, j.lastTime
}

type freshRuntimeStatusResponse struct {
	Mode       string                       `json:"mode"`
	Status     string                       `json:"status"`
	ObservedAt *time.Time                   `json:"observedAt,omitempty"`
	Manager    freshRuntimeManagerStatus    `json:"manager"`
	Gateway    *freshRuntimeGatewayResponse `json:"gateway,omitempty"`
	LastKnown  *freshRuntimeGatewayResponse `json:"lastKnown,omitempty"`
	ErrorCode  string                       `json:"errorCode,omitempty"`
}

type freshRuntimeManagerStatus struct {
	State string `json:"state"`
}

type freshRuntimeRecoveryResponse struct {
	State             string `json:"state"`
	AttemptsRemaining int    `json:"attemptsRemaining"`
}

type freshRuntimeGatewayResponse struct {
	Identity        string                        `json:"identity,omitempty"`
	Generation      uint64                        `json:"generation,omitempty"`
	ProtocolVersion string                        `json:"protocolVersion,omitempty"`
	State           string                        `json:"state"`
	Ready           bool                          `json:"ready"`
	Version         string                        `json:"version,omitempty"`
	ArtifactID      string                        `json:"artifactId,omitempty"`
	Capabilities    []string                      `json:"capabilities,omitempty"`
	Recovery        *freshRuntimeRecoveryResponse `json:"recovery,omitempty"`
}

func freshRuntimeGatewayStatus(status model.RuntimeObservedStatus) *freshRuntimeGatewayResponse {
	gateway := &freshRuntimeGatewayResponse{
		Identity: string(status.Identity), Generation: uint64(status.Generation),
		ProtocolVersion: string(status.ProtocolVersion), State: string(status.State),
		Ready: status.State == model.RuntimeStateReady,
	}
	gateway.Version = string(status.CPAObservedVersion)
	for _, capability := range status.Capabilities {
		gateway.Capabilities = append(gateway.Capabilities, string(capability))
	}
	if status.ActiveGatewayArtifact != nil {
		gateway.ArtifactID = string(status.ActiveGatewayArtifact.ArtifactID)
	}
	if status.Recovery != nil {
		gateway.Recovery = &freshRuntimeRecoveryResponse{
			State: string(status.Recovery.State), AttemptsRemaining: status.Recovery.AttemptsRemaining,
		}
	}
	return gateway
}

func (a *freshApplication) runtimeStatus(w http.ResponseWriter, r *http.Request, op bootstrap.FreshOperation) {
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !op.ProductComplete {
		w.WriteHeader(http.StatusPreconditionRequired)
		return
	}
	mode := string(model.RuntimeModeExternal)
	statusClient := a.runtime
	if a.runtimeExternal == nil && statusClient != nil {
		mode = string(model.RuntimeModeEmbedded)
	}
	if statusClient == nil && a.runtimeExternal != nil {
		statusClient = a.runtimeExternal
	}
	if statusClient == nil {
		writeFreshJSON(w, freshRuntimeUnknownResponse(mode, "runtime_unavailable", a.runtimeJournal))
		return
	}
	now := time.Now().UTC()
	observed, err := statusClient.Status(r.Context())
	if err != nil {
		writeFreshJSON(w, freshRuntimeUnknownResponse(mode, "runtime_unreachable", a.runtimeJournal))
		return
	}
	response := freshRuntimeStatusResponse{
		Mode: mode, Status: string(observed.State), ObservedAt: &now,
		Manager: freshRuntimeManagerStatus{State: "ready"}, Gateway: freshRuntimeGatewayStatus(observed),
	}
	a.runtimeJournal.setLast(&response, now)
	writeFreshJSON(w, response)
}

func freshRuntimeUnknownResponse(mode, code string, journal *freshRuntimeJournal) freshRuntimeStatusResponse {
	response := freshRuntimeStatusResponse{
		Mode: mode, Status: string(model.RuntimeStateUnknown),
		Manager: freshRuntimeManagerStatus{State: "ready"}, ErrorCode: code,
	}
	if journal == nil {
		return response
	}
	last, observedAt := journal.lastStatus()
	if last != nil {
		response.LastKnown = last.Gateway
		response.ObservedAt = &observedAt
	}
	return response
}

type freshRuntimeOperationResponse struct {
	RequestID         string    `json:"requestId"`
	Action            string    `json:"action"`
	OperationID       string    `json:"operationId"`
	State             string    `json:"state"`
	RuntimeIdentity   string    `json:"runtimeIdentity,omitempty"`
	RuntimeGeneration uint64    `json:"runtimeGeneration,omitempty"`
	FailureCode       string    `json:"failureCode,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

func (o freshRuntimeOperation) response() freshRuntimeOperationResponse {
	return freshRuntimeOperationResponse{
		RequestID: o.RequestID, Action: o.Action, OperationID: o.OperationID, State: o.State,
		RuntimeIdentity: o.RuntimeIdentity, RuntimeGeneration: o.RuntimeGeneration,
		FailureCode: o.FailureCode, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

func (a *freshApplication) runtimeOperations(w http.ResponseWriter, r *http.Request, op bootstrap.FreshOperation, resource string) {
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !op.ProductComplete {
		w.WriteHeader(http.StatusPreconditionRequired)
		return
	}
	if resource == "runtime/operations" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		a.submitRuntimeOperation(w, r)
		return
	}
	if !strings.HasPrefix(resource, "runtime/operations/") || r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identifier := strings.TrimPrefix(resource, "runtime/operations/")
	if !freshRuntimeOperationIDPattern.MatchString(identifier) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.runtimeJournal == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	a.runtimeJournal.mu.Lock()
	defer a.runtimeJournal.mu.Unlock()
	data, err := a.runtimeJournal.load(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	operation, found := data.Operations[identifier]
	if !found {
		for _, candidate := range data.Operations {
			if candidate.OperationID == identifier {
				operation, found = candidate, true
				break
			}
		}
	}
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeFreshJSON(w, operation.response())
}

func (a *freshApplication) submitRuntimeOperation(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action    string `json:"action"`
		RequestID string `json:"requestId"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || !freshRuntimeRequestIDPattern.MatchString(request.RequestID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.Action != string(model.RuntimeOperationStart) && request.Action != string(model.RuntimeOperationStop) && request.Action != string(model.RuntimeOperationRestart) {
		freshRuntimeOperationError(w, http.StatusBadRequest, "invalid_action")
		return
	}
	if a.runtimeExternal != nil || a.runtime == nil {
		freshRuntimeOperationError(w, http.StatusConflict, "runtime_mutation_unsupported")
		return
	}
	if a.runtimeJournal == nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "operation_journal_unavailable")
		return
	}
	a.runtimeJournal.mu.Lock()
	defer a.runtimeJournal.mu.Unlock()
	data, err := a.runtimeJournal.load(r.Context())
	if err != nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "operation_journal_unavailable")
		return
	}
	if existing, found := data.Operations[request.RequestID]; found {
		if existing.Action != request.Action {
			freshRuntimeOperationError(w, http.StatusConflict, "operation_id_conflict")
			return
		}
		writeFreshJSON(w, existing.response())
		return
	}
	for _, existing := range data.Operations {
		if (existing.State == "accepted" || existing.State == "running" || existing.State == "unknown") && existing.Action != request.Action {
			freshRuntimeOperationError(w, http.StatusConflict, "operation_conflict")
			return
		}
	}
	observed, err := a.runtime.Status(r.Context())
	if err != nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "runtime_unreachable")
		return
	}
	if observed.State == model.RuntimeStateUnknown {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "runtime_observation_unknown")
		return
	}
	operationType := model.RuntimeOperationType(request.Action)
	capability := model.RuntimeCapability(request.Action)
	if !observed.Capabilities.Supports(capability) {
		freshRuntimeOperationError(w, http.StatusConflict, "capability_not_supported")
		return
	}
	if a.runtimeSettings == nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "desired_lifecycle_unavailable")
		return
	}
	desired := model.EmbeddedRuntimeDesiredRunning
	if operationType == model.RuntimeOperationStop {
		desired = model.EmbeddedRuntimeDesiredStopped
	}
	if _, err := a.runtimeSettings.SetEmbeddedRuntimeDesiredLifecycle(r.Context(), desired); err != nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "desired_lifecycle_unavailable")
		return
	}
	now := time.Now().UTC()
	operation := freshRuntimeOperation{
		RequestID: request.RequestID, Action: request.Action,
		OperationID: "runtime-ui/v1:" + request.RequestID,
		State:       "accepted", RuntimeIdentity: string(observed.Identity),
		RuntimeGeneration: uint64(observed.Generation), CreatedAt: now, UpdatedAt: now,
	}
	data.Operations[request.RequestID] = operation
	if err := a.runtimeJournal.save(r.Context(), data); err != nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "operation_journal_unavailable")
		return
	}
	mutationRequest := model.RuntimeMutationRequest{
		OperationID: operation.OperationID, ExpectedRuntimeIdentity: observed.Identity,
		ExpectedRuntimeGeneration: observed.Generation,
	}
	if operationType == model.RuntimeOperationStart && observed.ActiveGatewayArtifact != nil {
		mutationRequest.ExpectedActiveArtifactID = observed.ActiveGatewayArtifact.ArtifactID
	}
	var result model.RuntimeOperationResult
	switch operationType {
	case model.RuntimeOperationStart:
		result, err = a.runtime.Start(r.Context(), mutationRequest)
	case model.RuntimeOperationStop:
		result, err = a.runtime.Stop(r.Context(), mutationRequest)
	case model.RuntimeOperationRestart:
		result, err = a.runtime.Restart(r.Context(), mutationRequest)
	}
	if err != nil {
		if code, ok := runtimeservice.ErrorCode(err); ok {
			switch code {
			case runtimeservice.ProtocolErrorOperationIDConflict:
				freshRuntimeOperationError(w, http.StatusConflict, "operation_id_conflict")
				return
			case runtimeservice.ProtocolErrorOperationStateConflict:
				freshRuntimeOperationError(w, http.StatusConflict, "operation_conflict")
				return
			}
		}
		operation.State = "unknown"
		operation.FailureCode = "runtime_result_unknown"
	} else {
		operation.State = string(result.State)
		if result.Failure != nil {
			operation.FailureCode = result.Failure.Code
		}
		if operation.State == "" {
			operation.State = "unknown"
		}
	}
	operation.UpdatedAt = time.Now().UTC()
	data.Operations[request.RequestID] = operation
	if err := a.runtimeJournal.save(r.Context(), data); err != nil {
		freshRuntimeOperationError(w, http.StatusServiceUnavailable, "operation_journal_unavailable")
		return
	}
	if operation.State == "unknown" {
		w.WriteHeader(http.StatusAccepted)
	}
	writeFreshJSON(w, operation.response())
}

func freshRuntimeOperationError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeFreshJSON(w, struct {
		Code string `json:"code"`
	}{Code: code})
}
