package legacypreflight

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func TestPlannedBridgeCandidateDoesNotIdentifyHistoricalSource(t *testing.T) {
	for _, fixture := range loadLegacyProfileFixtures(t) {
		t.Run(fixture.Ref, func(t *testing.T) {
			paths := buildLegacyProfileFixture(t, fixture)
			before := sourceState(t, filepath.Dir(paths.Database))
			report, err := Inspect(context.Background(), paths, Options{SourceStopped: true})
			if err != nil {
				t.Fatal(err)
			}
			candidate := report.PlannedBridgeCandidate
			if candidate == nil || candidate.Status != "planned" || candidate.Release != "v1.14.4" || candidate.SourceCommit != "166262365d506157b99968105c890f480cc7c07c" {
				t.Fatalf("missing fixed planned target: %+v", candidate)
			}
			if report.CompatibilityProfile.Family != fixture.Family || report.ExactSourceRelease.Status != "unknown" || !slices.Contains(report.Unknown, "exact-source-release") || !slices.Contains(report.Unknown, "forward-hop-evidence") || report.UpgradeAuthorized || startupAllowed(report) {
				t.Fatalf("planned target became source identity/authority: %+v", report)
			}
			if len(report.Blocked) != 0 || report.BridgeReadiness.Status != "unknown" {
				t.Fatal("planned target changed existing diagnostic disposition")
			}
			if !reflect.DeepEqual(before, sourceState(t, filepath.Dir(paths.Database))) {
				t.Fatal("planned target inspection changed source")
			}
		})
	}
}

func TestPlannedBridgeCandidateOmittedWithoutRecognizedLegacy(t *testing.T) {
	fixture := loadLegacyProfileFixtures(t)[6]
	for _, test := range []struct{ name, query, kind, status string }{
		{"partial", "alter table usage_events drop column request_id", "legacy-manager", "partial"},
		{"conflicting", "create table gateway_api_key_identities(id text primary key)", "legacy-manager", "conflicting"},
		{"unknown-shape", "alter table usage_events add column unreviewed text", "legacy-manager", "unknown"},
		{"unknown-source", "drop table settings; drop table usage_events", "unknown", "unknown"},
		{"fresh", "", "fresh", "not-applicable"},
		{"native", "", "native-v2", "not-applicable"},
		{"cancelled", "", "unknown", "unknown"},
		{"unsafe-source", "", "unknown", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := fixturePaths(t)
			if test.name == "native" {
				db, err := sqlite.Open(paths.Database)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			} else if test.name != "fresh" {
				paths = buildLegacyProfileFixture(t, fixture)
			}
			if test.query != "" {
				execLegacyFixture(t, paths.Database, test.query)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.name == "cancelled" {
				cancel()
			}
			before := sourceState(t, filepath.Dir(paths.Database))
			report, err := Inspect(ctx, paths, Options{SourceStopped: test.name != "unsafe-source"})
			if err != nil && test.name != "cancelled" {
				t.Fatal(err)
			}
			if report.SourceKind != test.kind || report.CompatibilityProfile.Status != test.status || report.PlannedBridgeCandidate != nil || slices.Contains(report.Unknown, "forward-hop-evidence") || report.UpgradeAuthorized {
				t.Fatalf("unexpected target or existing classification: %+v", report)
			}
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			if _, exists := object["plannedBridgeCandidate"]; exists {
				t.Fatal("inapplicable candidate was serialized")
			}
			if startupAllowed(report) != (test.name == "fresh" || test.name == "native") {
				t.Fatal("planned target changed startup authority")
			}
			if !reflect.DeepEqual(before, sourceState(t, filepath.Dir(paths.Database))) {
				t.Fatal("omitted target inspection changed source")
			}
		})
	}
}

func TestForwardHopEvidencePreservesOrthogonality(t *testing.T) {
	fixture := loadLegacyProfileFixtures(t)[0]
	paths := buildLegacyProfileFixture(t, fixture)
	if err := os.WriteFile(paths.DataKey, []byte("test-data-key-12345678901234567890\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Signed fixture tests validator-to-preflight projection only. Producer
	// simulation lives inside legacyartifact and has no exported test API.
	candidatePaths := buildLegacyProfileFixture(t, loadLegacyProfileFixtures(t)[6])
	if err := os.WriteFile(candidatePaths.DataKey, []byte("candidate-data-key-1234567890123456"), 0o600); err != nil {
		t.Fatal(err)
	}
	execLegacyFixture(t, candidatePaths.Database, `INSERT INTO usage_data_migrations (name,status) VALUES ('usage_cache_accounting_v2','completed'),('quota_snapshot_lifecycle_v1','completed')`)
	inputLayout := maintenancebundle.Layout{DatabasePath: paths.Database, DataKeyPath: paths.DataKey, ConfigPath: paths.Config, ArchivesPath: paths.Archives}
	out := maintenancebundle.Layout{DatabasePath: candidatePaths.Database, DataKeyPath: candidatePaths.DataKey, ConfigPath: candidatePaths.Config, ArchivesPath: candidatePaths.Archives}
	digest := func(layout maintenancebundle.Layout) string {
		t.Helper()
		for _, path := range []*string{&layout.DatabasePath, &layout.DataKeyPath, &layout.ConfigPath, &layout.ArchivesPath} {
			parent, err := filepath.EvalSymlinks(filepath.Dir(*path))
			if err != nil {
				t.Fatal(err)
			}
			*path = filepath.Join(parent, filepath.Base(*path))
		}
		manifest, err := maintenancebundle.Inspect(context.Background(), layout)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := legacyartifact.ComputeManifestDigest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	authKey, err := legacyartifact.GenerateAuthorityKey()
	if err != nil {
		t.Fatal(err)
	}
	authority, err := legacyartifact.NewAuthority(authKey)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := legacyartifact.InspectCheckpoints(context.Background(), candidatePaths.Database)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &legacyartifact.ExecutionReceipt{
		Version: legacyartifact.ReceiptVersion, OperationID: "signed-projection-fixture",
		Release: legacyartifact.Release, SourceCommit: legacyartifact.SourceCommit,
		Asset: "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", ExecutableSHA256: "fixture-executable-digest",
		InputManifestDigest: digest(inputLayout), OutputManifestDigest: digest(out),
		InputFamily: "LegacySchemaA", OutputFamily: "LegacySchemaC", IssuedAtMS: 1500,
		ProcessEvidence:    legacyartifact.ProcessEvidence{PID: 9999, StartedAtMS: 1000, FinishedAtMS: 1500, DurationMS: 500},
		CheckpointEvidence: checkpoints,
	}
	if err := receipt.Sign(authority); err != nil {
		t.Fatal(err)
	}
	journal := legacyartifact.NewMemoryJournal()
	if err := journal.RecordOperation(legacyartifact.OperationRecord{
		OperationID: receipt.OperationID, Release: receipt.Release, Asset: receipt.Asset,
		ExecutableSHA256: receipt.ExecutableSHA256, InputManifestDigest: receipt.InputManifestDigest,
		OutputManifestDigest: receipt.OutputManifestDigest, IssuedAtMS: receipt.IssuedAtMS,
	}); err != nil {
		t.Fatal(err)
	}

	// Validate to produce non-forgeable ValidatedHopEvidence
	valResult, err := legacyartifact.ValidateHop(context.Background(), legacyartifact.ValidateOptions{
		Receipt:      receipt,
		BundleLayout: out,
		Authority:    authority,
		Journal:      journal,
	})
	if err != nil {
		t.Fatalf("ValidateHop failed: %v", err)
	}
	if valResult.Evidence == nil || !valResult.Evidence.IsValid() {
		t.Fatalf("expected valid non-forgeable ValidatedHopEvidence")
	}

	if receipt.InputManifestDigest == receipt.OutputManifestDigest {
		t.Fatal("candidate must differ from original input")
	}
	oldReport, err := Inspect(context.Background(), paths, Options{SourceStopped: true, ValidatedHop: valResult.Evidence})
	if err != nil {
		t.Fatal(err)
	}
	if oldReport.ForwardHopEvidence != nil || !slices.Contains(oldReport.Unknown, "forward-hop-evidence") {
		t.Fatal("old input acquired output writer evidence")
	}
	// Case 1: with verified forward hop evidence from validator
	report, err := Inspect(context.Background(), candidatePaths, Options{
		SourceStopped: true,
		ValidatedHop:  valResult.Evidence,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Planned target stays planned
	if report.PlannedBridgeCandidate == nil || report.PlannedBridgeCandidate.Status != "planned" || report.PlannedBridgeCandidate.Release != "v1.14.4" {
		t.Fatalf("planned bridge candidate corrupted: %+v", report.PlannedBridgeCandidate)
	}
	// 2. Exact source release stays unknown
	if report.ExactSourceRelease == nil || report.ExactSourceRelease.Status != "unknown" {
		t.Fatalf("exact source release falsely identified: %+v", report.ExactSourceRelease)
	}
	// 3. ForwardHopEvidence is attached and contains validated operation ID
	if report.ForwardHopEvidence == nil || report.ForwardHopEvidence.Status != "verified-hop-evidence" || report.ForwardHopEvidence.OperationID != receipt.OperationID {
		t.Fatalf("forward hop evidence missing or corrupted: %+v", report.ForwardHopEvidence)
	}
	// 4. "forward-hop-evidence" is removed from unknown
	if slices.Contains(report.Unknown, "forward-hop-evidence") {
		t.Fatalf("forward-hop-evidence still in unknown: %v", report.Unknown)
	}
	// 5. Readiness remains independent
	if report.BridgeReadiness == nil {
		t.Fatalf("bridge readiness missing: %+v", report.BridgeReadiness)
	}
	// 6. UpgradeAuthorized MUST remain false
	if report.UpgradeAuthorized {
		t.Fatalf("UpgradeAuthorized became true upon hop evidence!")
	}
	// 7. Startup is still NOT allowed for legacy source
	if startupAllowed(report) {
		t.Fatalf("startupAllowed became true upon hop evidence!")
	}

	// Case 2: with nil ValidatedHop (no evidence)
	report2, err := Inspect(context.Background(), paths, Options{
		SourceStopped: true,
		ValidatedHop:  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report2.ForwardHopEvidence != nil {
		t.Fatalf("nil ValidatedHop evidence should not attach ForwardHopEvidence: %+v", report2.ForwardHopEvidence)
	}
	if !slices.Contains(report2.Unknown, "forward-hop-evidence") {
		t.Fatalf("forward-hop-evidence should remain unknown: %v", report2.Unknown)
	}
	if report2.UpgradeAuthorized {
		t.Fatalf("UpgradeAuthorized must remain false!")
	}

	// Case 3: with ValidatedHop evidence generated for source A, but passed to different source B
	otherFixture := loadLegacyProfileFixtures(t)[1]
	otherPaths := buildLegacyProfileFixture(t, otherFixture)
	if err := os.WriteFile(otherPaths.DataKey, []byte("other-data-key-12345678901234567890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report3, err := Inspect(context.Background(), otherPaths, Options{
		SourceStopped: true,
		ValidatedHop:  valResult.Evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report3.ForwardHopEvidence != nil {
		t.Fatalf("cross-source ValidatedHop evidence must not attach to mismatched source: %+v", report3.ForwardHopEvidence)
	}
	if !slices.Contains(report3.Unknown, "forward-hop-evidence") {
		t.Fatalf("forward-hop-evidence must remain unknown for mismatched source: %v", report3.Unknown)
	}
	if report3.UpgradeAuthorized {
		t.Fatalf("UpgradeAuthorized must remain false!")
	}
}
