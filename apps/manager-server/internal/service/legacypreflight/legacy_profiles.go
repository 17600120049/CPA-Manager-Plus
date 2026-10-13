package legacypreflight

import (
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyprofile"
)

// CompatibilityProfile is an observation, never an upgrade grant.
type CompatibilityProfile = legacyprofile.Profile

type ProfileObservation struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}
type BridgeReadiness struct {
	Status string                        `json:"status"`
	Items  map[string]ProfileObservation `json:"items"`
}

var legacyMetadataColumns = strings.Fields(legacyprofile.MetadataColumnSpecs)

func classifyLegacyProfile(source sqlite.LegacyInspection) CompatibilityProfile {
	profile := legacyprofile.Classify(source)
	if profile.Status == "conflicting" && nativeV2Schema(source) {
		return CompatibilityProfile{Status: "not-applicable", Reasons: []string{"native-v2-source"}}
	}
	return profile
}

func addCompatibilityObservations(report *Report) {
	profile := classifyLegacyProfile(report.Observed)
	if len(report.Blocked) != 0 {
		profile = CompatibilityProfile{Status: "unknown", Reasons: []string{"source-inspection-blocked"}}
	} else if report.SourceKind == "fresh" {
		profile = CompatibilityProfile{Status: "not-applicable", Reasons: []string{"fresh-source"}}
	}
	report.CompatibilityProfile = &profile
	report.ExactSourceRelease = &ProfileObservation{Status: "unknown", Reason: "trusted-release-evidence-unavailable"}
	bridge := legacyBridgeObservations(report)
	report.BridgeReadiness = &bridge
	if profile.Status == "recognized" {
		kept := report.Unknown[:0]
		for _, item := range report.Unknown {
			if item != "compatibility-profile" {
				kept = append(kept, item)
			}
		}
		report.Unknown = kept
	}
	if report.SourceKind == "legacy-manager" {
		if profile.Status != "recognized" {
			report.Blocked = append(report.Blocked, "legacy-profile-unresolved")
		}
		if bridge.Status == "blocked" {
			report.Blocked = append(report.Blocked, "legacy-bridge-blocked")
		}
	}
	// Do not use any profile/bridge observation in startupAllowed. Legacy
	// remains rejected; native v2 retains its already accepted authority.
	report.UpgradeAuthorized = false
}

func legacyBridgeObservations(report *Report) BridgeReadiness {
	result := BridgeReadiness{Status: "unknown", Items: map[string]ProfileObservation{}}
	for _, item := range []string{"cache-accounting-v2", "quota-snapshot-lifecycle-v1", "maintenance", "raw-deletion", "archive-coverage", "encrypted-key", "database-integrity", "foreign-keys", "verified-backup", "disk-space"} {
		result.Items[item] = ProfileObservation{Status: "unknown", Reason: "not-verified"}
	}
	if report.SourceKind != "legacy-manager" || len(report.Blocked) != 0 || report.Observed.SQLite != "observed" {
		return result
	}
	set := func(item, status, reason string) {
		result.Items[item] = ProfileObservation{Status: status, Reason: reason}
		if status == "blocked" {
			result.Status = "blocked"
		}
	}
	switch report.Observed.CacheAccountingV2 {
	case "completed-observed":
		set("cache-accounting-v2", "observed", "completed-row-observed-not-semantic-parity")
	case "failed-observed", "incomplete-observed":
		set("cache-accounting-v2", "blocked", "migration-not-completed")
	}
	switch report.Observed.QuotaSnapshotLifecycleV1 {
	case "completed-observed":
		set("quota-snapshot-lifecycle-v1", "observed", "completed-row-observed-not-semantic-parity")
	case "failed-observed", "incomplete-observed":
		set("quota-snapshot-lifecycle-v1", "blocked", "migration-not-completed")
	}
	switch report.Observed.History["maintenance"] {
	case "present-observed":
		set("maintenance", "blocked", "maintenance-lock-row-observed")
	case "none-observed":
		set("maintenance", "observed", "no-lock-row-observed-not-full-stage-check")
	}
	switch report.Observed.History["rawDeleted"] {
	case "present-observed":
		set("raw-deletion", "observed", "deleted-coverage-row-observed")
		set("archive-coverage", "blocked", "required-history-coverage-unverified")
	case "none-observed":
		set("raw-deletion", "observed", "no-deleted-coverage-row-observed-not-full-history-check")
	}
	if report.Observed.History["archives"] == "pending-observed" {
		set("archive-coverage", "blocked", "archive-stage-not-completed")
	}
	if report.Observed.ConnectionStorage.HasEncryptedConnection {
		for _, file := range report.Observed.Files {
			if file.Role != "data-key" {
				continue
			}
			if file.State == "missing" {
				set("encrypted-key", "blocked", "encrypted-connection-key-missing")
			} else if file.State == "present" {
				set("encrypted-key", "observed", "envelope-and-key-file-observed-decryption-unverified")
			}
		}
	}
	// Unknown obligations always prevent an overall ready/qualified result.
	return result
}
