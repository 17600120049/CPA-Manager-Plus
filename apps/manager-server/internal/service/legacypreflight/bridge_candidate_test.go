package legacypreflight

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
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

	// Case 1: with verified forward hop evidence
	verifiedEvidence := &ForwardHopEvidence{
		Status:               "verified",
		OperationID:          "op-12345",
		Release:              "v1.14.4",
		SourceCommit:         "166262365d506157b99968105c890f480cc7c07c",
		Asset:                "cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz",
		ExecutableSHA256:     "exe-sha-12345",
		OutputManifestDigest: "out-manifest-12345",
	}

	report, err := Inspect(context.Background(), paths, Options{
		SourceStopped:      true,
		ForwardHopEvidence: verifiedEvidence,
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
	// 3. ForwardHopEvidence is attached
	if report.ForwardHopEvidence == nil || report.ForwardHopEvidence.Status != "verified" || report.ForwardHopEvidence.OperationID != "op-12345" {
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

	// Case 2: with unverified hop evidence
	unverifiedEvidence := &ForwardHopEvidence{
		Status: "unverified",
	}
	report2, err := Inspect(context.Background(), paths, Options{
		SourceStopped:      true,
		ForwardHopEvidence: unverifiedEvidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report2.ForwardHopEvidence != nil {
		t.Fatalf("unverified forward hop evidence should not be attached: %+v", report2.ForwardHopEvidence)
	}
	if !slices.Contains(report2.Unknown, "forward-hop-evidence") {
		t.Fatalf("forward-hop-evidence should remain unknown: %v", report2.Unknown)
	}
	if report2.UpgradeAuthorized {
		t.Fatalf("UpgradeAuthorized must remain false!")
	}
}

