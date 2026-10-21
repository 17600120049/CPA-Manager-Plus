package main

import (
	"os"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
)

const freshExternalPermitPath = "/run/cpamp-a3/permit.json"
const freshExternalPrivateProfile = "plan77-private-fresh-external"

type freshExternalPermit struct {
	Scope          string `json:"scope"`
	RunID          string `json:"runId"`
	AdmissionID    string `json:"admissionId"`
	InstallationID string `json:"installationId"`
	Generation     string `json:"generation"`
	DatabasePath   string `json:"databasePath"`
	OperationID    string `json:"operationId"`
	IssuedAtMS     int64  `json:"issuedAtMs"`
	ExpiresAtMS    int64  `json:"expiresAtMs"`
}

func (p freshExternalPermit) valid(op bootstrap.FreshOperation, now time.Time) bool {
	t := now.UnixMilli()
	return p.Scope == freshExternalPrivateProfile && len(p.RunID) >= 16 &&
		p.AdmissionID == op.Identity.AdmissionID &&
		p.InstallationID == op.Identity.InstallationID &&
		p.Generation == op.Identity.Generation &&
		p.DatabasePath == op.Identity.DatabasePath &&
		p.OperationID == string(op.Identity.OperationID) &&
		p.IssuedAtMS > 0 && p.IssuedAtMS <= t && p.ExpiresAtMS > t &&
		p.ExpiresAtMS-p.IssuedAtMS <= 600000
}

// A browser header, accepted CPA consumer or S1/H1 permit is not A3 authority.
// Only the private host owner can mount the A3-specific root-owned permit.
func privateFreshExternalAdmitted(op bootstrap.FreshOperation) bool {
	if os.Getenv("CPAMP_PRIVATE_A3_PROFILE") != freshExternalPrivateProfile {
		return false
	}
	p, ok := loadPrivateFreshExternalPermit(freshExternalPermitPath)
	return ok && p.valid(op, time.Now())
}
