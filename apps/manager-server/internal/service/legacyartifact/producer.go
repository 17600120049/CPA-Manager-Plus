package legacyartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var (
	ErrInvalidSource          = errors.New("input source is invalid, corrupt, or unrecognized")
	ErrDowngradeProhibited    = errors.New("newer or native source cannot be downgraded")
	ErrExecutableMismatch     = errors.New("executable bytes digest does not match verified release pin")
	ErrProcessExecutionFailed = errors.New("process execution failed or did not reap cleanly")
	ErrInvalidHopOptions      = errors.New("invalid forward-hop producer options")
)

type CommandRunner func(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error)

type HopOptions struct {
	ArchivePath    string
	Asset          string
	ExecutablePath string
	InputLayout    maintenancebundle.Layout
	OutputRoot     string
	Authority      *Authority
	Journal        *Journal
	Timeout        time.Duration
	CommandRunner  CommandRunner
	// ExpectedExecutableSHA256 allows specifying or overriding verified digest in tests
	ExpectedExecutableSHA256 string
}

// DefaultCommandRunner executes the binary with controlled environment pointing
// to outputLayout.
func DefaultCommandRunner(ctx context.Context, exePath string, outputLayout maintenancebundle.Layout) (ProcessEvidence, error) {
	cmd := exec.CommandContext(ctx, exePath)
	cmd.Dir = filepath.Dir(outputLayout.DatabasePath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("CPA_MANAGER_DATABASE=%s", outputLayout.DatabasePath),
		fmt.Sprintf("CPA_MANAGER_DATA_KEY_PATH=%s", outputLayout.DataKeyPath),
		fmt.Sprintf("CPA_MANAGER_CONFIG=%s", outputLayout.ConfigPath),
	)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ProcessEvidence{}, fmt.Errorf("start process: %w", err)
	}
	pid := cmd.Process.Pid
	err := cmd.Wait()
	finished := time.Now()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return ProcessEvidence{}, err
		}
	}
	dur := finished.Sub(start).Milliseconds()
	if dur <= 0 {
		dur = 1
	}
	return ProcessEvidence{
		PID:          pid,
		ExitCode:     exitCode,
		StartedAtMS:  start.UnixMilli(),
		FinishedAtMS: finished.UnixMilli(),
		DurationMS:   dur,
	}, nil
}

// ExecuteHop performs a controlled, Manager-owned forward-hop operation.
// It verifies the input bundle, ensures it is an eligible legacy source (prohibiting
// downgrade of newer sources), executes the verified v1.14.4 executable in an isolated
// output workspace, validates post-condition migration checkpoints, seals the output
// bundle, and issues an operation-signed ExecutionReceipt.
func ExecuteHop(ctx context.Context, opts HopOptions) (*ExecutionReceipt, error) {
	if opts.Authority == nil || opts.Asset == "" || opts.OutputRoot == "" {
		return nil, ErrInvalidHopOptions
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

	// Resolve symlinks in InputLayout if any
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
	opts.InputLayout.DatabasePath = resolvePath(opts.InputLayout.DatabasePath)
	opts.InputLayout.DataKeyPath = resolvePath(opts.InputLayout.DataKeyPath)
	opts.InputLayout.ConfigPath = resolvePath(opts.InputLayout.ConfigPath)
	opts.InputLayout.ArchivesPath = resolvePath(opts.InputLayout.ArchivesPath)

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
		if table.Name == "gateway_api_key_identities" || table.Name == "gateway_credential_identities" {
			return nil, ErrDowngradeProhibited
		}
	}
	inputProfile := classifySourceProfile(inputObserved)
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
	exePath := opts.ExecutablePath
	expectedExeDigest := opts.ExpectedExecutableSHA256
	if exePath == "" && opts.ArchivePath != "" {
		tempExe := filepath.Join(opts.OutputRoot, ".tmp-bin", "cpa-manager-plus")
		extractedDigest, _, err := ExtractExecutable(ctx, opts.ArchivePath, opts.Asset, tempExe)
		if err != nil {
			return nil, err
		}
		exePath = tempExe
		expectedExeDigest = extractedDigest
	}
	if exePath == "" {
		return nil, ErrInvalidHopOptions
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

	// 3. Staging: Prepare Isolated Output Bundle Copy
	outputLayout := maintenancebundle.BundleLayout(opts.OutputRoot)
	if err := stageOutputBundle(ctx, opts.InputLayout, outputLayout); err != nil {
		return nil, err
	}

	// 4. Controlled Execution of the Process
	runner := opts.CommandRunner
	if runner == nil {
		runner = DefaultCommandRunner
	}
	processEvidence, err := runner(ctx, exePath, outputLayout)
	if err != nil || processEvidence.ExitCode != 0 || ctx.Err() != nil {
		return nil, errors.Join(ErrProcessExecutionFailed, err)
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
	outputProfile := classifySourceProfile(outputObserved)
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

	// 8. Record in Private Journal if configured
	if opts.Journal != nil {
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
			return nil, err
		}
	}

	return receipt, nil
}

func sha256Hex(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

type ProfileResult struct {
	Status string
	Family string
}

func classifySourceProfile(source sqlite.LegacyInspection) ProfileResult {
	tables := make(map[string]bool)
	for _, t := range source.Tables {
		tables[t.Name] = true
	}
	if !tables["settings"] || !tables["usage_events"] {
		return ProfileResult{Status: "unknown"}
	}
	// Check for LegacySchemaC
	if tables["usage_archive_runs"] && tables["usage_archive_segments"] &&
		tables["usage_archive_event_refs"] && tables["usage_archive_deleted_coverage_daily"] &&
		tables["usage_maintenance_locks"] {
		return ProfileResult{Status: "recognized", Family: "LegacySchemaC"}
	}
	// Check for LegacySchemaB vs A based on columns of usage_events
	for _, t := range source.Tables {
		if t.Name == "usage_events" {
			hasResponseModel := false
			for _, col := range t.Columns {
				if col.Name == "response_model" {
					hasResponseModel = true
					break
				}
			}
			if hasResponseModel {
				return ProfileResult{Status: "recognized", Family: "LegacySchemaB"}
			}
		}
	}
	return ProfileResult{Status: "recognized", Family: "LegacySchemaA"}
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
