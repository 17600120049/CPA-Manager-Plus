package bootstrap

import (
	"fmt"
	"regexp"
)

type OperationID string
type ContentDigest string

var (
	operationIDPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,127}$`)
	contentDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func validOperationID(id OperationID) bool    { return operationIDPattern.MatchString(string(id)) }
func validContentDigest(d ContentDigest) bool { return contentDigestPattern.MatchString(string(d)) }

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
		"target fingerprint":      r.TargetFingerprint,
		"input manifest digest":   r.InputManifestDigest,
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

type TrustedReceiptPreconditions struct {
	Receipt                      ReceiptBinding
	CurrentOperationID           OperationID
	CurrentTargetFingerprint     ContentDigest
	CurrentInputManifestDigest   ContentDigest
	CurrentOutputManifestDigest  ContentDigest
	CurrentArtifactArchiveDigest ContentDigest
	CurrentExecutableDigest      ContentDigest
	ManagerAuthenticityVerified  bool
	JournalRecordPresent         bool
	JournalRevoked               bool
	ProcessExited                bool
	ProcessExitCode              int
	ProcessHandlesReleased       bool
	ProcessReaped                bool
}

func CanAcceptTrustedHopReceipt(p TrustedReceiptPreconditions) bool {
	if p.Receipt.Validate() != nil ||
		!p.ManagerAuthenticityVerified || !p.JournalRecordPresent || p.JournalRevoked ||
		!p.ProcessExited || p.ProcessExitCode != 0 || !p.ProcessHandlesReleased || !p.ProcessReaped {
		return false
	}
	return p.Receipt.OperationID == p.CurrentOperationID &&
		p.Receipt.TargetFingerprint == p.CurrentTargetFingerprint &&
		p.Receipt.InputManifestDigest == p.CurrentInputManifestDigest &&
		p.Receipt.OutputManifestDigest == p.CurrentOutputManifestDigest &&
		p.Receipt.ArtifactArchiveDigest == p.CurrentArtifactArchiveDigest &&
		p.Receipt.ExecutableDigest == p.CurrentExecutableDigest
}

type CheckpointState string

const (
	CheckpointComplete        CheckpointState = "complete"
	CheckpointPending         CheckpointState = "pending"
	CheckpointRunning         CheckpointState = "running"
	CheckpointApplying        CheckpointState = "applying"
	CheckpointClearing        CheckpointState = "clearing"
	CheckpointFailed          CheckpointState = "failed"
	CheckpointOfflineRequired CheckpointState = "offline_required"
)

func validCheckpointState(s CheckpointState) bool {
	switch s {
	case CheckpointComplete, CheckpointPending, CheckpointRunning, CheckpointApplying,
		CheckpointClearing, CheckpointFailed, CheckpointOfflineRequired:
		return true
	default:
		return false
	}
}

type RequiredMigrationCheckpoints struct {
	CacheAccounting CheckpointState
	QuotaSnapshot   CheckpointState
}

func (c RequiredMigrationCheckpoints) Validate() error {
	if !validCheckpointState(c.CacheAccounting) || !validCheckpointState(c.QuotaSnapshot) {
		return fmt.Errorf("invalid required migration checkpoint state")
	}
	return nil
}

func (c RequiredMigrationCheckpoints) Complete() bool {
	return c.Validate() == nil && c.CacheAccounting == CheckpointComplete && c.QuotaSnapshot == CheckpointComplete
}

func (c RequiredMigrationCheckpoints) RecoveryReason() (RecoveryReason, bool) {
	if c.Validate() != nil || c.Complete() {
		return "", false
	}
	if c.CacheAccounting == CheckpointOfflineRequired || c.QuotaSnapshot == CheckpointOfflineRequired {
		return OfflineActionRequired, true
	}
	return MigrationIncomplete, true
}
