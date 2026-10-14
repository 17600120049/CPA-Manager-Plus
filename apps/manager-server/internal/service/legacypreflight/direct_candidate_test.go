package legacypreflight

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// A schema-only fixture remains a unit guard, never MIG/AUT runtime evidence.
func TestDirectCandidateMissingGenerationEvidenceFailsClosed(t *testing.T) {
	paths := buildLegacyProfileFixture(t, loadLegacyProfileFixtures(t)[6])
	root := filepath.Dir(paths.Database)
	before := sourceState(t, root)
	got, err := InspectDirectCandidateReadiness(context.Background(), DirectCandidateInput{SourceRoot: root})
	if err == nil || got.Status != "blocked" || got.UpgradeAuthorized || got.TrustedHopReceipt {
		t.Fatalf("missing provenance was eligible: %+v %v", got, err)
	}
	if !reflect.DeepEqual(before, sourceState(t, root)) {
		t.Fatal("missing-evidence check wrote source")
	}
}
