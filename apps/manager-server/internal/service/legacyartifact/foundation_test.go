package legacyartifact

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func validProcessFixture() ProcessEvidence {
	return ProcessEvidence{PID: 1234, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}
}

func hopValidationFixture(t *testing.T) ValidateOptions {
	t.Helper()
	temp := testTempDir(t)
	input := createTestInputBundle(t, filepath.Join(temp, "input"), "LegacySchemaA")
	auth, _ := setupTestAuthority(t)
	journal, err := OpenOrCreateJournal(DefaultJournalPath(temp))
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(temp, "writer")
	createFakeExecutable(t, exe)
	out := filepath.Join(temp, "output")
	receipt, err := executeHopInternal(context.Background(), HopOptions{
		Asset: "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", InputLayout: input,
		OutputRoot: out, Authority: auth, Journal: journal,
	}, exe, func(_ context.Context, _ string, layout maintenancebundle.Layout) (ProcessEvidence, error) {
		mockMigrateToV1144(t, layout.DatabasePath, "completed", "completed", false)
		return validProcessFixture(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := ValidateOptions{Receipt: receipt, BundleLayout: maintenancebundle.BundleLayout(out), Authority: auth, Journal: journal}
	if _, err := ValidateHop(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	return opts
}

func execOutputFixture(t *testing.T, path, query string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestJournalPermissionChangesFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission contract; Windows execution remains unqualified")
	}
	for _, test := range []struct {
		name   string
		target string
		mode   os.FileMode
	}{
		{"readable-journal", "journal", 0644},
		{"group-writable-journal", "journal", 0664},
		{"world-writable-journal", "journal", 0666},
		{"readable-directory", "directory", 0755},
		{"world-writable-directory", "directory", 0777},
		{"readable-lock", "lock", 0644},
		{"world-writable-lock", "lock", 0666},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := hopValidationFixture(t)
			path := opts.Journal.Path()
			target := path
			if test.target == "directory" {
				target = filepath.Dir(path)
			}
			if test.target == "lock" {
				target = path + ".lock"
			}
			if err := os.Chmod(target, test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenJournal(path); !errors.Is(err, ErrInsecureJournal) {
				t.Fatalf("open: %v", err)
			}
			if _, err := OpenOrCreateJournal(path); !errors.Is(err, ErrInsecureJournal) {
				t.Fatalf("create: %v", err)
			}
			if result, err := ValidateHop(context.Background(), opts); !errors.Is(err, ErrInsecureJournal) || result != nil {
				t.Fatalf("validate: %+v %v", result, err)
			}
			if _, ok, err := opts.Journal.GetOperation(opts.Receipt.OperationID); !errors.Is(err, ErrInsecureJournal) || ok {
				t.Fatalf("cached read: %v %v", ok, err)
			}
			if err := opts.Journal.RevokeOperation(opts.Receipt.OperationID); !errors.Is(err, ErrInsecureJournal) {
				t.Fatalf("revoke: %v", err)
			}
			if err := opts.Journal.RecordOperation(OperationRecord{OperationID: "other", Release: Release, Asset: "asset", ExecutableSHA256: "exe", InputManifestDigest: "in", OutputManifestDigest: "out", IssuedAtMS: 1000}); !errors.Is(err, ErrInsecureJournal) {
				t.Fatalf("write: %v", err)
			}
			info, err := os.Lstat(target)
			if err != nil || info.Mode().Perm() != test.mode {
				t.Fatalf("permissions were repaired: %v %v", info, err)
			}
		})
	}
}

func TestDefaultPrivatePathsPreserveManagerDirectory(t *testing.T) {
	dataDir := testTempDir(t)
	if err := os.Chmod(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	keyPath, journalPath := DefaultAuthorityKeyPath(dataDir), DefaultJournalPath(dataDir)
	if filepath.Dir(keyPath) != filepath.Join(dataDir, ".legacy-hop") || filepath.Dir(journalPath) != filepath.Dir(keyPath) {
		t.Fatal("private paths do not share dedicated directory")
	}
	if _, err := LoadOrCreateAuthority(keyPath); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenOrCreateJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuthority(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(journal.Path()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{dataDir: 0755, filepath.Dir(keyPath): 0700, keyPath: 0600, keyPath + ".lock": 0600, journalPath: 0600, journalPath + ".lock": 0600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("%s mode: %v %v", path, info, err)
			}
		}
	}
}

func TestInvalidLifecycleRejectedByProducerAndValidator(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProcessEvidence)
	}{
		{"missing-pid", func(p *ProcessEvidence) { p.PID = 0 }},
		{"negative-pid", func(p *ProcessEvidence) { p.PID = -1 }},
		{"nonzero-exit", func(p *ProcessEvidence) { p.ExitCode = 1 }},
		{"missing-start", func(p *ProcessEvidence) { p.StartedAtMS = 0 }},
		{"missing-finish", func(p *ProcessEvidence) { p.FinishedAtMS = 0 }},
		{"reversed-time", func(p *ProcessEvidence) { p.FinishedAtMS = 999 }},
		{"missing-duration", func(p *ProcessEvidence) { p.DurationMS = 0 }},
		{"duration-too-short", func(p *ProcessEvidence) { p.DurationMS = 98 }},
		{"duration-too-long", func(p *ProcessEvidence) { p.DurationMS = 102 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := hopValidationFixture(t)
			process := validProcessFixture()
			test.mutate(&process)
			opts.Receipt.ProcessEvidence = process
			if err := opts.Receipt.Sign(opts.Authority); err != nil {
				t.Fatal(err)
			}
			if result, err := ValidateHop(context.Background(), opts); !errors.Is(err, ErrProcessExecutionFailed) || result != nil {
				t.Fatalf("validator: %+v %v", result, err)
			}
			temp := testTempDir(t)
			exe := filepath.Join(temp, "writer")
			createFakeExecutable(t, exe)
			journal := NewMemoryJournal()
			receipt, err := executeHopInternal(context.Background(), HopOptions{Asset: opts.Receipt.Asset, InputLayout: opts.BundleLayout, OutputRoot: filepath.Join(temp, "out"), Authority: opts.Authority, Journal: journal}, exe,
				func(context.Context, string, maintenancebundle.Layout) (ProcessEvidence, error) { return process, nil })
			if !errors.Is(err, ErrProcessExecutionFailed) || receipt != nil || len(journal.records) != 0 {
				t.Fatalf("producer: %+v %v", receipt, err)
			}
		})
	}
	for _, duration := range []int64{99, 100, 101} {
		process := validProcessFixture()
		process.DurationMS = duration
		if err := process.Validate(); err != nil {
			t.Fatalf("millisecond rounding %d: %v", duration, err)
		}
	}
}

func TestReceiptAuthenticatesPIDAndRejectsOldVersion(t *testing.T) {
	opts := hopValidationFixture(t)
	opts.Receipt.ProcessEvidence.PID++
	if _, err := ValidateHop(context.Background(), opts); !errors.Is(err, ErrForgedReceipt) {
		t.Fatalf("unsigned PID accepted: %v", err)
	}
	opts.Receipt.Version = 1
	if _, err := ValidateHop(context.Background(), opts); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("old receipt accepted: %v", err)
	}
}

func TestValidatorReappliesCurrentOutputSemantics(t *testing.T) {
	for _, test := range []struct {
		name, query string
		mutate      func(*ExecutionReceipt)
		want        error
	}{
		{"unknown-shape", "alter table usage_events add column future_column text", nil, ErrInvalidSource},
		{"partial-shape", "alter table usage_events drop column stream", nil, ErrInvalidSource},
		{"conflicting-shape", "create table gateway_source_binding_revision(id integer primary key, revision integer)", nil, ErrInvalidSource},
		{"family-mismatch", "", func(r *ExecutionReceipt) { r.OutputFamily = "LegacySchemaB" }, ErrInvalidSource},
		{"checkpoint-mismatch", "", func(r *ExecutionReceipt) { r.CheckpointEvidence.CacheAccountingV2 = "not-observed" }, ErrCheckpointFailed},
		{"lock-evidence-mismatch", "", func(r *ExecutionReceipt) { r.CheckpointEvidence.MaintenanceLock = "active-observed" }, ErrCheckpointFailed},
		{"incomplete-checkpoint", "update usage_data_migrations set status='pending'", nil, ErrCheckpointFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := hopValidationFixture(t)
			if test.query != "" {
				execOutputFixture(t, opts.BundleLayout.DatabasePath, test.query)
			}
			if test.mutate != nil {
				test.mutate(opts.Receipt)
			}
			manifest, err := maintenancebundle.Inspect(context.Background(), opts.BundleLayout)
			if err != nil {
				t.Fatal(err)
			}
			opts.Receipt.OutputManifestDigest, err = ComputeManifestDigest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate an authentic receipt issued under older semantic rules.
			if err := opts.Receipt.Sign(opts.Authority); err != nil {
				t.Fatal(err)
			}
			record, ok, err := opts.Journal.GetOperation(opts.Receipt.OperationID)
			if err != nil || !ok {
				t.Fatalf("journal: %v %v", ok, err)
			}
			record.OutputManifestDigest = opts.Receipt.OutputManifestDigest
			opts.Journal = NewMemoryJournal()
			if err := opts.Journal.RecordOperation(record); err != nil {
				t.Fatal(err)
			}
			if result, err := ValidateHop(context.Background(), opts); !errors.Is(err, test.want) || result != nil {
				t.Fatalf("semantics: %+v %v", result, err)
			}
		})
	}
}

func TestHopProducerRejectsUnresolvedLegacyProfiles(t *testing.T) {
	for _, query := range []string{
		"alter table usage_events add column unreviewed text",
		"alter table usage_events add column response_model text",
		"create table gateway_source_binding_revision(id integer primary key, revision integer)",
	} {
		t.Run(query, func(t *testing.T) {
			temp := testTempDir(t)
			input := createTestInputBundle(t, filepath.Join(temp, "input"), "LegacySchemaA")
			execOutputFixture(t, input.DatabasePath, query)
			auth, _ := setupTestAuthority(t)
			called := false
			receipt, err := executeHopInternal(context.Background(), HopOptions{Asset: "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", InputLayout: input, OutputRoot: filepath.Join(temp, "out"), Authority: auth, Journal: NewMemoryJournal()}, filepath.Join(temp, "absent-writer"),
				func(context.Context, string, maintenancebundle.Layout) (ProcessEvidence, error) {
					called = true
					return validProcessFixture(), nil
				})
			if receipt != nil || called || (!errors.Is(err, ErrInvalidSource) && !errors.Is(err, ErrDowngradeProhibited)) {
				t.Fatalf("unsafe producer: %+v %v %v", receipt, called, err)
			}
		})
	}
}
