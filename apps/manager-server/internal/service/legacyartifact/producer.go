package legacyartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyprofile"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var (
	ErrExecutionNotQualified  = errors.New("legacy writer execution is not qualified; offline isolation, real published execution and Windows shutdown remain pending")
	ErrInvalidSource          = errors.New("input source is invalid, corrupt, or unrecognized")
	ErrDowngradeProhibited    = errors.New("newer or native source cannot be downgraded")
	ErrExecutableMismatch     = errors.New("executable bytes digest does not match verified release pin")
	ErrProcessExecutionFailed = errors.New("process execution failed or did not reap cleanly")
	ErrInvalidHopOptions      = errors.New("invalid forward-hop producer options")
	ErrJournalRequired        = errors.New("private journal is mandatory for forward-hop execution")
	ErrInsecureWorkspace      = errors.New("output root workspace is invalid, overlaps with input, or is not empty")
)

type commandRunner func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error)

type HopOptions struct {
	ArchivePath string
	Asset       string
	InputLayout maintenancebundle.Layout
	OutputRoot  string
	Authority   *Authority
	Journal     *Journal
	Timeout     time.Duration
}

// DefaultCommandRunner refuses execution on every platform until an offline
// exact v1.14.4 writer and shutdown/reap behavior have been qualified.
// No process is started and no bundle or authority files are touched.
func DefaultCommandRunner(context.Context, string, maintenancebundle.Layout) (ProcessEvidence, error) {
	return ProcessEvidence{}, ErrExecutionNotQualified
}

// ExecuteHop is unavailable in this producer/validator foundation.
// Artifact authenticity alone does not qualify full-server execution as a writer.
func ExecuteHop(context.Context, HopOptions) (*ExecutionReceipt, error) {
	return nil, ErrExecutionNotQualified
}

// Package-private foundation seam exercised by this package's tests only.
func executeHopInternal(ctx context.Context, opts HopOptions, testExePath string, testRunner commandRunner) (*ExecutionReceipt, error) {
	if opts.Authority == nil || opts.Asset == "" || opts.OutputRoot == "" || testRunner == nil {
		return nil, ErrInvalidHopOptions
	}
	if opts.Journal == nil {
		return nil, ErrJournalRequired
	}
	if _, ok := nativePins[opts.Asset]; !ok {
		return nil, ErrUnverified
	}

	var err error
	opts.OutputRoot, err = filepath.Abs(opts.OutputRoot)
	if err != nil {
		return nil, errors.Join(ErrInsecureWorkspace, err)
	}
	if realOut, err := filepath.EvalSymlinks(opts.OutputRoot); err == nil {
		opts.OutputRoot = realOut
	} else if parent, err := filepath.EvalSymlinks(filepath.Dir(opts.OutputRoot)); err == nil {
		opts.OutputRoot = filepath.Join(parent, filepath.Base(opts.OutputRoot))
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	opts.InputLayout, err = normalizeHopLayout(opts.InputLayout)
	if err != nil {
		return nil, errors.Join(ErrInvalidSource, err)
	}

	// Validate output workspace isolation
	inDbDir := filepath.Dir(opts.InputLayout.DatabasePath)
	if opts.OutputRoot == inDbDir || opts.OutputRoot == opts.InputLayout.DatabasePath {
		return nil, errors.Join(ErrInsecureWorkspace, errors.New("output root cannot overlap with input source directory"))
	}
	if rel, err := filepath.Rel(opts.OutputRoot, inDbDir); err == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
		return nil, errors.Join(ErrInsecureWorkspace, errors.New("output root cannot be a parent of input source"))
	}
	if rel, err := filepath.Rel(inDbDir, opts.OutputRoot); err == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
		return nil, errors.Join(ErrInsecureWorkspace, errors.New("output root cannot be inside input source directory"))
	}
	if entries, err := os.ReadDir(opts.OutputRoot); err == nil && len(entries) > 0 {
		return nil, errors.Join(ErrInsecureWorkspace, errors.New("output directory already exists and is not empty"))
	}

	// 1. Validate Input Source Pre-conditions
	inputPaths := sqlite.InspectionPaths{
		Database: opts.InputLayout.DatabasePath,
		Config:   opts.InputLayout.ConfigPath,
		DataKey:  opts.InputLayout.DataKeyPath,
		Archives: opts.InputLayout.ArchivesPath,
	}
	inputObserved, err := sqlite.InspectLegacySource(ctx, inputPaths, sqlite.InspectionOptions{SourceStopped: true})
	if err != nil || len(inputObserved.Issues) > 0 || inputObserved.SQLite != "observed" {
		return nil, ErrInvalidSource
	}
	// Check for native v2 or newer schema: prohibit downgrade
	for _, table := range inputObserved.Tables {
		if strings.HasPrefix(table.Name, "gateway_") {
			return nil, ErrDowngradeProhibited
		}
	}
	inputProfile := legacyprofile.Classify(inputObserved)
	if inputProfile.Status != "recognized" {
		return nil, ErrInvalidSource
	}

	// Calculate Input Manifest Digest
	inputManifest, err := maintenancebundle.Inspect(ctx, opts.InputLayout)
	if err != nil {
		return nil, errors.Join(ErrInvalidSource, err)
	}
	inputManifestDigest, err := ComputeManifestDigest(inputManifest)
	if err != nil {
		return nil, err
	}

	// 2. Resolve and Validate Executable
	// Authenticated extraction is retained for future qualification fixtures.
	// The production entry point remains disabled.
	var exePath string
	var expectedExeDigest string
	if opts.ArchivePath != "" {
		tempBinDir, err := os.MkdirTemp("", "cpamp-hop-bin-*")
		if err != nil {
			return nil, fmt.Errorf("create temporary binary directory: %w", err)
		}
		defer os.RemoveAll(tempBinDir)

		binName := "cpa-manager-plus"
		if strings.HasSuffix(opts.Asset, ".zip") {
			binName += ".exe"
		}
		tempExe := filepath.Join(tempBinDir, binName)
		extractedDigest, _, err := ExtractExecutable(ctx, opts.ArchivePath, opts.Asset, tempExe)
		if err != nil {
			return nil, err
		}
		exePath = tempExe
		expectedExeDigest = extractedDigest
	} else if testExePath != "" && testRunner != nil {
		// Package-private test fixture injection; no public execution path uses it.
		exePath = testExePath
	} else {
		return nil, errors.Join(ErrInvalidHopOptions, errors.New("archive path is required for forward hop"))
	}

	// Verify executable bytes on disk
	exeBytes, err := os.ReadFile(exePath)
	if err != nil {
		return nil, ErrExecutableMismatch
	}
	actualExeDigest := hex.EncodeToString(sha256Hex(exeBytes))
	if expectedExeDigest != "" && actualExeDigest != expectedExeDigest {
		return nil, ErrExecutableMismatch
	}

	// 3. Staging & Quiescence / Consistency Revalidation
	outputLayout := maintenancebundle.BundleLayout(opts.OutputRoot)
	if err := stageOutputBundle(ctx, opts.InputLayout, outputLayout); err != nil {
		return nil, err
	}
	if err := maintenancebundle.Revalidate(ctx, opts.InputLayout, inputManifest); err != nil {
		return nil, errors.Join(ErrInvalidSource, fmt.Errorf("input source changed during staging: %w", err))
	}
	if err := maintenancebundle.Revalidate(ctx, outputLayout, inputManifest); err != nil {
		return nil, errors.Join(ErrInvalidSource, fmt.Errorf("staged output bundle does not match input manifest: %w", err))
	}

	// 4. Test-only simulated operation; no production runner is available.
	processEvidence, err := testRunner(ctx, exePath, outputLayout)
	if err != nil || ctx.Err() != nil {
		return nil, errors.Join(ErrProcessExecutionFailed, err, ctx.Err())
	}
	if err := processEvidence.Validate(); err != nil {
		return nil, err
	}

	// 5. Verify Required Checkpoints Post-condition
	checkpointEvidence, err := InspectCheckpoints(ctx, outputLayout.DatabasePath)
	if err != nil {
		return nil, err
	}

	// Verify Output Schema
	outputObserved, err := sqlite.InspectLegacySource(ctx, sqlite.InspectionPaths{Database: outputLayout.DatabasePath}, sqlite.InspectionOptions{SourceStopped: true})
	if err != nil || len(outputObserved.Issues) > 0 {
		return nil, ErrInvalidSource
	}
	outputProfile := legacyprofile.Classify(outputObserved)
	if outputProfile.Status != "recognized" {
		return nil, ErrInvalidSource
	}

	// 6. Seal Output Bundle and Compute Digest
	outputManifest, err := maintenancebundle.Inspect(ctx, outputLayout)
	if err != nil {
		return nil, err
	}
	outputManifestDigest, err := ComputeManifestDigest(outputManifest)
	if err != nil {
		return nil, err
	}

	// 7. Generate and Sign Receipt
	opID, err := GenerateOperationID()
	if err != nil {
		return nil, err
	}
	receipt := &ExecutionReceipt{
		Version:              ReceiptVersion,
		OperationID:          opID,
		Release:              Release,
		SourceCommit:         SourceCommit,
		Asset:                opts.Asset,
		ExecutableSHA256:     actualExeDigest,
		InputManifestDigest:  inputManifestDigest,
		OutputManifestDigest: outputManifestDigest,
		InputFamily:          inputProfile.Family,
		OutputFamily:         outputProfile.Family,
		ProcessEvidence:      processEvidence,
		CheckpointEvidence:   checkpointEvidence,
		IssuedAtMS:           time.Now().UnixMilli(),
	}
	if err := receipt.Sign(opts.Authority); err != nil {
		return nil, err
	}

	// 8. Commit into Private Journal atomically (mandatory)
	rec := OperationRecord{
		OperationID:          opID,
		Release:              Release,
		Asset:                opts.Asset,
		ExecutableSHA256:     actualExeDigest,
		InputManifestDigest:  inputManifestDigest,
		OutputManifestDigest: outputManifestDigest,
		IssuedAtMS:           receipt.IssuedAtMS,
	}
	if err := opts.Journal.RecordOperation(rec); err != nil {
		return nil, fmt.Errorf("persist operation in journal: %w", err)
	}

	return receipt, nil
}

func sha256Hex(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// Resolve before staging so paths agree even if a future qualified child changes cwd.
func normalizeHopLayout(layout maintenancebundle.Layout) (maintenancebundle.Layout, error) {
	for _, p := range []*string{&layout.DatabasePath, &layout.DataKeyPath, &layout.ConfigPath, &layout.ArchivesPath} {
		if *p == "" {
			continue
		}
		absolute, err := filepath.Abs(*p)
		if err != nil {
			return maintenancebundle.Layout{}, err
		}
		if real, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = real
		} else if parent, err := filepath.EvalSymlinks(filepath.Dir(absolute)); err == nil {
			absolute = filepath.Join(parent, filepath.Base(absolute))
		}
		*p = absolute
	}
	return layout, nil
}

func stageOutputBundle(ctx context.Context, in, out maintenancebundle.Layout) error {
	if err := os.MkdirAll(filepath.Dir(out.DatabasePath), 0o700); err != nil {
		return err
	}
	// copy database
	if err := copyFileIfExists(in.DatabasePath, out.DatabasePath); err != nil {
		return err
	}
	// copy wal, shm, journal if present
	for _, ext := range []string{"-wal", "-shm", "-journal"} {
		if err := copyFileIfExists(in.DatabasePath+ext, out.DatabasePath+ext); err != nil {
			return err
		}
	}
	// copy data-key
	if err := copyFileIfExists(in.DataKeyPath, out.DataKeyPath); err != nil {
		return err
	}
	// copy config if present
	if in.ConfigPath != "" {
		if err := copyFileIfExists(in.ConfigPath, out.ConfigPath); err != nil {
			return err
		}
	}
	// copy archives if present
	if in.ArchivesPath != "" {
		if info, err := os.Stat(in.ArchivesPath); err == nil && info.IsDir() {
			if err := copyDir(in.ArchivesPath, out.ArchivesPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFileIfExists(src, dst string) error {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source %s is not a regular file", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFileIfExists(path, target)
	})
}
