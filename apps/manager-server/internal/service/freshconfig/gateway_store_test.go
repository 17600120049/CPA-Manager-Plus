package freshconfig

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// gatewayStore builds a real SQLite Fresh store holding a committed managed
// installation, so a test exercises the projection and the journal exactly as an
// installed Manager does instead of through the in-memory fake.
func gatewayStore(t *testing.T) (freshstore.Store, bootstrap.FreshIdentity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := freshstore.New(db)
	id := bootstrap.FreshIdentity{
		AdmissionID: "admission", InstallationID: "installation-gateway-01",
		Generation: "gen", OperationID: "fresh-operation", DatabasePath: path,
	}
	credential, err := security.NewAdminCredential("cpamp_GatewayAdmin0123456789!", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(t.Context(), id, &credential)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(t.Context(), id, record.Operation.Revision, func(v *freshstore.Record) error {
		v.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
			Strategy: bootstrap.NewManaged, Stage: "committed",
			TargetIdentity: "managed-template", TargetArtifactID: "sha256:" + strings.Repeat("b", 64),
			TargetGeneration: 7, DestinationConnectionIdentity: "managed:sha256:" + strings.Repeat("a", 64),
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, id
}

func gatewayRealService(t *testing.T, store freshstore.Store, wire configWire) *Service {
	t.Helper()
	service := New(store, func(context.Context, uint64) (string, error) { return "management-test-only", nil }, managedTargets)
	service.wire = wire
	service.transport = nil
	service.managed = true
	service.endpoint = "http://127.0.0.1:8317"
	return service
}

// secondDeleteRefused accepts the first DELETE and refuses the second, exactly
// like a CPA that rejects one of two "restore the default" removals.
type secondDeleteRefused struct {
	*fakeWire
	deletes int
}

func (w *secondDeleteRefused) DeleteConfigPath(ctx context.Context, secret, path string) error {
	w.deletes++
	if w.deletes == 2 {
		return cpa.ConfigWriteError{Status: 422, Code: "invalid_config"}
	}
	return w.fakeWire.DeleteConfigPath(ctx, secret, path)
}

// TestDeleteOnlySaveThatPartlyLandedIsNeverFailed is the round-2 #3 regression on
// the real store: a save made only of "use default" edits sends no PATCH, so the
// previous "was the PATCH acknowledged" test said nothing had been written even
// after the first DELETE removed a leaf. The second DELETE then reported a
// failure against a document that had already changed.
func TestDeleteOnlySaveThatPartlyLandedIsNeverFailed(t *testing.T) {
	store, _ := gatewayStore(t)
	wire := &secondDeleteRefused{fakeWire: &fakeWire{document: map[string]any{
		"routing": map[string]any{"retry": map[string]any{
			"request-retry": 3, "max-retry-credentials": 2, "max-retry-interval": 30,
		}},
	}}}
	service := gatewayRealService(t, store, wire)
	request := GroupRequest{
		RequestID: strings.Repeat("ab", 16),
		Expected:  Expected{Generation: "7", Baseline: baseline(t, service, "retry")},
		Edits: []Edit{
			{Path: "routing.retry.max-retry-credentials", Action: "default"},
			{Path: "routing.retry.max-retry-interval", Action: "default"},
		},
	}
	if _, err := service.SaveGroup(t.Context(), "retry", request); !errors.Is(err, cpa.ErrApplyUnknown) {
		t.Fatalf("err = %v, want an unknown result", err)
	}
	if len(wire.patches) != 0 || wire.deletes != 2 {
		t.Fatalf("patches=%d deletes=%d", len(wire.patches), wire.deletes)
	}
	// The first DELETE landed, so "failed" would claim the document is unchanged.
	loaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	operation := loaded.ConfigGroups["retry"].Operations[request.RequestID]
	if operation.State != "unknown" || operation.Code != "" || operation.HTTP200 {
		t.Fatalf("partially applied delete-only save recorded as %+v", operation)
	}
	// Reading back records exactly what landed, and the group is writable again.
	reconciled, err := service.Operation(t.Context(), request.RequestID, true)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != "persisted" || reconciled.Code != "partial" {
		t.Fatalf("read-back resolved to %+v", reconciled)
	}
}

// TestManagedProfileSurfacesTheUnresolvedLegacyOperation is the round-2 #9
// regression on the real store: a pre-cutover field operation blocked its group
// with copy that said "reconcile it first", but the group carried no operation,
// so the page had no request ID to reconcile with and the group stayed locked
// forever. The field journal operation is now the group's own operation.
func TestManagedProfileSurfacesTheUnresolvedLegacyOperation(t *testing.T) {
	store, id := gatewayStore(t)
	wire := &fakeWire{
		document:      map[string]any{"routing": map[string]any{"retry": map[string]any{"request-retry": 3}}},
		fieldPresence: "present", fieldValue: 4,
	}
	service := gatewayRealService(t, store, wire)
	legacyID := strings.Repeat("cd", 16)
	loaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(t.Context(), id, loaded.Operation.Revision, func(v *freshstore.Record) error {
		v.Config["routing.retry.request-retry"] = &freshstore.FieldJournal{
			Latest: legacyID,
			Operations: map[string]freshstore.ConfigOperation{
				legacyID: {
					RequestID: legacyID, Field: "routing.retry.request-retry", Value: 4,
					SourceID: id.InstallationID, Generation: "7", Revision: 1, State: "unknown",
				},
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := service.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var retry *GroupView
	for index := range view.Groups {
		if view.Groups[index].ID == "retry" {
			retry = &view.Groups[index]
		}
	}
	if retry == nil {
		t.Fatal("retry group missing")
	}
	if retry.Writable || retry.Reason != ReasonLegacyUnresolved {
		t.Fatalf("group holding an unresolved legacy operation stayed writable: %+v", retry)
	}
	// Without this the page cannot offer reconcile, and the group never unlocks.
	if retry.Operation == nil || retry.Operation.RequestID != legacyID || retry.Operation.State != "unknown" {
		t.Fatalf("group did not carry the legacy operation: %+v", retry.Operation)
	}
	if retry.Persisted != "unknown" {
		t.Fatalf("persisted evidence %q", retry.Persisted)
	}
	// The request ID the page now shows is really reconcilable.
	reconciled, err := service.Operation(t.Context(), retry.Operation.RequestID, true)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != "persisted" || reconciled.RequestID != legacyID {
		t.Fatalf("reconcile from the surfaced operation = %+v", reconciled)
	}
	if len(wire.patches) != 0 {
		t.Fatal("reconcile wrote to CPA")
	}
	// After the reconcile the group is writable again.
	view, err = service.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range view.Groups {
		if group.ID == "retry" && (!group.Writable || group.Reason != "") {
			t.Fatalf("group stayed locked after reconcile: %+v", group)
		}
	}
}

// ctxCheckingStore refuses a write whose context is already cancelled, which an
// in-memory store would silently accept.
type ctxCheckingStore struct {
	freshstore.Store
	checks int
}

func (c *ctxCheckingStore) Update(ctx context.Context, id bootstrap.FreshIdentity, revision uint64, fn func(*freshstore.Record) error) (freshstore.Record, error) {
	c.checks++
	if err := ctx.Err(); err != nil {
		return freshstore.Record{}, err
	}
	return c.Store.Update(ctx, id, revision, fn)
}

// abortingH1Wire aborts the browser request right after the Host accepted the
// write, which is exactly the window the success flag is written in.
type abortingH1Wire struct {
	*privateWire
	abort context.CancelFunc
}

func (w *abortingH1Wire) WriteH1ConfigField(ctx context.Context, parent string, step cpa.H1Step, management, path string, value any) error {
	if err := w.privateWire.WriteH1ConfigField(ctx, parent, step, management, path, value); err != nil {
		return err
	}
	w.abort()
	return nil
}

// TestH1SuccessFlagSurvivesABrowserAbort is the round-2 P2 regression: the Host
// accepted the write, then the browser aborted. The flag is written with a
// detached bounded context, so the receipt survives; were it tied to the request,
// H1 reconcile (which requires it) would be refused and the group locked forever.
func TestH1SuccessFlagSurvivesABrowserAbort(t *testing.T) {
	service, wire, _, memory := privateFixture(t, true)
	checking := &ctxCheckingStore{Store: memory}
	service.store = checking
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	aborting := &abortingH1Wire{privateWire: wire, abort: cancel}
	service.wire = aborting
	path := "requests.streaming.keepalive-seconds"
	requestID := strings.Repeat("d9", 16)
	// The browser is gone after the Host accepted the write: whatever the save
	// answers is irrelevant to its caller, but the receipt must be durable.
	_, _ = service.SaveGroup(ctx, "streaming", GroupRequest{
		RequestID: requestID,
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "streaming")},
		Edits:     []Edit{{Path: path, Action: "set", Value: json.RawMessage("20")}},
	})
	journal := memory.record.Config[path]
	if journal == nil {
		t.Fatal("private save not journaled")
	}
	if !journal.Operations[journal.Latest].HTTP200 {
		t.Fatalf("success flag lost to the abort: %+v", journal.Operations[journal.Latest])
	}
	if checking.checks == 0 {
		t.Fatal("the store was never asked to write")
	}
}
