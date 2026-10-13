package legacyartifact_test

import (
	"context"
	"slices"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func TestReservedEvidenceProjectionBindsOutputCandidate(t *testing.T) {
	input, output, other, evidence := legacyartifact.FixtureHopProjection(t)
	if evidence.InputManifestDigest() == evidence.OutputManifestDigest() {
		t.Fatal("fixture candidate did not change")
	}
	for _, test := range []struct {
		name   string
		layout maintenancebundle.Layout
		attach bool
	}{
		{"input", input, false}, {"output", output, true}, {"other", other, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := test.layout
			report, err := legacypreflight.Inspect(context.Background(), sqlite.InspectionPaths{Database: l.DatabasePath, DataKey: l.DataKeyPath, Config: l.ConfigPath, Archives: l.ArchivesPath}, legacypreflight.Options{SourceStopped: true, ValidatedHop: evidence})
			if err != nil {
				t.Fatal(err)
			}
			if (report.ForwardHopEvidence != nil) != test.attach {
				t.Fatalf("wrong candidate binding: %+v", report)
			}
			if slices.Contains(report.Unknown, "forward-hop-evidence") == test.attach {
				t.Fatalf("wrong unknown disposition: %+v", report)
			}
			if report.ExactSourceRelease.Status != "unknown" || report.UpgradeAuthorized || report.PlannedBridgeCandidate.Status != "planned" {
				t.Fatalf("projection granted source identity or upgrade: %+v", report)
			}
		})
	}
}
