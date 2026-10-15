package src11

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/datamigration"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const (
	mig02CaseID     = "MIG-02"
	mig02PlanCommit = "06a47aaefff1629b3d556f8a49e0544648bfd4bc"
	mig02Profile    = "mig-02-applying-supported-resume-v1"
)

// The harness never issues SQL writes or changes product defaults. It schedules
// the same bounded production repository calls as the maintained worker, stops
// after a committed partial apply, and resumes from a newly restored candidate.
// This is a controlled cancellation/restart, not a power-loss qualification.
func TestQualifyMIG02(t *testing.T) {
	root := os.Getenv("CPAMP_MIG02_SOURCE")
	if root == "" {
		if os.Getenv("CPAMP_MIG02_REQUIRED") == "1" {
			t.Fatal("MIG-02 source required; recovery must not skip")
		}
		t.Skip("opt-in MIG-02 lifecycle qualification")
	}
	ctx := context.Background()
	archive := envRequired(t, "CPAMP_SRC11_ARCHIVE")
	anchor := revalidateCase(t, root, archive, mig02CaseID, mig02PlanCommit, mig02Profile)
	out := envRequired(t, "CPAMP_MIG02_OUTPUT")
	if _, e := os.Lstat(out); !os.IsNotExist(e) {
		t.Fatal("output must be absent")
	}
	must(t, os.MkdirAll(out, 0700))
	source := inventory(t, filepath.Join(root, "bundle"))
	exact, err := inspectDirectCandidateReadiness(ctx, directCandidateInput{SourceRoot: root, ProducerArchive: archive, GenerationRecordSHA256: fileDigest(t, filepath.Join(root, "source-generation/record.json")), BundleSHA256: source.SourceRevision})
	must(t, err)
	if exact.Status != "ready" {
		t.Fatal("independent exact-source baseline is not ready")
	}
	save(t, out, "source-readiness.json", exact)
	live, err := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), taskScratch(t, out, "interruption-"))
	must(t, err)
	lock, err := processlock.Acquire(live.DatabasePath)
	must(t, err)
	t.Cleanup(func() { lock.Close() })
	db := openDB(t, live.DatabasePath, false)
	before := mig02AllTables(t, db)
	sourceStates := query(t, db, "select * from usage_data_migrations order by name")
	initial, err := inspectMigrationReadiness(ctx, db)
	must(t, err)
	if !initial.completedV1144() {
		t.Fatal("source is not cache revision 2 / quota completed")
	}
	mig02QuotaNonVacuous(t, db)
	repo := datamigration.New(db)
	started, err := repo.DiscoverUsageCacheAccounting(ctx)
	must(t, err)
	if started.Status != "pending" || started.StartedAtMS <= 0 {
		t.Fatalf("real discovery did not start migration: %+v", started)
	}
	op := digestBytes([]byte(fmt.Sprintf("%s:%s:%d:%d:%s", source.SourceRevision, started.Name, started.StartedAtMS, started.TargetEventID, codeIdentity(t))))
	save(t, out, "pre-interruption-migration-state.json", map[string]any{"source": sourceStates, "discovered": started, "operationID": op, "operationIdentityKind": "qualification correlation over native row identity; no trusted journal receipt", "executorCommit": codeIdentity(t), "sourceRevision": 2, "executorRevision": 3})
	var trace []any
	state := started
	for i := 0; i < 100 && state.Status != "applying"; i++ {
		b, e := repo.RunUsageCacheAccountingBatch(ctx, 1)
		must(t, e)
		trace = append(trace, b)
		state = b.State
	}
	if state.Status != "applying" || state.ChangedRows != 2 || state.AppliedRows != 0 {
		t.Fatalf("real scan did not stage two changes: %+v", state)
	}
	stagedBefore := query(t, db, "select * from usage_cache_accounting_v2_changes order by event_id")
	partial, err := repo.RunUsageCacheAccountingBatch(ctx, 1)
	must(t, err)
	trace = append(trace, partial)
	state = partial.State
	if state.Status != "applying" || state.AppliedRows != 1 || state.ChangedRows != 2 {
		t.Fatalf("no committed partial apply: %+v", state)
	}
	pending := query(t, db, "select * from usage_cache_accounting_v2_changes order by event_id")
	mid := mig02AllTables(t, db)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.RunUsageCacheAccountingBatch(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("controlled interruption was not cancellation: %v", err)
	}
	if !reflect.DeepEqual(mid, mig02AllTables(t, db)) {
		t.Fatal("cancelled batch mutated persisted data")
	}
	interruptedChecks := sqliteChecks(t, db, true)
	must(t, db.Close())
	applyingBundle := filepath.Join(out, "applying-bundle")
	applying, err := maintenancebundle.Create(ctx, live, applyingBundle, lock)
	must(t, err)
	must(t, lock.Close())
	// Re-open immutable sealed bytes: applying must survive all live handles.
	persisted := mig02ImmutableDB(t, maintenancebundle.BundleLayout(applyingBundle).DatabasePath)
	persistedState, found, err := datamigration.New(persisted).UsageCacheAccountingState(ctx)
	must(t, err)
	if !found || !reflect.DeepEqual(state, persistedState) || !reflect.DeepEqual(mid, mig02AllTables(t, persisted)) {
		t.Fatal("partial checkpoint was not persisted")
	}
	inspection, err := inspectMigrationReadiness(ctx, persisted)
	must(t, err)
	must(t, persisted.Close())
	blocked, err := inspectMIG02Candidate(ctx, applyingBundle, source)
	if !errors.Is(err, errCacheApplyingRecovery) || blocked.Status != "blocked" || blocked.Recovery != "supported-resume" {
		t.Fatalf("normal readiness did not classify recovery: %+v %v", blocked, err)
	}
	applyingLayout := maintenancebundle.BundleLayout(applyingBundle)
	admission, admissionErr := legacypreflight.AdmitStartup(ctx, sqlite.InspectionPaths{Database: applyingLayout.DatabasePath, Config: applyingLayout.ConfigPath, DataKey: applyingLayout.DataKeyPath, Archives: applyingLayout.ArchivesPath}, legacypreflight.Options{SourceStopped: true})
	if admission != nil {
		admission.Close()
		t.Fatal("normal startup admitted interrupted legacy candidate")
	}
	if admissionErr == nil {
		t.Fatal("normal startup was not blocked")
	}
	sameInventory(t, applying, inventory(t, applyingBundle))
	save(t, out, "applying-state-snapshot.json", map[string]any{"state": persistedState, "inspection": inspection, "staging": pending, "allTables": mid, "bundle": applying})
	save(t, out, "interruption-record.json", map[string]any{"operationID": op, "source": "qualification scheduler: context cancellation after committed partial production batch; close all DB handles", "nativeIdentity": map[string]any{"name": state.Name, "startedAtMs": state.StartedAtMS, "targetEventId": state.TargetEventID, "revision": 3}, "ownership": "processlock held for migration and sealing; released before inspection", "journal": interruptedChecks, "completedSteps": trace, "remainingStaging": pending, "stagingBeforeApply": stagedBefore, "persistedApplying": true, "quotaCompleted": true, "recoveryEligibility": "supported-resume"})
	save(t, out, "readiness-before-recovery.json", map[string]any{"controlled": blocked, "normalStartupBlocked": true, "normalStartupError": admissionErr.Error()})
	// Recovery starts from the actual sealed applying bundle, not an earlier
	// completed source. No reset, marker deletion, schema migration or quota worker.
	resumedLayout, err := maintenancebundle.RestoreNew(ctx, applyingBundle, taskScratch(t, out, "recovery-"))
	must(t, err)
	recoveryLock, err := processlock.Acquire(resumedLayout.DatabasePath)
	must(t, err)
	defer recoveryLock.Close()
	db = openDB(t, resumedLayout.DatabasePath, false)
	if !reflect.DeepEqual(mid, mig02AllTables(t, db)) {
		t.Fatal("recovery input is not persisted applying")
	}
	repo = datamigration.New(db)
	resumed, err := repo.DiscoverUsageCacheAccounting(ctx)
	must(t, err)
	if !reflect.DeepEqual(persistedState, resumed) {
		t.Fatal("resume reset or replaced native checkpoint identity")
	}
	recoveryTrace := []any{resumed}
	var final datamigration.State
	var recoveredIDs []any
	for i := 0; i < 100; i++ {
		staged := query(t, db, "select event_id from usage_cache_accounting_v2_changes order by event_id")
		b, e := repo.RunUsageCacheAccountingBatch(ctx, 1)
		must(t, e)
		afterStage := query(t, db, "select event_id from usage_cache_accounting_v2_changes order by event_id")
		if b.State.AppliedRows > resumed.AppliedRows {
			delta := b.State.AppliedRows - resumed.AppliedRows
			if delta != int64(len(staged)-len(afterStage)) || delta != 1 {
				t.Fatal("apply checkpoint and staging removal differ")
			}
			recoveredIDs = append(recoveredIDs, staged[0]["event_id"])
		}
		recoveryTrace = append(recoveryTrace, b)
		resumed = b.State
		final = b.State
		if b.Completed {
			break
		}
	}
	if final.Status != "completed" || final.AppliedRows != 2 || final.ChangedRows != 2 || final.StartedAtMS != started.StartedAtMS || final.TargetEventID != started.TargetEventID || final.FinishedAtMS <= 0 {
		t.Fatalf("supported resume incomplete: %+v", final)
	}
	if len(recoveredIDs) != 1 || recoveredIDs[0] != pending[0]["event_id"] || len(pending) != 1 {
		t.Fatal("resume repeated completed work or lost staged work")
	}
	after := mig02AllTables(t, db)
	mig02AssertPreserved(t, before, after)
	if len(query(t, db, "select * from usage_cache_accounting_v2_changes")) != 0 {
		t.Fatal("staging did not close")
	}
	corrected := query(t, db, "select request_id,cache_input_mode,normalized_uncached_input_tokens,normalized_total_input_tokens,normalized_cache_read_tokens,normalized_cache_creation_tokens,total_tokens from usage_events where provider='workbuddy' order by id")
	if len(corrected) != 2 {
		t.Fatal("affected input missing")
	}
	for _, row := range corrected {
		for key, want := range map[string]any{"cache_input_mode": "read_included_creation_separate", "normalized_uncached_input_tokens": int64(100), "normalized_total_input_tokens": int64(125), "normalized_cache_read_tokens": int64(20), "normalized_cache_creation_tokens": int64(5), "total_tokens": int64(135)} {
			if row[key] != want {
				t.Fatalf("wrong migrated %s: %v != %v", key, row[key], want)
			}
		}
	}
	afterByID := make(map[any]map[string]any, len(after["usage_events"]))
	for _, row := range after["usage_events"] {
		afterByID[row["id"]] = row
	}
	for _, row := range mid["usage_events"] {
		if row["id"] == stagedBefore[0]["event_id"] && !reflect.DeepEqual(row, afterByID[row["id"]]) {
			t.Fatal("already applied event was rewritten on resume")
		}
	}
	repeated, err := repo.DiscoverUsageCacheAccounting(ctx)
	must(t, err)
	idempotent, err := repo.RunUsageCacheAccountingBatch(ctx, 1)
	must(t, err)
	if !reflect.DeepEqual(final, repeated) || !idempotent.Completed || !reflect.DeepEqual(final, idempotent.State) || !reflect.DeepEqual(after, mig02AllTables(t, db)) {
		t.Fatal("completed replay was not a no-op")
	}
	completedChecks := sqliteChecks(t, db, true)
	must(t, db.Close())
	completedBundle := filepath.Join(out, "completed-bundle")
	completed, err := maintenancebundle.Create(ctx, resumedLayout, completedBundle, recoveryLock)
	must(t, err)
	must(t, recoveryLock.Close())
	ready, err := inspectMIG02Candidate(ctx, completedBundle, source)
	must(t, err)
	if ready.Status != "ready" || ready.Preflight.BridgeReadiness == nil || ready.Preflight.BridgeReadiness.Status != "unknown" || ready.Preflight.UpgradeAuthorized || ready.UpgradeAuthorized || ready.TrustedHopReceipt {
		t.Fatal("readiness authority separation failed")
	}
	reopened := mig02ImmutableDB(t, maintenancebundle.BundleLayout(completedBundle).DatabasePath)
	completedState, found, err := datamigration.New(reopened).UsageCacheAccountingState(ctx)
	must(t, err)
	if !found || !reflect.DeepEqual(final, completedState) {
		t.Fatal("completed state cannot be re-inspected")
	}
	must(t, reopened.Close())
	sameInventory(t, source, inventory(t, filepath.Join(root, "bundle")))
	save(t, out, "post-resume-migration-state.json", map[string]any{"state": completedState, "inspection": ready.Migration, "bundle": completed, "sqlite": completedChecks})
	save(t, out, "recovery-resume-record.json", map[string]any{"operationID": op, "supportedProductionCalls": []string{"DiscoverUsageCacheAccounting", "RunUsageCacheAccountingBatch"}, "executorCommit": codeIdentity(t), "inputBundleDigest": applying.SourceRevision, "outputBundleDigest": completed.SourceRevision, "resumeCheckpoint": persistedState, "steps": recoveryTrace, "resumedEventIDs": recoveredIDs, "alreadyAppliedEventIDs": []any{stagedBefore[0]["event_id"]}, "idempotentCompletedReplay": idempotent, "noReset": true})
	save(t, out, "readiness-after-recovery.json", ready)
	save(t, out, "quota-evidence.json", map[string]any{"before": mig02Quota(before), "applying": mig02Quota(mid), "after": mig02Quota(after)})
	save(t, out, "authoritative-digest.json", map[string]any{"before": before, "after": after, "sourceBundleBefore": source.SourceRevision, "sourceBundleAfter": inventory(t, filepath.Join(root, "bundle")).SourceRevision, "correctedRows": corrected})
	save(t, out, "semantic-assertions.json", map[string]any{"SEM-07": map[string]any{"status": "runtime-pass", "quotaRowsEqual": true, "quotaMigrationEqual": true, "quotaSettingsEqual": true, "bankedResetEvidencePreserved": true, "cooldownEvidencePreserved": true}, "historyPreserved": true, "noDuplicateProcessing": true, "noLostEvents": true, "sourceBytesUnchanged": true, "unrelatedProviderStateUnchanged": true, "allowedChanges": "production cache revision2-to-3 derived fields, native migration state/staging, and derived invalidation only"})
	save(t, out, "execution-record.json", map[string]any{"executionCommit": codeIdentity(t), "sourceProducerCommit": anchor.SourceProducerBuildCodeCommit, "sourceBundleDigest": source.SourceRevision, "applyingBundleDigest": applying.SourceRevision, "completedBundleDigest": completed.SourceRevision, "operationID": op, "interruptionActuallyExecuted": true, "recoveryActuallyExecuted": true, "normalStartupBlocked": true, "controlledDirectCandidateReadinessBefore": "blocked", "controlledDirectCandidateReadinessAfter": "ready", "productionBridgeReadiness": "unknown", "applicableSEM": []string{"SEM-07"}})
	save(t, out, "result.json", map[string]any{"caseID": mig02CaseID, "runtimeEvidence": "pass", "casePass": false, "independentAcceptance": "pending", "SourceRelease": anchor.SourceRelease, "SourceCommit": anchor.SourceCommit, "PlanCommit": mig02PlanCommit, "SourceGeneratorCodeCommit": anchor.GeneratorCodeCommit, "executionCommit": codeIdentity(t), "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation/record.json")), "inputBundleDigest": source.SourceRevision, "applyingBundleDigest": applying.SourceRevision, "completedBundleDigest": completed.SourceRevision, "CacheBeforeRecovery": "applying", "CacheAfterRecovery": "completed", "Quota": "completed", "sourceCacheRevision": 2, "migrationExecutorRevision": 3, "normalReadinessBlocked": true, "recovery": "supported-resume", "recoveryActuallyExecuted": true, "controlledDirectCandidateReadinessBefore": "blocked", "controlledDirectCandidateReadinessAfter": "ready", "productionBridgeReadiness": "unknown", "applicableSEM": []string{"SEM-07"}, "UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false})
}

func mig02ImmutableDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbURI(path, true)+"&immutable=1&_pragma=query_only(1)")
	must(t, err)
	db.SetMaxOpenConns(1)
	must(t, db.Ping())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mig02AllTables(t *testing.T, db *sql.DB) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for _, table := range query(t, db, "select name from sqlite_schema where type='table' and name not like 'sqlite_%' order by name") {
		name := table["name"].(string)
		// Some SQLite maintenance tables are WITHOUT ROWID. SQLite returns the
		// same logical rows, but their scan order is not a durable evidence
		// contract. Sort the decoded rows by canonical JSON so before/after
		// comparisons and retained evidence are stable across SQLite builds.
		rows := query(t, db, "select * from \""+strings.ReplaceAll(name, "\"", "\"\"")+"\"")
		sort.SliceStable(rows, func(i, j int) bool {
			left, _ := json.Marshal(rows[i])
			right, _ := json.Marshal(rows[j])
			return string(left) < string(right)
		})
		out[name] = rows
	}
	return out
}

func mig02Quota(all map[string][]map[string]any) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for name, rows := range all {
		if strings.Contains(name, "quota") {
			out[name] = rows
		}
	}
	out["quotaMigration"] = []map[string]any{}
	for _, row := range all["usage_data_migrations"] {
		if row["name"] != "usage_cache_accounting_v2" {
			out["quotaMigration"] = append(out["quotaMigration"], row)
		}
	}
	out["unrelatedSettings"] = []map[string]any{}
	for _, row := range all["settings"] {
		if row["key"] != "usage_cache_accounting_semantics_revision" {
			out["unrelatedSettings"] = append(out["unrelatedSettings"], row)
		}
	}
	return out
}

func mig02QuotaNonVacuous(t *testing.T, db *sql.DB) {
	for _, name := range []string{"account_quota_observations", "account_quota_windows", "account_quota_window_activations", "account_quota_cycles", "account_quota_snapshots", "quota_cooldowns"} {
		if scalar(t, db, "select count(*) from "+name) == 0 {
			t.Fatalf("vacuous quota evidence: %s", name)
		}
	}
	if scalar(t, db, "select count(*) from account_quota_snapshots where reset_credits_available>0 and reset_credits_json is not null") == 0 {
		t.Fatal("banked resets missing")
	}
}

func mig02AssertPreserved(t *testing.T, before, after map[string][]map[string]any) {
	if !reflect.DeepEqual(mig02Quota(before), mig02Quota(after)) {
		t.Fatal("quota/reset/cooldown or unrelated metadata changed")
	}
	if len(before) != len(after) {
		t.Fatal("schema changed")
	}
	mutable := map[string]bool{"usage_events": true, "usage_data_migrations": true, "settings": true, "usage_cache_accounting_v2_changes": true, "usage_monitoring_event_projection_v1": true, "usage_hourly_aggregate_state": true, "usage_pricing_rollup_state": true, "usage_monitoring_rollup_state": true, "usage_account_model_rollups": true, "usage_dashboard_hourly_rollups": true, "usage_hourly_aggregate_v1": true, "usage_rollup_rebuild_state": true, "usage_rollup_checkpoints": true}
	for table, rows := range before {
		if !mutable[table] && !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("unrelated table changed: %s", table)
		}
	}
	derived := map[string]bool{"cache_input_mode": true, "normalized_uncached_input_tokens": true, "normalized_total_input_tokens": true, "normalized_cache_read_tokens": true, "normalized_cache_creation_tokens": true}
	if len(before["usage_events"]) != len(after["usage_events"]) {
		t.Fatal("history row loss/duplication")
	}
	afterByID := make(map[any]map[string]any, len(after["usage_events"]))
	for _, row := range after["usage_events"] {
		afterByID[row["id"]] = row
	}
	for _, row := range before["usage_events"] {
		afterRow, ok := afterByID[row["id"]]
		if !ok {
			t.Fatalf("authoritative event %v missing after migration", row["id"])
		}
		for col, want := range row {
			if row["provider"] == "workbuddy" && derived[col] {
				continue
			}
			if !reflect.DeepEqual(want, afterRow[col]) {
				t.Fatalf("authoritative/unrelated event %v %s changed", row["id"], col)
			}
		}
	}
}
