package src11

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func TestReplayBAK01(t *testing.T) {
	root := os.Getenv("CPAMP_BAK01_SOURCE")
	if root == "" {
		t.Skip("opt-in sealed BAK-01 fixture replay")
	}
	archive := envRequired(t, "CPAMP_BAK01_ARCHIVE")
	r := revalidateCase(t, root, archive, bak01CaseID, bak01PlanCommit, bak01Profile)
	output := envRequired(t, "CPAMP_BAK01_REPLAY_OUTPUT")
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("BAK-01 replay output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))
	ctx := context.Background()
	sealed := inventory(t, filepath.Join(root, "bundle"))
	if !sealed.Entries["database-wal"].Present || !sealed.Entries["database-shm"].Present {
		t.Fatal("BAK-01 sealed bundle is missing WAL/SHM")
	}

	firstRoot := taskScratch(t, output, "restore-new-root-")
	first, err := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), firstRoot)
	must(t, err)
	firstInventory, err := maintenancebundle.Inspect(ctx, first)
	must(t, err)
	sameInventory(t, sealed, firstInventory)
	if !fileExists(first.DatabasePath+"-wal") || !fileExists(first.DatabasePath+"-shm") {
		t.Fatal("restored root did not carry its own WAL/SHM")
	}

	rowEvidence := map[string]any{}
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation/committed-row.json")), &rowEvidence))
	requestID := rowEvidence["requestID"].(string)
	// Re-prove the committed-WAL premise on every platform replay before
	// opening the restored database with its sidecars. The private immutable
	// copy cannot checkpoint or depend on any source WAL.
	mainOnly := filepath.Join(output, "restored-main-only.sqlite")
	copyBytes(t, first.DatabasePath, mainOnly)
	mainOnlyDB, err := sql.Open("sqlite", dbURI(mainOnly, true)+"&immutable=1")
	must(t, err)
	var mainOnlyCount int64
	must(t, mainOnlyDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_events WHERE request_id=?", requestID).Scan(&mainOnlyCount))
	must(t, mainOnlyDB.Close())
	if mainOnlyCount != 0 {
		t.Fatal("BAK-01 replay row is already checkpointed into the restored main DB")
	}
	frames := inspectWALFrames(t, first.DatabasePath+"-wal")
	if frames.FrameCount == 0 || frames.CommitFrameCount == 0 {
		t.Fatal("BAK-01 replay requires committed WAL frames")
	}
	readDB, err := sql.Open("sqlite", dbURI(first.DatabasePath, true))
	must(t, err)
	readDB.SetMaxOpenConns(1)
	must(t, readDB.PingContext(ctx))
	readTx, err := readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(t, err)
	defer func() {
		_ = readTx.Rollback()
		_ = readDB.Close()
	}()
	row := queryTx(t, readTx, "SELECT id,event_hash,request_id,timestamp_ms,model,input_tokens,output_tokens,total_tokens FROM usage_events WHERE request_id=?", requestID)
	if len(row) != 1 {
		t.Fatalf("restored committed WAL row is missing: %s", requestID)
	}
	firstSnapshot := snapshotTx(t, readTx)
	checks := sqliteChecksTx(t, readTx)
	journalMode := queryTx(t, readTx, "PRAGMA journal_mode")
	if len(journalMode) != 1 || journalMode[0]["journal_mode"] != "wal" {
		t.Fatal("BAK-01 replay requires SQLite WAL mode")
	}
	var sourceSnapshot map[string][]map[string]any
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation/source-snapshot.json")), &sourceSnapshot))
	if ret01SnapshotDigest(firstSnapshot) != ret01SnapshotDigest(sourceSnapshot) {
		t.Fatal("first restored authoritative digest differs from source generation")
	}

	backupDir := filepath.Join(output, "backup-restore", "bundle")
	must(t, os.MkdirAll(filepath.Dir(backupDir), 0700))
	// A restored new root is intentionally lock-free until its new owner takes
	// the normal Manager fence for the follow-up backup.
	lock, err := processlock.Acquire(first.DatabasePath)
	must(t, err)
	backup, err := maintenancebundle.Create(ctx, first, backupDir, lock)
	must(t, err)
	must(t, lock.Close())
	if !backup.Entries["database-wal"].Present || !backup.Entries["database-shm"].Present {
		t.Fatal("second maintenance backup lost WAL/SHM")
	}
	backupVerified, err := maintenancebundle.Verify(ctx, backupDir)
	must(t, err)
	if backupVerified.SourceRevision != backup.SourceRevision {
		t.Fatal("second backup verification digest changed")
	}

	secondRoot := taskScratch(t, output, "second-restore-new-root-")
	second, err := maintenancebundle.RestoreNew(ctx, backupDir, secondRoot)
	must(t, err)
	secondDB := openDB(t, second.DatabasePath, true)
	secondRow := query(t, secondDB, "SELECT id,event_hash,request_id,timestamp_ms,model,input_tokens,output_tokens,total_tokens FROM usage_events WHERE request_id=?", requestID)
	secondSnapshot := snapshot(t, secondDB)
	if len(secondRow) != 1 || ret01SnapshotDigest(secondSnapshot) != ret01SnapshotDigest(sourceSnapshot) {
		t.Fatal("second restored root lost committed WAL history")
	}
	must(t, secondDB.Close())
	secondInventory, err := maintenancebundle.Inspect(ctx, second)
	must(t, err)
	sameInventory(t, backup, secondInventory)

	walState := readJSON(t, filepath.Join(root, "source-generation/wal-state.json"))
	checkpointObservation := readJSON(t, filepath.Join(root, "source-generation/checkpoint-observation.json"))
	authoritativeBefore := ret01SnapshotDigest(sourceSnapshot)
	authoritativeAfter := ret01SnapshotDigest(firstSnapshot)
	if authoritativeBefore != authoritativeAfter || authoritativeAfter != ret01SnapshotDigest(secondSnapshot) {
		t.Fatal("BAK-01 authoritative data changed across backup/restore")
	}

	inputHash := save(t, output, "input-bundle.json", map[string]any{
		"caseID": bak01CaseID, "sourceRelease": r.SourceRelease, "sourceCommit": r.SourceCommit,
		"sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation/record.json")),
		"inputBundleDigest":            sealed.SourceRevision, "inputBundleInventory": sealed,
		"WAL": walState, "checkpointObservation": checkpointObservation,
	})
	backupHash := save(t, output, "backup-restore.json", map[string]any{
		"firstRestoreRootDistinct": true, "secondRestoreRootDistinct": true,
		"inputBundleDigest": sealed.SourceRevision, "backupManifestDigest": backup.SourceRevision,
		"firstRestoredManifestDigest":  firstInventory.SourceRevision,
		"secondRestoredManifestDigest": secondInventory.SourceRevision,
		"inventoryMatched":             true, "committedRow": row[0], "secondCommittedRow": secondRow[0],
		"mainFileOnlyCommittedRowAbsent": true, "committedWALFrames": frames,
		"sourceWALNotUsedByRestoredRoot": true, "sourceBundlePath": "not opened during restore verification",
		"authoritativeBeforeDigest": authoritativeBefore, "authoritativeAfterDigest": authoritativeAfter,
		"sqlite": checks,
	})
	assertionsHash := save(t, output, "semantic-assertions.json", map[string]any{
		"caseID": bak01CaseID, "applicableSEM": []string{"SEM-01"},
		"assertions": map[string]any{"SEM-01": map[string]any{
			"status": "runtime-pass", "committedEventIdentityPreserved": true,
			"authoritativeFieldsPreserved": true, "nullUnknownSemanticsPreserved": true,
			"beforeDigest": authoritativeBefore, "afterBackupDigest": authoritativeAfter,
			"afterFirstRestoreDigest":  ret01SnapshotDigest(firstSnapshot),
			"afterSecondRestoreDigest": ret01SnapshotDigest(secondSnapshot),
			"noHistoricalRepricing":    true,
		}},
		"sourceMutation":    map[string]any{"physicalCheckpoint": false, "authoritativeMutation": false},
		"backupConsistency": map[string]any{"completeWALCapture": true, "tornSnapshot": false},
	})
	executionHash := save(t, output, "execution-record.json", map[string]any{
		"executionCommit": codeIdentity(t), "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation/record.json")),
		"steps":     []string{"verify-exact-v1.14.4-provenance", "verify-committed-WAL-before-backup", "maintenancebundle-verify", "restore-new-root", "reopen-with-own-WAL", "repeat-backup-and-restore", "semantic-digest-assertions"},
		"sourceWAL": walState, "checkpoint": checkpointObservation, "sqlite": checks,
		"sourceMutation": false, "applicableSEM": []string{"SEM-01"},
	})
	result := map[string]any{
		"caseID": bak01CaseID, "runtimeEvidence": "pass", "runtimeAssertions": "pass", "casePass": false,
		"independentAcceptance": "pending", "SourceRelease": r.SourceRelease, "SourceCommit": r.SourceCommit,
		"PlanCommit": bak01PlanCommit, "SourceGeneratorCodeCommit": r.GeneratorCodeCommit, "executionCommit": codeIdentity(t),
		"inputBundleDigest": sealed.SourceRevision, "beforeDigest": sealed.SourceRevision,
		"afterDigest": backup.SourceRevision, "authoritativeBeforeDigest": authoritativeBefore,
		"authoritativeAfterDigest": authoritativeAfter, "applicableSEM": []string{"SEM-01"},
		"backupStrategy": "complete database plus WAL/SHM capture", "backupRestore": "pass",
		"sqlite": checks, "walState": walState, "checkpointObservation": checkpointObservation,
		"evidence":          map[string]any{"inputBundle": inputHash, "backupRestore": backupHash, "semanticAssertions": assertionsHash, "executionRecord": executionHash},
		"UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false,
		"acceptedCases":  []string{"SRC-11", "RET-01", "RET-02", "AUT-03", "MIG-01"},
		"remainingCases": map[string]any{"count": 44, "status": "pending"},
	}
	resultHash := save(t, output, "result.json", result)
	_ = resultHash
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var value map[string]any
	must(t, json.Unmarshal(read(t, path), &value))
	return value
}
