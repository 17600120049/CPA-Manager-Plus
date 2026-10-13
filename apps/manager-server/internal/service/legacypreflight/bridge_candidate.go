package legacypreflight

import (
	"context"
	"path/filepath"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

// PlannedBridgeCandidate describes a target from the migration plan. It is
// neither the inspected source's release nor evidence of upgrade eligibility.
type PlannedBridgeCandidate struct {
	Status       string `json:"status"`
	Release      string `json:"release"`
	SourceCommit string `json:"sourceCommit"`
}

// ForwardHopEvidence projects a validator-authenticated operation onto its
// output candidate. This foundation does not qualify production execution.
// The projection is orthogonal to target, historical source and upgrade grant.
type ForwardHopEvidence struct {
	Status               string `json:"status"`
	OperationID          string `json:"operationId,omitempty"`
	Release              string `json:"release,omitempty"`
	SourceCommit         string `json:"sourceCommit,omitempty"`
	Asset                string `json:"asset,omitempty"`
	ExecutableSHA256     string `json:"executableSHA256,omitempty"`
	OutputManifestDigest string `json:"outputManifestDigest,omitempty"`
}

func addPlannedBridgeCandidate(ctx context.Context, report *Report, paths sqlite.InspectionPaths, validatedHop *legacyartifact.ValidatedHopEvidence) {
	if report.SourceKind != "legacy-manager" || report.CompatibilityProfile == nil || report.CompatibilityProfile.Status != "recognized" {
		return
	}
	report.PlannedBridgeCandidate = &PlannedBridgeCandidate{
		Status:       "planned",
		Release:      "v1.14.4",
		SourceCommit: "166262365d506157b99968105c890f480cc7c07c",
	}

	matchesCandidate := false
	if validatedHop != nil && validatedHop.IsValid() {
		resolvePath := func(p string) string {
			if p == "" {
				return ""
			}
			if real, err := filepath.EvalSymlinks(p); err == nil {
				return real
			} else if parent, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
				return filepath.Join(parent, filepath.Base(p))
			}
			return p
		}
		layout := maintenancebundle.Layout{
			DatabasePath: resolvePath(paths.Database),
			DataKeyPath:  resolvePath(paths.DataKey),
			ConfigPath:   resolvePath(paths.Config),
			ArchivesPath: resolvePath(paths.Archives),
		}
		if manifest, err := maintenancebundle.Inspect(ctx, layout); err == nil {
			if digest, err := legacyartifact.ComputeManifestDigest(manifest); err == nil {
				if digest == validatedHop.OutputManifestDigest() {
					matchesCandidate = true
				}
			}
		}
	}

	if matchesCandidate {
		report.ForwardHopEvidence = &ForwardHopEvidence{
			Status:               "verified-hop-evidence",
			OperationID:          validatedHop.OperationID(),
			Release:              validatedHop.Release(),
			SourceCommit:         validatedHop.SourceCommit(),
			Asset:                validatedHop.Asset(),
			ExecutableSHA256:     validatedHop.ExecutableSHA256(),
			OutputManifestDigest: validatedHop.OutputManifestDigest(),
		}
		kept := report.Unknown[:0]
		for _, item := range report.Unknown {
			if item != "forward-hop-evidence" {
				kept = append(kept, item)
			}
		}
		report.Unknown = kept
	} else {
		report.Unknown = append(report.Unknown, "forward-hop-evidence")
	}
}
