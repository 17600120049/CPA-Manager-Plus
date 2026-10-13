package legacypreflight

// PlannedBridgeCandidate describes a target from the migration plan. It is
// neither the inspected source's release nor evidence of upgrade eligibility.
type PlannedBridgeCandidate struct {
	Status       string `json:"status"`
	Release      string `json:"release"`
	SourceCommit string `json:"sourceCommit"`
}

// ForwardHopEvidence reflects proven forward-hop execution evidence produced
// by a controlled, Manager-owned v1.14.4 execution. It is orthogonal to the
// planned target, observed source, and upgrade authorization.
type ForwardHopEvidence struct {
	Status               string `json:"status"`
	OperationID          string `json:"operationId,omitempty"`
	Release              string `json:"release,omitempty"`
	SourceCommit         string `json:"sourceCommit,omitempty"`
	Asset                string `json:"asset,omitempty"`
	ExecutableSHA256     string `json:"executableSHA256,omitempty"`
	OutputManifestDigest string `json:"outputManifestDigest,omitempty"`
}

func addPlannedBridgeCandidate(report *Report, hopEvidence *ForwardHopEvidence) {
	if report.SourceKind != "legacy-manager" || report.CompatibilityProfile == nil || report.CompatibilityProfile.Status != "recognized" {
		return
	}
	report.PlannedBridgeCandidate = &PlannedBridgeCandidate{
		Status:       "planned",
		Release:      "v1.14.4",
		SourceCommit: "166262365d506157b99968105c890f480cc7c07c",
	}
	if hopEvidence != nil && hopEvidence.Status == "verified" {
		report.ForwardHopEvidence = hopEvidence
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
