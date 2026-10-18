package legacypreflight

import (
	"context"
	"errors"
	"strings"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var ErrUpgradePreconditionsBlocked = errors.New("Upgrade prerequisites blocked; no migration authority granted")

// UpgradePreconditions is a host-only observation in the existing preflight
// report, not a public Bootstrap status, persisted checkpoint or write handle.
type UpgradePreconditions struct {
	Admission              bootstrap.UpgradeAdmission `json:"admission"`
	AdminVerified          bool                       `json:"adminVerified"`
	Fence                  string                     `json:"fence,omitempty"`
	ExpectedSourceRevision string                     `json:"expectedSourceRevision,omitempty"`
	Backup                 *BackupObservation         `json:"backup,omitempty"`
	TrustedHopReceipt      bool                       `json:"trustedHopReceipt"`
}

// BackupObservation separates byte/matching validation from historical
// completeness and quiescence. Even a verified bundle is not Upgrade input
// authority. Its revision references the existing bundle manifest, no new ledger.
type BackupObservation struct {
	SourceRevision           string `json:"sourceRevision"`
	IntegrityVerified        bool   `json:"integrityVerified"`
	MatchingEvidenceVerified bool   `json:"matchingEvidenceVerified"`
}

type UpgradeBackupOptions struct {
	AdminKey               string
	SourceStopped          bool
	BundleDirectory        string
	ExpectedSourceRevision string
}

// CheckUpgradeBackup verifies an existing snapshot under the canonical source
// fence. Snapshot creation/recovery remains maintenancebundle's responsibility.
// This seam never creates markers, keys, journals, bundles or source schemas.
// Exact source provenance and noncooperating-writer isolation have no qualified
// production producer yet: those obligations MUST remain explicitly blocked.
func CheckUpgradeBackup(ctx context.Context, paths sqlite.InspectionPaths, options UpgradeBackupOptions) (Report, error) {
	preconditions := &UpgradePreconditions{Admission: bootstrap.UpgradeAdmission{
		Shape: bootstrap.UnknownShape, PlannedCandidate: bootstrap.ExactV1144Candidate(),
		EvidenceStatus: bootstrap.SourceUnverified, Readiness: bootstrap.BridgeBlocked,
	}}
	report := Report{SourceKind: "unknown", UpgradePreconditions: preconditions}
	blocked := func(reason string) (Report, error) {
		if ctx.Err() != nil {
			reason = "inspection-interrupted"
		}
		report.Blocked = append(report.Blocked, reason)
		return report, ErrUpgradePreconditionsBlocked
	}
	if ctx.Err() != nil {
		return blocked("inspection-interrupted")
	}
	if strings.TrimSpace(options.AdminKey) == "" {
		return blocked("existing-admin-authorization-required")
	}
	if !options.SourceStopped {
		return blocked("source-quiescence-unproven")
	}
	lock, err := processlock.AcquireExisting(paths.Database)
	if err != nil {
		return blocked("source-ownership-unavailable")
	}
	defer lock.Close()
	paths.Database = lock.DatabasePath()
	inspected, err := inspect(ctx, paths, Options{SourceStopped: true, adminKey: options.AdminKey}, lock, false)
	if err != nil {
		if errors.Is(err, sqlite.ErrLegacyAdminUnauthorized) {
			return blocked("existing-admin-unverifiable-use-stopped-host-recovery")
		}
		return blocked("source-inspection-failed")
	}
	// Only the authenticated branch may expose the schema/history inventory.
	report = inspected
	report.UpgradePreconditions = preconditions
	preconditions.AdminVerified = true
	preconditions.Fence = maintenancebundle.CooperatingFence
	if report.SourceKind != "legacy-manager" || report.CompatibilityProfile == nil || report.CompatibilityProfile.Status != "recognized" {
		return blocked("upgrade-source-profile-unresolved")
	}
	switch report.CompatibilityProfile.Family {
	case "LegacySchemaA":
		preconditions.Admission.Shape = bootstrap.LegacySchemaA
	case "LegacySchemaB":
		preconditions.Admission.Shape = bootstrap.LegacySchemaB
	case "LegacySchemaC":
		preconditions.Admission.Shape = bootstrap.LegacySchemaC
	}
	if preconditions.Admission.Shape != bootstrap.LegacySchemaC {
		return blocked("qualified-forward-hop-required")
	}
	// A valid version string, schema, archive, Session or artifact pin cannot
	// prove who wrote this particular database. No caller-supplied proof flag.
	report.Blocked = append(report.Blocked, "exact-source-provenance-unverified", "external-writer-isolation-unverified")
	if report.Observed.History["rawDeleted"] == "present-observed" {
		report.Blocked = append(report.Blocked, "retained-history-coverage-unverified")
	}
	if options.BundleDirectory == "" || options.ExpectedSourceRevision == "" {
		return blocked("verified-backup-and-expected-revision-required")
	}
	layout := maintenancebundle.Layout{DatabasePath: paths.Database, DataKeyPath: paths.DataKey,
		ConfigPath: paths.Config, ArchivesPath: paths.Archives, RequireConfig: true}
	// Missing archive roots remain explicit in the manifest. DB references to
	// archives are checked by ValidateMatchingEvidence even if the root is absent.
	source, err := maintenancebundle.Inspect(ctx, layout)
	if err != nil {
		return blocked("source-backup-inventory-incomplete")
	}
	if source.SourceRevision != options.ExpectedSourceRevision {
		return blocked("source-revision-mismatch")
	}
	preconditions.ExpectedSourceRevision = source.SourceRevision
	backup, err := maintenancebundle.Verify(ctx, options.BundleDirectory)
	if err != nil {
		return blocked("backup-integrity-or-publication-unverified")
	}
	if backup.Recovery != nil || backup.SourceRevision != source.SourceRevision {
		return blocked("backup-source-mismatch")
	}
	if err := maintenancebundle.ValidateMatchingEvidence(ctx, maintenancebundle.BundleLayout(options.BundleDirectory)); err != nil {
		return blocked("backup-sqlite-key-config-or-archive-evidence-invalid")
	}
	// Recheck both sets after the expensive SQLite/archive validation, retaining
	// the same source lock throughout. Matching bytes are never a writer fence.
	current, err := maintenancebundle.Verify(ctx, options.BundleDirectory)
	if err != nil || current.SourceRevision != backup.SourceRevision || current.Recovery != nil {
		return blocked("backup-changed-during-validation")
	}
	if err := maintenancebundle.RevalidateSource(ctx, layout, source); err != nil {
		return blocked("source-changed-during-validation")
	}
	if err := lock.Validate(); err != nil {
		return blocked("source-ownership-changed")
	}
	if ctx.Err() != nil {
		return blocked("inspection-interrupted")
	}
	preconditions.Backup = &BackupObservation{SourceRevision: backup.SourceRevision, IntegrityVerified: true, MatchingEvidenceVerified: true}
	return report, ErrUpgradePreconditionsBlocked
}
