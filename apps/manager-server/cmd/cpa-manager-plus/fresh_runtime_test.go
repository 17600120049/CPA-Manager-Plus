package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

type freshRuntimeTestClient struct {
	runtimeservice.RuntimeClient
	status        model.RuntimeObservedStatus
	statusErr     error
	startResult   model.RuntimeOperationResult
	startErr      error
	stopResult    model.RuntimeOperationResult
	stopErr       error
	restartResult model.RuntimeOperationResult
	restartErr    error
	starts        int
	stops         int
	restarts      int
}

func (c *freshRuntimeTestClient) Status(_ context.Context) (model.RuntimeObservedStatus, error) {
	return c.status, c.statusErr
}

func (c *freshRuntimeTestClient) Start(_ context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	c.starts++
	if c.startResult.OperationID == "" {
		c.startResult = freshRuntimeResult(request, model.RuntimeOperationStart, model.RuntimeOperationSucceeded)
	}
	return c.startResult, c.startErr
}

func (c *freshRuntimeTestClient) Stop(_ context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	c.stops++
	if c.stopResult.OperationID == "" {
		c.stopResult = freshRuntimeResult(request, model.RuntimeOperationStop, model.RuntimeOperationSucceeded)
	}
	return c.stopResult, c.stopErr
}

func (c *freshRuntimeTestClient) Restart(_ context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	c.restarts++
	if c.restartResult.OperationID == "" {
		c.restartResult = freshRuntimeResult(request, model.RuntimeOperationRestart, model.RuntimeOperationSucceeded)
	}
	return c.restartResult, c.restartErr
}

func freshRuntimeResult(request model.RuntimeMutationRequest, operation model.RuntimeOperationType, state model.RuntimeOperationState) model.RuntimeOperationResult {
	return model.RuntimeOperationResult{
		OperationID: request.OperationID, OperationType: operation,
		RuntimeIdentity: request.ExpectedRuntimeIdentity, RuntimeGeneration: request.ExpectedRuntimeGeneration,
		State: state,
	}
}

func freshRuntimeReadyStatus(capabilities ...model.RuntimeCapability) model.RuntimeObservedStatus {
	return model.RuntimeObservedStatus{
		Identity: "runtime-test", Generation: 7, ProtocolVersion: "v1", State: model.RuntimeStateReady,
		Capabilities: capabilities, Recovery: &model.RuntimeRecoveryObservation{State: model.RuntimeRecoveryStateInactive},
	}
}

func freshRuntimeRequest(t *testing.T, action, requestID string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{"action": action, "requestId": requestID})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(http.MethodPost, "/runtime/operations", bytes.NewReader(body))
}

func freshRuntimeProductCompleteOperation(t *testing.T, app *freshApplication) bootstrap.FreshOperation {
	t.Helper()
	op, err := app.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	op.ProductComplete = true
	return op
}

func TestFreshRuntimeOperationsCapabilityIdempotencyAndConflict(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	client := &freshRuntimeTestClient{status: freshRuntimeReadyStatus(model.RuntimeCapabilityStart)}
	app.runtime = client
	app.runtimeExternal = nil
	op := freshRuntimeProductCompleteOperation(t, app)

	unsupported := httptest.NewRecorder()
	app.runtimeOperations(unsupported, freshRuntimeRequest(t, "stop", "stop-1"), op, "runtime/operations")
	if unsupported.Code != http.StatusConflict || unsupported.Body.String() == "" {
		t.Fatalf("capability refusal = %d %s", unsupported.Code, unsupported.Body.String())
	}

	first := httptest.NewRecorder()
	app.runtimeOperations(first, freshRuntimeRequest(t, "start", "start-1"), op, "runtime/operations")
	if first.Code != http.StatusOK || client.starts != 1 {
		t.Fatalf("first start = %d starts=%d body=%s", first.Code, client.starts, first.Body.String())
	}
	second := httptest.NewRecorder()
	app.runtimeOperations(second, freshRuntimeRequest(t, "start", "start-1"), op, "runtime/operations")
	if second.Code != http.StatusOK || client.starts != 1 || second.Body.String() != first.Body.String() {
		t.Fatalf("idempotent start = %d starts=%d first=%s second=%s", second.Code, client.starts, first.Body.String(), second.Body.String())
	}

	client.status.Capabilities = model.RuntimeCapabilities{model.RuntimeCapabilityStart, model.RuntimeCapabilityStop}
	accepted := model.RuntimeOperationResult{
		OperationID: "runtime-ui/v1:stop-1", OperationType: model.RuntimeOperationStop,
		RuntimeIdentity: "runtime-test", RuntimeGeneration: 7, State: model.RuntimeOperationAccepted,
	}
	client.stopResult = accepted
	stop := httptest.NewRecorder()
	app.runtimeOperations(stop, freshRuntimeRequest(t, "stop", "stop-1"), op, "runtime/operations")
	if stop.Code != http.StatusOK || client.stops != 1 {
		t.Fatalf("stop = %d stops=%d body=%s", stop.Code, client.stops, stop.Body.String())
	}
	conflict := httptest.NewRecorder()
	app.runtimeOperations(conflict, freshRuntimeRequest(t, "start", "start-2"), op, "runtime/operations")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting start = %d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestFreshRuntimeUnknownOperationCanBeQueriedWithoutResend(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	client := &freshRuntimeTestClient{
		status:   freshRuntimeReadyStatus(model.RuntimeCapabilityStart),
		startErr: errors.New("transport interrupted after acceptance"),
	}
	app.runtime = client
	app.runtimeExternal = nil
	op := freshRuntimeProductCompleteOperation(t, app)

	submitted := httptest.NewRecorder()
	app.runtimeOperations(submitted, freshRuntimeRequest(t, "start", "unknown-1"), op, "runtime/operations")
	if submitted.Code != http.StatusAccepted {
		t.Fatalf("unknown submit = %d body=%s", submitted.Code, submitted.Body.String())
	}
	queried := httptest.NewRecorder()
	app.runtimeOperations(queried, httptest.NewRequest(http.MethodGet, "/runtime/operations/runtime-ui/v1:unknown-1", nil), op, "runtime/operations/runtime-ui/v1:unknown-1")
	if queried.Code != http.StatusOK || client.starts != 1 || !bytes.Contains(queried.Body.Bytes(), []byte(`"state":"unknown"`)) {
		t.Fatalf("unknown query = %d starts=%d body=%s", queried.Code, client.starts, queried.Body.String())
	}
}

func TestFreshRuntimeRequiresProductComplete(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	app.runtime = &freshRuntimeTestClient{status: freshRuntimeReadyStatus(model.RuntimeCapabilityStart)}
	op, err := app.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.runtimeOperations(response, freshRuntimeRequest(t, "start", "before-complete"), op, "runtime/operations")
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("ProductComplete gate = %d body=%s", response.Code, response.Body.String())
	}
}

// The journal keeps every non-terminal entry plus only the most recent terminal
// ones, so the single settings row cannot grow without limit.
func TestFreshRuntimeJournalBoundsTerminalEntries(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	defer closeApp()
	journal := app.runtimeJournal
	base := time.Now().UTC()
	data := freshRuntimeJournalData{Operations: map[string]freshRuntimeOperation{}}
	data.Operations["pending-stop"] = freshRuntimeOperation{
		RequestID: "pending-stop", Action: string(model.RuntimeOperationStop),
		OperationID: "runtime-ui/v1:pending-stop", State: freshRuntimeStateUnknown,
		CreatedAt: base, UpdatedAt: base,
	}
	for index := range freshRuntimeJournalTerminalRetention + 10 {
		requestID := fmt.Sprintf("done-%02d", index)
		data.Operations[requestID] = freshRuntimeOperation{
			RequestID: requestID, Action: string(model.RuntimeOperationStart),
			OperationID: "runtime-ui/v1:" + requestID, State: freshRuntimeStateSucceeded,
			CreatedAt: base.Add(time.Duration(index) * time.Second), UpdatedAt: base.Add(time.Duration(index) * time.Second),
		}
	}
	if err := journal.save(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	loaded, err := journal.load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Operations) != freshRuntimeJournalTerminalRetention+1 {
		t.Fatalf("bounded journal retained %d entries", len(loaded.Operations))
	}
	if _, found := loaded.Operations["pending-stop"]; !found {
		t.Fatal("bounded journal dropped a non-terminal entry")
	}
	if _, found := loaded.Operations[fmt.Sprintf("done-%02d", freshRuntimeJournalTerminalRetention+9)]; !found {
		t.Fatal("bounded journal dropped the newest terminal entry")
	}
	if _, found := loaded.Operations["done-00"]; found {
		t.Fatal("bounded journal retained the oldest terminal entry")
	}
}
