package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	usageapp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/application/identityusage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

// externalUsageCPA is the user's own CPA as these tests model it: one management
// credential, one usage-statistics switch, and a usage queue the real Collector
// polls.
type externalUsageCPA struct {
	management atomic.Value
	enabled    atomic.Bool
	rejects    atomic.Bool
	statWrites atomic.Int32
	queueReads atomic.Int32
	version    atomic.Value
	server     *httptest.Server
}

func newExternalUsageCPA(t *testing.T, management string) *externalUsageCPA {
	t.Helper()
	f := &externalUsageCPA{}
	f.management.Store(management)
	f.version.Store("v8.0.13")
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.rejects.Load() || r.Header.Get("Authorization") != "Bearer "+f.management.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v0/management/config":
			w.Header().Set("X-CPA-Version", f.version.Load().(string))
			_, _ = w.Write([]byte(`{}`))
		case "/v0/management/usage-statistics-enabled":
			if r.Method == http.MethodPut {
				f.statWrites.Add(1)
				f.enabled.Store(true)
			}
			_, _ = w.Write([]byte(`{"usage-statistics-enabled":` + boolText(f.enabled.Load()) + `}`))
		case "/v8/management/config/access/api-keys":
			_, _ = w.Write([]byte("[]"))
		case "/v8/management/config.yaml":
			_, _ = w.Write([]byte(`{"config-version":8}`))
		case "/v0/management/usage-queue":
			f.queueReads.Add(1)
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// externalUsageHarness is a routed Fresh application whose committed checkpoint
// and protected connection name one fake External CPA.
type externalUsageHarness struct {
	app *freshApplication
	cpa *externalUsageCPA
	db  *sql.DB
}

func newExternalUsageHarness(t *testing.T) *externalUsageHarness {
	t.Helper()
	hostCtx, stopHost := context.WithCancel(t.Context())
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(hostCtx, paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil {
		stopHost()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopHost()
		_ = opened.Close()
	})
	// The Collector polls over HTTP in these tests; RESP discovery would only add
	// an unrelated TCP exchange to the fake CPA.
	app := newFreshApplication(opened, config.Config{CollectorMode: "http"}, nil)
	app.runCtx = hostCtx
	quietProjection(app)

	fake := newExternalUsageCPA(t, externalAuthorityManagement)
	normalized, err := cpa.NormalizeV8BaseURL(fake.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := opened.Protector.ProtectString(externalAuthorityManagement)
	if err != nil {
		t.Fatal(err)
	}
	op := opened.Record.Operation
	if _, err = app.store.Update(hostCtx, op.Identity, op.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{
			OperationID: op.Identity.OperationID, Revision: 1, Target: "/control", Stage: bootstrap.PrefixCommitted, Committed: true,
		}
		r.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
			Strategy: bootstrap.ConnectExternal, Stage: "committed",
			TargetIdentity: freshkeyapply.ExternalIdentity(normalized), TargetGeneration: 1,
		}
		r.Operation.ProductComplete = true
		r.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: normalized, ManagementKey: protected}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app.externalSource = externalInstallSource(app.store, opened.Protector)
	app.keyApply = freshkeyapply.NewExternal(app.keyRepo, app.keys, app.externalSource)
	return &externalUsageHarness{app: app, cpa: fake, db: opened.DB}
}

func (h *externalUsageHarness) call(t *testing.T, method, resource, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/control/api/v2/"+resource, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
	response := httptest.NewRecorder()
	h.app.canonicalAPI(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s weakened API cache control", resource)
	}
	return response
}

func (h *externalUsageHarness) collectionStartRows(t *testing.T) int {
	t.Helper()
	var count int
	if err := h.db.QueryRow(`select count(*) from settings where key = 'fresh_usage_collection_started_at_ms'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (h *externalUsageHarness) bindingIdentity(t *testing.T, id freshkeys.Key) string {
	t.Helper()
	var identity string
	if err := h.db.QueryRow(`select runtime_identity from gateway_api_key_source_bindings where api_key_id = ?`, string(id.ID)).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	return identity
}

func (h *externalUsageHarness) operation(t *testing.T) bootstrap.FreshOperation {
	t.Helper()
	record, err := h.app.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return record.Operation
}

func decodeExternalUsageState(t *testing.T, body []byte) externalUsageState {
	t.Helper()
	var state externalUsageState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decode usage state: %v (%s)", err, string(body))
	}
	return state
}

// TestExternalUsageCollectionNeedsTheAdministratorConfirmation proves the manager
// never enables usage statistics on a CPA it does not own without being told to,
// and that the confirmed write is proven by reading the switch back.
func TestExternalUsageCollectionNeedsTheAdministratorConfirmation(t *testing.T) {
	h := newExternalUsageHarness(t)

	h.app.startUsageCollection(h.operation(t))
	if h.cpa.statWrites.Load() != 0 {
		t.Fatal("usage statistics were enabled without the administrator's confirmation")
	}
	if phase := h.app.collectionPhase(); phase != usageapp.PhaseStopped {
		t.Fatalf("collector = %s, want stopped without a confirmed switch", phase)
	}
	if reason := h.app.usageStartBlock(); reason != usageStartReasonStatisticsDisabled {
		t.Fatalf("blocked reason = %q", reason)
	}
	if rows := h.collectionStartRows(t); rows != 0 {
		t.Fatal("a refused start recorded a coverage window")
	}

	// The confirmation is the only path that writes the switch.
	response := h.call(t, http.MethodPost, "external/usage",
		`{"requestId":"`+strings.Repeat("a", 32)+`","confirmEnableStatistics":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", response.Code, response.Body.String())
	}
	if writes := h.cpa.statWrites.Load(); writes != 1 {
		t.Fatalf("statistics writes = %d, want exactly one", writes)
	}
	if rows := h.collectionStartRows(t); rows != 1 {
		t.Fatalf("coverage windows = %d, want one", rows)
	}
	state := decodeExternalUsageState(t, response.Body.Bytes())
	if !state.StatisticsEnabled || !state.CollectionStarted || state.BlockedReason != "" {
		t.Fatalf("state = %+v", state)
	}
	// The state read never carries a credential.
	if strings.Contains(response.Body.String(), externalAuthorityManagement) {
		t.Fatal("the usage state leaked the management credential")
	}
}

// TestExternalUsageCollectionRejectsAnUnconfirmedRequest proves a POST that does
// not confirm anything writes nothing at all.
func TestExternalUsageCollectionRejectsAnUnconfirmedRequest(t *testing.T) {
	h := newExternalUsageHarness(t)
	for _, body := range []string{
		`{"requestId":"` + strings.Repeat("a", 32) + `"}`,
		`{"requestId":"` + strings.Repeat("a", 32) + `","confirmEnableStatistics":false}`,
		`{"confirmEnableStatistics":true}`,
		`{"requestId":"` + strings.Repeat("a", 32) + `","confirmEnableStatistics":true,"extra":1}`,
	} {
		if response := h.call(t, http.MethodPost, "external/usage", body); response.Code != http.StatusBadRequest {
			t.Fatalf("unconfirmed %s = %d", body, response.Code)
		}
	}
	if writes := h.cpa.statWrites.Load(); writes != 0 {
		t.Fatalf("statistics writes = %d, want none", writes)
	}
	if rows := h.collectionStartRows(t); rows != 0 {
		t.Fatal("an unconfirmed request recorded a coverage window")
	}
}

// TestExternalUsageCollectionBindsTheConnectionIdentity proves staged keys are
// attributed to external:sha256:<digest>, never to a raw address and never to
// the managed destination, so history cannot follow the connection elsewhere.
func TestExternalUsageCollectionBindsTheConnectionIdentity(t *testing.T) {
	h := newExternalUsageHarness(t)
	h.cpa.enabled.Store(true)
	staged := stageClientKey(t, h.app)
	identity := h.operation(t).Strategy.TargetIdentity
	if !strings.HasPrefix(identity, "external:sha256:") {
		t.Fatalf("committed identity = %q", identity)
	}

	h.app.startUsageCollection(h.operation(t))
	if phase := h.app.collectionPhase(); phase == usageapp.PhaseStopped {
		t.Fatalf("collector = %s after an already enabled switch", phase)
	}
	if bound := h.bindingIdentity(t, staged.Key); bound != identity {
		t.Fatalf("binding identity = %q, want the connection digest %q", bound, identity)
	}
	if rows := h.collectionStartRows(t); rows != 1 {
		t.Fatalf("coverage windows = %d, want one", rows)
	}
	// A later attempt is a replay: no second Collector, no second binding.
	h.app.startUsageCollection(h.operation(t))
	if bound := h.bindingIdentity(t, staged.Key); bound != identity {
		t.Fatalf("replay moved the binding to %q", bound)
	}
}

// TestExternalUsageCollectionStopsOnARejectedCredential proves a 401 stops
// collection and surfaces the typed blocker instead of retrying.
func TestExternalUsageCollectionStopsOnARejectedCredential(t *testing.T) {
	h := newExternalUsageHarness(t)
	h.cpa.enabled.Store(true)
	h.app.startUsageCollection(h.operation(t))
	if phase := h.app.collectionPhase(); phase == usageapp.PhaseStopped {
		t.Fatalf("collector = %s before the credential was rotated", phase)
	}

	h.cpa.rejects.Store(true)
	outcome := h.app.startExternalUsageCollector(t.Context())
	if outcome.Started || outcome.Reason != freshconfig.ReasonManagementRejected {
		t.Fatalf("outcome = %+v, want the typed rejection", outcome)
	}
	if phase := h.app.collectionPhase(); phase != usageapp.PhaseStopped {
		t.Fatalf("collector = %s after a 401, want stopped", phase)
	}
	response := h.call(t, http.MethodGet, "external/usage", "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), freshconfig.ReasonManagementRejected) {
		t.Fatalf("usage state after a 401 = %d %s", response.Code, response.Body.String())
	}
}

// TestExternalUsageCollectionClaimsNothingWhenNothingStarts covers the defect the
// plan names: MarkCollectionStart must only follow a real start.
func TestExternalUsageCollectionClaimsNothingWhenNothingStarts(t *testing.T) {
	h := newExternalUsageHarness(t)
	h.app.externalSource = nil
	h.app.managedSource = nil
	h.app.privateCollector = nil

	h.app.startUsageCollection(h.operation(t))
	if rows := h.collectionStartRows(t); rows != 0 {
		t.Fatal("a strategy with no Collector authority recorded a coverage window")
	}
	if phase := h.app.collectionPhase(); phase != usageapp.PhaseStopped {
		t.Fatalf("collector = %s, want stopped", phase)
	}
	if reason := h.app.usageStartBlock(); reason != usageStartReasonNoAuthority {
		t.Fatalf("blocked reason = %q, want %q", reason, usageStartReasonNoAuthority)
	}
}

// TestExternalUsageCollectionRefusesAForeignConnection proves an authority that
// no longer names the committed CPA starts nothing and keeps the first binding:
// a rebind to another CPA must never inherit the previous history.
func TestExternalUsageCollectionRefusesAForeignConnection(t *testing.T) {
	h := newExternalUsageHarness(t)
	h.cpa.enabled.Store(true)
	staged := stageClientKey(t, h.app)
	firstIdentity := h.operation(t).Strategy.TargetIdentity
	h.app.startUsageCollection(h.operation(t))

	// The checkpoint and the live connection disagree: the authority refuses,
	// so no Collector and no new attribution window exist.
	record, err := h.app.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.app.store.Update(t.Context(), record.Operation.Identity, record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Strategy.TargetIdentity = "external:sha256:" + strings.Repeat("c", 64)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.app.resetUsageStart()
	outcome := h.app.startExternalUsageCollector(t.Context())
	if outcome.Started || outcome.Reason != usageStartReasonExternalUnavailable {
		t.Fatalf("outcome = %+v", outcome)
	}
	if bound := h.bindingIdentity(t, staged.Key); bound != firstIdentity {
		t.Fatalf("binding moved to %q, want the original %q", bound, firstIdentity)
	}
}

// TestExternalUsageStateRefusesAnIncompleteInstall proves the workspace answers a
// state conflict instead of a quiet empty posture before completion.
func TestExternalUsageStateRefusesAnIncompleteInstall(t *testing.T) {
	h := newExternalUsageHarness(t)
	op := h.operation(t)
	op.ProductComplete = false
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request := httptest.NewRequest(method, "/control/api/v2/external/usage",
			bytes.NewBufferString(`{"requestId":"`+strings.Repeat("a", 32)+`","confirmEnableStatistics":true}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.app.externalUsageRoute(response, request, op, "")
		if response.Code != http.StatusConflict || response.Body.Len() != 0 {
			t.Fatalf("%s before completion = %d %s", method, response.Code, response.Body.String())
		}
	}
	if writes := h.cpa.statWrites.Load(); writes != 0 {
		t.Fatalf("statistics writes = %d, want none", writes)
	}
}

// TestManagedUsageStartRecordsCollectionOnlyAfterAuthenticating proves the
// managed profile gained the same guarantee: a Collector whose credential does
// not answer an authenticated read records no coverage window.
func TestManagedUsageStartRecordsCollectionOnlyAfterAuthenticating(t *testing.T) {
	h := newExternalUsageHarness(t)
	h.app.externalSource = nil
	h.app.keyApply = nil
	managed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer managed.Close()
	h.app.managedSource = func(context.Context) (freshkeyapply.ManagedInstall, error) {
		op := h.operation(t)
		return freshkeyapply.ManagedInstall{
			RunID: "managed-run", Generation: "1", Identity: op.Strategy.TargetIdentity,
			BaseURL: managed.URL, ManagementKey: strings.Repeat("a", 64),
		}, nil
	}
	h.app.startUsageCollection(h.operation(t))
	if rows := h.collectionStartRows(t); rows != 0 {
		t.Fatal("the managed profile claimed collection without an authenticated sample")
	}
}

var _ = config.Config{}
