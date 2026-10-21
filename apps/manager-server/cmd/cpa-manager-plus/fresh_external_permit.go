package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
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
	ExpectedRevision uint64 `json:"expectedRevision"`
	IntentDigest string `json:"intentDigest"`
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
		p.ExpiresAtMS-p.IssuedAtMS <= 600000 && p.ExpectedRevision > 0 && len(p.IntentDigest) == 64
}

// The private Host must commit the original request before issuing the permit.
// Bind the normalized URL, Management Key and original revision; do not expose
// this value through the API or persist it in the Fresh operation.
func freshExternalIntentDigest(runID string, revision uint64, baseURL, key string) string {
	data := "cpamp-a3-intent-v1\x00" + runID + "\x00" + strconv.FormatUint(revision, 10) + "\x00" + baseURL + "\x00" + key
	digest := sha256.Sum256([]byte(data))
	return hex.EncodeToString(digest[:])
}

func (p freshExternalPermit) matchesIntent(op bootstrap.FreshOperation, revision uint64, baseURL, key string, now time.Time) bool {
	return p.valid(op, now) && p.ExpectedRevision == revision &&
		p.IntentDigest == freshExternalIntentDigest(p.RunID, revision, baseURL, key)
}

// A browser header, accepted CPA consumer or S1/H1 permit is not A3 authority.
// Only the private host owner can mount the A3-specific root-owned permit.
func privateFreshExternalAdmitted(op bootstrap.FreshOperation, revision uint64, baseURL, key string) bool {
	if os.Getenv("CPAMP_PRIVATE_A3_PROFILE") != freshExternalPrivateProfile {
		return false
	}
	p, ok := loadPrivateFreshExternalPermit(freshExternalPermitPath)
	return ok && p.matchesIntent(op, revision, baseURL, key, time.Now())
}
