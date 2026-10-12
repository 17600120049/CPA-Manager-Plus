package legacypreflight

// PlannedBridgeCandidate describes a target from the migration plan. It is
// neither the inspected source's release nor evidence of upgrade eligibility.
type PlannedBridgeCandidate struct {
	Status       string `json:"status"`
	Release      string `json:"release"`
	SourceCommit string `json:"sourceCommit"`
}

func addPlannedBridgeCandidate(report *Report) {
	if report.SourceKind != "legacy-manager" || report.CompatibilityProfile == nil || report.CompatibilityProfile.Status != "recognized" {
		return
	}
	report.PlannedBridgeCandidate = &PlannedBridgeCandidate{
		Status:       "planned",
		Release:      "v1.14.4",
		SourceCommit: "166262365d506157b99968105c890f480cc7c07c",
	}
	report.Unknown = append(report.Unknown, "forward-hop-evidence")
}
