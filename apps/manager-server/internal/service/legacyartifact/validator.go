package legacyartifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyprofile"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var (
	ErrBundleMismatch         = errors.New("output bundle contents do not match execution receipt manifest digest")
	ErrOperationRevoked       = errors.New("operation was revoked in private journal")
	ErrRecordMismatch         = errors.New("journal operation record does not match receipt authority fields")
	ErrValidationNotQualified = errors.New("trusted hop validation is not qualified; Manager owner authority bootstrap and writer qualification remain pending")
)

// ValidateOptions has no caller-supplied authority, journal or owner directory.
type ValidateOptions struct {
	Receipt      *ExecutionReceipt
	ReceiptPath  string
	BundleLayout maintenancebundle.Layout
}

// ValidateHop cannot mint trusted evidence until the Manager owner boundary is
// qualified. Private HMAC files selected by a caller cannot establish that owner.
// Refuse before reading receipts, bundles, keys or journals.
func ValidateHop(context.Context, ValidateOptions) (*ValidationResult, error) {
	return nil, ErrValidationNotQualified
}

type validateOptions struct {
	Receipt      *ExecutionReceipt
	ReceiptPath  string
	BundleLayout maintenancebundle.Layout
	authority    *authority
	journal      *journal
}

type ValidationResult struct {
	Valid                bool                  `json:"valid"`
	Status               string                `json:"status"`
	OperationID          string                `json:"operationId"`
	Release              string                `json:"release"`
	SourceCommit         string                `json:"sourceCommit"`
	Asset                string                `json:"asset"`
	ExecutableSHA256     string                `json:"executableSHA256"`
	InputManifestDigest  string                `json:"inputManifestDigest"`
	OutputManifestDigest string                `json:"outputManifestDigest"`
	InputFamily          string                `json:"inputFamily"`
	OutputFamily         string                `json:"outputFamily"`
	CheckpointEvidence   CheckpointEvidence    `json:"checkpointEvidence"`
	ProcessEvidence      ProcessEvidence       `json:"processEvidence"`
	Evidence             *ValidatedHopEvidence `json:"-"`
}

// validateReceiptInternal checks receipt consistency against private foundation
// state, published executable pins and the current bundle. It provides diagnostics
// only: file permissions and HMAC authenticity do not qualify Manager ownership,
// actual execution or a provenance capability.
func validateReceiptInternal(ctx context.Context, opts validateOptions) (*ValidationResult, error) {
	receipt := opts.Receipt
	if receipt == nil && opts.ReceiptPath != "" {
		data, err := os.ReadFile(opts.ReceiptPath)
		if err != nil {
			return nil, errors.Join(ErrInvalidReceipt, err)
		}
		var r ExecutionReceipt
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, errors.Join(ErrInvalidReceipt, err)
		}
		receipt = &r
	}
	if receipt == nil || opts.authority == nil {
		return nil, ErrInvalidReceipt
	}
	if opts.journal == nil {
		return nil, ErrJournalRequired
	}
	if opts.journal.Path() == "" {
		return nil, ErrJournalRequired
	}

	var err error
	opts.BundleLayout, err = normalizeHopLayout(opts.BundleLayout)
	if err != nil {
		return nil, errors.Join(ErrBundleMismatch, err)
	}

	// 1. Authenticity verification: check HMAC signature with constant time comparison
	if err := receipt.verifyAuthenticity(opts.authority); err != nil {
		return nil, err
	}

	// 2. Artifact and target binding verification
	if receipt.Release != Release || receipt.SourceCommit != SourceCommit {
		return nil, ErrInvalidReceipt
	}
	pin, ok := nativePins[receipt.Asset]
	if !ok {
		return nil, ErrUnverified
	}
	if receipt.ExecutableSHA256 != pin.ExecutableSHA256 {
		return nil, ErrExecutableMismatch
	}

	// 3. Process execution evidence verification
	if err := receipt.validateLifecycle(); err != nil {
		return nil, err
	}

	// 4. Anti-replay and journal verification (mandatory)
	rec, ok, err := opts.journal.getOperation(receipt.OperationID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrOperationNotFound
	}
	if rec.Revoked {
		return nil, ErrOperationRevoked
	}
	// Verify that ALL authority-critical fields match the journal record exactly
	canonicalDigest, err := receipt.canonicalDigest()
	if err != nil {
		return nil, err
	}
	if rec.OperationID != receipt.OperationID || rec.ReceiptVersion != receipt.Version || rec.CanonicalReceiptSHA256 != canonicalDigest {
		return nil, ErrRecordMismatch
	}

	// 5. Output Bundle Binding Verification:
	// Re-inspect the actual bundle on disk and ensure its current manifest digest matches
	currentManifest, err := maintenancebundle.Inspect(ctx, opts.BundleLayout)
	if err != nil {
		return nil, errors.Join(ErrBundleMismatch, err)
	}
	currentDigest, err := ComputeManifestDigest(currentManifest)
	if err != nil || currentDigest != receipt.OutputManifestDigest {
		return nil, ErrBundleMismatch
	}

	// 6. Reapply current semantic rules to the output. Authenticity does not
	// grandfather a receipt whose schema is no longer recognized.
	observed, err := sqlite.InspectLegacySource(ctx, sqlite.InspectionPaths{
		Database: opts.BundleLayout.DatabasePath,
		DataKey:  opts.BundleLayout.DataKeyPath,
		Config:   opts.BundleLayout.ConfigPath,
		Archives: opts.BundleLayout.ArchivesPath,
	}, sqlite.InspectionOptions{SourceStopped: true})
	if err != nil {
		return nil, errors.Join(ErrInvalidSource, err)
	}
	profile := legacyprofile.Classify(observed)
	if profile.Status != "recognized" || profile.Family != receipt.OutputFamily {
		return nil, ErrInvalidSource
	}
	checkpoints, err := checkpointsFromInspection(observed)
	if err != nil {
		return nil, err
	}
	if checkpoints != receipt.CheckpointEvidence {
		return nil, ErrCheckpointFailed
	}
	if err := maintenancebundle.Revalidate(ctx, opts.BundleLayout, currentManifest); err != nil {
		return nil, errors.Join(ErrBundleMismatch, err)
	}

	return &ValidationResult{
		Valid:                true,
		Status:               "verified-receipt-only",
		OperationID:          receipt.OperationID,
		Release:              receipt.Release,
		SourceCommit:         receipt.SourceCommit,
		Asset:                receipt.Asset,
		ExecutableSHA256:     receipt.ExecutableSHA256,
		InputManifestDigest:  receipt.InputManifestDigest,
		OutputManifestDigest: receipt.OutputManifestDigest,
		InputFamily:          receipt.InputFamily,
		OutputFamily:         receipt.OutputFamily,
		CheckpointEvidence:   checkpoints,
		ProcessEvidence:      receipt.ProcessEvidence,
	}, nil
}
