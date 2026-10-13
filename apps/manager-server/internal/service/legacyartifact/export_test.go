package legacyartifact

import (
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

// FixtureHopProjection exists only in the test binary. It exercises the
// reserved preflight projection, never a production evidence issuer.
func FixtureHopProjection(t *testing.T) (maintenancebundle.Layout, maintenancebundle.Layout, maintenancebundle.Layout, *ValidatedHopEvidence) {
	t.Helper()
	opts := hopValidationFixture(t)
	root := filepath.Dir(filepath.Dir(opts.BundleLayout.DatabasePath))
	input := maintenancebundle.BundleLayout(filepath.Join(root, "input"))
	other := createTestInputBundle(t, filepath.Join(testTempDir(t), "other"), "LegacySchemaA")
	r := opts.Receipt
	e := &ValidatedHopEvidence{
		operationID: r.OperationID, release: r.Release, sourceCommit: r.SourceCommit,
		asset: r.Asset, executableSHA256: r.ExecutableSHA256,
		inputManifestDigest: r.InputManifestDigest, outputManifestDigest: r.OutputManifestDigest,
		inputFamily: r.InputFamily, outputFamily: r.OutputFamily,
		processEvidence: r.ProcessEvidence, checkpointEvidence: r.CheckpointEvidence, validated: true,
	}
	return input, opts.BundleLayout, other, e
}
