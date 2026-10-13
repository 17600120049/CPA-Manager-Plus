package legacyartifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var (
	ErrBundleMismatch   = errors.New("output bundle contents do not match execution receipt manifest digest")
	ErrOperationRevoked = errors.New("operation was revoked in private journal")
	ErrRecordMismatch   = errors.New("journal operation record does not match receipt authority fields")
)

type ValidateOptions struct {
	Receipt      *ExecutionReceipt
	ReceiptPath  string
	BundleLayout maintenancebundle.Layout
	Authority    *Authority
	Journal      *Journal
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

// ValidateHop validates that an execution receipt was genuinely issued by the Manager's
// private authority and matches the current output bundle contents,
// verifies that the operation was committed into the private journal with identical authority
// fields, ensures that the operation was not revoked, and re-inspects that required migration
// checkpoints are satisfied.
func ValidateHop(ctx context.Context, opts ValidateOptions) (*ValidationResult, error) {
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
	if receipt == nil || opts.Authority == nil {
		return nil, ErrInvalidReceipt
	}
	if opts.Journal == nil {
		return nil, ErrJournalRequired
	}

	var err error
	opts.BundleLayout, err = normalizeHopLayout(opts.BundleLayout)
	if err != nil {
		return nil, errors.Join(ErrBundleMismatch, err)
	}

	// 1. Authenticity verification: check HMAC signature with constant time comparison
	if err := receipt.VerifyAuthenticity(opts.Authority); err != nil {
		return nil, err
	}

	// 2. Artifact and target binding verification
	if receipt.Release != Release || receipt.SourceCommit != SourceCommit {
		return nil, ErrInvalidReceipt
	}
	if _, ok := nativePins[receipt.Asset]; !ok {
		return nil, ErrUnverified
	}
	if strings.TrimSpace(receipt.ExecutableSHA256) == "" {
		return nil, ErrExecutableMismatch
	}

	// 3. Process execution evidence verification
	if receipt.ProcessEvidence.ExitCode != 0 || receipt.ProcessEvidence.DurationMS <= 0 {
		return nil, ErrProcessExecutionFailed
	}

	// 4. Anti-replay and journal verification (mandatory)
	rec, ok, err := opts.Journal.GetOperation(receipt.OperationID)
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
	if rec.OperationID != receipt.OperationID ||
		rec.Release != receipt.Release ||
		rec.Asset != receipt.Asset ||
		rec.ExecutableSHA256 != receipt.ExecutableSHA256 ||
		rec.InputManifestDigest != receipt.InputManifestDigest ||
		rec.OutputManifestDigest != receipt.OutputManifestDigest ||
		rec.IssuedAtMS != receipt.IssuedAtMS {
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

	// 6. Re-inspect Checkpoints on the actual output database
	checkpoints, err := InspectCheckpoints(ctx, opts.BundleLayout.DatabasePath)
	if err != nil {
		return nil, err
	}

	evidence := &ValidatedHopEvidence{
		operationID:          receipt.OperationID,
		release:              receipt.Release,
		sourceCommit:         receipt.SourceCommit,
		asset:                receipt.Asset,
		executableSHA256:     receipt.ExecutableSHA256,
		inputManifestDigest:  receipt.InputManifestDigest,
		outputManifestDigest: receipt.OutputManifestDigest,
		inputFamily:          receipt.InputFamily,
		outputFamily:         receipt.OutputFamily,
		processEvidence:      receipt.ProcessEvidence,
		checkpointEvidence:   checkpoints,
		validated:            true,
	}

	return &ValidationResult{
		Valid:                true,
		Status:               "verified-hop-evidence",
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
		Evidence:             evidence,
	}, nil
}
