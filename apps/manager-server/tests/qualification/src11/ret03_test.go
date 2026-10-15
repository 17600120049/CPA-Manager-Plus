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
	ret03CaseID     = "RET-03"
	ret03PlanCommit = "06a47aaefff1629b3d556f8a49e0544648bfd4bc"
	ret03Profile    = "ret-03-archived-raw-deleted-v1"
)

var ret03GenerationFiles = []string{
	"archive-flow.json", "archive-readiness.json", "pre-archive-snapshot.json",
	"pre-delete-snapshot.json", "post-delete-snapshot.json", "archive-inventory.json",
	"retained-projection-snapshot.json", "raw-event-inventory.json", "v1-archived-analytics.json",
}

// generateRET03Archive drives the published v1.14.4 process through the real
// archive lifecycle. The destructive step is the production DeleteArchive path;
// this function never edits the source SQLite database directly.
func generateRET03Archive(t *testing.T, base, live, generation string, p profile, costs map[string]monitoring.Response) {
	t.Helper()
	var readiness usagesvc.MaintenanceStatus
	wait(t, func() bool {
		must(t, json.Unmarshal(httpCall(t, "GET", base, "/v0/management/usage/maintenance", nil), &readiness))
		return readiness.RawEventCount == 4 && readiness.MigrationCoverage.Complete && readiness.HourlyAggregateCoverage.Complete
	})
	save(t, generation, "archive-readiness.json", readiness)
	db := openDB(t, filepath.Join(live, "usage.sqlite"), true)
	preArchive := snapshot(t, db)
	checkFullRaw(t, db)
	must(t, db.Close())
	save(t, generation, "pre-archive-snapshot.json", preArchive)

	createdBytes := httpCall(t, "POST", base, "/v0/management/usage/archives", map[string]any{"cutoff_timestamp_ms": p.ToMS})
	var created usagesvc.ArchiveStatusSummary
	must(t, json.Unmarshal(createdBytes, &created))
	if created.Run.ID == "" || created.Run.Status != "previewed" {
		t.Fatalf("RET-03 archive create did not return previewed run: %#v", created.Run)
	}
	path := "/v0/management/usage/archives/" + created.Run.ID
	var archived, verified, deleted usagesvc.ArchiveStatusSummary
	must(t, json.Unmarshal(httpCall(t, "POST", base, path+"/resume?wait=true&expected_stage=archiving", nil), &archived))
	must(t, json.Unmarshal(httpCall(t, "POST", base, path+"/verify?wait=true", nil), &verified))
	if archived.Run.Status != "archived" || verified.Run.Status != "verified" || verified.Run.DeletedEventCount != 0 {
		t.Fatalf("RET-03 archive crossed the pre-delete boundary: archived=%#v verified=%#v", archived.Run, verified.Run)
	}

	db = openDB(t, filepath.Join(live, "usage.sqlite"), true)
	preDelete := snapshot(t, db)
	checkRET03PreDelete(t, db)
	must(t, db.Close())
	save(t, generation, "pre-delete-snapshot.json", preDelete)
	must(t, json.Unmarshal(httpCall(t, "POST", base, path+"/delete?wait=true", nil), &deleted))
	if deleted.Run.Status != "completed" || deleted.Run.DeletedEventCount != 4 {
		t.Fatalf("RET-03 raw deletion did not complete for all events: %#v", deleted.Run)
	}
	save(t, generation, "archive-flow.json", map[string]any{
		"producer": "published exact v1.14.4", "automaticRetentionEnabled": false,
		"actions": []string{"create", "resume?wait=true&expected_stage=archiving", "verify?wait=true", "delete?wait=true"},
		"create":  created, "resume": archived, "verify": verified, "delete": deleted,
		"deleteCalled": true, "deletionMethod": "production DeleteArchive/DeleteBatch",
	})

	archivedCosts := map[string]monitoring.Response{}
	for _, name := range append(sortedModels(p), "") {
		var response monitoring.Response
		must(t, json.Unmarshal(httpCall(t, "POST", base, "/v0/management/monitoring/analytics", analyticsRequest(p, name)), &response))
		if response.Summary == nil || response.Summary.TotalCalls != map[bool]int64{true: 4, false: 1}[name == ""] || response.Coverage == nil || response.Coverage.RawComplete || response.Coverage.RawDeletedEventCount != 4 {
			t.Fatalf("RET-03 post-delete query coverage is not honest: model=%q response=%s", name, responseBytes(response))
		}
		want := 0.0
		if name == "" {
			for _, price := range p.ExpectedUSD {
				want += price
			}
		} else {
			want = costs[name].Summary.TotalCost
		}
		assertClose(t, name, response.Summary.TotalCost, want)
		archivedCosts[name] = response
	}
	save(t, generation, "v1-archived-analytics.json", archivedCosts)

	db = openDB(t, filepath.Join(live, "usage.sqlite"), true)
	postDelete := snapshot(t, db)
	state := checkRET03Archive(t, db, filepath.Join(live, "usage-archives"))
	assertRET03Preserved(t, preDelete, postDelete, state)
	save(t, generation, "post-delete-snapshot.json", postDelete)
	save(t, generation, "archive-inventory.json", state)
	save(t, generation, "retained-projection-snapshot.json", state["retainedProjection"])
	save(t, generation, "raw-event-inventory.json", map[string]any{
		"beforeRawCount": 4, "deletedCount": 4, "afterRawCount": len(query(t, db, "select id from usage_events")),
		"rawEventIDsBefore":      query(t, db, "select raw_event_id from usage_archive_event_refs order by raw_event_id"),
		"onlineRawEventIDsAfter": query(t, db, "select id from usage_events order by id"),
		"archiveEventCount":      state["archiveEventCount"], "retainedProjectionCount": state["retainedProjectionCount"],
	})
	must(t, db.Close())
}

// checkRET03PreDelete proves the archive lifecycle has reached the deletion
// boundary while the four raw rows are still present. Unlike SRC-11's
// full-raw assertion, RET-03 expects a verified archive to already exist.
func checkRET03PreDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	if scalar(t, db, "select count(*) from usage_events") != 4 ||
		scalar(t, db, "select count(*) from usage_archive_event_refs") != 4 ||
		scalar(t, db, "select count(*) from usage_archive_segments") == 0 ||
		scalar(t, db, "select count(*) from usage_archive_runs where status='verified' and deleted_event_count=0") != 1 ||
		scalar(t, db, "select count(*) from usage_archive_event_refs where raw_deleted_at_ms is not null") != 0 {
		t.Fatal("RET-03 pre-delete boundary is not archived with all raw rows intact")
	}
}

func responseBytes(response monitoring.Response) string {
	b, _ := json.Marshal(response)
	return string(b)
}

// checkRET03Archive independently inspects the physical SQLite state and every
// archive byte. It intentionally cannot pass if a marker was changed while raw
// rows remained present, or if the retained projection is incomplete.
func checkRET03Archive(t *testing.T, db *sql.DB, archives string) map[string]any {
	t.Helper()
	runs := query(t, db, "select * from usage_archive_runs order by id")
	raw := query(t, db, "select * from usage_events order by id")
	refs := query(t, db, "select * from usage_archive_event_refs order by raw_event_id")
	segments := query(t, db, "select * from usage_archive_segments order by sequence")
	projections := query(t, db, "select * from "+usageprojection.EventTable+" order by event_id")
	if len(runs) != 1 || len(raw) != 0 || len(refs) != 4 || len(segments) == 0 || len(projections) != 4 {
		t.Fatalf("RET-03 physical deletion/retained coverage is incomplete: raw=%d refs=%d segments=%d projections=%d", len(raw), len(refs), len(segments), len(projections))
	}
	run := runs[0]
	if run["status"] != "completed" || run["event_count"] != int64(4) || run["archived_event_count"] != int64(4) || run["deleted_event_count"] != int64(4) || run["delete_started_at_ms"] == nil || run["completed_at_ms"] == nil {
		t.Fatalf("RET-03 archive run is not completed with four deleted rows: %v", run)
	}
	if scalar(t, db, "select coalesce(sum(deleted_event_count),0) from usage_archive_deleted_coverage_daily") != 4 {
		t.Fatal("RET-03 deleted coverage ledger is missing rows")
	}
	manifestPath := filepath.Join(archives, filepath.FromSlash(run["manifest_file"].(string)))
	if fileDigest(t, manifestPath) != run["manifest_sha256"] {
		t.Fatal("RET-03 manifest digest mismatch")
	}
	var manifest usagesvc.ArchiveManifest
	must(t, json.Unmarshal(read(t, manifestPath), &manifest))
	if manifest.RunID != run["id"] || manifest.EventCount != 4 || manifest.EventHashDigest != run["archive_digest"] || len(manifest.Segments) != len(segments) {
		t.Fatal("RET-03 manifest/run mismatch")
	}
	projectionByID := map[int64]map[string]any{}
	for _, row := range projections {
		projectionByID[row["event_id"].(int64)] = row
	}
	refByID := map[int64]map[string]any{}
	for _, ref := range refs {
		id := ref["raw_event_id"].(int64)
		if ref["run_id"] != run["id"] || ref["raw_deleted_at_ms"] == nil || ref["event_hash"] == "" {
			t.Fatalf("RET-03 deleted ref is incomplete: %v", ref)
		}
		refByID[id] = ref
	}
	overall := sha256.New()
	represented := make([]map[string]any, 0, 4)
	seen := map[int64]bool{}
	files := map[string]any{"manifest": map[string]any{"path": run["manifest_file"], "sha256": fileDigest(t, manifestPath)}}
	for i, segment := range segments {
		ms := manifest.Segments[i]
		if segment["status"] != "verified" || ms.FileName != segment["file_name"] || ms.Sequence != i+1 || ms.EventCount != segment["event_count"] || ms.ContentSHA256 != segment["content_sha256"] || ms.EventHashDigest != segment["event_hash_digest"] {
			t.Fatal("RET-03 segment metadata mismatch")
		}
		path := filepath.Join(archives, filepath.FromSlash(ms.FileName))
		if fileDigest(t, path) != ms.ContentSHA256 {
			t.Fatal("RET-03 segment content digest mismatch")
		}
		f, e := os.Open(path)
		must(t, e)
		info, e := f.Stat()
		must(t, e)
		gz, e := gzip.NewReader(f)
		must(t, e)
		scanner := bufio.NewScanner(gz)
		scanner.Buffer(make([]byte, 64*1024), 16<<20)
		segmentDigest := sha256.New()
		var n, size int64
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			var record map[string]any
			must(t, json.Unmarshal(line, &record))
			id := int64(record["_cpamp_archive_event_id"].(float64))
			hash, ok := record["event_hash"].(string)
			if !ok || hash == "" || seen[id] || refByID[id] == nil || refByID[id]["event_hash"] != hash || projectionByID[id] == nil {
				t.Fatalf("RET-03 archive identity is not backed by deleted ref/projection: id=%d", id)
			}
			seen[id] = true
			projection := projectionByID[id]
			for _, field := range []string{"timestamp_ms", "model", "requested_model", "resolved_model", "provider", "executor_type", "auth_index", "account_snapshot", "api_key_hash", "source", "source_hash", "auth_file_snapshot", "auth_label_snapshot", "auth_provider_snapshot", "auth_account_id_snapshot", "auth_project_id_snapshot", "input_tokens", "output_tokens", "reasoning_tokens", "cached_tokens", "cache_tokens", "cache_read_tokens", "cache_creation_tokens", "normalized_total_input_tokens", "total_tokens", "latency_ms", "failed", "reasoning_effort", "service_tier", "header_error_code", "header_error_kind", "header_quota_plan_type", "header_trace_id"} {
				if !sameArchiveValue(record[field], projection[field]) {
					t.Fatalf("RET-03 retained projection field %s differs for event %d: archive=%v projection=%v", field, id, record[field], projection[field])
				}
			}
			identity := fmt.Sprintf("%d%c%s\n", id, 0, hash)
			_, e = io.WriteString(segmentDigest, identity)
			must(t, e)
			_, e = io.WriteString(overall, identity)
			must(t, e)
			represented = append(represented, record)
			n++
			size += int64(len(line) + 1)
		}
		must(t, scanner.Err())
		must(t, gz.Close())
		must(t, f.Close())
		if n != ms.EventCount || size != ms.UncompressedBytes || hex.EncodeToString(segmentDigest.Sum(nil)) != ms.EventHashDigest || info.Size() != ms.CompressedBytes {
			t.Fatal("RET-03 archive segment record/digest mismatch")
		}
		files[ms.FileName] = map[string]any{"sha256": ms.ContentSHA256, "events": n, "size": info.Size(), "eventHashDigest": ms.EventHashDigest}
	}
	if len(represented) != 4 || hex.EncodeToString(overall.Sum(nil)) != manifest.EventHashDigest {
		t.Fatal("RET-03 archive does not cover exactly four deleted events")
	}
	return map[string]any{
		"variant": "archived + raw physically deleted", "run": run, "segments": segments, "manifest": manifest,
		"files": files, "refs": refs, "archiveRecords": represented, "rawEventCount": 0,
		"preDeleteRawCount": 4, "archiveEventCount": len(represented), "rawDeletedEventCount": 4,
		"retainedProjectionCount": len(projections), "rawComplete": false,
		"authoritativeArchiveProjectionComplete": true,
		"retainedProjection":                     projections,
	}
}

func sameArchiveValue(archive, projection any) bool {
	if af, ok := archive.(float64); ok {
		switch pv := projection.(type) {
		case int64:
			return af == float64(pv)
		case int:
			return af == float64(pv)
		case float64:
			return af == pv
		}
	}
	if archive == nil && (projection == nil || projection == "") {
		return true
	}
	return reflect.DeepEqual(archive, projection)
}

// Bind archived authority to the sealed pre-delete raw observation. Reuse
// RET-02's complete metadata comparison, including nullable stream/generate,
// response metadata, request/session linkage and access-token hashes.
func assertRET03Preserved(t *testing.T, pre, post map[string][]map[string]any, state map[string]any) {
	t.Helper()
	rows := pre["usage_events"]
	records := state["archiveRecords"].([]map[string]any)
	if len(rows) != 4 || len(records) != len(rows) {
		t.Fatal("RET-03 pre-delete authority does not cover every archived event")
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		// JSON snapshots decode SQL integers as float64; RET-02's comparator
		// expects SQL int64 for the two nullable boolean columns only.
		copy := map[string]any{}
		for field, value := range row {
			copy[field] = value
		}
		for _, field := range []string{"generate", "stream"} {
			if value, ok := copy[field].(float64); ok {
				copy[field] = int64(value)
			}
		}
		byID[string(mustJSON(row["id"]))] = copy
	}
	for _, record := range records {
		row := byID[string(mustJSON(record["_cpamp_archive_event_id"]))]
		if row == nil {
			t.Fatal("RET-03 archive event is absent from pre-delete authority")
		}
		assertRET02Record(t, row, record)
	}
	// Deletion must preserve pricing authority and its context/service tiers.
	for _, table := range []string{"model_prices", "model_price_context_tiers", "model_price_service_tiers"} {
		if !bytes.Equal(mustJSON(pre[table]), mustJSON(post[table])) {
			t.Fatalf("RET-03 historical pricing authority changed: %s", table)
		}
	}
}

func ret03Queries(t *testing.T, ctx context.Context, st *store.Store, dbPath string, p profile, v1 map[string]monitoring.Response) map[string]any {
	t.Helper()
	queries := map[string]any{}
	for _, name := range append(sortedModels(p), "") {
		responses := make([]monitoring.Response, 0, 2)
		for _, aggregate := range []bool{false, true} {
			response, e := monitoring.New(st, aggregate).Analytics(ctx, analyticsRequest(p, name))
			must(t, e)
			wantCalls := int64(1)
			wantCost := 0.0
			if name == "" {
				wantCalls = 4
				for _, price := range p.ExpectedUSD {
					wantCost += price
				}
			} else {
				wantCost = v1[name].Summary.TotalCost
			}
			if response.Summary == nil || response.Summary.TotalCalls != wantCalls || response.Coverage == nil || response.Coverage.RawComplete || response.Coverage.RawDeletedEventCount != 4 {
				t.Fatalf("RET-03 supported query has dishonest raw coverage: model=%q aggregate=%v response=%s", name, aggregate, responseBytes(response))
			}
			assertClose(t, name, response.Summary.TotalCost, wantCost)
			responses = append(responses, response)
		}
		if responses[0].Summary.TotalTokens != responses[1].Summary.TotalTokens {
			t.Fatal("RET-03 raw/retained query token totals drifted")
		}
		queries[name] = responses
	}
	rawOnly := monitoring.Request{FromMS: p.FromMS, ToMS: p.ToMS, NowMS: p.ToMS, TimeZone: "UTC", Include: monitoring.Include{EventsPage: &monitoring.EventsPage{Limit: 10}}}
	rawOnlyResponse, e := monitoring.New(st, false).Analytics(ctx, rawOnly)
	must(t, e)
	if rawOnlyResponse.Coverage == nil || rawOnlyResponse.Coverage.RawComplete || !contains(rawOnlyResponse.Coverage.FidelityLimitations, "event_details_require_raw_events") {
		t.Fatalf("RET-03 raw-only query was not explicitly degraded: %s", responseBytes(rawOnlyResponse))
	}
	queries["rawOnlyQuery"] = rawOnlyResponse

	db := openDB(t, dbPath, true)
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(t, e)
	must(t, usageprojection.VerifyRetainedEdgeTx(ctx, tx, p.FromMS, p.ToMS))
	rows, e := usagepricing.New(db).LoadHourlyRowsFromEventsTx(ctx, tx, usagepricing.HourlyFilter{FromMS: p.FromMS, ToMS: p.ToMS, IncludeFailed: true})
	must(t, e)
	must(t, tx.Rollback())
	var calls, tokens int64
	for _, row := range rows {
		calls += row.Calls
		tokens += row.TotalTokens
	}
	if calls != 4 || tokens != scalar(t, db, "select sum(total_tokens) from "+usageprojection.EventTable) {
		t.Fatal("RET-03 retained pricing source is incomplete or duplicated")
	}
	queries["retainedPricingCalls"] = calls
	queries["retainedPricingTokens"] = tokens
	queries["retainedProjectionCount"] = scalar(t, db, "select count(*) from "+usageprojection.EventTable)
	return queries
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestReplayRET03(t *testing.T) {
	root := os.Getenv("CPAMP_RET03_SOURCE_EVIDENCE")
	if root == "" {
		t.Skip("opt-in RET-03 exact-source archive/raw-deleted replay")
	}
	ctx := context.Background()
	anchor := revalidateCase(t, root, envRequired(t, "CPAMP_RET03_ARCHIVE"), ret03CaseID, ret03PlanCommit, ret03Profile)
	p, _ := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	output := envRequired(t, "CPAMP_RET03_REPLAY_OUTPUT")
	if _, e := os.Lstat(output); !os.IsNotExist(e) {
		t.Fatal("RET-03 output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))
	sealed := inventory(t, filepath.Join(root, "bundle"))
	restored, e := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), taskScratch(t, output, "replay-"))
	must(t, e)
	restoredManifest, e := maintenancebundle.Inspect(ctx, restored)
	must(t, e)
	sameInventory(t, sealed, restoredManifest)
	db := openDB(t, restored.DatabasePath, true)
	before := snapshot(t, db)
	if scalar(t, db, "select count(*) from usage_events") != 0 {
		t.Fatal("RET-03 restored source regenerated raw rows")
	}
	archiveState := checkRET03Archive(t, db, restored.ArchivesPath)
	preDelete := loadSnapshotFile(t, filepath.Join(root, "source-generation", "pre-delete-snapshot.json"))
	assertRET03Preserved(t, preDelete, before, archiveState)
	if ret01SnapshotDigest(before) != ret01SnapshotDigest(loadSnapshotFile(t, filepath.Join(root, "source-generation", "post-delete-snapshot.json"))) {
		t.Fatal("RET-03 restored post-delete authoritative snapshot differs from generation")
	}
	must(t, db.Close())

	key, created, e := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	must(t, e)
	if created {
		t.Fatal("RET-03 replay created a replacement data key")
	}
	protector, e := security.NewProtector(key)
	must(t, e)
	st, e := store.Open(restored.DatabasePath, protector)
	must(t, e)
	defer st.Close()
	states := ret03RetentionReopen(t, ctx, st, restored.DatabasePath)
	var v1 map[string]monitoring.Response
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "v1-analytics.json")), &v1))
	firstQueries := ret03Queries(t, ctx, st, restored.DatabasePath, p, v1)

	archiveRecords := archiveState["archiveRecords"].([]map[string]any)
	var archiveBytes bytes.Buffer
	for _, record := range archiveRecords {
		archiveBytes.Write(mustJSON(record))
		archiveBytes.WriteByte('\n')
	}
	imports := make([]any, 0, 2)
	for i := 0; i < 2; i++ {
		result, _, e := usagesvc.New(st).Import(ctx, bytes.NewReader(archiveBytes.Bytes()))
		must(t, e)
		if result.Added != 0 || result.Skipped != 4 || result.Failed != 0 {
			t.Fatalf("RET-03 archive replay changed raw state or double-counted: %+v", result)
		}
		imports = append(imports, result)
	}
	afterQueries := ret03Queries(t, ctx, st, restored.DatabasePath, p, v1)
	db = openDB(t, restored.DatabasePath, true)
	if scalar(t, db, "select count(*) from usage_events") != 0 {
		t.Fatal("RET-03 archive replay resurrected raw rows")
	}
	after := snapshot(t, db)
	archiveAfter := checkRET03Archive(t, db, restored.ArchivesPath)
	assertRET03Preserved(t, preDelete, after, archiveAfter)
	must(t, db.Close())
	if ret01Digest(t, archiveState) != ret01Digest(t, archiveAfter) {
		t.Fatal("RET-03 archive evidence changed during replay")
	}
	must(t, st.Close())

	backupDir := filepath.Join(output, "backup-restore", "bundle")
	must(t, os.MkdirAll(filepath.Dir(backupDir), 0700))
	lock, e := processlock.Acquire(restored.DatabasePath)
	must(t, e)
	backup, e := maintenancebundle.Create(ctx, restored, backupDir, lock)
	must(t, e)
	must(t, lock.Close())
	recoveryRoot := taskScratch(t, output, "recovery-")
	recovered, e := maintenancebundle.RestoreNew(ctx, backupDir, recoveryRoot)
	must(t, e)
	recoveredManifest, e := maintenancebundle.Inspect(ctx, recovered)
	must(t, e)
	sameInventory(t, backup, recoveredManifest)
	db = openDB(t, recovered.DatabasePath, false)
	recoveryState := checkRET03Archive(t, db, recovered.ArchivesPath)
	assertRET03Preserved(t, preDelete, snapshot(t, db), recoveryState)
	if scalar(t, db, "select count(*) from usage_events") != 0 {
		t.Fatal("RET-03 new-root restore regenerated raw rows")
	}
	must(t, db.Close())
	recoveredKey, created, e := security.LoadOrCreateDataKey("", recovered.DataKeyPath)
	must(t, e)
	if created {
		t.Fatal("RET-03 new-root restore created replacement key")
	}
	recoveredProtector, e := security.NewProtector(recoveredKey)
	must(t, e)
	recoveredStore, e := store.Open(recovered.DatabasePath, recoveredProtector)
	must(t, e)
	recoveryQueries := ret03Queries(t, ctx, recoveredStore, recovered.DatabasePath, p, v1)
	must(t, recoveredStore.Close())

	backupHash := save(t, output, "backup-restore.json", map[string]any{
		"newRoot": true, "backupManifestDigest": backup.SourceRevision, "restoredManifestDigest": recoveredManifest.SourceRevision,
		"inventoryMatched": true, "archiveRawParity": true, "rawStillDeleted": true,
		"rawEventCount": 0, "archiveEventCount": recoveryState["archiveEventCount"], "retainedProjectionCount": recoveryState["retainedProjectionCount"],
		"queries": recoveryQueries,
	})
	rawHash := save(t, output, "raw-event-inventory.json", map[string]any{
		"beforeRawCount": 4, "deletedCount": 4, "afterRawCount": 0, "archiveEventCount": 4, "retainedProjectionCount": 4,
		"sourceRawRowsAbsent": true, "archiveRefsPresent": true,
	})
	archiveHash := save(t, output, "archive-inventory.json", archiveAfter)
	projectionHash := save(t, output, "retained-projection-snapshot.json", archiveAfter["retainedProjection"])
	inputHash := save(t, output, "input-bundle.json", map[string]any{"caseID": ret03CaseID, "inputBundleIdentity": ret03CaseID + "/input-bundle@" + sealed.SourceRevision, "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation/record.json")), "sourceBundleDigest": sealed.SourceRevision, "inventory": sealed})
	sem := []string{"SEM-01", "SEM-02", "SEM-03", "SEM-06", "SEM-09"}
	assertionsHash := save(t, output, "semantic-assertions.json", map[string]any{"caseID": ret03CaseID, "applicableSEM": sem, "assertions": map[string]any{
		"SEM-01": map[string]any{"status": "runtime-pass", "rawRowsPhysicallyAbsent": true, "archiveProjectionAuthority": true, "unknownAndNullSemanticsPreserved": true},
		"SEM-02": map[string]any{"status": "runtime-pass", "archiveImports": imports, "before": firstQueries, "after": afterQueries, "noDoubleCounting": true, "rawRemainsDeleted": true},
		"SEM-03": map[string]any{"status": "runtime-pass", "v1Costs": v1, "v2Queries": afterQueries, "historicalPricingPreserved": true, "historicalPricingAuthorityPreserved": true},
		"SEM-06": map[string]any{"status": "runtime-pass", "metadataContinuity": true, "archiveProjectionIdentity": true, "accessTokenSHA256": true},
		"SEM-09": map[string]any{"status": "runtime-pass", "rawDeleted": true, "archiveComplete": true, "retainedProjectionComplete": true, "onlineRawComplete": false, "rawOnlyQueryDegraded": true, "supportedHistoryQuery": true},
	}})
	executionHash := save(t, output, "execution-record.json", map[string]any{"caseID": ret03CaseID, "executionCommit": codeIdentity(t), "sourceGenerationCaseID": ret03CaseID, "migrationStates": states, "archiveImports": imports, "newRootRecovery": true, "deletionVerified": true, "rawRowsAfterDelete": 0})
	save(t, output, "result.json", map[string]any{"caseID": ret03CaseID, "runtimeEvidence": "pass", "casePass": false, "independentAcceptance": "pending", "UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false, "remainingCases": map[string]any{"count": 44, "status": "pending"}, "sourceProvenance": anchor, "inputBundleIdentity": ret03CaseID + "/input-bundle@" + sealed.SourceRevision, "inputBundleDigest": sealed.SourceRevision, "beforeDigest": restoredManifest.SourceRevision, "afterDigest": restoredManifest.SourceRevision, "rawEventCount": 0, "beforeRawCount": 4, "deletedCount": 4, "afterRawCount": 0, "archiveEventCount": 4, "retainedProjectionCount": 4, "onlineRawComplete": false, "backupRestore": "pass", "applicableSEM": sem, "evidence": map[string]any{"inputBundle": inputHash, "semanticAssertions": assertionsHash, "executionRecord": executionHash, "backupRestore": backupHash, "rawEventInventory": rawHash, "archiveInventory": archiveHash, "retainedProjectionSnapshot": projectionHash}})
	_ = after
}

// RET-03 reopens an already-qualified retained/archive bundle. Production
// derived rebuilds that require raw rows must stay fail-closed after deletion;
// this path verifies the persisted retained state and exercises read-only
// history queries without manufacturing replacement raw events.
func ret03RetentionReopen(t *testing.T, ctx context.Context, st *store.Store, dbPath string) []any {
	t.Helper()
	state, e := st.DiscoverUsageCacheAccounting(ctx)
	must(t, e)
	if state.Status != "completed" {
		t.Fatalf("RET-03 retained reopen requires completed cache accounting: %#v", state)
	}
	db := openDB(t, dbPath, true)
	defer db.Close()
	if scalar(t, db, "select count(*) from usage_events") != 0 ||
		scalar(t, db, "select count(*) from "+usageprojection.EventTable) != 4 {
		t.Fatal("RET-03 retained reopen lost the deleted raw boundary or retained projection")
	}
	return []any{map[string]any{
		"mode":                    "retained-archive-read-only",
		"cacheAccounting":         state,
		"rawEventCount":           0,
		"retainedProjectionCount": 4,
		"derivedRebuild":          "not-run-after-raw-deletion",
		"rawOnlyRebuildGuard":     "fail-closed-by-production-source-guard",
	}}
}

func loadSnapshotFile(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	var snapshot map[string][]map[string]any
	must(t, json.Unmarshal(read(t, path), &snapshot))
	return snapshot
}
