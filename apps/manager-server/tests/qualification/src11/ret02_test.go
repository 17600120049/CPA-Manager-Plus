package src11

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagepricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageprojection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	usagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	ret02CaseID     = "RET-02"
	ret02PlanCommit = "7eefdbf50d3596890a75e97a400dc1f5286e6498"
	ret02Profile    = "ret-02-archived-raw-retained-v1"
)

var ret02GenerationFiles = []string{"archive-flow.json", "archive-readiness.json", "pre-archive-snapshot.json", "archive-inventory.json", "raw-event-inventory.json", "v1-archived-analytics.json"}

func generateRET02Archive(t *testing.T, base, live, generation string, p profile, costs map[string]monitoring.Response) {
	// Producer workers are asynchronous. Observe their real readiness; never
	// synthesize a watermark or mutate projection state to permit verification.
	var readiness usagesvc.MaintenanceStatus
	wait(t, func() bool {
		must(t, json.Unmarshal(httpCall(t, "GET", base, "/v0/management/usage/maintenance", nil), &readiness))
		return readiness.RawEventCount == 4 && readiness.MigrationCoverage.Complete && readiness.HourlyAggregateCoverage.Complete
	})
	save(t, generation, "archive-readiness.json", readiness)
	db := openDB(t, filepath.Join(live, "usage.sqlite"), true)
	before := snapshot(t, db)
	checkFullRaw(t, db)
	must(t, db.Close())
	save(t, generation, "pre-archive-snapshot.json", before)
	createdBytes := httpCall(t, "POST", base, "/v0/management/usage/archives", map[string]any{"cutoff_timestamp_ms": p.ToMS})
	var created usagesvc.ArchiveStatusSummary
	must(t, json.Unmarshal(createdBytes, &created))
	if created.Run.ID == "" || created.Run.Status != "previewed" {
		t.Fatal("producer did not create archive")
	}
	path := "/v0/management/usage/archives/" + created.Run.ID
	resumedBytes := httpCall(t, "POST", base, path+"/resume?wait=true&expected_stage=archiving", nil)
	verifiedBytes := httpCall(t, "POST", base, path+"/verify?wait=true", nil)
	var resumed, verified usagesvc.ArchiveStatusSummary
	must(t, json.Unmarshal(resumedBytes, &resumed))
	must(t, json.Unmarshal(verifiedBytes, &verified))
	if resumed.Run.Status != "archived" || verified.Run.Status != "verified" || verified.Run.DeletedEventCount != 0 || verified.Run.DeleteStartedAtMS != 0 {
		t.Fatal("archive producer crossed no-deletion boundary")
	}
	save(t, generation, "archive-flow.json", map[string]any{"producer": "published exact v1.14.4", "automaticRetentionEnabled": false, "actions": []string{"create", "resume?wait=true&expected_stage=archiving", "verify?wait=true"}, "create": created, "resume": resumed, "verify": verified, "deleteCalled": false})
	archivedCosts := map[string]monitoring.Response{}
	for _, name := range sortedModels(p) {
		var response monitoring.Response
		must(t, json.Unmarshal(httpCall(t, "POST", base, "/v0/management/monitoring/analytics", analyticsRequest(p, name)), &response))
		if response.Summary == nil || response.Summary.TotalCalls != 1 || response.Coverage != nil {
			t.Fatal("raw+archive producer query double counted or claims raw deletion")
		}
		assertClose(t, name, response.Summary.TotalCost, costs[name].Summary.TotalCost)
		archivedCosts[name] = response
	}
	save(t, generation, "v1-archived-analytics.json", archivedCosts)
	db = openDB(t, filepath.Join(live, "usage.sqlite"), true)
	assertPreserved(t, before, snapshot(t, db))
	state := checkRET02Archive(t, db, filepath.Join(live, "usage-archives"))
	save(t, generation, "archive-inventory.json", state)
	save(t, generation, "raw-event-inventory.json", map[string]any{"count": 4, "authoritativeDigest": ret01SnapshotDigest(before), "events": query(t, db, "select id,event_hash,request_id,timestamp_ms from usage_events order by id"), "deleted": 0})
	must(t, db.Close())
}

// Inspect every actual gzip record independently of the producer's VerifyArchive.
// SQL is read-only; no archive metadata, raw history or fixture is fabricated.
func checkRET02Archive(t *testing.T, db *sql.DB, archives string) map[string]any {
	t.Helper()
	runs := query(t, db, "select * from usage_archive_runs order by id")
	raw := query(t, db, "select * from usage_events order by id")
	refs := query(t, db, "select * from usage_archive_event_refs order by raw_event_id")
	segments := query(t, db, "select * from usage_archive_segments order by sequence")
	if len(runs) != 1 || len(raw) != 4 || len(refs) != 4 || len(segments) == 0 {
		t.Fatal("RET-02 nonvacuous raw/archive inventories incomplete")
	}
	run := runs[0]
	if run["status"] != "verified" || run["deleted_event_count"] != int64(0) || run["last_deleted_event_id"] != int64(0) || run["delete_started_at_ms"] != nil || run["completed_at_ms"] != nil || run["event_count"] != int64(4) || run["archived_event_count"] != int64(4) {
		t.Fatalf("RET-02 archive not verified with all raw retained: %v", run)
	}
	if scalar(t, db, "select coalesce(sum(deleted_event_count),0) from usage_archive_deleted_coverage_daily") != 0 {
		t.Fatal("raw deleted coverage appeared")
	}
	manifestPath := filepath.Join(archives, filepath.FromSlash(run["manifest_file"].(string)))
	if fileDigest(t, manifestPath) != run["manifest_sha256"] {
		t.Fatal("manifest digest mismatch")
	}
	var manifest usagesvc.ArchiveManifest
	must(t, json.Unmarshal(read(t, manifestPath), &manifest))
	if manifest.RunID != run["id"] || manifest.EventCount != 4 || manifest.EventHashDigest != run["archive_digest"] || len(manifest.Segments) != len(segments) {
		t.Fatal("manifest/run mismatch")
	}
	byID := map[int64]map[string]any{}
	for _, row := range raw {
		byID[row["id"].(int64)] = row
	}
	overall := sha256.New()
	represented := []map[string]any{}
	files := map[string]any{"manifest": map[string]any{"path": run["manifest_file"], "sha256": fileDigest(t, manifestPath)}}
	for i, segment := range segments {
		ms := manifest.Segments[i]
		if segment["status"] != "verified" || ms.FileName != segment["file_name"] || ms.Sequence != i+1 || ms.EventCount != segment["event_count"] || ms.ContentSHA256 != segment["content_sha256"] || ms.EventHashDigest != segment["event_hash_digest"] || ms.FirstEventID != segment["first_event_id"] || ms.LastEventID != segment["last_event_id"] || ms.CompressedBytes != segment["compressed_bytes"] || ms.UncompressedBytes != segment["uncompressed_bytes"] {
			t.Fatal("segment metadata mismatch")
		}
		path := filepath.Join(archives, filepath.FromSlash(ms.FileName))
		if fileDigest(t, path) != ms.ContentSHA256 {
			t.Fatal("segment content digest mismatch")
		}
		f, e := os.Open(path)
		must(t, e)
		info, e := f.Stat()
		must(t, e)
		if info.Size() != ms.CompressedBytes {
			t.Fatal("segment compressed size mismatch")
		}
		gz, e := gzip.NewReader(f)
		must(t, e)
		scanner := bufio.NewScanner(gz)
		scanner.Buffer(make([]byte, 64*1024), 16<<20)
		segmentDigest := sha256.New()
		var n, size int64
		for scanner.Scan() {
			var record map[string]any
			must(t, json.Unmarshal(scanner.Bytes(), &record))
			id := int64(record["_cpamp_archive_event_id"].(float64))
			row, ok := byID[id]
			if !ok {
				t.Fatal("archive identity missing/duplicate")
			}
			if record["_cpamp_archive_schema_version"] != float64(manifest.SchemaVersion) {
				t.Fatal("archive schema mismatch")
			}
			assertRET02Record(t, row, record)
			if n == 0 && id != ms.FirstEventID || id < ms.FirstEventID || id > ms.LastEventID {
				t.Fatal("archive identity bounds mismatch")
			}
			if row["timestamp_ms"].(int64) < ms.MinTimestampMS || row["timestamp_ms"].(int64) > ms.MaxTimestampMS {
				t.Fatal("archive timestamp bounds mismatch")
			}
			ref := refs[len(represented)]
			if ref["raw_event_id"] != id || ref["event_hash"] != row["event_hash"] || ref["run_id"] != run["id"] || ref["segment_sequence"] != int64(ms.Sequence) || ref["timestamp_ms"] != row["timestamp_ms"] || ref["raw_deleted_at_ms"] != nil {
				t.Fatal("archive/raw reference mismatch or deletion")
			}
			identity := fmt.Sprintf("%d%c%s\n", id, 0, row["event_hash"])
			_, e = io.WriteString(segmentDigest, identity)
			must(t, e)
			_, e = io.WriteString(overall, identity)
			must(t, e)
			represented = append(represented, record)
			delete(byID, id)
			n++
			size += int64(len(scanner.Bytes()) + 1)
		}
		must(t, scanner.Err())
		must(t, gz.Close())
		must(t, f.Close())
		if n != ms.EventCount || size != ms.UncompressedBytes || hex.EncodeToString(segmentDigest.Sum(nil)) != ms.EventHashDigest {
			t.Fatal("archive segment record/digest mismatch")
		}
		files[ms.FileName] = map[string]any{"sha256": ms.ContentSHA256, "events": n, "size": info.Size(), "eventHashDigest": ms.EventHashDigest}
	}
	if len(byID) != 0 || len(represented) != 4 || hex.EncodeToString(overall.Sum(nil)) != manifest.EventHashDigest {
		t.Fatal("archive coverage does not equal raw authoritative history")
	}
	// Full metadata comes from real producer input, including nonvacuous NULLs.
	truth := map[any]bool{}
	for _, row := range raw {
		truth[row["stream"]] = true
		if row["response_model"] == "" || row["access_token_sha256"] != strings.Repeat("a", 64) || row["method"] != "POST" || row["path"] != "/v1/responses" {
			t.Fatal("RET-02 metadata profile not persisted")
		}
	}
	if !truth[nil] || !truth[int64(0)] || !truth[int64(1)] {
		t.Fatal("metadata true/false/unknown coverage missing")
	}
	return map[string]any{"variant": "archived + no raw deletion", "run": run, "segments": segments, "manifest": manifest, "files": files, "refs": refs, "archiveRecords": represented, "rawEventCount": 4, "archiveEventCount": 4, "rawDeletedEventCount": 0, "rawComplete": true, "authoritativeArchiveRawEqual": true}
}

func assertRET02Record(t *testing.T, row, record map[string]any) {
	t.Helper()
	// An independent field list, not the SQL producer expression. Nullable fields
	// may be absent in JSON merge-patch; absence preserves unknown, never zero.
	fields := strings.Fields("request_id event_hash timestamp_ms timestamp provider executor_type model endpoint method path client_ip x_forwarded_for user_agent auth_type auth_index source source_hash api_key_hash account_snapshot auth_label_snapshot auth_file_snapshot generate stream auth_provider_snapshot auth_account_id_snapshot auth_project_id_snapshot auth_snapshot_at_ms requested_model resolved_model response_model session_id parent_session_id access_token_sha256 reasoning_effort service_tier request_service_tier response_service_tier cache_input_mode input_tokens output_tokens reasoning_tokens cached_tokens cache_tokens cache_read_tokens cache_creation_tokens normalized_uncached_input_tokens normalized_total_input_tokens normalized_cache_read_tokens normalized_cache_creation_tokens total_tokens latency_ms ttft_ms failed fail_status_code fail_summary response_metadata_json header_quota_recover_at_ms header_quota_used_percent header_quota_plan_type header_error_kind header_error_code header_trace_id fail_body raw_json created_at_ms")
	for _, field := range fields {
		want, exists := row[field]
		if !exists {
			t.Fatalf("raw authoritative column missing: %s", field)
		}
		got := record[field]
		if field == "generate" || field == "stream" {
			if want != nil {
				want = want.(int64) != 0
			}
		}
		var normalized any
		must(t, json.Unmarshal(mustJSON(want), &normalized))
		if !reflect.DeepEqual(normalized, got) {
			t.Fatalf("archive/raw field %s differs: %v / %v", field, want, got)
		}
	}
	var metadata any
	if text, ok := row["response_metadata_json"].(string); ok && text != "" {
		must(t, json.Unmarshal([]byte(text), &metadata))
	}
	if !reflect.DeepEqual(metadata, record["response_metadata"]) {
		t.Fatal("archive response metadata lost")
	}
}

func ret02Queries(t *testing.T, ctx context.Context, st *store.Store, dbPath string, p profile, v1 map[string]monitoring.Response) map[string]any {
	t.Helper()
	costs := map[string]any{}
	for _, name := range append(sortedModels(p), "") {
		responses := []monitoring.Response{}
		for _, aggregate := range []bool{false, true} {
			r, e := monitoring.New(st, aggregate).Analytics(ctx, analyticsRequest(p, name))
			must(t, e)
			wantCalls := int64(1)
			var wantCost float64
			if name == "" {
				wantCalls = 4
				for _, price := range p.ExpectedUSD {
					wantCost += price
				}
			} else {
				wantCost = v1[name].Summary.TotalCost
			}
			if r.Summary == nil || r.Summary.TotalCalls != wantCalls || r.Coverage != nil {
				t.Fatalf("RET-02 query double counted or raw-deleted fallback: model=%s aggregate=%v", name, aggregate)
			}
			assertClose(t, name, r.Summary.TotalCost, wantCost)
			responses = append(responses, r)
		}
		if responses[0].Summary.TotalTokens != responses[1].Summary.TotalTokens {
			t.Fatal("raw/aggregate tokens drift")
		}
		costs[name] = responses
	}
	coverage, e := st.UsageArchives.RawCoverage(ctx, p.FromMS, p.ToMS)
	must(t, e)
	if coverage.RawDeletedEventCount != 0 {
		t.Fatal("raw deletion semantics appeared")
	}
	// This calls the real archive-aware retained pricing source. Its projection
	// arm and raw tail must be disjoint even though all raw rows still exist.
	db := openDB(t, dbPath, true)
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(t, e)
	rows, e := usagepricing.New(db).LoadHourlyRowsFromEventsTx(ctx, tx, usagepricing.HourlyFilter{FromMS: p.FromMS, ToMS: p.ToMS, IncludeFailed: true})
	must(t, e)
	must(t, tx.Rollback())
	var calls, tokens int64
	for _, row := range rows {
		calls += row.Calls
		tokens += row.TotalTokens
	}
	if calls != 4 || tokens != scalar(t, db, "select sum(total_tokens) from usage_events") {
		t.Fatal("retained pricing/raw coexistence duplicated accounting")
	}
	if scalar(t, db, "select count(*) from "+usageprojection.EventTable) != 4 {
		t.Fatal("retained projection incomplete")
	}
	return map[string]any{"costs": costs, "rawDeletedCoverage": coverage, "retainedPricingRows": rows, "retainedPricingCalls": calls, "retainedPricingTokens": tokens}
}

func TestReplayRET02(t *testing.T) {
	root := os.Getenv("CPAMP_RET02_SOURCE_EVIDENCE")
	if root == "" {
		t.Skip("opt-in RET-02 exact-source archive/raw replay")
	}
	ctx := context.Background()
	anchor := revalidateCase(t, root, envRequired(t, "CPAMP_RET02_ARCHIVE"), ret02CaseID, ret02PlanCommit, ret02Profile)
	p, _ := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	output := envRequired(t, "CPAMP_RET02_REPLAY_OUTPUT")
	if _, e := os.Lstat(output); !os.IsNotExist(e) {
		t.Fatal("RET-02 output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))
	sealed := inventory(t, filepath.Join(root, "bundle"))
	restored, e := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), taskScratch(t, output, "replay-"))
	must(t, e)
	original, e := maintenancebundle.Inspect(ctx, restored)
	must(t, e)
	sameInventory(t, sealed, original)
	db := openDB(t, restored.DatabasePath, true)
	before := snapshot(t, db)
	var generated, preArchive map[string][]map[string]any
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "source-snapshot.json")), &generated))
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "pre-archive-snapshot.json")), &preArchive))
	if ret01SnapshotDigest(before) != ret01SnapshotDigest(generated) || ret01SnapshotDigest(before) != ret01SnapshotDigest(preArchive) {
		t.Fatal("archive creation/restoration rewrote authoritative data")
	}
	archiveBefore := checkRET02Archive(t, db, restored.ArchivesPath)
	must(t, db.Close())
	key, created, e := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	must(t, e)
	if created {
		t.Fatal("source key missing")
	}
	protector, e := security.NewProtector(key)
	must(t, e)
	st, e := store.Open(restored.DatabasePath, protector)
	must(t, e)
	defer st.Close()
	states := qualificationRetentionCatchUp(t, ctx, st, p)
	var v1 map[string]monitoring.Response
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "v1-analytics.json")), &v1))
	first := ret02Queries(t, ctx, st, restored.DatabasePath, p, v1)
	// Re-import actual archive bytes into their matching live history, twice.
	// Both runs must dedupe every record, leaving identities and raw evidence intact.
	var archiveBytes bytes.Buffer
	for _, v := range archiveBefore["archiveRecords"].([]map[string]any) {
		archiveBytes.Write(mustJSON(v))
		archiveBytes.WriteByte('\n')
	}
	imports := []any{}
	for i := 0; i < 2; i++ {
		result, _, e := usagesvc.New(st).Import(ctx, bytes.NewReader(archiveBytes.Bytes()))
		must(t, e)
		if result.Added != 0 || result.Skipped != 4 || result.Failed != 0 {
			t.Fatalf("archive replay double counts: %+v", result)
		}
		imports = append(imports, result)
		qualificationRetentionCatchUp(t, ctx, st, p)
	}
	afterQueries := ret02Queries(t, ctx, st, restored.DatabasePath, p, v1)
	db = openDB(t, restored.DatabasePath, true)
	after := snapshot(t, db)
	assertPreserved(t, before, after)
	archiveAfter := checkRET02Archive(t, db, restored.ArchivesPath)
	if ret01Digest(t, archiveBefore) != ret01Digest(t, archiveAfter) {
		t.Fatal("archive evidence changed during v2 replay")
	}
	must(t, db.Close())
	must(t, st.Close())
	db = openDB(t, restored.DatabasePath, false)
	checks := sqliteChecks(t, db, true)
	must(t, db.Close())
	afterBundle, e := maintenancebundle.Inspect(ctx, restored)
	must(t, e)
	lock, e := processlock.AcquireExisting(restored.DatabasePath)
	must(t, e)
	backupDir := filepath.Join(output, "backup-restore", "bundle")
	backup, e := maintenancebundle.Create(ctx, restored, backupDir, lock)
	must(t, e)
	must(t, lock.Close())
	recovered, e := maintenancebundle.RestoreNew(ctx, backupDir, taskScratch(t, output, "recovery-"))
	must(t, e)
	recoveredManifest, e := maintenancebundle.Inspect(ctx, recovered)
	must(t, e)
	sameInventory(t, backup, recoveredManifest)
	db = openDB(t, recovered.DatabasePath, false)
	recoveryChecks := sqliteChecks(t, db, true)
	assertPreserved(t, after, snapshot(t, db))
	recoveryArchive := checkRET02Archive(t, db, recovered.ArchivesPath)
	if ret01Digest(t, recoveryArchive) != ret01Digest(t, archiveAfter) {
		t.Fatal("backup restore lost archive/raw correspondence")
	}
	must(t, db.Close())
	recoveredKey, created, e := security.LoadOrCreateDataKey("", recovered.DataKeyPath)
	must(t, e)
	if created {
		t.Fatal("recovery created replacement key")
	}
	recoveredProtector, e := security.NewProtector(recoveredKey)
	must(t, e)
	recoveredStore, e := store.Open(recovered.DatabasePath, recoveredProtector)
	must(t, e)
	recoveryQueries := ret02Queries(t, ctx, recoveredStore, recovered.DatabasePath, p, v1)
	must(t, recoveredStore.Close())
	backupHash := save(t, output, "backup-restore.json", map[string]any{"newRoot": true, "backupManifestDigest": backup.SourceRevision, "restoredManifestDigest": recoveredManifest.SourceRevision, "inventoryMatched": true, "archiveRawParity": true, "sqlite": recoveryChecks, "queries": recoveryQueries, "authoritativeDigest": ret01SnapshotDigest(after)})
	rawHash := save(t, output, "raw-event-inventory.json", map[string]any{"before": before["usage_events"], "after": after["usage_events"], "count": 4, "rawDeletedEventCount": 0})
	archiveHash := save(t, output, "archive-inventory.json", archiveAfter)
	inputHash := save(t, output, "input-bundle.json", map[string]any{"caseID": ret02CaseID, "inputBundleIdentity": ret02CaseID + "/input-bundle@" + sealed.SourceRevision, "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation", "record.json")), "sourceBundleDigest": sealed.SourceRevision, "inventory": sealed})
	sem := []string{"SEM-01", "SEM-02", "SEM-03", "SEM-06"}
	assertionsHash := save(t, output, "semantic-assertions.json", map[string]any{"caseID": ret02CaseID, "applicableSEM": sem, "assertions": map[string]any{
		"SEM-01": map[string]any{"status": "runtime-pass", "authoritativeBeforeDigest": ret01SnapshotDigest(before), "authoritativeAfterDigest": ret01SnapshotDigest(after), "rawPreserved": true, "archiveRawEqual": true},
		"SEM-02": map[string]any{"status": "runtime-pass", "archiveImports": imports, "before": first, "after": afterQueries, "noDoubleCounting": true},
		"SEM-03": map[string]any{"status": "runtime-pass", "v1Costs": v1, "v2Queries": afterQueries, "historicalPricingPreserved": true},
		"SEM-06": map[string]any{"status": "runtime-pass", "metadataContinuity": true, "trueFalseUnknown": true, "archiveRoundTrip": true, "accessTokenSHA256": true},
	}})
	executionHash := save(t, output, "execution-record.json", map[string]any{"caseID": ret02CaseID, "executionCommit": codeIdentity(t), "sourceGenerationCaseID": ret02CaseID, "migrationStates": states, "archiveImports": imports, "newRootRecovery": true, "deleteCalled": false})
	save(t, output, "result.json", map[string]any{"caseID": ret02CaseID, "runtimeEvidence": "pass", "casePass": false, "independentAcceptance": "pending", "UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false, "remainingCases": map[string]any{"count": 46, "status": "pending"}, "sourceProvenance": anchor, "inputBundleIdentity": ret02CaseID + "/input-bundle@" + sealed.SourceRevision, "inputBundleDigest": sealed.SourceRevision, "beforeDigest": original.SourceRevision, "afterDigest": afterBundle.SourceRevision, "authoritativeBeforeDigest": ret01SnapshotDigest(before), "authoritativeAfterDigest": ret01SnapshotDigest(after), "executionCommit": codeIdentity(t), "applicableSEM": sem, "archiveRawParity": true, "rawDeletedEventCount": 0, "backupRestore": "pass", "SQLite": checks, "evidence": map[string]any{"inputBundle": inputHash, "semanticAssertions": assertionsHash, "executionRecord": executionHash, "backupRestore": backupHash, "rawEventInventory": rawHash, "archiveInventory": archiveHash}})
}
