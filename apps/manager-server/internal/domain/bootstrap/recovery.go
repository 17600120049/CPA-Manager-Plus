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
