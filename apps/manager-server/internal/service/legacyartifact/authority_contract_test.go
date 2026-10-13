package legacyartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"strings"
	"testing"
)

// A distinct synthetic asset exists only in the test binary. Published pins
// are never replaced by fixture digests and no old writer is executed.
const testNativeAsset = "fixture-writer-v1.14.4.tar.gz"

func init() {
	nativePins[testNativeAsset] = nativePin{ExecutableSHA256: "306c6ca7407560340797866e077e053627ad409277d1b9da58106fce4cf717cb"}
}

func openTestJournal(t *testing.T) *journal {
	t.Helper()
	j, err := openOrCreateJournal(DefaultJournalPath(testTempDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func testOperationRecord(id string) operationRecord {
	return operationRecord{OperationID: id, ReceiptVersion: ReceiptVersion, CanonicalReceiptSHA256: strings.Repeat("1", 64)}
}

func TestInternalValidatorRequiresPersistentJournal(t *testing.T) {
	opts := hopValidationFixture(t)
	opts.journal = &journal{records: map[string]operationRecord{}}
	if result, err := validateReceiptInternal(context.Background(), opts); !errors.Is(err, ErrJournalRequired) || result != nil {
		t.Fatalf("memory journal accepted: %+v %v", result, err)
	}
}

func TestValidatorRejectsSelfReportedExecutable(t *testing.T) {
	for _, digest := range []string{"fixture-executable-digest", strings.Repeat("0", 64)} {
		t.Run(digest, func(t *testing.T) {
			opts := hopValidationFixture(t)
			opts.Receipt.Asset = "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz"
			opts.Receipt.ExecutableSHA256 = digest
			if err := opts.Receipt.sign(opts.authority); err != nil {
				t.Fatal(err)
			}
			opts.journal = openTestJournal(t)
			record, err := recordForReceipt(opts.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := opts.journal.recordOperation(record); err != nil {
				t.Fatal(err)
			}
			if result, err := validateReceiptInternal(context.Background(), opts); !errors.Is(err, ErrExecutableMismatch) || result != nil {
				t.Fatalf("self-reported executable accepted: %+v %v", result, err)
			}
		})
	}
}

func TestJournalBindsCompleteCanonicalReceipt(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ExecutionReceipt)
	}{
		{"pid", func(r *ExecutionReceipt) { r.ProcessEvidence.PID++ }},
		{"process-time", func(r *ExecutionReceipt) { r.ProcessEvidence.StartedAtMS++; r.ProcessEvidence.FinishedAtMS++ }},
		{"input-family", func(r *ExecutionReceipt) { r.InputFamily = "LegacySchemaB" }},
		{"output-family", func(r *ExecutionReceipt) { r.OutputFamily = "LegacySchemaB" }},
		{"cache-checkpoint", func(r *ExecutionReceipt) { r.CheckpointEvidence.CacheAccountingV2 = "different" }},
		{"quota-checkpoint", func(r *ExecutionReceipt) { r.CheckpointEvidence.QuotaSnapshotLifecycleV1 = "different" }},
		{"lock-checkpoint", func(r *ExecutionReceipt) { r.CheckpointEvidence.MaintenanceLock = "different" }},
		{"issuance", func(r *ExecutionReceipt) { r.IssuedAtMS++ }},
		{"input-digest", func(r *ExecutionReceipt) { r.InputManifestDigest = "different" }},
		{"output-digest", func(r *ExecutionReceipt) { r.OutputManifestDigest = "different" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := hopValidationFixture(t)
			test.mutate(opts.Receipt)
			if err := opts.Receipt.sign(opts.authority); err != nil {
				t.Fatal(err)
			}
			if result, err := validateReceiptInternal(context.Background(), opts); !errors.Is(err, ErrRecordMismatch) || result != nil {
				t.Fatalf("changed receipt reused operation: %+v %v", result, err)
			}
		})
	}
}

func TestReceiptIssuanceFollowsReap(t *testing.T) {
	for _, offset := range []int64{-1, 0, 1} {
		opts := hopValidationFixture(t)
		opts.Receipt.IssuedAtMS = opts.Receipt.ProcessEvidence.FinishedAtMS + offset
		if err := opts.Receipt.validateLifecycle(); (offset < 0) != errors.Is(err, ErrProcessExecutionFailed) {
			t.Fatalf("chronology: %d %v", offset, err)
		}
		if err := opts.Receipt.sign(opts.authority); err != nil {
			t.Fatal(err)
		}
		opts.journal = openTestJournal(t)
		record, err := recordForReceipt(opts.Receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := opts.journal.recordOperation(record); err != nil {
			t.Fatal(err)
		}
		result, err := validateReceiptInternal(context.Background(), opts)
		if offset < 0 {
			if !errors.Is(err, ErrProcessExecutionFailed) || result != nil {
				t.Fatalf("pre-reap issuance: %+v %v", result, err)
			}
		} else if err != nil || result.Evidence != nil {
			t.Fatalf("valid chronology: %+v %v", result, err)
		}
	}
	temp := testTempDir(t)
	input := createTestInputBundle(t, filepath.Join(temp, "in"), "LegacySchemaA")
	exe := filepath.Join(temp, "writer")
	createFakeExecutable(t, exe)
	auth, _ := setupTestAuthority(t)
	j := openTestJournal(t)
	receipt, err := executeHopInternal(context.Background(), hopOptions{Asset: testNativeAsset, InputLayout: input, OutputRoot: filepath.Join(temp, "out"), authority: auth, journal: j}, exe,
		func(_ context.Context, _ string, layout maintenancebundle.Layout) (ProcessEvidence, error) {
			mockMigrateToV1144(t, layout.DatabasePath, "completed", "completed", false)
			future := time.Now().Add(time.Hour).UnixMilli()
			return ProcessEvidence{PID: 1234, StartedAtMS: future, FinishedAtMS: future + 100, DurationMS: 100}, nil
		})
	if !errors.Is(err, ErrProcessExecutionFailed) || receipt != nil || len(j.records) != 0 {
		t.Fatalf("producer issued before reap: %+v %v", receipt, err)
	}
}

func TestPublicValidationRefusesEvenInternallyValidReceipt(t *testing.T) {
	opts := hopValidationFixture(t)
	for _, public := range []ValidateOptions{{Receipt: opts.Receipt, BundleLayout: opts.BundleLayout}, {ReceiptPath: filepath.Join(testTempDir(t), "missing")}} {
		if result, err := ValidateHop(context.Background(), public); !errors.Is(err, ErrValidationNotQualified) || result != nil {
			t.Fatalf("trusted validation available: %+v %v", result, err)
		}
	}
	dir := testTempDir(t)
	if result, err := ValidateHop(context.Background(), ValidateOptions{ReceiptPath: filepath.Join(dir, "missing-receipt"), BundleLayout: maintenancebundle.BundleLayout(filepath.Join(dir, "missing-bundle"))}); !errors.Is(err, ErrValidationNotQualified) || result != nil {
		t.Fatalf("public validation: %+v %v", result, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("unqualified validation touched files: %v %v", entries, err)
	}
}

func TestCanonicalReceiptRejectsDelimiterAmbiguity(t *testing.T) {
	opts := hopValidationFixture(t)
	opts.Receipt.InputFamily = "LegacySchemaA|outf:LegacySchemaB"
	if _, err := opts.Receipt.CanonicalBytes(); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("ambiguous canonical fields: %v", err)
	}
}
