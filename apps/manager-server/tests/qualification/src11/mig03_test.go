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
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/datamigration"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	usageaggregaterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageaggregate"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagerollup"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

// MIG-03 records a real cleanup checkpoint and stops there. The cancellation
// is deliberately after one committed clear batch: the persisted state proves
// that clearing had begun, while the remaining derived rows prove that the
// lifecycle was not silently completed or reset by qualification.
func TestQualifyMIG03(t *testing.T) {
	root := os.Getenv("CPAMP_MIG03_SOURCE")
	if root == "" {
		if os.Getenv("CPAMP_MIG03_REQUIRED") == "1" {
			t.Fatal("MIG-03 source required; clearing qualification must not skip")
		}
		t.Skip("opt-in MIG-03 clearing lifecycle qualification")
	}
	ctx := context.Background()
	archive := envRequired(t, "CPAMP_SRC11_ARCHIVE")
	anchor := revalidateCase(t, root, archive, mig03CaseID, mig03PlanCommit, mig03Profile)
	out := envRequired(t, "CPAMP_MIG03_OUTPUT")
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatal("output must be absent")
	}
	must(t, os.MkdirAll(out, 0700))

	source := inventory(t, filepath.Join(root, "bundle"))
	sourceReady, err := inspectDirectCandidateReadiness(ctx, directCandidateInput{
		SourceRoot:             root,
		ProducerArchive:        archive,
		GenerationRecordSHA256: fileDigest(t, filepath.Join(root, "source-generation/record.json")),
		BundleSHA256:           source.SourceRevision,
	})
	must(t, err)
	if sourceReady.Status != "ready" {
		t.Fatal("independent exact-source baseline is not ready")
	}
	save(t, out, "source-readiness.json", sourceReady)

	live, err := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), taskScratch(t, out, "clearing-"))
	must(t, err)
	lock, err := processlock.Acquire(live.DatabasePath)
	must(t, err)
	defer lock.Close()
	db := openDB(t, live.DatabasePath, false)
	initial := mig02AllTables(t, db)
	initialReadiness, err := inspectMigrationReadiness(ctx, db)
	must(t, err)
	if !initialReadiness.completedV1144() {
		t.Fatal("source is not cache revision 2 / quota completed")
	}
	mig02QuotaNonVacuous(t, db)

	seedTrace := mig03SeedDerivedRows(t, ctx, db)
	prepared := mig02AllTables(t, db)
	preparedCounts := mig03DerivedCounts(t, db)
	for table, count := range preparedCounts {
		if count <= 0 {
			t.Fatalf("real clearing premise is empty: %s", table)
		}
	}
	if reflect.DeepEqual(initial, prepared) {
		t.Fatal("derived lifecycle preparation did not create a distinct candidate")
	}

	repo := datamigration.New(db)
	started, err := repo.DiscoverUsageCacheAccounting(ctx)
	must(t, err)
	if started.Status != "pending" || started.StartedAtMS <= 0 || started.TargetEventID <= 0 {
		t.Fatalf("real discovery did not start migration: %+v", started)
	}
	operationID := digestBytes([]byte(fmt.Sprintf("%s:%s:%d:%d:%s", source.SourceRevision, started.Name, started.StartedAtMS, started.TargetEventID, codeIdentity(t))))
	var lifecycleTrace []any
	lifecycleTrace = append(lifecycleTrace, map[string]any{"phase": "discovery", "state": started})
	state := started
	for i := 0; i < 100 && state.Status != "clearing"; i++ {
		batch, batchErr := repo.RunUsageCacheAccountingBatch(ctx, 100)
		must(t, batchErr)
		lifecycleTrace = append(lifecycleTrace, map[string]any{"phase": "scan-or-apply", "batch": batch})
		state = batch.State
	}
	if state.Status != "clearing" || state.ChangedRows <= 0 || state.AppliedRows != state.ChangedRows || state.FinishedAtMS != 0 {
		t.Fatalf("real lifecycle did not reach clearing before cleanup: %+v", state)
	}
	preClearingState := state
	preClearingCounts := mig03DerivedCounts(t, db)

	clearBatch, err := repo.RunUsageCacheAccountingBatch(ctx, 1)
	must(t, err)
	lifecycleTrace = append(lifecycleTrace, map[string]any{"phase": "clearing", "batch": clearBatch})
	if clearBatch.State.Status != "clearing" || clearBatch.Completed || clearBatch.Processed <= 0 {
		t.Fatalf("bounded clear did not persist an incomplete clearing state: %+v", clearBatch)
	}
	state = clearBatch.State
	clearingCounts := mig03DerivedCounts(t, db)
	clearingCounts["clearedRows"] = preClearingCounts["totalRows"] - clearingCounts["totalRows"]
	clearingCounts["remainingRows"] = clearingCounts["totalRows"]
	if clearingCounts["clearedRows"] != clearBatch.Processed || clearingCounts["remainingRows"] <= 0 {
		t.Fatalf("clear operation did not leave remaining cleanup work: counts=%v result=%+v", clearingCounts, clearBatch)
	}
	if clearingCounts["totalRows"] >= preClearingCounts["totalRows"] {
		t.Fatalf("clear operation removed no derived rows: before=%v after=%v", preClearingCounts, clearingCounts)
	}

	clearingSnapshot := mig02AllTables(t, db)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, interruptionErr := repo.RunUsageCacheAccountingBatch(cancelled, 1)
	if !errors.Is(interruptionErr, context.Canceled) {
		t.Fatalf("controlled cancellation was not observed: %v", interruptionErr)
	}
	if !reflect.DeepEqual(clearingSnapshot, mig02AllTables(t, db)) {
		t.Fatal("cancelled cleanup mutated the persisted clearing checkpoint")
	}
	interruption := map[string]any{
		"mechanism":             "context cancellation at the next production clear-batch boundary",
		"operationID":           operationID,
		"cancellationError":     interruptionErr.Error(),
		"cancelledBeforeCall":   true,
		"persistedState":        state,
		"completedClearSteps":   clearBatch.Processed,
		"preClearingCounts":     preClearingCounts,
		"postClearCounts":       clearingCounts,
		"remainingCleanupRows":  clearingCounts["remainingRows"],
		"noMutationAfterCancel": true,
	}
	checks := sqliteChecks(t, db, true)
	must(t, db.Close())

	clearingBundlePath := filepath.Join(out, "clearing-bundle")
	clearingBundle, err := maintenancebundle.Create(ctx, live, clearingBundlePath, lock)
	must(t, err)
	must(t, lock.Close())
	persisted := mig02ImmutableDB(t, maintenancebundle.BundleLayout(clearingBundlePath).DatabasePath)
	persistedState, found, err := datamigration.New(persisted).UsageCacheAccountingState(ctx)
	must(t, err)
	if !found || !reflect.DeepEqual(state, persistedState) || !reflect.DeepEqual(clearingSnapshot, mig02AllTables(t, persisted)) {
		t.Fatal("clearing checkpoint was not durably sealed")
	}
	persistedInspection, err := inspectMigrationReadiness(ctx, persisted)
	must(t, err)
	if persistedInspection.Cache[0].Status != "clearing" || persistedInspection.Quota[0].Status != "completed" {
		t.Fatal("persisted state changed during read-only inspection")
	}
	must(t, persisted.Close())

	blocked, readinessErr := inspectMIG03Candidate(ctx, clearingBundlePath, source)
	if !errors.Is(readinessErr, errCacheClearingRecovery) || blocked.Status != "blocked" || blocked.Blocker != "cache-accounting-clearing" || blocked.Recovery != "block" {
		t.Fatalf("normal readiness did not classify clearing as Recovery/block: %+v %v", blocked, readinessErr)
	}
	if blocked.Migration == nil || blocked.Migration.Cache[0].Status != "clearing" || blocked.Migration.Quota[0].Status != "completed" {
		t.Fatal("readiness result omitted the persisted clearing/quota states")
	}
	layout := maintenancebundle.BundleLayout(clearingBundlePath)
	admission, admissionErr := legacypreflight.AdmitStartup(ctx, sqlite.InspectionPaths{
		Database: layout.DatabasePath,
		Config:   layout.ConfigPath,
		DataKey:  layout.DataKeyPath,
		Archives: layout.ArchivesPath,
	}, legacypreflight.Options{SourceStopped: true})
	if admission != nil {
		must(t, admission.Close())
		t.Fatal("normal startup admitted a clearing candidate")
	}
	if admissionErr == nil {
		t.Fatal("normal startup was not blocked")
	}
	blockedAfter := mig02ImmutableDB(t, layout.DatabasePath)
	blockedSnapshot := mig02AllTables(t, blockedAfter)
	if !reflect.DeepEqual(clearingSnapshot, blockedSnapshot) {
		t.Fatal("readiness or startup inspection mutated the clearing bundle")
	}
	must(t, blockedAfter.Close())
	sameInventory(t, clearingBundle, inventory(t, clearingBundlePath))

	// This comparison allows only the production migration's derived fields,
	// migration rows, and bounded clear tables to differ. Quota and source rows
	// remain authoritative and must match exactly.
	mig02AssertPreserved(t, prepared, blockedSnapshot)
	if !reflect.DeepEqual(mig02Quota(prepared), mig02Quota(blockedSnapshot)) {
		t.Fatal("SEM-07 quota/reset/cooldown evidence changed while clearing was blocked")
	}
	if blocked.Migration.Cache[0].Status == "completed" || blocked.Migration.Cache[0].FinishedAtMS != nil {
		t.Fatal("clearing was automatically completed")
	}

	sourceStateDB := openDB(t, layout.DatabasePath, true)
	sourceStates := query(t, sourceStateDB, "select * from usage_data_migrations order by name")
	must(t, sourceStateDB.Close())
	save(t, out, "pre-clearing-migration-state.json", map[string]any{
		"source":               sourceStates,
		"discovered":           started,
		"preClearing":          preClearingState,
		"operationID":          operationID,
		"executorCommit":       codeIdentity(t),
		"sourceRevision":       2,
		"executorRevision":     3,
		"seedDerivedLifecycle": seedTrace,
		"preparedCounts":       preparedCounts,
		"lifecycleTrace":       lifecycleTrace,
	})
	save(t, out, "clearing-operation.json", map[string]any{
		"operationID":           operationID,
		"migrationRevision":     3,
		"start":                 map[string]any{"startedAtMs": started.StartedAtMS, "targetEventId": started.TargetEventID, "name": started.Name},
		"preClearingState":      preClearingState,
		"clearBatch":            clearBatch,
		"preClearingCounts":     preClearingCounts,
		"postClearCounts":       clearingCounts,
		"remainingCleanupState": clearingCounts,
		"checkpoint":            map[string]any{"name": state.Name, "startedAtMs": state.StartedAtMS, "targetEventId": state.TargetEventID, "status": state.Status},
		"interruption":          interruption,
		"affectedResources":     []string{"usage_account_model_rollups", "usage_dashboard_hourly_rollups", "usage_hourly_aggregate_v1"},
	})
	save(t, out, "clearing-state-snapshot.json", map[string]any{
		"state":                persistedState,
		"inspection":           persistedInspection,
		"preparedCounts":       preparedCounts,
		"preClearingCounts":    preClearingCounts,
		"clearingCounts":       clearingCounts,
		"remainingCleanupRows": clearingCounts["remainingRows"],
		"quotaMigration":       blocked.Migration.Quota,
	})
	save(t, out, "interruption-record.json", interruption)
	save(t, out, "readiness-before-recovery.json", map[string]any{
		"controlled":           blocked,
		"normalStartupBlocked": true,
		"normalStartupError":   admissionErr.Error(),
		"automaticRecovery":    false,
		"automaticCleanup":     false,
		"automaticReset":       false,
	})
	save(t, out, "recovery-block-record.json", map[string]any{
		"classification":          "Recovery / block",
		"entry":                   "explicit recovery inspection required before any supported cleanup decision",
		"resumeExecuted":          false,
		"cleanupResumeAuthorized": false,
		"markerDeleted":           false,
		"stateReset":              false,
		"operationID":             operationID,
		"blocker":                 blocked.Blocker,
	})
	save(t, out, "quota-evidence.json", map[string]any{
		"before":                             mig02Quota(prepared),
		"clearing":                           mig02Quota(blockedSnapshot),
		"afterBlockedReadiness":              mig02Quota(blockedSnapshot),
		"quotaStatus":                        "completed",
		"observationsWindowsCyclesSnapshots": "preserved",
		"resetEvidence":                      "preserved",
		"bankedResets":                       "preserved",
		"cooldownState":                      "preserved",
	})
	authoritativeBefore := digestBytes(mig02JSON(t, prepared["usage_events"]))
	authoritativeAfter := digestBytes(mig02JSON(t, blockedSnapshot["usage_events"]))
	clearingSnapshotDigest := digestBytes(mig02JSON(t, clearingSnapshot))
	blockedReadinessSnapshotDigest := digestBytes(mig02JSON(t, blockedSnapshot))
	clearingUsageEventsDigest := digestBytes(mig02JSON(t, clearingSnapshot["usage_events"]))
	blockedReadinessUsageEventsDigest := digestBytes(mig02JSON(t, blockedSnapshot["usage_events"]))
	save(t, out, "authoritative-digest.json", map[string]any{
		"beforeUsageEventsDigest":                 authoritativeBefore,
		"afterUsageEventsDigest":                  authoritativeAfter,
		"usageEventRowsBefore":                    len(prepared["usage_events"]),
		"usageEventRowsAfter":                     len(blockedSnapshot["usage_events"]),
		"clearingSnapshotDigest":                  clearingSnapshotDigest,
		"blockedReadinessSnapshotDigest":          blockedReadinessSnapshotDigest,
		"clearingUsageEventsDigest":               clearingUsageEventsDigest,
		"blockedReadinessUsageEventsDigest":       blockedReadinessUsageEventsDigest,
		"noMutationDuringBlockedReadiness":        clearingSnapshotDigest == blockedReadinessSnapshotDigest,
		"usageEventsStableDuringBlockedReadiness": clearingUsageEventsDigest == blockedReadinessUsageEventsDigest,
		"sourceBundleBefore":                      source.SourceRevision,
		"sourceBundleAfter":                       clearingBundle.SourceRevision,
		"sourceBytesUnchanged":                    true,
	})
	save(t, out, "semantic-assertions.json", map[string]any{
		"SEM-07": map[string]any{
			"status":                         "runtime-pass",
			"quotaRowsEqual":                 true,
			"quotaMigrationEqual":            true,
			"quotaSettingsEqual":             true,
			"bankedResetEvidencePreserved":   true,
			"cooldownEvidencePreserved":      true,
			"cacheClearingDoesNotAlterQuota": true,
		},
		"clearing":                      map[string]any{"status": "persisted", "completedSteps": clearBatch.Processed, "remainingRows": clearingCounts["remainingRows"], "noImplicitCompletion": true},
		"historyPreserved":              len(prepared["usage_events"]) == len(blockedSnapshot["usage_events"]),
		"sourceBytesUnchanged":          true,
		"unrelatedSettingsUnchanged":    true,
		"noAutomaticDestructiveCleanup": true,
		"noAutomaticReset":              true,
		"normalReadinessBlocked":        true,
	})
	save(t, out, "execution-record.json", map[string]any{
		"executionCommit":                    codeIdentity(t),
		"sourceProducerCommit":               anchor.SourceProducerBuildCodeCommit,
		"sourceBundleDigest":                 source.SourceRevision,
		"clearingBundleDigest":               clearingBundle.SourceRevision,
		"operationID":                        operationID,
		"interruptionActuallyExecuted":       true,
		"recoveryInspectionActuallyExecuted": true,
		"recoveryResumeActuallyExecuted":     false,
		"normalStartupBlocked":               true,
		"controlledDirectCandidateReadiness": blocked.Status,
		"productionBridgeReadiness":          "unknown",
		"observedBridgeClassification":       blocked.Preflight.BridgeReadiness.Status,
		"applicableSEM":                      []string{"SEM-07"},
		"sqlite":                             checks,
	})
	save(t, out, "result.json", map[string]any{
		"caseID":                             mig03CaseID,
		"runtimeEvidence":                    "pass",
		"casePass":                           false,
		"independentAcceptance":              "pending",
		"SourceRelease":                      anchor.SourceRelease,
		"SourceCommit":                       anchor.SourceCommit,
		"PlanCommit":                         mig03PlanCommit,
		"SourceGeneratorCodeCommit":          anchor.GeneratorCodeCommit,
		"executionCommit":                    codeIdentity(t),
		"sourceGenerationRecordSHA256":       fileDigest(t, filepath.Join(root, "source-generation/record.json")),
		"inputBundleDigest":                  source.SourceRevision,
		"clearingBundleDigest":               clearingBundle.SourceRevision,
		"Cache":                              "clearing",
		"Quota":                              "completed",
		"sourceCacheRevision":                2,
		"migrationExecutorRevision":          3,
		"normalReadinessBlocked":             true,
		"recovery":                           "block",
		"recoveryClassification":             "Recovery / block",
		"recoveryResumeActuallyExecuted":     false,
		"controlledDirectCandidateReadiness": blocked.Status,
		"productionBridgeReadiness":          "unknown",
		"observedBridgeClassification":       blocked.Preflight.BridgeReadiness.Status,
		"applicableSEM":                      []string{"SEM-07"},
		"UpgradeAuthorized":                  false,
		"TrustedHopReceipt":                  false,
		"Phase4A07Complete":                  false,
		"Phase4AComplete":                    false,
	})
}

func mig03SeedDerivedRows(t *testing.T, ctx context.Context, db *sql.DB) map[string]any {
	t.Helper()
	nowMS := int64(1_800_000_000_000)
	rollups := usagerollup.New(db)
	account, err := rollups.CatchUpAccountHistory(ctx, 100, nowMS)
	must(t, err)
	dashboard, err := rollups.CatchUpDashboardHourly(ctx, 100, nowMS)
	must(t, err)
	aggregate, err := usageaggregaterepo.New(db).CatchUp(ctx, 100, nowMS)
	must(t, err)
	return map[string]any{
		"productionCalls":     []string{"usagerollup.CatchUpAccountHistory", "usagerollup.CatchUpDashboardHourly", "usageaggregate.CatchUp"},
		"accountHistory":      account,
		"dashboardHourly":     dashboard,
		"hourlyAggregate":     aggregate,
		"noSQLStateInjection": true,
	}
}

func mig03DerivedCounts(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, table := range []string{"usage_account_model_rollups", "usage_dashboard_hourly_rollups", "usage_hourly_aggregate_v1"} {
		counts[table] = scalar(t, db, "select count(*) from "+table)
	}
	counts["totalRows"] = counts["usage_account_model_rollups"] + counts["usage_dashboard_hourly_rollups"] + counts["usage_hourly_aggregate_v1"]
	return counts
}

func mig02JSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	must(t, err)
	return b
}
