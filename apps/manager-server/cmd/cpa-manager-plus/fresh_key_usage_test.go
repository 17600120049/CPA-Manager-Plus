package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	usageadapter "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/adapters/sqlite/identityusage"
	usageapp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/application/identityusage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

type freshUsageHarness struct {
	app             *freshApplication
	db              *sql.DB
	key             freshkeys.Key
	secret          string
	hash            string
	runtimeIdentity string
	// nowMS is a deterministic read instant after the key's creation so the
	// window, the collection start and the recorded calls are all comparable.
	nowMS   int64
	call    func(method, resource, body string) *httptest.ResponseRecorder
	cleanup func()
}

func newFreshUsageHarness(t *testing.T) *freshUsageHarness {
	t.Helper()
	ctx := t.Context()
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	app := newFreshApplication(opened, config.Config{}, nil)
	if _, err = app.store.Update(ctx, opened.Record.Operation.Identity, opened.Record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: r.Operation.Identity.OperationID, Revision: 1, Target: "/control", Stage: bootstrap.PrefixCommitted, Committed: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	markFreshProductComplete(t, app)
	harness := &freshUsageHarness{
		app: app, db: opened.DB, runtimeIdentity: string(validInstallationRuntime().Identity),
		cleanup: func() { _ = opened.Close() },
	}
	harness.call = func(method, resource, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/control/api/v2/"+resource, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
		response := httptest.NewRecorder()
		httpapi.InitialFullWebHandler("/control", http.HandlerFunc(app.canonicalAPI)).ServeHTTP(response, request)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("usage read weakened API cache control")
		}
		payload := response.Body.String()
		if strings.Contains(payload, strategyTestAdmin) {
			t.Fatalf("%s leaked the admin credential: %s", resource, payload)
		}
		if strings.Contains(resource, "usage") {
			for _, forbidden := range []string{harness.secret, harness.hash, harness.runtimeIdentity} {
				if forbidden != "" && strings.Contains(payload, forbidden) {
					t.Fatalf("%s leaked key or runtime material: %s", resource, payload)
				}
			}
			if lowered := strings.ToLower(payload); strings.Contains(lowered, "managementkey") || strings.Contains(lowered, "secret") || strings.Contains(lowered, "hash") {
				t.Fatalf("%s leaked credential fields: %s", resource, payload)
			}
		}
		return response
	}

	created := harness.call("POST", "api-keys", `{"requestId":"`+strings.Repeat("a", 32)+`"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", created.Code, created.Body.String())
	}
	var result freshkeys.Result
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.CreatedSecret == "" {
		t.Fatalf("creation result: %v", err)
	}
	harness.key, harness.secret = result.Key, result.CreatedSecret
	sum := sha256.Sum256([]byte(result.CreatedSecret))
	harness.hash = hex.EncodeToString(sum[:])
	harness.nowMS = result.Key.CreatedAtMS + 3_600_000
	harness.useCollection(t, usageapp.PhaseStopped)
	return harness
}

// useCollection keeps the real adapter and swaps only the live Collector phase.
func (h *freshUsageHarness) useCollection(t *testing.T, phase usageapp.CollectionPhase) {
	t.Helper()
	service := usageapp.New(usageadapter.New(h.db), func() usageapp.CollectionPhase { return phase })
	service.SetClock(func() int64 { return h.nowMS })
	h.app.usage = service
}

func (h *freshUsageHarness) bindingCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := h.db.QueryRow(`select count(*) from gateway_api_key_source_bindings where api_key_id = ?`, string(h.key.ID)).Scan(&count); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	return count
}

func (h *freshUsageHarness) dropBinding(t *testing.T) {
	t.Helper()
	if _, err := h.db.Exec(`delete from gateway_api_key_source_bindings where api_key_id = ?`, string(h.key.ID)); err != nil {
		t.Fatalf("drop binding: %v", err)
	}
}

func (h *freshUsageHarness) insertUsage(t *testing.T, calls int) {
	t.Helper()
	for i := range calls {
		eventID := int64(i + 1)
		eventHash := sha256Hex(fmt.Sprintf("usage-%d", eventID))
		failed := 0
		if i%3 == 2 {
			failed = 1
		}
		timestampMS := h.key.CreatedAtMS + int64(i+1)*1000
		if _, err := h.db.Exec(`insert into usage_events
			(id, event_hash, request_id, timestamp_ms, timestamp, model, api_key_hash, input_tokens, output_tokens, total_tokens, failed, created_at_ms)
			values (?, ?, ?, ?, '2026-01-01T00:00:00Z', 'gpt-4', ?, 10, 5, 15, ?, ?)`,
			eventID, eventHash, fmt.Sprintf("request-%d", eventID), timestampMS, h.hash, failed, timestampMS); err != nil {
			t.Fatalf("insert usage event: %v", err)
		}
		if _, err := h.db.Exec(`insert into gateway_usage_identity_projection_v1
			(usage_event_id, event_hash, request_id, evidence_timestamp_ms, api_key_state, api_key_id, api_key_source_hash,
			 credential_state, credential_id, credential_source_auth_id, schema_version, projected_at_ms)
			values (?, ?, ?, ?, 'mapped', ?, ?, 'unknown', null, '', 1, ?)`,
			eventID, eventHash, fmt.Sprintf("request-%d", eventID), timestampMS, string(h.key.ID), h.hash, timestampMS); err != nil {
			t.Fatalf("insert projection: %v", err)
		}
	}
	if _, err := h.db.Exec(`update gateway_usage_identity_projection_state
		set status = 'ready', last_processed_event_id = ?, target_event_id = ? where state_name = 'canonical_identity_v1'`,
		calls, calls); err != nil {
		t.Fatalf("mark projection ready: %v", err)
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func decodeUsage[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return value
}

func TestFreshKeyUsageRequiresACompletedInstall(t *testing.T) {
	harness := newFreshUsageHarness(t)
	defer harness.cleanup()
	detailPath := "api-keys/" + string(harness.key.ID) + "/usage"

	record, err := harness.app.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.app.store.Update(t.Context(), record.Operation.Identity, record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.ProductComplete = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"api-keys/usage-summary", detailPath} {
		if got := harness.call("GET", resource, ""); got.Code != http.StatusConflict || got.Body.Len() != 0 {
			t.Fatalf("%s before completion = %d %s", resource, got.Code, got.Body.String())
		}
	}

	// External keeps the existing conflict semantics for usage as well.
	record, err = harness.app.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	external := record.Operation
	external.Strategy.Strategy = bootstrap.ConnectExternal
	for _, resource := range []string{"api-keys/usage-summary", detailPath} {
		response := httptest.NewRecorder()
		harness.app.clientKeys(response, httptest.NewRequest("GET", "/control/api/v2/"+resource, nil), external, resource)
		if response.Code != http.StatusConflict || response.Body.Len() != 0 {
			t.Fatalf("external %s = %d", resource, response.Code)
		}
	}

	markFreshProductComplete(t, harness.app)
	if got := harness.call("GET", "api-keys/usage-summary", ""); got.Code != http.StatusOK {
		t.Fatalf("completed install usage = %d %s", got.Code, got.Body.String())
	}
}

func TestFreshKeyUsageReportsRealNumbersAndHonestCoverage(t *testing.T) {
	harness := newFreshUsageHarness(t)
	defer harness.cleanup()
	summaryPath := "api-keys/usage-summary"
	detailPath := "api-keys/" + string(harness.key.ID) + "/usage"

	// Creation binds the key's source in the same transaction, so the
	// workspace has an attribution window from the first event on.
	if harness.bindingCount(t) != 1 {
		t.Fatal("created key has no source binding")
	}
	var firstSeen int64
	if err := harness.db.QueryRow(`select first_seen_at_ms from gateway_api_key_source_bindings where api_key_id = ?`,
		string(harness.key.ID)).Scan(&firstSeen); err != nil || firstSeen != harness.key.CreatedAtMS {
		t.Fatalf("binding window = %d err = %v, want the creation instant", firstSeen, err)
	}

	harness.insertUsage(t, 6)
	if err := harness.app.usage.MarkCollectionStart(t.Context()); err != nil {
		t.Fatal(err)
	}

	// The Collector is not running in this harness: Manager must not present a
	// quiet window it cannot prove.
	detail := decodeUsage[usageapp.DetailResponse](t, harness.call("GET", detailPath, ""))
	if detail.Coverage.State != "unavailable" || detail.Coverage.Reason != "collection_stopped" {
		t.Fatalf("stopped coverage = %+v", detail.Coverage)
	}
	if detail.Totals != nil || len(detail.Series) != 0 {
		t.Fatalf("stopped window published totals=%+v series=%+v", detail.Totals, detail.Series)
	}

	harness.useCollection(t, usageapp.PhaseRunning)
	detail = decodeUsage[usageapp.DetailResponse](t, harness.call("GET", detailPath, ""))
	if detail.Coverage.State != "complete" || detail.Coverage.Reason != "observed" {
		t.Fatalf("complete coverage = %+v", detail.Coverage)
	}
	if detail.Coverage.CollectingSinceMS != harness.nowMS || detail.FromMS != harness.key.CreatedAtMS {
		t.Fatalf("coverage window = %+v from = %d", detail.Coverage, detail.FromMS)
	}
	if detail.Totals == nil || detail.Totals.Calls != 6 || detail.Totals.Failure != 2 || detail.Totals.Success != 4 ||
		detail.Totals.TotalTokens != 90 || detail.Totals.LastUsedAtMS == nil {
		t.Fatalf("detail totals = %+v", detail.Totals)
	}
	if len(detail.Series) != 1 || detail.Series[0].Calls != 6 {
		t.Fatalf("detail series = %+v", detail.Series)
	}

	summary := decodeUsage[usageapp.SummaryResponse](t, harness.call("GET", summaryPath, ""))
	if summary.Coverage.State != "complete" || summary.Items[0].Calls != 6 ||
		summary.Items[0].Coverage.State != "complete" || summary.Items[0].LastUsedAtMS == nil {
		t.Fatalf("summary = %+v", summary)
	}

	// A key without an attribution window is unavailable, never zero.
	harness.dropBinding(t)
	summary = decodeUsage[usageapp.SummaryResponse](t, harness.call("GET", summaryPath, ""))
	if summary.Items[0].Coverage.State != "unavailable" || summary.Items[0].Coverage.Reason != "binding_missing" || summary.Items[0].Calls != 0 {
		t.Fatalf("unbound summary = %+v", summary)
	}
	detail = decodeUsage[usageapp.DetailResponse](t, harness.call("GET", detailPath, ""))
	if detail.Coverage.Reason != "binding_missing" || detail.Totals != nil {
		t.Fatalf("unbound detail = %+v", detail)
	}

	// The startup backfill re-establishes attribution from the key's creation,
	// so history recorded before the binding existed is not lost.
	bound, err := harness.app.keyRepo.BindFreshKeySources(t.Context(), harness.runtimeIdentity, harness.nowMS)
	if err != nil || bound != 1 {
		t.Fatalf("backfill = %d err = %v", bound, err)
	}
	if again, err := harness.app.keyRepo.BindFreshKeySources(t.Context(), harness.runtimeIdentity, harness.nowMS); err != nil || again != 0 {
		t.Fatalf("repeat backfill = %d err = %v", again, err)
	}
	summary = decodeUsage[usageapp.SummaryResponse](t, harness.call("GET", summaryPath, ""))
	if summary.Items[0].Coverage.State != "complete" || summary.Items[0].Calls != 6 {
		t.Fatalf("backfilled summary = %+v", summary)
	}

	// Revoked history stays readable: retirement closes attribution without
	// deleting what was already attributed.
	if _, err := harness.db.Exec(`update gateway_api_key_identities set lifecycle = 'superseded', revision = revision + 1 where id = ?`, string(harness.key.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.db.Exec(`update gateway_api_key_source_bindings set retired_at_ms = ? where api_key_id = ?`, harness.nowMS, string(harness.key.ID)); err != nil {
		t.Fatal(err)
	}
	retained := decodeUsage[usageapp.DetailResponse](t, harness.call("GET", detailPath, ""))
	if retained.Totals == nil || retained.Totals.Calls != 6 || retained.Coverage.State != "complete" {
		t.Fatalf("revoked history = %+v", retained)
	}

	// Bad input stays a client error and an unknown key stays absent.
	if got := harness.call("GET", detailPath+"?from_ms=1", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("partial window accepted: %d", got.Code)
	}
	if got := harness.call("GET", detailPath+"?from_ms=100&to_ms=200&granularity=month", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown window parameter accepted: %d", got.Code)
	}
	if got := harness.call("GET", "api-keys/"+strings.Repeat("b", 32)+"/usage", ""); got.Code != http.StatusNotFound {
		t.Fatalf("unknown key usage: %d", got.Code)
	}
	if got := harness.call("GET", "api-keys/usage-summary", ""); got.Code != http.StatusOK {
		t.Fatalf("summary after revocation = %d", got.Code)
	}
}
