package bootstrap

import (
	"fmt"
	"regexp"
)

type OperationID string
type ContentDigest string

var (
	operationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,127}$`)
	contentDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func validOperationID(id OperationID) bool {
	return operationIDPattern.MatchString(string(id))
}

func validContentDigest(digest ContentDigest) bool {
	return contentDigestPattern.MatchString(string(digest))
}

type OperationStage string

const (
	OperationClassified        OperationStage = "classified"
	OperationBackupVerified    OperationStage = "backup_verified"
	OperationSourceRevalidated OperationStage = "source_revalidated"
	OperationWritableMigration OperationStage = "writable_migration"
	OperationRecovery          OperationStage = "recovery"
	OperationCompleted         OperationStage = "completed"
)

func validOperationStage(stage OperationStage) bool {
	switch stage {
	case OperationClassified, OperationBackupVerified, OperationSourceRevalidated,
		OperationWritableMigration, OperationRecovery, OperationCompleted:
		return true
	default:
		return false
	}
}

// ActualOperationRecord freezes the minimum persistent identity every future
// migration or forward-hop operation must carry. Persistence itself belongs to
// Phase4 backend implementation.
type ActualOperationRecord struct {
	OperationID         OperationID
	StartedAtUnixMilli  int64
	OperatorIdentityRef string
	Stage               OperationStage
}

func (r ActualOperationRecord) Validate() error {
	if !validOperationID(r.OperationID) {
		return fmt.Errorf("invalid operation id %q", r.OperationID)
	}
	if r.StartedAtUnixMilli <= 0 {
		return fmt.Errorf("operation %q requires start time", r.OperationID)
	}
	if r.OperatorIdentityRef == "" {
		return fmt.Errorf("operation %q requires operator identity reference", r.OperationID)
	}
	if !validOperationStage(r.Stage) {
		return fmt.Errorf("operation %q has invalid stage %q", r.OperationID, r.Stage)
	}
	return nil
}

// ReceiptBinding is the immutable identity a future Manager-owned receipt must
// bind. It is data only; this package neither signs nor trusts a receipt.
type ReceiptBinding struct {
	OperationID           OperationID
	TargetFingerprint     ContentDigest
	InputManifestDigest   ContentDigest
	OutputManifestDigest  ContentDigest
	ArtifactArchiveDigest ContentDigest
	ExecutableDigest      ContentDigest
}

func (r ReceiptBinding) Validate() error {
	if !validOperationID(r.OperationID) {
		return fmt.Errorf("invalid operation id %q", r.OperationID)
	}
	for name, digest := range map[string]ContentDigest{
		"target fingerprint":     r.TargetFingerprint,
		"input manifest digest":  r.InputManifestDigest,
		"output manifest digest":  r.OutputManifestDigest,
		"artifact archive digest": r.ArtifactArchiveDigest,
		"executable digest":       r.ExecutableDigest,
	} {
		if !validContentDigest(digest) {
			return fmt.Errorf("invalid %s %q", name, digest)
		}
	}
	return nil
}

// TrustedReceiptPreconditions captures facts that a future production
// validator must establish. ManagerAuthenticityVerified must come from the
// private Manager authority implementation; a JSON field or EvidenceRef is
// never sufficient.
type TrustedReceiptPreconditions struct {
	Receipt                     ReceiptBinding
	CurrentOperationID          OperationID
	CurrentTargetFingerprint    ContentDigest
	CurrentInputManifestDigest  ContentDigest
	CurrentOutputManifestDigest  ContentDigest
	CurrentArtifactArchiveDigest ContentDigest
	CurrentExecutableDigest      ContentDigest
	ManagerAuthenticityVerified  bool
	JournalRecordPresent        bool
	JournalRevoked              bool
	ProcessExited               bool
	ProcessExitCode             int
	ProcessHandlesReleased      bool
	ProcessReaped               bool
}

// CanAcceptTrustedHopReceipt is a pure acceptance predicate. It does not mint,
// sign, persist, revoke, or publish any receipt.
func CanAcceptTrustedHopReceipt(p TrustedReceiptPreconditions) bool {
	if p.Receipt.Validate() != nil ||
		!p.ManagerAuthenticityVerified ||
		!p.JournalRecordPresent ||
		p.JournalRevoked ||
		!p.ProcessExited ||
		p.ProcessExitCode != 0 ||
		!p.ProcessHandlesReleased ||
		!p.ProcessReaped {
		return false
	}
	return p.Receipt.OperationID == p.CurrentOperationID &&
		p.Receipt.TargetFingerprint == p.CurrentTargetFingerprint &&
		p.Receipt.InputManifestDigest == p.CurrentInputManifestDigest &&
		p.Receipt.OutputManifestDigest == p.CurrentOutputManifestDigest &&
		p.Receipt.ArtifactArchiveDigest == p.CurrentArtifactArchiveDigest &&
		p.Receipt.ExecutableDigest == p.CurrentExecutableDigest
}

type RequiredMigrationCheckpoints struct {
	CacheAccountingRevision2Complete bool
	QuotaSnapshotLifecycleComplete   bool
}

func (c RequiredMigrationCheckpoints) Complete() bool {
	return c.CacheAccountingRevision2Complete && c.QuotaSnapshotLifecycleComplete
}
