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

	// Ensure Archive tables exist for SchemaC
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

// 1. Happy path: ExecuteHop -> Receipt -> ValidateHop
func TestHopProducerAndValidatorHappyPath(t *testing.T) {
	temp := testTempDir(t)
	inRoot := filepath.Join(temp, "in")
	outRoot := filepath.Join(temp, "out")
	inLayout := createTestInputBundle(t, inRoot, "LegacySchemaA")

	auth, _ := setupTestAuthority(t)
	journal := NewMemoryJournal()
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

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

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    asset,
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		Journal:                  journal,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
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
}

// 2. Wrong artifact: Asset not in nativePins
func TestHopProducerRejectsWrongArtifact(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	_, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "unsupported_asset_v1.14.4.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out"),
		Authority:                auth,
		ExpectedExecutableSHA256: exeDigest,
	})
	if err == nil {
		t.Fatal("expected failure on wrong artifact, got nil")
	}
}

// 3. Changed executable: bytes on disk do not match expected digest
func TestHopProducerRejectsChangedExecutable(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	_, _ = createFakeExecutable(t, fakeExePath)

	_, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out"),
		Authority:                auth,
		ExpectedExecutableSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if !errors.Is(err, ErrExecutableMismatch) {
		t.Fatalf("expected ErrExecutableMismatch, got %v", err)
	}
}

// 4. Newer / native source downgrade prohibited
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
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	_, err = ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out"),
		Authority:                auth,
		ExpectedExecutableSHA256: exeDigest,
	})
	if !errors.Is(err, ErrDowngradeProhibited) {
		t.Fatalf("expected ErrDowngradeProhibited, got %v", err)
	}
}

// 5. Unrecognized / corrupted input source
func TestHopProducerRejectsUnrecognizedSource(t *testing.T) {
	temp := testTempDir(t)
	inLayout := maintenancebundle.BundleLayout(filepath.Join(temp, "in"))
	_ = os.MkdirAll(filepath.Dir(inLayout.DatabasePath), 0o700)
	_ = os.WriteFile(inLayout.DatabasePath, []byte("not a sqlite database"), 0o600)
	_ = os.WriteFile(inLayout.DataKeyPath, []byte("key"), 0o600)

	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	_, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out"),
		Authority:                auth,
		ExpectedExecutableSHA256: exeDigest,
	})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

// 6. Process failure or non-zero exit code
func TestHopProducerRejectsProcessFailureOrDirtyExit(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		return ProcessEvidence{PID: 1234, ExitCode: 1}, errors.New("exit status 1")
	}

	_, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out"),
		Authority:                auth,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
	if !errors.Is(err, ErrProcessExecutionFailed) {
		t.Fatalf("expected ErrProcessExecutionFailed, got %v", err)
	}
}

// 7. Context cancellation / interruption
func TestHopProducerRejectsContextInterruption(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := ExecuteHop(ctx, HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out"),
		Authority:                auth,
		ExpectedExecutableSHA256: exeDigest,
	})
	if err == nil {
		t.Fatal("expected failure on cancelled context, got nil")
	}
}

// 8. Incomplete required migration checkpoints
func TestHopProducerRejectsIncompleteCheckpoints(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	// Case A: Cache accounting incomplete
	runnerIncompleteCache := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "running", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	_, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out1"),
		Authority:                auth,
		CommandRunner:            runnerIncompleteCache,
		ExpectedExecutableSHA256: exeDigest,
	})
	if !errors.Is(err, ErrCheckpointFailed) {
		t.Fatalf("expected ErrCheckpointFailed for incomplete cache, got %v", err)
	}

	// Case B: Active maintenance lock left on database
	runnerWithLock := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", true)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	_, err = ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               filepath.Join(temp, "out2"),
		Authority:                auth,
		CommandRunner:            runnerWithLock,
		ExpectedExecutableSHA256: exeDigest,
	})
	if !errors.Is(err, ErrMaintenanceLockActive) {
		t.Fatalf("expected ErrMaintenanceLockActive, got %v", err)
	}
}

// 9. Validator rejects forged signature
func TestHopValidatorRejectsForgedSignature(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Forge signature
	receipt.Signature = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outRoot),
		Authority:    auth,
	})
	if !errors.Is(err, ErrForgedReceipt) {
		t.Fatalf("expected ErrForgedReceipt, got %v", err)
	}
}

// 10. Validator rejects tampered receipt payload
func TestHopValidatorRejectsTamperedReceiptPayload(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tamper payload field without updating signature
	receipt.OperationID = "tampered-op-id"
	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outRoot),
		Authority:    auth,
	})
	if !errors.Is(err, ErrForgedReceipt) {
		t.Fatalf("expected ErrForgedReceipt on tampered payload, got %v", err)
	}
}

// 11. Validator rejects copied receipt applied to another bundle
func TestHopValidatorRejectsCopiedReceiptToDifferentBundle(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create another different bundle and try validating with the receipt of outRoot
	otherRoot := filepath.Join(temp, "other")
	otherLayout := createTestInputBundle(t, otherRoot, "LegacySchemaC")
	mockMigrateToV1144(t, otherLayout.DatabasePath, "completed", "completed", false)

	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: otherLayout,
		Authority:    auth,
	})
	if !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("expected ErrBundleMismatch when receipt is copied to different bundle, got %v", err)
	}
}

// 12. Validator rejects output bundle content change (tampered DB or key)
func TestHopValidatorRejectsOutputBundleContentChange(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
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
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		Journal:                  journal,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
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

// 14. Validator rejects active maintenance lock detected during re-check
func TestHopValidatorRejectsActiveMaintenanceLockOnRecheck(t *testing.T) {
	temp := testTempDir(t)
	inLayout := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	outRoot := filepath.Join(temp, "out")
	auth, _ := setupTestAuthority(t)
	fakeExePath := filepath.Join(temp, "bin", "cpa-manager-plus")
	exeDigest, _ := createFakeExecutable(t, fakeExePath)

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, outputLayout.DatabasePath, "completed", "completed", false)
		return ProcessEvidence{PID: 1234, ExitCode: 0, DurationMS: 100}, nil
	}

	receipt, err := ExecuteHop(context.Background(), HopOptions{
		Asset:                    "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutablePath:           fakeExePath,
		InputLayout:              inLayout,
		OutputRoot:               outRoot,
		Authority:                auth,
		CommandRunner:            runner,
		ExpectedExecutableSHA256: exeDigest,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert lock into output DB after receipt was signed
	outLayout := maintenancebundle.BundleLayout(outRoot)
	db, err := sql.Open("sqlite", outLayout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`INSERT INTO usage_maintenance_locks VALUES ('lock_created_later');`)
	db.Close()

	// Content changed, so manifest digest fails first
	_, err = ValidateHop(context.Background(), ValidateOptions{
		Receipt:      receipt,
		BundleLayout: outLayout,
		Authority:    auth,
	})
	if !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("expected ErrBundleMismatch on lock insertion, got %v", err)
	}
}

// 15. Validator rejects wrong release or target in receipt
func TestHopValidatorRejectsWrongTargetReleaseOrCommit(t *testing.T) {
	auth, _ := setupTestAuthority(t)
	receipt := &ExecutionReceipt{
		Version:              ReceiptVersion,
		OperationID:          "op-1",
		Release:              "v1.14.3", // wrong release
		SourceCommit:         SourceCommit,
		Asset:                "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutableSHA256:     "exe",
		InputManifestDigest:  "in",
		OutputManifestDigest: "out",
		ProcessEvidence:      ProcessEvidence{ExitCode: 0, DurationMS: 100},
	}
	_ = receipt.Sign(auth)

	_, err := ValidateHop(context.Background(), ValidateOptions{
		Receipt:   receipt,
		Authority: auth,
	})
	if !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("expected ErrInvalidReceipt on wrong release, got %v", err)
	}
}
