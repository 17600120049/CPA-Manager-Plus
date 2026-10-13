package legacypreflight

import (
	"context"
	"database/sql"
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

	// Step 1: Set up a genuine forward-hop execution closed-loop
	tempDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	authKey, err := legacyartifact.GenerateAuthorityKey()
	if err != nil {
		t.Fatal(err)
	}
	authority, err := legacyartifact.NewAuthority(authKey)
	if err != nil {
		t.Fatal(err)
	}
	journal := legacyartifact.NewMemoryJournal()

	// Fake executable for runner-injected test
	fakeExePath := filepath.Join(tempDir, "bin", "cpa-manager-plus")
	if err := os.MkdirAll(filepath.Dir(fakeExePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeExePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	outputRoot := filepath.Join(tempDir, "hop-output")
	inputLayout := maintenancebundle.Layout{
		DatabasePath: paths.Database,
		DataKeyPath:  paths.DataKey,
		ConfigPath:   paths.Config,
		ArchivesPath: paths.Archives,
	}

	runner := func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (legacyartifact.ProcessEvidence, error) {
		db, err := sql.Open("sqlite", outputLayout.DatabasePath)
		if err != nil {
			return legacyartifact.ProcessEvidence{}, err
		}
		defer db.Close()
		_, err = db.Exec(`
			CREATE TABLE IF NOT EXISTS usage_archive_runs (id TEXT PRIMARY KEY, status TEXT);
			CREATE TABLE IF NOT EXISTS usage_archive_segments (run_id TEXT, sequence INTEGER, PRIMARY KEY (run_id, sequence));
			CREATE TABLE IF NOT EXISTS usage_archive_event_refs (event_hash TEXT PRIMARY KEY);
			CREATE TABLE IF NOT EXISTS usage_archive_deleted_coverage_daily (utc_day INTEGER PRIMARY KEY);
			CREATE TABLE IF NOT EXISTS usage_maintenance_locks (name TEXT PRIMARY KEY);
			DELETE FROM usage_data_migrations;
			INSERT INTO usage_data_migrations (name, status) VALUES ('usage_cache_accounting_v2', 'completed');
			INSERT INTO usage_data_migrations (name, status) VALUES ('quota_snapshot_lifecycle_v1', 'completed');
			DELETE FROM usage_maintenance_locks;
		`)
		if err != nil {
			return legacyartifact.ProcessEvidence{}, err
		}
		return legacyartifact.ProcessEvidence{
			PID:          9999,
			ExitCode:     0,
			StartedAtMS:  1000,
			FinishedAtMS: 1500,
			DurationMS:   500,
		}, nil
	}

	receipt, err := legacyartifact.ExecuteHopForTest(t, context.Background(), legacyartifact.HopOptions{
		Asset:       "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		InputLayout: inputLayout,
		OutputRoot:  outputRoot,
		Authority:   authority,
		Journal:     journal,
	}, fakeExePath, runner)
	if err != nil {
		t.Fatalf("ExecuteHop failed: %v", err)
	}

	// Validate to produce non-forgeable ValidatedHopEvidence
	valResult, err := legacyartifact.ValidateHop(context.Background(), legacyartifact.ValidateOptions{
		Receipt:      receipt,
		BundleLayout: maintenancebundle.BundleLayout(outputRoot),
		Authority:    authority,
		Journal:      journal,
	})
	if err != nil {
		t.Fatalf("ValidateHop failed: %v", err)
	}
	if valResult.Evidence == nil || !valResult.Evidence.IsValid() {
		t.Fatalf("expected valid non-forgeable ValidatedHopEvidence")
	}

	// Case 1: with verified forward hop evidence from validator
	report, err := Inspect(context.Background(), paths, Options{
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
