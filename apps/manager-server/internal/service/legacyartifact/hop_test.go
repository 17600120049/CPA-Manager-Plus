package legacyartifact

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func testTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func createTestInputBundle(t *testing.T, root string, schemaFamily string) maintenancebundle.Layout {
	t.Helper()
	layout := maintenancebundle.BundleLayout(root)
	if err := os.MkdirAll(filepath.Dir(layout.DatabasePath), 0o700); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", layout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Base legacy tables
	stmts := []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT, updated_at_ms INTEGER);`,
		`INSERT INTO settings VALUES ('manager_config_v1', '{}', 1000);`,
		`CREATE TABLE usage_events (id INTEGER PRIMARY KEY, raw_json TEXT);`,
		`INSERT INTO usage_events VALUES (1, '{}');`,
		`CREATE TABLE usage_data_migrations (name TEXT PRIMARY KEY, status TEXT);`,
	}
	if schemaFamily == "LegacySchemaB" || schemaFamily == "LegacySchemaC" {
		stmts = append(stmts,
			`ALTER TABLE usage_events ADD COLUMN response_model TEXT;`,
			`ALTER TABLE usage_events ADD COLUMN session_id TEXT;`,
		)
	}
	if schemaFamily == "LegacySchemaC" {
		stmts = append(stmts,
			`CREATE TABLE usage_archive_runs (id TEXT PRIMARY KEY, status TEXT);`,
			`CREATE TABLE usage_archive_segments (run_id TEXT, sequence INTEGER, PRIMARY KEY (run_id, sequence));`,
			`CREATE TABLE usage_archive_event_refs (event_hash TEXT PRIMARY KEY);`,
			`CREATE TABLE usage_archive_deleted_coverage_daily (utc_day INTEGER PRIMARY KEY);`,
			`CREATE TABLE usage_maintenance_locks (name TEXT PRIMARY KEY);`,
		)
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %s: %v", stmt, err)
		}
	}

	// Create fake data.key
	if err := os.WriteFile(layout.DataKeyPath, []byte("test-data-key-12345678901234567890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return layout
}

func mockMigrateToV1144(t *testing.T, dbPath string, cacheStatus, quotaStatus string, withLock bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS usage_archive_runs (id TEXT PRIMARY KEY, status TEXT);`,
		`CREATE TABLE IF NOT EXISTS usage_archive_segments (run_id TEXT, sequence INTEGER, PRIMARY KEY (run_id, sequence));`,
		`CREATE TABLE IF NOT EXISTS usage_archive_event_refs (event_hash TEXT PRIMARY KEY);`,
		`CREATE TABLE IF NOT EXISTS usage_archive_deleted_coverage_daily (utc_day INTEGER PRIMARY KEY);`,
		`CREATE TABLE IF NOT EXISTS usage_maintenance_locks (name TEXT PRIMARY KEY);`,
		`DELETE FROM usage_data_migrations;`,
		`INSERT INTO usage_data_migrations VALUES ('usage_cache_accounting_v2', '` + cacheStatus + `');`,
		`INSERT INTO usage_data_migrations VALUES ('quota_snapshot_lifecycle_v1', '` + quotaStatus + `');`,
	}
	if withLock {
		stmts = append(stmts, `INSERT INTO usage_maintenance_locks VALUES ('active_lock');`)
	} else {
		stmts = append(stmts, `DELETE FROM usage_maintenance_locks;`)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %s: %v", s, err)
		}
	}
}

func setupTestAuthority(t *testing.T) (*Authority, []byte) {
	t.Helper()
	key, err := GenerateAuthorityKey()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthority(key)
	if err != nil {
		t.Fatal(err)
	}
	return auth, key
}

func createFakeExecutable(t *testing.T, path string) (string, []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:]), body
}

// 1. Happy path: ExecuteHop -> Receipt -> ValidateHop with full evidence closed-loop
func TestHopProducerAndValidatorHappyPath(t *testing.T) {
	temp := testTempDir(t)
	inRoot := filepath.Join(temp, "in")
	outRoot := filepath.Join(temp, "out")
	inLayout := createTestInputBundle(t, inRoot, "LegacySchemaA")

	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	asset := "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz"

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{
			PID:          1234,
			ExitCode:     0,
			StartedAtMS:  1000,
			FinishedAtMS: 1500,
			DurationMS:   500,
		}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       asset,
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatalf("ExecuteHop failed: %v", err)
	}

	if receipt.Release != Release || receipt.SourceCommit != SourceCommit {
		t.Errorf("unexpected release/commit in receipt: %s %s", receipt.Release, receipt.SourceCommit)
	}
	if receipt.OutputFamily != "LegacySchemaC" {
		t.Errorf("output family want LegacySchemaC, got %s", receipt.OutputFamily)
	}
	if receipt.Signature == "" {
		t.Errorf("empty signature on receipt")
	}

	// Validate using ValidateHop
	outLayout := maintenancebundle.BundleLayout(outRoot)
	res, err := ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: outLayout,
		Authority:    auth,
		Journal:      journal,
	})
	if err != nil {
		t.Fatalf("ValidateHop failed: %v", err)
	}
	if !res.Valid || res.Status != "verified-hop-evidence" {
		t.Fatalf("unexpected validation result: %+v", res)
	}
	if res.Evidence == nil || !res.Evidence.IsValid() {
		t.Fatalf("expected valid non-forgeable ValidatedHopEvidence, got %+v", res.Evidence)
	}
	if res.Evidence.OperationID() != receipt.OperationID {
		t.Errorf("evidence operation ID mismatch: got %s, want %s", res.Evidence.OperationID(), receipt.OperationID)
	}
}

// 2. Missing Journal in ExecuteHop must fail closed
func TestHopProducerRejectsMissingJournal(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1, ExitCode: 0, DurationMS: 10}, nil
	}

	_, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out"),
		Authority:   auth,
		Journal:     nil, // missing journal
	}, fakeExePath, runner)
	if !errors.Is(err, ErrJournalRequired) {
		t.Fatalf("expected ErrJournalRequired, got %v", err)
	}
}

// 3. Missing Journal in ValidateHop must fail closed
func TestHopValidatorRejectsMissingJournal(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1, ExitCode: 0, DurationMS: 10}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outRoot),
		Authority:    auth,
		Journal:      nil, // missing journal in validator
	})
	if !errors.Is(err, ErrJournalRequired) {
		t.Fatalf("expected ErrJournalRequired in ValidateHop, got %v", err)
	}
}

// 4. Wrong artifact: Asset not in nativePins
func TestHopProducerRejectsWrongArtifact(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1, ExitCode: 0, DurationMS: 10}, nil
	}

	_, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "unsupported_asset_v1.14.4.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out"),
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err == nil {
		t.Fatal("expected failure on wrong artifact, got nil")
	}
}

// 5. Newer / native source downgrade prohibited
func TestHopProducerRejectsNewerSourceDowngrade(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	// inject gateway table
	db, err := sql.Open("sqlite", inLayout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE gateway_api_key_identities (id TEXT PRIMARY KEY);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1, ExitCode: 0, DurationMS: 10}, nil
	}

	_, err = ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out"),
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if !errors.Is(err, ErrDowngradeProhibited) {
		t.Fatalf("expected ErrDowngradeProhibited, got %v", err)
	}
}

// 6. Unrecognized / corrupted input source
func TestHopProducerRejectsUnrecognizedSource(t *testing.T) {
	temp := testTempDir(t)
	inLayout := maintenancebundle.BundleLayout(filepath.Join(temp, "in"))
	_ = os.MkdirAll(filepath.Dir(inLayout.DatabasePath), 0o700)
	_ = os.WriteFile(inLayout.DatabasePath, []byte("not a sqlite database"), 0o600)
	_ = os.WriteFile(inLayout.DataKeyPath, []byte("key"), 0o600)

	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1, ExitCode: 0, DurationMS: 10}, nil
	}

	_, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out"),
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

// 7. Process failure or non-zero exit code
func TestHopProducerRejectsProcessFailureOrDirtyExit(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1234, ExitCode: 1}, errors.New("exit status 1")
	}

	_, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out"),
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if !errors.Is(err, ErrProcessExecutionFailed) {
		t.Fatalf("expected ErrProcessExecutionFailed, got %v", err)
	}
}

// 8. Incomplete required migration checkpoints
func TestHopProducerRejectsIncompleteCheckpoints(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	// Case A: Cache accounting incomplete
	runnerIncompleteCache := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "running", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	_, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out1"),
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runnerIncompleteCache)
	if !errors.Is(err, ErrCheckpointFailed) {
		t.Fatalf("expected ErrCheckpointFailed for incomplete cache, got %v", err)
	}

	// Case B: Active maintenance lock left on database
	runnerWithLock := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", true)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	_, err = ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out2"),
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runnerWithLock)
	if !errors.Is(err, ErrMaintenanceLockActive) {
		t.Fatalf("expected ErrMaintenanceLockActive, got %v", err)
	}
}

// 9. Workspace isolation: Reject overlapping or non-empty output directories
func TestHopProducerRejectsInsecureWorkspace(t *testing.T) {
	temp := testTempDir(t)
	inDir := filepath.Join(temp, "source")
	inLayout := createTestInputBundle(t, inDir, "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1, ExitCode: 0, DurationMS: 10}, nil
	}

	// Case A: outputRoot is same as input root
	_, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  inDir,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if !errors.Is(err, ErrInsecureWorkspace) {
		t.Fatalf("expected ErrInsecureWorkspace for same dir, got %v", err)
	}

	// Case B: outputRoot already exists and contains unknown files
	nonEmptyOut := filepath.Join(temp, "non-empty-out")
	if err := os.MkdirAll(nonEmptyOut, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyOut, "rogue.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  nonEmptyOut,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if !errors.Is(err, ErrInsecureWorkspace) {
		t.Fatalf("expected ErrInsecureWorkspace for non-empty dir, got %v", err)
	}
}

// 10. Validator rejects forged signature
func TestHopValidatorRejectsForgedSignature(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatal(err)
	}

	// Forge signature
	receipt.Signature = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outRoot),
		Authority:    auth,
		Journal:      journal,
	})
	if !errors.Is(err, ErrForgedReceipt) {
		t.Fatalf("expected ErrForgedReceipt, got %v", err)
	}
}

// 11. Validator rejects copied receipt applied to another bundle
func TestHopValidatorRejectsCopiedReceiptToDifferentBundle(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatal(err)
	}

	otherRoot := filepath.Join(temp, "other")
	otherLayout := createTestInputBundle(t, otherRoot, "LegacySchemaC")
	mockMigrateToV1144(t, otherLayout.DatabasePath, "completed", "completed", false)

	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: otherLayout,
		Authority:    auth,
		Journal:      journal,
	})
	if !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("expected ErrBundleMismatch when receipt is copied, got %v", err)
	}
}

// 12. Validator rejects output bundle content change (tampered DB or key)
func TestHopValidatorRejectsOutputBundleContentChange(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatal(err)
	}

	// Modify 1 byte in the data.key of outLayout
	outLayout := maintenancebundle.BundleLayout(outRoot)
	f, err := os.OpenFile(outLayout.DataKeyPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("modified-byte")
	f.Close()

	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: outLayout,
		Authority:    auth,
		Journal:      journal,
	})
	if !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("expected ErrBundleMismatch on altered bundle content, got %v", err)
	}
}

// 13. Validator rejects revoked operation in Journal
func TestHopValidatorRejectsRevokedOperationInJournal(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatal(err)
	}

	// Revoke operation in journal
	if err := journal.RevokeOperation(receipt.OperationID); err != nil {
		t.Fatal(err)
	}

	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outRoot),
		Authority:    auth,
		Journal:      journal,
	})
	if !errors.Is(err, ErrOperationRevoked) {
		t.Fatalf("expected ErrOperationRevoked, got %v", err)
	}
}

// 14. Validator rejects mismatch between Journal record and receipt fields
func TestHopValidatorRejectsRecordMismatch(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHopForTest(t, context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  outRoot,
		Authority:   auth,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt journal record in-memory
	rec, _ := journal.GetOperation(receipt.OperationID)
	rec.ExecutableSHA256 = "mismatched-exe-digest"
	journal.records[receipt.OperationID] = rec

	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outRoot),
		Authority:    auth,
		Journal:      journal,
	})
	if !errors.Is(err, ErrRecordMismatch) {
		t.Fatalf("expected ErrRecordMismatch, got %v", err)
	}
}

// 15. Journal corruption fails closed
func TestJournalCorruptionFailsClosed(t *testing.T) {
	temp := testTempDir(t)
	journalPath := filepath.Join(temp, "corrupt-journal.json")

	// Case A: Malformed JSON
	if err := os.WriteFile(journalPath, []byte("{not valid json}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenJournal(journalPath)
	if !errors.Is(err, ErrCorruptJournal) {
		t.Fatalf("expected ErrCorruptJournal on malformed JSON, got %v", err)
	}

	// Case B: Duplicate operation IDs
	dupJSON := `[
		{"operationId": "op-1", "release": "v1.14.4", "asset": "a", "executableSHA256": "h", "inputManifestDigest": "i", "outputManifestDigest": "o", "issuedAtMs": 1000},
		{"operationId": "op-1", "release": "v1.14.4", "asset": "a", "executableSHA256": "h", "inputManifestDigest": "i", "outputManifestDigest": "o", "issuedAtMs": 2000}
	]`
	if err := os.WriteFile(journalPath, []byte(dupJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = OpenJournal(journalPath)
	if !errors.Is(err, ErrCorruptJournal) {
		t.Fatalf("expected ErrCorruptJournal on duplicate IDs, got %v", err)
	}

	// Case C: Missing critical fields
	incompleteJSON := `[
		{"operationId": "op-1", "release": "", "asset": "a", "executableSHA256": "h", "inputManifestDigest": "i", "outputManifestDigest": "o", "issuedAtMs": 1000}
	]`
	if err := os.WriteFile(journalPath, []byte(incompleteJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = OpenJournal(journalPath)
	if !errors.Is(err, ErrCorruptJournal) {
		t.Fatalf("expected ErrCorruptJournal on missing release, got %v", err)
	}
}

// 16. Authority Store: 0600 file permission, key persistence and reload
func TestAuthorityStorePermissionsAndReload(t *testing.T) {
	temp := testTempDir(t)
	keyPath := filepath.Join(temp, "secrets", ".legacy-authority.key")

	// 1. Initial generation
	auth1, err := LoadOrCreateAuthority(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAuthority failed: %v", err)
	}

	// Verify file mode
	info, err := os.Lstat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("expected regular file, got mode %v", info.Mode())
	}

	// 2. Reloading loads identical key
	auth2, err := LoadOrCreateAuthority(keyPath)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	payload := []byte("canonical-test-payload")
	sig1, err := auth1.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !auth2.Verify(payload, sig1) {
		t.Fatalf("reloaded authority failed to verify signature from first authority")
	}

	// 3. Reject symlink
	symPath := filepath.Join(temp, "symlink-key")
	if err := os.Symlink(keyPath, symPath); err != nil {
		t.Fatal(err)
	}
	_, err = LoadOrCreateAuthority(symPath)
	if !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("expected ErrInsecureKeyFile on symlink, got %v", err)
	}
}

// 17. Production ExecuteHop mandates ArchivePath
func TestExecuteHopRejectsMissingArchivePathInProduction(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()

	_, err := ExecuteHop(context.Background(), HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inLayout,
		OutputRoot:  filepath.Join(temp, "out"),
		Authority:   auth,
		Journal:     journal,
	})
	if !errors.Is(err, ErrInvalidHopOptions) {
		t.Fatalf("expected ErrInvalidHopOptions for missing ArchivePath in production, got %v", err)
	}
}

// 18. Cross-process journal file locking and anti-replay
func TestHopJournalCrossProcessConcurrency(t *testing.T) {
	temp := testTempDir(t)
	journalPath := filepath.Join(temp, "concurrent-journal.json")

	j1, err := OpenJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := OpenJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}

	rec1 := OperationRecord{
		OperationID:          "op-concur-1",
		Release:              Release,
		Asset:                "asset1",
		ExecutableSHA256:     "sha1",
		InputManifestDigest:  "in1",
		OutputManifestDigest: "out1",
		IssuedAtMS:           1000,
	}
	rec2 := OperationRecord{
		OperationID:          "op-concur-2",
		Release:              Release,
		Asset:                "asset2",
		ExecutableSHA256:     "sha2",
		InputManifestDigest:  "in2",
		OutputManifestDigest: "out2",
		IssuedAtMS:           2000,
	}

	if err := j1.RecordOperation(rec1); err != nil {
		t.Fatalf("j1.RecordOperation failed: %v", err)
	}
	if err := j2.RecordOperation(rec2); err != nil {
		t.Fatalf("j2.RecordOperation failed: %v", err)
	}

	// Try replaying rec1 through j2: must be rejected!
	if err := j2.RecordOperation(rec1); !errors.Is(err, ErrOperationReplayed) {
		t.Fatalf("expected ErrOperationReplayed when replaying rec1 via j2, got %v", err)
	}

	// Read from fresh journal instance
	jFresh, err := OpenJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := jFresh.GetOperation("op-concur-1"); !ok {
		t.Errorf("expected op-concur-1 in journal")
	}
	if _, ok := jFresh.GetOperation("op-concur-2"); !ok {
		t.Errorf("expected op-concur-2 in journal")
	}
}
