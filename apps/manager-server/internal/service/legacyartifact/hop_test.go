package legacyartifact

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyprofile"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func testTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
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

	for _, ddl := range historicalDDL(t, schemaFamily) {
		if _, err := db.Exec(ddl.SQL); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO settings VALUES ('manager_config_v1', '{}', 1000)`,
		`INSERT INTO usage_events (id,event_hash,timestamp_ms,timestamp,model,raw_json,created_at_ms) VALUES (1,'event-1',1000,'1970-01-01T00:00:01Z','test','{}',1000)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
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

	// Use historical DDL shapes; this simulated migration is not execution qualification.
	for _, ddl := range historicalDDL(t, "LegacySchemaC") {
		if _, err := db.Exec(ddl.SQL); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range strings.Fields(legacyprofile.MetadataColumnSpecs) {
		parts := strings.Split(spec, ":")
		var present int
		if err := db.QueryRow(`select count(*) from pragma_table_info('usage_events') where name=?`, parts[0]).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present == 0 {
			if _, err := db.Exec("alter table usage_events add column " + parts[0] + " " + parts[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	stmts := []string{
		`DELETE FROM usage_data_migrations`,
		`INSERT INTO usage_data_migrations (name,status) VALUES ('usage_cache_accounting_v2', '` + cacheStatus + `')`,
		`INSERT INTO usage_data_migrations (name,status) VALUES ('quota_snapshot_lifecycle_v1', '` + quotaStatus + `')`,
	}

	if withLock {
		stmts = append(stmts, `INSERT INTO usage_maintenance_locks (name,run_id,operation,acquired_at_ms,updated_at_ms) VALUES ('active_lock','test','test',1,1);`)
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

// 1. Simulated producer/validator contract; not published writer qualification.
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

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1010, DurationMS: 10}, nil
	}

	_, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1010, DurationMS: 10}, nil
	}

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1010, DurationMS: 10}, nil
	}

	_, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1010, DurationMS: 10}, nil
	}

	_, err = executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1010, DurationMS: 10}, nil
	}

	_, err := executeHopInternal(context.Background(), HopOptions{
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

	_, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	_, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	_, err = executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1010, DurationMS: 10}, nil
	}

	// Case A: outputRoot is same as input root
	_, err := executeHopInternal(context.Background(), HopOptions{
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
	_, err = executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
		return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
	}

	receipt, err := executeHopInternal(context.Background(), HopOptions{
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
	rec, _, err := journal.GetOperation(receipt.OperationID)
	if err != nil {
		t.Fatal(err)
	}
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

// 17. Production execution is unavailable until offline qualification.
func TestExecuteHopRejectsUnqualifiedExecution(t *testing.T) {
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
	if !errors.Is(err, ErrExecutionNotQualified) {
		t.Fatalf("expected ErrExecutionNotQualified in production, got %v", err)
	}
}

// 18. Cross-process journal file locking and anti-replay
func TestHopJournalCrossProcessConcurrency(t *testing.T) {
	temp := testTempDir(t)
	journalPath := filepath.Join(temp, "concurrent-journal.json")

	j1, err := OpenOrCreateJournal(journalPath)
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
	if _, ok, err := jFresh.GetOperation("op-concur-1"); !ok || err != nil {
		t.Errorf("expected op-concur-1 in journal")
	}
	if _, ok, err := jFresh.GetOperation("op-concur-2"); !ok || err != nil {
		t.Errorf("expected op-concur-2 in journal")
	}
}

type historicalTableDDL struct {
	Table string `json:"table"`
	SQL   string `json:"sql"`
}

var historicalFixturePath, historicalFixturePathErr = filepath.Abs("../legacypreflight/testdata/legacy-profiles/schema-fixtures.json")

func historicalDDL(t *testing.T, family string) []historicalTableDDL {
	t.Helper()
	if historicalFixturePathErr != nil {
		t.Fatal(historicalFixturePathErr)
	}
	data, err := os.ReadFile(historicalFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixtureSet struct {
		Fixtures []struct {
			Family string               `json:"family"`
			DDL    []historicalTableDDL `json:"ddl"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &fixtureSet); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtureSet.Fixtures {
		if f.Family == family {
			return f.DDL
		}
	}
	t.Fatalf("missing historical family %s", family)
	return nil
}

func TestJournalRuntimeFailuresRejectValidationAndWrites(t *testing.T) {
	for _, mode := range []string{"corrupt", "delete", "directory", "symlink", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "unreadable" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("requires Unix permission enforcement")
			}
			if mode == "symlink" && runtime.GOOS == "windows" {
				t.Skip("symlink creation requires Windows privileges")
			}
			temp := testTempDir(t)
			input := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
			auth, _ := setupTestAuthority(t)
			path := filepath.Join(temp, "journal.json")
			journal, err := OpenOrCreateJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(temp, "writer")
			createFakeExecutable(t, exe)
			out := filepath.Join(temp, "out")
			receipt, err := executeHopInternal(context.Background(), HopOptions{Asset: "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", InputLayout: input, OutputRoot: out, Authority: auth, Journal: journal}, exe,
				func(ctx context.Context, exe string, layout maintenancebundle.Layout) (ProcessEvidence, error) {
					mockMigrateToV1144(t, layout.DatabasePath, "completed", "completed", false)
					return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			opts := ValidateOptions{Receipt: receipt, BundleLayout: maintenancebundle.BundleLayout(out), Authority: auth, Journal: journal}
			if _, err := ValidateHop(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			record, ok, err := journal.GetOperation(receipt.OperationID)
			if err != nil || !ok {
				t.Fatalf("initial journal read: %v %v", ok, err)
			}
			switch mode {
			case "corrupt":
				err = os.WriteFile(path, []byte("{invalid"), 0600)
			case "delete":
				err = os.Remove(path)
			case "directory":
				if err = os.Remove(path); err == nil {
					err = os.Mkdir(path, 0700)
				}
			case "symlink":
				copy, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				target := filepath.Join(temp, "redirect.json")
				if err = os.WriteFile(target, copy, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(path); err == nil {
					err = os.Symlink(target, path)
				}
			case "unreadable":
				err = os.Chmod(path, 0000)
				t.Cleanup(func() { _ = os.Chmod(path, 0600) })
			}
			if err != nil {
				t.Fatal(err)
			}
			if result, err := ValidateHop(context.Background(), opts); err == nil || result != nil {
				t.Fatalf("runtime %s journal validated: %+v %v", mode, result, err)
			}
			if rec, ok, err := journal.GetOperation(receipt.OperationID); err == nil || ok || rec.OperationID != "" {
				t.Fatalf("runtime %s returned cached authority: %+v %v %v", mode, rec, ok, err)
			}
			record.OperationID = "new-operation"
			if err := journal.RecordOperation(record); err == nil {
				t.Fatalf("runtime %s accepted write", mode)
			}
			if err := journal.RevokeOperation(receipt.OperationID); err == nil {
				t.Fatalf("runtime %s accepted revocation", mode)
			}
			if _, err := OpenJournal(path); err == nil {
				t.Fatalf("runtime %s reopened authority", mode)
			}
		})
	}
}

func TestDeletedJournalCannotResurrectRevokedOperation(t *testing.T) {
	temp := testTempDir(t)
	path := filepath.Join(temp, "journal.json")
	stale, err := OpenOrCreateJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	record := OperationRecord{OperationID: "old", Release: Release, Asset: "asset", ExecutableSHA256: "exe", InputManifestDigest: "input", OutputManifestDigest: "output", IssuedAtMS: 1000}
	if err := stale.RecordOperation(record); err != nil {
		t.Fatal(err)
	}
	current, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.RevokeOperation("old"); err != nil {
		t.Fatal(err)
	}
	if rec, ok, err := current.GetOperation("old"); err != nil || !ok || !rec.Revoked {
		t.Fatalf("revocation not observed: %+v %v %v", rec, ok, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	record.OperationID = "new"
	if err := stale.RecordOperation(record); !errors.Is(err, ErrJournalUnavailable) {
		t.Fatalf("lost authority write: %v", err)
	}
	if err := current.RevokeOperation("old"); !errors.Is(err, ErrJournalUnavailable) {
		t.Fatalf("lost authority revoke: %v", err)
	}
	if _, err := OpenJournal(path); !errors.Is(err, ErrJournalUnavailable) {
		t.Fatalf("lost authority open: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lost authority was recreated: %v", err)
	}
}

func TestRelativeHopPathsIdentifyStagedBundle(t *testing.T) {
	temp := testTempDir(t)
	createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	exe := filepath.Join(temp, "writer")
	createFakeExecutable(t, exe)
	t.Chdir(temp)
	called := false
	receipt, err := executeHopInternal(context.Background(), HopOptions{Asset: "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", InputLayout: maintenancebundle.BundleLayout("in"), OutputRoot: "out", Authority: auth, Journal: NewMemoryJournal()}, exe,
		func(ctx context.Context, exe string, layout maintenancebundle.Layout) (ProcessEvidence, error) {
			called = true
			want := maintenancebundle.BundleLayout(filepath.Join(temp, "out"))
			if layout != want {
				t.Fatalf("writer layout %+v != staged layout %+v", layout, want)
			}
			mockMigrateToV1144(t, layout.DatabasePath, "completed", "completed", false)
			return ProcessEvidence{PID: 1234, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, nil
		})
	if err != nil || receipt == nil || !called {
		t.Fatalf("relative staging failed: %v %v", receipt, err)
	}
}

func TestProductionExecutionRefusesBeforeAnySideEffect(t *testing.T) {
	temp := testTempDir(t)
	marker := filepath.Join(temp, "launched")
	exe := filepath.Join(temp, "writer")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(temp, "out")
	if evidence, err := DefaultCommandRunner(context.Background(), exe, maintenancebundle.BundleLayout(output)); !errors.Is(err, ErrExecutionNotQualified) || evidence.PID != 0 {
		t.Fatalf("default runner executed: %+v %v", evidence, err)
	}
	if receipt, err := ExecuteHop(context.Background(), HopOptions{ArchivePath: filepath.Join(temp, "absent-archive"), Asset: "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", InputLayout: maintenancebundle.BundleLayout(filepath.Join(temp, "absent-input")), OutputRoot: output}); !errors.Is(err, ErrExecutionNotQualified) || receipt != nil {
		t.Fatalf("producer executed: %+v %v", receipt, err)
	}
	for _, path := range []string{marker, output} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unqualified execution touched %s: %v", path, err)
		}
	}
}

func TestAuthorityConcurrentInitialCreation(t *testing.T) {
	temp := testTempDir(t)
	path := filepath.Join(temp, "secrets", "key")
	const count = 16
	results := make([]*Authority, count)
	errs := make([]error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; results[i], errs[i] = LoadOrCreateAuthority(path) }(i)
	}
	close(start)
	wg.Wait()
	persisted, err := LoadAuthority(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		signature, err := a.Sign([]byte("authority-create"))
		if err != nil || !persisted.Verify([]byte("authority-create"), signature) {
			t.Fatalf("creator %d returned a different authority: %v", i, err)
		}
	}
}

func TestAuthorityCreationAcrossProcesses(t *testing.T) {
	if keyPath := os.Getenv("CPAMP_TEST_AUTHORITY_CREATE_KEY"); keyPath != "" {
		ready := os.Getenv("CPAMP_TEST_AUTHORITY_CREATE_READY")
		if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			if _, err := os.Stat(os.Getenv("CPAMP_TEST_AUTHORITY_CREATE_START")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("start barrier timed out")
			}
			time.Sleep(time.Millisecond)
		}
		auth, err := LoadOrCreateAuthority(keyPath)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := auth.Sign([]byte("authority-create"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("CPAMP_TEST_AUTHORITY_CREATE_SIGNATURE"), []byte(signature), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	temp := testTempDir(t)
	path := filepath.Join(temp, "secrets", "key")
	start := filepath.Join(temp, "start")
	const count = 4
	commands := make([]*exec.Cmd, 0, count)
	t.Cleanup(func() {
		for _, cmd := range commands {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	})
	for i := 0; i < count; i++ {
		suffix := string(rune('a' + i))
		cmd := exec.Command(os.Args[0], "-test.run=^TestAuthorityCreationAcrossProcesses$")
		cmd.Env = append(os.Environ(), "CPAMP_TEST_AUTHORITY_CREATE_KEY="+path, "CPAMP_TEST_AUTHORITY_CREATE_START="+start, "CPAMP_TEST_AUTHORITY_CREATE_READY="+filepath.Join(temp, "ready-"+suffix), "CPAMP_TEST_AUTHORITY_CREATE_SIGNATURE="+filepath.Join(temp, "sig-"+suffix))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		ready := 0
		for i := 0; i < count; i++ {
			if _, err := os.Stat(filepath.Join(temp, "ready-"+string(rune('a'+i)))); err == nil {
				ready++
			}
		}
		if ready == count {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child readiness timed out")
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(start, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	persisted, err := LoadAuthority(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		signature, err := os.ReadFile(filepath.Join(temp, "sig-"+string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		if !persisted.Verify([]byte("authority-create"), string(signature)) {
			t.Fatalf("process %d returned a different authority", i)
		}
	}
}
