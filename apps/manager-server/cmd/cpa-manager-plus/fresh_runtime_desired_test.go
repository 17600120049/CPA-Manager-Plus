package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
)

// completedRuntimeTestApplication promotes the shared Fresh fixture to a
// completed installation so the canonical Runtime resource is served through
// the real ProductComplete gate.
func completedRuntimeTestApplication(t *testing.T) (*freshApplication, bootstrap.FreshOperation) {
	t.Helper()
	app, closeApp := newA3TestApp(t)
	t.Cleanup(closeApp)
	op, err := app.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	record, err := app.store.Update(t.Context(), op.Identity, op.Revision, func(record *freshstore.Record) error {
		record.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
			Strategy: bootstrap.NewManaged, Stage: "committed",
			TargetIdentity: "runtime-test", TargetGeneration: 7,
			TargetArtifactID: "sha256:" + strings.Repeat("a", 64),
		}
		record.Operation.ProductComplete = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, record.Operation
}

// A successful stop is delivered product behaviour: it persists the Manager's
// desired lifecycle beside the Fresh checkpoint. The next request reloads that
// checkpoint, so the stop must leave the Manager healthy and still able to
// report the operation it recorded — otherwise the first stop takes the whole
// Console and every lane resource down until the container is recreated.
func TestRuntimeStopKeepsTheManagerLoadableAndQueryable(t *testing.T) {
	app, op := completedRuntimeTestApplication(t)
	client := &freshRuntimeTestClient{status: freshRuntimeReadyStatus(model.RuntimeCapabilityStart, model.RuntimeCapabilityStop)}
	app.runtime = client
	app.runtimeExternal = nil

	stop := httptest.NewRecorder()
	app.runtimeOperations(stop, freshRuntimeRequest(t, "stop", "stop-after-complete"), op, "runtime/operations")
	if stop.Code != http.StatusOK || client.stops != 1 {
		t.Fatalf("stop = %d stops=%d body=%s", stop.Code, client.stops, stop.Body.String())
	}
	desired, found, err := app.runtimeSettings.LoadEmbeddedRuntimeDesiredState(t.Context())
	if err != nil || !found || desired.DesiredLifecycle != model.EmbeddedRuntimeDesiredStopped {
		t.Fatalf("desired lifecycle after stop = %+v found=%v err=%v", desired, found, err)
	}

	// The liveness probe re-reads the validated store on every call.
	health := httptest.NewRecorder()
	app.ServeHTTP(health, httptest.NewRequest(http.MethodGet, bootstrap.FixedLivenessPath, nil))
	if health.Code != http.StatusOK {
		t.Fatalf("Manager health after stop = %d %s", health.Code, health.Body.String())
	}

	// The canonical resource reloads the same store before answering.
	query := httptest.NewRequest(http.MethodGet, "/control/api/v2/runtime/operations/runtime-ui/v1:stop-after-complete", nil)
	query.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
	response := httptest.NewRecorder()
	app.canonicalAPI(response, query)
	if response.Code != http.StatusOK {
		t.Fatalf("operation query after stop = %d body=%s", response.Code, response.Body.String())
	}
	var operation struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
		t.Fatalf("operation query body: %v", err)
	}
	if operation.State != string(model.RuntimeOperationSucceeded) {
		t.Fatalf("operation state after stop = %q body=%s", operation.State, response.Body.String())
	}
}
