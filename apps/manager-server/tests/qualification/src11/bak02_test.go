package src11

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	managerconfig "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestReplayBAK02(t *testing.T) {
	root := os.Getenv("CPAMP_BAK02_SOURCE_EVIDENCE")
	if root == "" {
		t.Skip("opt-in BAK-02 matching-bundle replay")
	}
	ctx := context.Background()
	archive := envRequired(t, "CPAMP_BAK02_ARCHIVE")
	anchor := revalidateCase(t, root, archive, bak02CaseID, bak02PlanCommit, bak02Profile)
	if anchor.SourceRelease != "v1.14.4" || anchor.SourceCommit != sourceCommit {
		t.Fatal("BAK-02 source generation is not exact v1.14.4")
	}
	p, _ := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	output := envRequired(t, "CPAMP_BAK02_REPLAY_OUTPUT")
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("BAK-02 replay output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))

	sealed := inventory(t, filepath.Join(root, "bundle"))
	if sealed.SourceRevision != anchor.GeneratedBundleSHA256 {
		t.Fatalf("input bundle digest %s != generated digest %s", sealed.SourceRevision, anchor.GeneratedBundleSHA256)
	}
	inputCopy := filepath.Join(output, "source-input")
	copyTree(t, filepath.Join(root, "bundle"), filepath.Join(inputCopy, "bundle"))
	firstRoot := taskScratch(t, output, "restore-new-root-")
	first, err := maintenancebundle.RestoreNew(ctx, filepath.Join(inputCopy, "bundle"), firstRoot)
	must(t, err)
	// Delete the task-owned input copy before opening the restored root. All
	// subsequent reads therefore prove that RestoreNew produced a self-contained root.
	must(t, os.RemoveAll(inputCopy))

	firstInventory, err := maintenancebundle.Inspect(ctx, first)
	must(t, err)
	sameInventory(t, sealed, firstInventory)
	if firstInventory.SourceRevision != sealed.SourceRevision {
		t.Fatal("restored inventory digest changed")
	}
	if !strings.HasPrefix(first.DatabasePath, firstRoot+string(os.PathSeparator)) ||
		!strings.HasPrefix(first.DataKeyPath, firstRoot+string(os.PathSeparator)) ||
		!strings.HasPrefix(first.ConfigPath, firstRoot+string(os.PathSeparator)) ||
		!strings.HasPrefix(first.ArchivesPath, firstRoot+string(os.PathSeparator)) {
		t.Fatal("restored layout escaped the new root")
	}

	configBytes := read(t, first.ConfigPath)
	if digestBytes(configBytes) != sealed.Entries["config"].SHA256 {
		t.Fatal("restored config digest differs from matching bundle")
	}
	var configValue map[string]any
	must(t, json.Unmarshal(configBytes, &configValue))
	for key, want := range map[string]any{"dataDir": ".", "dbPath": "usage.sqlite", "dataKeyPath": "data.key", "collectorMode": "http", "pollIntervalMs": float64(100), "usageArchiveRetentionEnabled": false} {
		if !reflect.DeepEqual(configValue[key], want) {
			t.Fatalf("restored config %s=%v, want %v", key, configValue[key], want)
		}
	}
	resolved, err := managerconfig.ResolveInspectionLocations(first.ConfigPath, first.DatabasePath)
	must(t, err)
	if resolved.DataKeyPath != first.DataKeyPath || resolved.UsageArchiveDir != first.ArchivesPath {
		t.Fatalf("restored config resolved external paths: key=%s archive=%s", resolved.DataKeyPath, resolved.UsageArchiveDir)
	}

	db := openDB(t, first.DatabasePath, true)
	initial := snapshot(t, db)
	archiveInventory := checkRET02Archive(t, db, first.ArchivesPath)
	initialChecks := sqliteChecks(t, db, false)
	must(t, db.Close())

	key, created, err := security.LoadOrCreateDataKey("", first.DataKeyPath)
	must(t, err)
	if created {
		t.Fatal("restore generated a replacement data.key")
	}
	protector, err := security.NewProtector(key)
	must(t, err)
	st, err := store.Open(first.DatabasePath, protector)
	must(t, err)
	defer st.Close()
	setup, setupOK, err := st.LoadSetup(ctx)
	must(t, err)
	manager, managerOK, err := st.LoadManagerConfig(ctx)
	must(t, err)
	if !setupOK || !managerOK || setup.ManagementKey == "" || manager.CPAConnection.ManagementKey == "" {
		t.Fatal("restored matching data.key did not decrypt persisted settings")
	}
	queries := ret02Queries(t, ctx, st, first.DatabasePath, p, loadAnalytics(t, filepath.Join(root, "source-generation", "v1-analytics.json")))
	db = openDB(t, first.DatabasePath, true)
	after := snapshot(t, db)
	assertPreserved(t, initial, after)
	authoritativeBefore := ret01SnapshotDigest(initial)
	authoritativeAfter := ret01SnapshotDigest(after)
	if authoritativeBefore != authoritativeAfter {
		t.Fatal("historical authoritative digest changed after restored queries")
	}
	metadata := query(t, db, "select request_id,response_model,session_id,parent_session_id,stream,generate,access_token_sha256 from usage_events order by id")
	if len(metadata) != 4 || scalar(t, db, "select count(*) from usage_events where session_id='bak02-session'") < 2 {
		t.Fatal("restored request/session metadata is incomplete")
	}
	checks := sqliteChecks(t, db, true)
	must(t, db.Close())
	must(t, st.Close())

	lock, err := processlock.Acquire(first.DatabasePath)
	must(t, err)
	backupDir := filepath.Join(output, "backup-restore", "bundle")
	must(t, os.MkdirAll(filepath.Dir(backupDir), 0700))
	backup, err := maintenancebundle.Create(ctx, first, backupDir, lock)
	must(t, err)
	must(t, lock.Close())
	verified, err := maintenancebundle.Verify(ctx, backupDir)
	must(t, err)
	sameInventory(t, backup, verified)
	secondRoot := taskScratch(t, output, "second-restore-new-root-")
	second, err := maintenancebundle.RestoreNew(ctx, backupDir, secondRoot)
	must(t, err)
	secondInventory, err := maintenancebundle.Inspect(ctx, second)
	must(t, err)
	sameInventory(t, backup, secondInventory)
	secondDB := openDB(t, second.DatabasePath, true)
	secondSnapshot := snapshot(t, secondDB)
	assertPreserved(t, initial, secondSnapshot)
	secondArchive := checkRET02Archive(t, secondDB, second.ArchivesPath)
	secondChecks := sqliteChecks(t, secondDB, false)
	must(t, secondDB.Close())
	secondKey, secondCreated, err := security.LoadOrCreateDataKey("", second.DataKeyPath)
	must(t, err)
	if secondCreated || !reflect.DeepEqual(key, secondKey) {
		t.Fatal("second restore did not preserve the matching data.key")
	}

	inputHash := save(t, output, "input-bundle.json", map[string]any{"caseID": bak02CaseID, "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation", "record.json")), "sourceBundleDigest": sealed.SourceRevision, "inventory": sealed})
	manifestHash := save(t, output, "bundle-manifest.json", sealed)
	restoredHash := save(t, output, "restored-inventory.json", map[string]any{"first": firstInventory, "second": secondInventory, "firstMatchesInput": true, "secondMatchesBackup": true})
	databaseHash := save(t, output, "database-identity.json", map[string]any{"input": sealed.Entries["database"], "restored": firstInventory.Entries["database"], "secondRestored": secondInventory.Entries["database"], "digestMatch": true})
	keyHash := save(t, output, "key-identity.json", map[string]any{"input": sealed.Entries["data-key"], "restored": firstInventory.Entries["data-key"], "secondRestored": secondInventory.Entries["data-key"], "createdOnRestore": false, "decryptsSetup": true, "decryptsManagerConfig": true})
	configHash := save(t, output, "config-identity.json", map[string]any{"input": sealed.Entries["config"], "restored": firstInventory.Entries["config"], "resolvedDataKeyPath": first.DataKeyPath, "resolvedArchivePath": first.ArchivesPath, "defaultFallback": false})
	archiveHash := save(t, output, "archive-inventory.json", map[string]any{"first": archiveInventory, "second": secondArchive, "rootPresent": true, "manifestAndSegmentsRead": true, "digestAndSizeMatched": true, "archiveRefsMatchDatabase": true})
	restoreHash := save(t, output, "restore-record.json", map[string]any{"inputBundleVerified": true, "firstRestoreNew": true, "secondRestoreNew": true, "sourceInputRemovedBeforeQueries": true, "sourceRootDependency": false, "firstRoot": "task-owned/restore-new-root", "secondRoot": "task-owned/second-restore-new-root"})
	authoritativeHash := save(t, output, "authoritative-digest.json", map[string]any{"before": authoritativeBefore, "after": authoritativeAfter, "second": ret01SnapshotDigest(secondSnapshot), "equal": authoritativeBefore == authoritativeAfter && authoritativeAfter == ret01SnapshotDigest(secondSnapshot)})
	metadataHash := save(t, output, "metadata-continuity.json", map[string]any{"fields": []string{"request_id", "response_model", "session_id", "parent_session_id", "stream", "generate", "access_token_sha256"}, "rows": metadata, "archiveBacked": true, "preserved": true})
	sem := []string{"SEM-01", "SEM-06"}
	semanticHash := save(t, output, "semantic-assertions.json", map[string]any{"caseID": bak02CaseID, "applicableSEM": sem, "historicalQueries": queries, "assertions": map[string]any{"SEM-01": map[string]any{"status": "runtime-pass", "authoritativeBeforeDigest": authoritativeBefore, "authoritativeAfterDigest": authoritativeAfter, "eventIdentityPreserved": true, "tokenCostPricingPreserved": true, "nullUnknownSemanticsPreserved": true, "archiveBackedHistoryReadable": true}, "SEM-06": map[string]any{"status": "runtime-pass", "metadataContinuity": true, "requestResponseModelMetadata": true, "sessionParentContinuity": true, "streamGenerateContinuity": true, "accessTokenSHA256Continuity": true, "archiveMetadataLinkage": true}}})
	executionHash := save(t, output, "execution-record.json", map[string]any{"caseID": bak02CaseID, "executionCommit": codeIdentity(t), "steps": []string{"verify-exact-v1.14.4-provenance", "verify-matching-inventory", "restore-new-root", "remove-source-input", "decrypt-settings-with-restored-key", "read-archive-manifests-and-segments", "run-historical-query", "backup-verify", "second-restore-new-root"}, "sqlite": checks, "initialSQLite": initialChecks, "secondSQLite": secondChecks, "sourceInputRemovedBeforeQueries": true, "noSilentRegeneration": true})
	resultHash := save(t, output, "result.json", map[string]any{"caseID": bak02CaseID, "runtimeEvidence": "pass", "runtimeAssertions": "pass", "casePass": false, "independentAcceptance": "pending", "UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false, "remainingCases": map[string]any{"count": 40, "status": "pending"}, "sourceProvenance": anchor, "inputBundleDigest": sealed.SourceRevision, "beforeDigest": sealed.SourceRevision, "afterDigest": backup.SourceRevision, "authoritativeBeforeDigest": authoritativeBefore, "authoritativeAfterDigest": authoritativeAfter, "applicableSEM": sem, "backupRestore": "matching pass", "selfContainedRoot": true, "evidence": map[string]any{"inputBundle": inputHash, "bundleManifest": manifestHash, "restoredInventory": restoredHash, "databaseIdentity": databaseHash, "keyIdentity": keyHash, "configIdentity": configHash, "archiveInventory": archiveHash, "restoreRecord": restoreHash, "authoritativeDigest": authoritativeHash, "metadataContinuity": metadataHash, "semanticAssertions": semanticHash, "executionRecord": executionHash}})
	_ = resultHash
}

func loadAnalytics(t *testing.T, path string) map[string]monitoring.Response {
	t.Helper()
	var value map[string]monitoring.Response
	must(t, json.Unmarshal(read(t, path), &value))
	return value
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	info, err := os.Stat(source)
	must(t, err)
	must(t, os.MkdirAll(destination, info.Mode().Perm()))
	entries, err := os.ReadDir(source)
	must(t, err)
	for _, entry := range entries {
		src := filepath.Join(source, entry.Name())
		dst := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			copyTree(t, src, dst)
			continue
		}
		data := read(t, src)
		must(t, os.WriteFile(dst, data, 0600))
	}
}
