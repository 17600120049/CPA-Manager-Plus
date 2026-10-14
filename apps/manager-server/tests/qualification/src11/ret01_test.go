package src11

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	ret01CaseID             = "RET-01"
	ret01PlanCommit         = "2719d8466951da1ea457bd1a00dca3fa6f09dc43"
	ret01SourceBundleDigest = "0e106589baef4148ee075ed77ce93bc71e83ca315a377311d62daeb9144236e3"
)

// RET-01 consumes the already accepted SRC-11 exact-source provenance as an
// immutable input anchor. It never regenerates the v1.14.4 source and never
// changes the sealed source bundle. The replay, assertions and output bundle
// are owned by this case and are intentionally separate from SRC-11 output.
func TestReplayRET01(t *testing.T) {
	root := os.Getenv("CPAMP_RET01_SOURCE_EVIDENCE")
	if root == "" {
		t.Skip("opt-in RET-01 exact-source retention/pricing replay")
	}
	ctx := context.Background()
	archive := envRequired(t, "CPAMP_RET01_ARCHIVE")
	anchor := revalidate(t, root, archive)
	if anchor.SourceRelease != "v1.14.4" || anchor.SourceCommit != sourceCommit ||
		anchor.GeneratedBundleSHA256 != ret01SourceBundleDigest ||
		anchor.SourceProducerBuildCodeCommit != sourceCommit {
		t.Fatal("RET-01 source anchor is not the accepted exact SRC-11 v1.14.4 provenance")
	}

	p, _ := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	output := envRequired(t, "CPAMP_RET01_REPLAY_OUTPUT")
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("RET-01 output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))
	sealed := inventory(t, filepath.Join(root, "bundle"))
	if sealed.SourceRevision != ret01SourceBundleDigest {
		t.Fatalf("RET-01 input bundle digest %s != accepted SRC-11 digest %s", sealed.SourceRevision, ret01SourceBundleDigest)
	}

	replayRoot := taskScratch(t, output, "replay-")
	restored, err := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), replayRoot)
	must(t, err)
	restoredBefore, err := maintenancebundle.Inspect(ctx, restored)
	must(t, err)
	sameInventory(t, sealed, restoredBefore)
	if restoredBefore.SourceRevision != ret01SourceBundleDigest {
		t.Fatal("RET-01 restored input bundle digest changed")
	}

	db := openDB(t, restored.DatabasePath, true)
	before := snapshot(t, db)
	checkFullRaw(t, db)
	if scalar(t, db, "select count(*) from usage_events") != 4 {
		t.Fatal("RET-01 input is not the accepted four-event full-raw fixture")
	}
	must(t, db.Close())

	key, created, err := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	must(t, err)
	if created {
		t.Fatal("RET-01 replay must use the accepted source data.key")
	}
	protector, err := security.NewProtector(key)
	must(t, err)
	st, err := store.Open(restored.DatabasePath, protector)
	must(t, err)

	// Execute the existing production migration/projection primitives in bounded
	// batches. No Upgrade API, hop receipt or writable admission is involved.
	migrationStates := []any{}
	for i := 0; i < 100; i++ {
		state, err := st.DiscoverUsageCacheAccounting(ctx)
		must(t, err)
		migrationStates = append(migrationStates, state)
		if state.Status == "completed" {
			break
		}
		batch, err := st.RunUsageCacheAccountingBatch(ctx, 2)
		must(t, err)
		migrationStates = append(migrationStates, batch)
		if i == 99 {
			t.Fatal("RET-01 cache migration did not complete")
		}
	}
	for i := 0; i < 100; i++ {
		batch, err := st.BackfillLegacyQuotaSnapshotsBatch(ctx, 2)
		must(t, err)
		migrationStates = append(migrationStates, batch)
		if batch.Completed {
			break
		}
		if i == 99 {
			t.Fatal("RET-01 quota migration did not complete")
		}
	}
	for i := 0; i < 100; i++ {
		changed, err := st.BackfillUsageResponseMetadata(ctx, 2)
		must(t, err)
		migrationStates = append(migrationStates, map[string]any{"responseMetadataChanged": changed})
		if changed == 0 {
			break
		}
		if i == 99 {
			t.Fatal("RET-01 response metadata migration did not complete")
		}
	}
	must(t, st.RunDerivedStartupMaintenance(ctx))
	for i := 0; i < 100; i++ {
		identity, err := st.CatchUpIdentityProjection(ctx, 2, p.ToMS)
		must(t, err)
		projection, err := st.CatchUpUsageMonitoringProjection(ctx, 2, p.ToMS)
		must(t, err)
		stats, err := st.CatchUpUsageMonitoringStats(ctx, 2, p.ToMS)
		must(t, err)
		metadata, err := st.CatchUpUsageMonitoringMetadata(ctx, 2, p.ToMS)
		must(t, err)
		legacyIdentity, err := st.CatchUpCodexLegacyIdentityEvidence(ctx, 2, p.ToMS)
		must(t, err)
		hourly, err := st.CatchUpUsageHourlyAggregate(ctx, 2, p.ToMS)
		must(t, err)
		pricing, err := st.CatchUpUsagePricing(ctx, 2, p.ToMS)
		must(t, err)
		_, err = st.CatchUpAccountHistoryRollups(ctx, 2, p.ToMS)
		must(t, err)
		migrationStates = append(migrationStates, map[string]any{
			"identity": identity, "monitoringProjection": projection,
			"monitoringStats": stats, "monitoringMetadata": metadata,
			"legacyIdentity": legacyIdentity, "hourly": hourly, "pricing": pricing,
		})
		if !identity.Pending && !projection.Pending && !stats.Pending && !metadata.Pending &&
			!legacyIdentity.Pending && !hourly.Pending && !pricing.Pending {
			break
		}
		if i == 99 {
			t.Fatal("RET-01 derived projections did not become ready")
		}
	}

	db = openDB(t, restored.DatabasePath, true)
	after := snapshot(t, db)
	assertPreserved(t, before, after)
	checkFullRaw(t, db)
	if scalar(t, db, "select count(*) from usage_events") != scalar(t, db, "select count(distinct event_hash) from usage_events") {
		t.Fatal("RET-01 event dedupe is not stable")
	}
	devin := query(t, db, `select input_tokens,cache_read_tokens,cache_creation_tokens,total_tokens,
 cache_input_mode,normalized_uncached_input_tokens,normalized_total_input_tokens,
 normalized_cache_read_tokens,normalized_cache_creation_tokens from usage_events
 where request_id='src11-devin-mixed'`)
	if len(devin) != 1 {
		t.Fatal("RET-01 Devin revision 2 evidence is missing")
	}
	for key, want := range map[string]any{
		"input_tokens": int64(1000), "cache_read_tokens": int64(900), "cache_creation_tokens": int64(50),
		"total_tokens": int64(1150), "cache_input_mode": "read_included_creation_separate",
		"normalized_uncached_input_tokens": int64(100), "normalized_total_input_tokens": int64(1050),
		"normalized_cache_read_tokens": int64(900), "normalized_cache_creation_tokens": int64(50),
	} {
		if devin[0][key] != want {
			t.Fatalf("RET-01 Devin revision 2 %s=%v != %v", key, devin[0][key], want)
		}
	}
	metadataRows := query(t, db, `select request_id,response_model,session_id,parent_session_id,
 response_metadata_json,generate,stream from usage_events order by rowid`)
	sessionRows := int64(0)
	for _, row := range metadataRows {
		if row["session_id"] == "src11-session" {
			sessionRows++
		}
	}
	if scalar(t, db, "select count(*) from usage_events where session_id='src11-session'") < 2 ||
		scalar(t, db, "select count(*) from usage_events where response_metadata_json is not null and response_metadata_json != ''") < 2 {
		t.Fatal("RET-01 request/response/session metadata fixture is vacuous")
	}
	quotaCounts := map[string]int64{}
	for _, table := range []string{"account_quota_snapshots", "account_quota_windows", "account_quota_window_activations", "account_quota_cycles", "quota_cooldowns"} {
		quotaCounts[table] = scalar(t, db, "select count(*) from "+table)
	}
	if quotaCounts["account_quota_snapshots"] < 2 || quotaCounts["quota_cooldowns"] < 1 {
		t.Fatal("RET-01 quota/reset evidence is vacuous")
	}
	archiveRuns := scalar(t, db, "select count(*) from usage_archive_runs")
	archiveSegments := scalar(t, db, "select count(*) from usage_archive_segments")
	rawEvents := scalar(t, db, "select count(*) from usage_events")
	if archiveRuns != 0 || archiveSegments != 0 || rawEvents != 4 {
		t.Fatalf("RET-01 full-raw retention state changed: runs=%d segments=%d raw=%d", archiveRuns, archiveSegments, rawEvents)
	}
	must(t, db.Close())

	var v1 map[string]monitoring.Response
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "v1-analytics.json")), &v1))
	svc := monitoring.New(st, true)
	v2 := map[string]monitoring.Response{}
	for _, name := range sortedModels(p) {
		response, err := svc.Analytics(ctx, analyticsRequest(p, name))
		must(t, err)
		if response.Summary == nil || response.Summary.TotalCalls != 1 || v1[name].Summary == nil {
			t.Fatalf("RET-01 historical pricing projection missing for %s", name)
		}
		assertClose(t, name, response.Summary.TotalCost, p.ExpectedUSD[name])
		assertClose(t, name, response.Summary.TotalCost, v1[name].Summary.TotalCost)
		v2[name] = response
	}

	prices, err := st.LoadModelPrices(ctx)
	must(t, err)
	free, ok := prices["fixture-free"]
	if !ok || free.Prompt != 0 || free.Completion != 0 || free.Cache != 0 || free.CacheRead != 0 || free.CacheCreation != 0 ||
		!free.PromptConfigured || !free.CompletionConfigured || !free.CacheReadConfigured || !free.CacheCreationConfigured {
		t.Fatalf("RET-01 explicit zero price lost its configured authority: %#v", free)
	}
	candidates := map[string]store.ModelPrice{}
	for name := range prices {
		candidates[name] = store.ModelPrice{Prompt: 999, Completion: 999, Source: "ret-01-sync-candidate"}
	}
	syncResult, err := st.UpsertSyncedModelPrices(ctx, candidates)
	must(t, err)
	pricesAfter, err := st.LoadModelPrices(ctx)
	must(t, err)
	oldPrices, err := json.Marshal(prices)
	must(t, err)
	newPrices, err := json.Marshal(pricesAfter)
	must(t, err)
	if !bytes.Equal(oldPrices, newPrices) || syncResult.Imported != 0 {
		t.Fatal("RET-01 migration/sync candidate overrode manual or explicit-zero pricing")
	}
	if v2["fixture-free"].Summary == nil || v2["fixture-free"].Summary.TotalCost != 0 {
		t.Fatal("RET-01 explicit zero cost was not preserved")
	}

	// Keep the full-raw RET-01 variant separate from RET-03. The raw-deleted
	// online-coverage assertion is recorded as not applicable here and is not
	// executed under this case, so no RET-03 evidence is smuggled into RET-01.
	retention := map[string]any{
		"variant":              "full raw",
		"archiveRuns":          archiveRuns,
		"archiveSegments":      archiveSegments,
		"rawEventCount":        rawEvents,
		"rawDeletedEventCount": 0,
		"onlineRawComplete":    true,
		"rawDeletedAssertion": map[string]any{
			"status": "not-applicable", "case": "RET-03", "sem": "SEM-09",
			"reason": "RET-01 exact Plan variant has no archive/raw deletion dependency",
		},
	}

	afterDB := openDB(t, restored.DatabasePath, false)
	sqliteAfter := sqliteChecks(t, afterDB, true)
	must(t, afterDB.Close())
	afterBundle, err := maintenancebundle.Inspect(ctx, restored)
	must(t, err)
	if afterBundle.SourceRevision == sealed.SourceRevision {
		t.Fatal("RET-01 after digest did not reflect the executed v2 migration")
	}

	// Recovery is real: create a complete task-owned backup and restore it to a
	// new root, then compare inventory, SQLite checks and authoritative history.
	must(t, st.Close())
	must(t, os.MkdirAll(filepath.Join(output, "backup-restore"), 0700))
	lock, err := processlock.Acquire(restored.DatabasePath)
	must(t, err)
	backup, err := maintenancebundle.Create(ctx, restored, filepath.Join(output, "backup-restore", "bundle"), lock)
	must(t, err)
	must(t, lock.Close())
	restoreRoot := taskScratch(t, output, "backup-restore-")
	restoredBackup, err := maintenancebundle.RestoreNew(ctx, filepath.Join(output, "backup-restore", "bundle"), restoreRoot)
	must(t, err)
	restoredBackupManifest, err := maintenancebundle.Inspect(ctx, restoredBackup)
	must(t, err)
	sameInventory(t, backup, restoredBackupManifest)
	restoredDB := openDB(t, restoredBackup.DatabasePath, false)
	restoredBackupSQLite := sqliteChecks(t, restoredDB, true)
	restoredSnapshot := snapshot(t, restoredDB)
	if ret01SnapshotDigest(after) != ret01SnapshotDigest(restoredSnapshot) {
		t.Fatal("RET-01 backup/restore changed authoritative history")
	}
	must(t, restoredDB.Close())
	backupEvidence := save(t, output, "backup-restore.json", map[string]any{
		"backupManifestDigest":   backup.SourceRevision,
		"restoredManifestDigest": restoredBackupManifest.SourceRevision,
		"inventoryMatched":       true,
		"authoritativeDigest":    ret01SnapshotDigest(after),
		"sqlite":                 restoredBackupSQLite,
	})

	code := codeIdentity(t)
	inputEvidence := save(t, output, "input-bundle.json", map[string]any{
		"caseID":              ret01CaseID,
		"inputBundleIdentity": ret01CaseID + "/input-bundle@" + sealed.SourceRevision,
		"provenanceRole":      "accepted SRC-11 exact-source anchor; no source regeneration or reinterpretation",
		"sourceAnchor": map[string]any{
			"caseID": "SRC-11", "acceptance": "accepted",
			"acceptanceEvidence": "seakee/cpamp-plan/main@" + ret01PlanCommit,
			"sourceRelease":      anchor.SourceRelease, "sourceCommit": anchor.SourceCommit,
			"sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation", "record.json")),
			"sourceBundleDigest":           sealed.SourceRevision,
			"producerArchiveSHA256":        anchor.SourceProducerArchiveSHA256,
			"producerExecutableSHA256":     anchor.SourceProducerExecutableSHA256,
		},
		"inputBundle": map[string]any{
			"manifestDigest":   sealed.SourceRevision,
			"inventory":        sealed.Entries,
			"materializedFrom": "immutable SRC-11 qualification asset; verified before extraction",
		},
	})
	assertionsEvidence := save(t, output, "semantic-assertions.json", map[string]any{
		"caseID":        ret01CaseID,
		"applicableSEM": []string{"SEM-01", "SEM-02", "SEM-03", "SEM-06", "SEM-08"},
		"assertions": map[string]any{
			"SEM-01": map[string]any{"status": "runtime-pass", "authoritativeBeforeDigest": ret01SnapshotDigest(before), "authoritativeAfterDigest": ret01SnapshotDigest(after), "authoritativeFieldsPreserved": true},
			"SEM-02": map[string]any{"status": "runtime-pass", "eventCount": rawEvents, "distinctEventHashes": rawEvents},
			"SEM-03": map[string]any{"status": "runtime-pass", "v1Costs": v1, "v2Costs": v2, "historicalProjectionStable": true},
			"SEM-06": map[string]any{"status": "runtime-pass", "metadataRows": len(metadataRows), "sessionRows": sessionRows, "metadataContinuity": true},
			"SEM-08": map[string]any{"status": "runtime-pass", "devinRevision": 2, "record": devin[0]},
		},
		"additionalRequiredAssertions": map[string]any{
			"retainedHistoricalPricing": map[string]any{"status": "runtime-pass", "manualPriceAuthority": true, "explicitZeroPreserved": true, "syncImported": syncResult.Imported},
			"quotaResetEvidence":        map[string]any{"status": "runtime-pass", "counts": quotaCounts},
			"retentionArchiveState":     retention,
		},
	})
	executionEvidence := save(t, output, "execution-record.json", map[string]any{
		"caseID":             ret01CaseID,
		"executionCommit":    code,
		"verifierCodeCommit": code,
		"sourceAnchorCaseID": "SRC-11",
		"steps": []map[string]any{
			{"step": "materialize-and-verify-source-anchor", "status": "pass"},
			{"step": "restore-input-to-new-root", "status": "pass", "manifestDigest": sealed.SourceRevision},
			{"step": "run-bounded-v2-migration-and-derived-rebuild", "status": "pass", "states": len(migrationStates)},
			{"step": "verify-pricing-accounting-metadata-quota", "status": "pass"},
			{"step": "create-and-restore-complete-backup", "status": "pass", "backupManifestDigest": backup.SourceRevision},
		},
		"authority": map[string]any{"UpgradeAuthorized": false, "TrustedHopReceipt": false},
	})
	result := map[string]any{
		"caseID":                ret01CaseID,
		"runtimeEvidence":       "pass",
		"casePass":              true,
		"independentAcceptance": "pending",
		"UpgradeAuthorized":     false,
		"TrustedHopReceipt":     false,
		"Phase4A07Complete":     false,
		"Phase4AComplete":       false,
		"remainingCases":        map[string]any{"count": 47, "status": "pending"},
		"sourceProvenance": map[string]any{
			"anchorCaseID": "SRC-11", "sourceRelease": anchor.SourceRelease, "sourceCommit": anchor.SourceCommit,
			"sourceBundleDigest": sealed.SourceRevision, "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation", "record.json")),
			"acceptance": "accepted", "acceptanceEvidence": "seakee/cpamp-plan/main@" + ret01PlanCommit,
		},
		"inputBundleIdentity":          ret01CaseID + "/input-bundle@" + sealed.SourceRevision,
		"inputBundleDigest":            sealed.SourceRevision,
		"beforeDigest":                 restoredBefore.SourceRevision,
		"afterDigest":                  afterBundle.SourceRevision,
		"authoritativeBeforeDigest":    ret01SnapshotDigest(before),
		"authoritativeAfterDigest":     ret01SnapshotDigest(after),
		"executionCommit":              code,
		"sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation", "record.json")),
		"applicableSEM":                []string{"SEM-01", "SEM-02", "SEM-03", "SEM-06", "SEM-08"},
		"evidence":                     map[string]any{"inputBundle": inputEvidence, "semanticAssertions": assertionsEvidence, "executionRecord": executionEvidence, "backupRestore": backupEvidence},
		"migrationStates":              migrationStates,
		"retentionArchiveState":        retention,
		"pricing":                      map[string]any{"v1Costs": v1, "v2Costs": v2, "manualPriceSync": syncResult, "explicitZero": true},
		"accounting":                   map[string]any{"devinRevision": 2, "devin": devin[0]},
		"metadata":                     map[string]any{"rows": len(metadataRows), "digest": ret01Digest(t, metadataRows)},
		"quota":                        quotaCounts,
		"SQLite":                       sqliteAfter,
		"backupRestore":                map[string]any{"manifestDigest": backup.SourceRevision, "restoredManifestDigest": restoredBackupManifest.SourceRevision, "inventoryMatched": true, "sqlite": restoredBackupSQLite},
	}
	save(t, output, "result.json", result)
	t.Log("RET-01 runtime case passed; independent acceptance remains pending")
}

func ret01SnapshotDigest(snapshot map[string][]map[string]any) string {
	return digestBytes(mustJSON(snapshotWithoutDerived(snapshot)))
}

func snapshotWithoutDerived(snapshot map[string][]map[string]any) map[string][]map[string]any {
	derived := map[string]bool{
		"cache_input_mode": true, "normalized_uncached_input_tokens": true,
		"normalized_total_input_tokens": true, "normalized_cache_read_tokens": true,
		"normalized_cache_creation_tokens": true,
	}
	copy := map[string][]map[string]any{}
	for table, rows := range snapshot {
		copy[table] = make([]map[string]any, len(rows))
		for i, row := range rows {
			copy[table][i] = map[string]any{}
			for column, value := range row {
				if table == "usage_events" && derived[column] {
					continue
				}
				copy[table][i][column] = value
			}
		}
	}
	return copy
}

func mustJSON(value any) []byte {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return b
}

func ret01Digest(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	must(t, err)
	return digestBytes(b)
}
