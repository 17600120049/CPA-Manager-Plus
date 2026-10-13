package legacyartifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var (
	ErrBundleMismatch   = errors.New("output bundle contents do not match execution receipt manifest digest")
	ErrOperationRevoked = errors.New("operation was revoked in private journal")
)

type ValidateOptions struct {
	Receipt      *ExecutionReceipt
	ReceiptPath  string
	BundleLayout maintenancebundle.Layout
	Authority    *Authority
	Journal      *Journal
}

type ValidationResult struct {
	Valid                bool               `json:"valid"`
	Status               string             `json:"status"`
	OperationID          string             `json:"operationId"`
	Release              string             `json:"release"`
	SourceCommit         string             `json:"sourceCommit"`
	Asset                string             `json:"asset"`
	ExecutableSHA256     string             `json:"executableSHA256"`
	InputManifestDigest  string             `json:"inputManifestDigest"`
	OutputManifestDigest string             `json:"outputManifestDigest"`
	InputFamily          string             `json:"inputFamily"`
	OutputFamily         string             `json:"outputFamily"`
	CheckpointEvidence   CheckpointEvidence `json:"checkpointEvidence"`
	ProcessEvidence      ProcessEvidence    `json:"processEvidence"`
}

// ValidateHop validates that an execution receipt was genuinely issued by the Manager's
// private authority, binds exactly to the target bundle layout without any changes or copies,
// verifies that the operation was not revoked, and re-inspects that required migration
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

	// Resolve symlinks in BundleLayout paths
	resolvePath := func(p string) string {
		if p == "" {
			return ""
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real
		} else if parent, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			return filepath.Join(parent, filepath.Base(p))
		}
		return p
	}
	opts.BundleLayout.DatabasePath = resolvePath(opts.BundleLayout.DatabasePath)
	opts.BundleLayout.DataKeyPath = resolvePath(opts.BundleLayout.DataKeyPath)
	opts.BundleLayout.ConfigPath = resolvePath(opts.BundleLayout.ConfigPath)
	opts.BundleLayout.ArchivesPath = resolvePath(opts.BundleLayout.ArchivesPath)

	// 1. Authenticity verification: check HMAC signature
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

	// 3. Process execution evidence verification
	if receipt.ProcessEvidence.ExitCode != 0 || receipt.ProcessEvidence.DurationMS <= 0 {
		return nil, ErrProcessExecutionFailed
	}

	// 4. Anti-replay and journal verification if journal is configured
	if opts.Journal != nil {
		rec, ok := opts.Journal.GetOperation(receipt.OperationID)
		if !ok {
			return nil, ErrOperationNotFound
		}
		if rec.Revoked {
			return nil, ErrOperationRevoked
		}
		if rec.OutputManifestDigest != receipt.OutputManifestDigest {
			return nil, ErrBundleMismatch
		}
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
	}, nil
}
