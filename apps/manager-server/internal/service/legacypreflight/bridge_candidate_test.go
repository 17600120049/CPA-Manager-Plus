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
