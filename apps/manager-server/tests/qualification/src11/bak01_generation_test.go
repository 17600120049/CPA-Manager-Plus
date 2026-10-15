package src11

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const (
	bak01CaseID     = "BAK-01"
	bak01PlanCommit = "06a47aaefff1629b3d556f8a49e0544648bfd4bc"
	bak01Profile    = "bak-01-committed-wal-v1"
)

var bak01GenerationFiles = []string{
	"controlled-input.json", "release-info.json", "producer-build.json", "producer.log",
	"config-write.json", "price-write.json", "quota-write.json", "v1-analytics.json",
	"v1-export.jsonl", "source-snapshot.json", "main-db-before-backup.sqlite",
	"wal-state.json", "committed-row.json", "checkpoint-observation.json",
	"backup-execution.json", "authoritative-digest.json",
}

type bak01GenerationInput struct {
	ctx                      context.Context
	root                     string
	generation               string
	live                     string
	layout                   maintenancebundle.Layout
	preCheckpoint            maintenancebundle.Manifest
	profile                  profile
	input                    []byte
	archive                  string
	executableSHA256         string
	producerBuildCommit      string
	generatorCommit          string
	started                  time.Time
	finished                 time.Time
	pid                      int
	exitCode                 int
	reaped                   bool
	queueCalls               int64
	readDB                   *sql.DB
	readTx                   *sql.Tx
	preTransactionMainDigest string
	preTransactionMainSize   int64
}

// generateBAK01SourceTail runs after the exact v1.14.4 producer has been
// reaped, while a read transaction keeps the committed WAL snapshot alive.
// It deliberately never opens the source in a writable connection before the
// main-file-only proof and the maintenance bundle copy are complete.
func generateBAK01SourceTail(t *testing.T, in bak01GenerationInput) {
	t.Helper()
	ctx := in.ctx
	if in.readTx == nil || in.readDB == nil {
		t.Fatal("BAK-01 requires a held read transaction")
	}
	defer func() {
		_ = in.readTx.Rollback()
		_ = in.readDB.Close()
	}()

	requestID := ""
	var event struct {
		RequestID string `json:"request_id"`
	}
	must(t, json.Unmarshal(in.profile.Events[0], &event))
	requestID = event.RequestID
	if requestID == "" {
		t.Fatal("BAK-01 controlled event has no request_id")
	}
	row := queryTx(t, in.readTx, `SELECT id,event_hash,request_id,timestamp_ms,model,input_tokens,output_tokens,total_tokens
		FROM usage_events WHERE request_id=?`, requestID)
	if len(row) != 1 {
		t.Fatalf("BAK-01 committed event identity missing: %s", requestID)
	}
	committedRow := row[0]

	mainOnly := filepath.Join(in.generation, "main-db-before-backup.sqlite")
	copyBytes(t, in.layout.DatabasePath, mainOnly)
	mainDigest := fileDigest(t, mainOnly)
	mainOnlyDB, err := sql.Open("sqlite", dbURI(mainOnly, true)+"&immutable=1")
	must(t, err)
	mainOnlyDB.SetMaxOpenConns(1)
	mainOnlyVisible := int64(-1)
	err = mainOnlyDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_events WHERE request_id=?", requestID).Scan(&mainOnlyVisible)
	must(t, err)
	must(t, mainOnlyDB.Close())
	if mainOnlyVisible != 0 {
		t.Fatalf("BAK-01 committed WAL row is already in the main DB file: %d", mainOnlyVisible)
	}

	walPath := in.layout.DatabasePath + "-wal"
	walInfo, err := os.Stat(walPath)
	must(t, err)
	if walInfo.Size() <= 32 {
		t.Fatalf("BAK-01 source has no WAL frames: %d bytes", walInfo.Size())
	}
	walFrames := inspectWALFrames(t, walPath)
	if walFrames.FrameCount == 0 || walFrames.CommitFrameCount == 0 {
		t.Fatalf("BAK-01 WAL has no committed frame evidence: %#v", walFrames)
	}
	walState := map[string]any{
		"journalMode":                      queryTx(t, in.readTx, "PRAGMA journal_mode"),
		"walAutoCheckpoint":                queryTx(t, in.readTx, "PRAGMA wal_autocheckpoint"),
		"preTransactionMainDatabaseSHA256": in.preTransactionMainDigest,
		"preTransactionMainDatabaseSize":   in.preTransactionMainSize,
		"mainDatabaseSHA256":               mainDigest,
		"mainDatabaseSize":                 fileSize(t, mainOnly),
		"walPath":                          "usage.sqlite-wal",
		"walSize":                          walInfo.Size(),
		"walSHA256":                        fileDigest(t, walPath),
		"walFrameInventory":                walFrames,
		"shmPresent":                       fileExists(in.layout.DatabasePath + "-shm"),
		"committedRowVisibleLive":          true,
		"committedRowVisibleMain":          false,
		"passiveCheckpointOnSource":        "not executed; source premise is preserved by frame and main-file evidence",
	}
	save(t, in.generation, "wal-state.json", walState)
	save(t, in.generation, "committed-row.json", map[string]any{
		"requestID": requestID, "eventHash": committedRow["event_hash"], "row": committedRow,
		"liveConnectionVisible": true, "mainDatabaseOnlyVisible": false,
	})

	checkpointObservation := probePassiveCheckpoint(t, ctx, in.layout, in.generation)
	save(t, in.generation, "checkpoint-observation.json", checkpointObservation)

	before := snapshotTx(t, in.readTx)
	if len(before["usage_events"]) != 4 || len(before["account_quota_snapshots"]) < 2 || len(before["quota_cooldowns"]) == 0 {
		t.Fatal("BAK-01 authoritative source is incomplete")
	}
	checks := sqliteChecksTx(t, in.readTx)
	if checks["journalMode"] == nil {
		t.Fatal("BAK-01 journal mode observation missing")
	}
	authoritativeBefore := ret01SnapshotDigest(before)

	lock, err := processlock.AcquireExisting(in.layout.DatabasePath)
	must(t, err)
	sealed, err := maintenancebundle.Create(ctx, in.layout, filepath.Join(in.root, "bundle"), lock)
	must(t, err)
	must(t, lock.Close())
	if !sealed.Entries["database-wal"].Present || !sealed.Entries["database-shm"].Present {
		t.Fatal("BAK-01 bundle did not capture the complete WAL sidecar set")
	}
	verified, err := maintenancebundle.Verify(ctx, filepath.Join(in.root, "bundle"))
	must(t, err)
	if verified.SourceRevision != sealed.SourceRevision {
		t.Fatal("BAK-01 verified bundle revision changed")
	}
	must(t, maintenancebundle.Revalidate(ctx, in.layout, sealed))

	authoritativeAfterBackup := ret01SnapshotDigest(snapshotTx(t, in.readTx))
	if authoritativeAfterBackup != authoritativeBefore {
		t.Fatal("BAK-01 backup changed authoritative history")
	}
	backupExecution := map[string]any{
		"strategy":                  "maintenancebundle.Create complete database plus WAL/SHM capture",
		"sourceStopped":             true,
		"producerReaped":            in.reaped,
		"bundleVerified":            true,
		"bundleRevalidated":         true,
		"databaseEntry":             sealed.Entries["database"],
		"databaseWAL":               sealed.Entries["database-wal"],
		"databaseSHM":               sealed.Entries["database-shm"],
		"sourceRevision":            sealed.SourceRevision,
		"sourceAuthoritativeDigest": authoritativeBefore,
		"afterAuthoritativeDigest":  authoritativeAfterBackup,
		"sourcePhysicalMutation":    false,
	}
	save(t, in.generation, "backup-execution.json", backupExecution)
	save(t, in.generation, "authoritative-digest.json", map[string]any{
		"beforeBackup":             authoritativeBefore,
		"afterBackup":              authoritativeAfterBackup,
		"sourceRevision":           sealed.SourceRevision,
		"sourceMainDatabaseSHA256": mainDigest,
		"sourceWALSHA256":          fileDigest(t, walPath),
	})
	save(t, in.generation, "source-snapshot.json", before)

	report := map[string]any{
		"caseID": bak01CaseID, "SourceRelease": "v1.14.4", "SourceCommit": sourceCommit,
		"SourceProducerKind": "published-artifact", "SourceProducerArtifact": map[string]any{
			"immutableReleaseID": 404941931, "assetID": 616064478, "asset": asset,
			"releaseInfoSHA256": releaseInfoDigest,
		},
		"SourceProducerArchiveSHA256":    in.archiveSHA256(),
		"SourceProducerExecutableSHA256": in.executableSHA256,
		"SourceProducerBuildCodeCommit":  in.producerBuildCommit,
		"GeneratorCodeCommit":            in.generatorCommit, "PlanCommit": bak01PlanCommit,
		"GenerationProfile":     in.profile.Profile,
		"GenerationProcedure":   "published exact v1.14.4 executable; isolated HTTP collector and controlled price/quota writes; held read snapshot across producer reap; main-file-only row absence; WAL frame inventory; private passive-checkpoint probe; maintenancebundle complete DB+WAL+SHM capture",
		"ControlledInputSHA256": digestBytes(in.input),
		"process":               map[string]any{"pid": in.pid, "startedAt": in.started.UTC(), "finishedAt": in.finished.UTC(), "exitCode": in.exitCode, "reaped": in.reaped, "queueCalls": in.queueCalls, "deliveredPayloads": 5, "expectedUniqueEvents": 4},
		"producerBuildIdentity": "producer-build.json", "harnessToolchain": platformIdentity(),
		"StoppedBeforeCheckpointInventory": in.preCheckpoint, "SQLite": checks,
		"GeneratedBundleSHA256": sealed.SourceRevision, "bundleInventory": sealed,
		"sourceVariant": "committed WAL; full raw; cache/quota completed",
		"walState":      walState, "committedRow": committedRow, "checkpointObservation": checkpointObservation,
		"authoritativeBeforeDigest": authoritativeBefore, "authoritativeAfterBackupDigest": authoritativeAfterBackup,
		"generationSuccess": true, "casePass": false, "UpgradeAuthorized": false, "TrustedHopReceipt": false,
	}
	hashes := map[string]string{}
	for _, name := range bak01GenerationFiles {
		hashes[name] = fileDigest(t, filepath.Join(in.generation, name))
	}
	report["GenerationEvidenceFiles"] = hashes
	generationDigest := save(t, in.generation, "record.json", report)
	save(t, in.root, "generation-index.json", map[string]any{
		"location": "source-generation/record.json", "SHA256": generationDigest,
		"bundleDigest": sealed.SourceRevision, "generationSuccess": true, "casePass": false,
	})
}

func (in bak01GenerationInput) archiveSHA256() string {
	// The runner has already authenticated the archive before entering this
	// helper; retaining its byte digest here avoids a second source identity.
	b, err := os.ReadFile(in.archive)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:])
}

type walFrameObservation struct {
	HeaderMagic      uint32   `json:"headerMagic"`
	PageSize         uint32   `json:"pageSize"`
	FrameCount       int      `json:"frameCount"`
	CommitFrameCount int      `json:"commitFrameCount"`
	CommitFramePages []uint32 `json:"commitFramePages"`
}

func inspectWALFrames(t *testing.T, path string) walFrameObservation {
	t.Helper()
	b := read(t, path)
	if len(b) < 32 {
		t.Fatalf("WAL header is truncated: %d bytes", len(b))
	}
	magic := binary.BigEndian.Uint32(b[:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		t.Fatalf("invalid WAL magic: %#x", magic)
	}
	pageSize := binary.BigEndian.Uint32(b[8:12])
	if pageSize < 512 || pageSize > 65536 {
		t.Fatalf("invalid WAL page size: %d", pageSize)
	}
	frameSize := int(pageSize) + 24
	if (len(b)-32)%frameSize != 0 {
		t.Fatalf("WAL has partial frame bytes: size=%d frameSize=%d", len(b), frameSize)
	}
	out := walFrameObservation{HeaderMagic: magic, PageSize: pageSize, FrameCount: (len(b) - 32) / frameSize}
	for offset := 32; offset < len(b); offset += frameSize {
		page := binary.BigEndian.Uint32(b[offset : offset+4])
		dbSize := binary.BigEndian.Uint32(b[offset+4 : offset+8])
		if dbSize != 0 {
			out.CommitFrameCount++
			out.CommitFramePages = append(out.CommitFramePages, page)
		}
	}
	return out
}

func probePassiveCheckpoint(t *testing.T, ctx context.Context, source maintenancebundle.Layout, generation string) map[string]any {
	t.Helper()
	probe := filepath.Join(generation, "..", "checkpoint-probe")
	probe, err := filepath.Abs(probe)
	must(t, err)
	must(t, os.MkdirAll(probe, 0700))
	defer os.RemoveAll(probe)
	probeDB := filepath.Join(probe, "database")
	copyBytes(t, source.DatabasePath, probeDB)
	for _, suffix := range []string{"-wal", "-shm"} {
		if fileExists(source.DatabasePath + suffix) {
			copyBytes(t, source.DatabasePath+suffix, probeDB+suffix)
		}
	}
	db, err := sql.Open("sqlite", dbURI(probeDB, false))
	must(t, err)
	db.SetMaxOpenConns(1)
	rows := query(t, db, "PRAGMA wal_checkpoint(PASSIVE)")
	must(t, db.Close())
	if len(rows) != 1 {
		t.Fatalf("passive checkpoint returned %d rows", len(rows))
	}
	return map[string]any{"executedOn": "private copy", "sourceMutated": false, "result": rows[0], "pendingFramesReported": rows[0]["log"] != rows[0]["checkpointed"], "contextCancelled": ctx.Err() != nil}
}

func copyBytes(t *testing.T, source, target string) {
	t.Helper()
	b := read(t, source)
	must(t, os.WriteFile(target, b, 0600))
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	must(t, err)
	return info.Size()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func queryTx(t *testing.T, tx *sql.Tx, statement string, args ...any) []map[string]any {
	t.Helper()
	rows, err := tx.Query(statement, args...)
	must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	must(t, err)
	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		must(t, rows.Scan(pointers...))
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			if bytes, ok := values[i].([]byte); ok {
				values[i] = string(bytes)
			}
			row[column] = values[i]
		}
		result = append(result, row)
	}
	must(t, rows.Err())
	return result
}

func snapshotTx(t *testing.T, tx *sql.Tx) map[string][]map[string]any {
	t.Helper()
	out := map[string][]map[string]any{}
	for _, table := range []string{"usage_events", "model_prices", "model_price_context_tiers", "model_price_service_tiers", "account_quota_observations", "account_quota_windows", "account_quota_window_activations", "account_quota_cycles", "account_quota_snapshots", "quota_cooldowns"} {
		out[table] = queryTx(t, tx, "select * from "+table+" order by rowid")
	}
	return out
}

func sqliteChecksTx(t *testing.T, tx *sql.Tx) map[string]any {
	t.Helper()
	integrity := queryTx(t, tx, "PRAGMA integrity_check")
	if len(integrity) != 1 || integrity[0]["integrity_check"] != "ok" {
		t.Fatalf("BAK-01 integrity check failed: %v", integrity)
	}
	foreignKeys := queryTx(t, tx, "PRAGMA foreign_key_check")
	if len(foreignKeys) != 0 {
		t.Fatalf("BAK-01 foreign key check failed: %v", foreignKeys)
	}
	return map[string]any{"integrityCheck": integrity, "foreignKeyCheck": foreignKeys, "journalMode": queryTx(t, tx, "PRAGMA journal_mode")}
}
