package legacyartifact

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f := flag.NewFlagSet("legacy-writer-artifact", flag.ContinueOnError)
	// The standard parser echoes invalid values. Keep diagnostic inputs out of
	// logs, just as artifact-read failures omit paths and payloads.
	f.SetOutput(io.Discard)
	f.Usage = func() {
		_, _ = io.WriteString(stderr, "Usage: cpa-manager-plus legacy-writer-artifact --archive PATH --asset NAME [--timeout DURATION]\n       --offline-cleanup --archive PATH --asset NAME --input-bundle PATH --input-digest SHA256 --operation-workspace NEW_PATH [--timeout DURATION]\n       --inspect-offline-operation ID --operation-workspace PATH\n       --recover-offline-operation ID --operation-workspace PATH\n       --verify-receipt PATH --bundle PATH\n       --execute-hop --archive PATH --asset NAME --input-bundle PATH --output-bundle PATH\nOffline cleanup observes the fixed early-dispatch command on a private copy (Linux/Darwin AMD64 only). Diagnostic output is not authority. Full hop execution and trusted receipt validation remain refused on all platforms. Never authorizes upgrade.\n")
	}
	archive := f.String("archive", "", "local published native archive (read-only)")
	asset := f.String("asset", "", "exact published v1.14.4 native asset name")
	timeout := f.Duration("timeout", 10*time.Second, "verification time budget")

	verifyReceipt := f.String("verify-receipt", "", "unsupported until Manager owner authority is qualified")
	f.String("bundle", "", "associated bundle directory to verify against receipt")

	executeHop := f.Bool("execute-hop", false, "unsupported until offline writer execution is qualified")
	inputBundle := f.String("input-bundle", "", "input sealed maintenance bundle directory")
	f.String("output-bundle", "", "output bundle directory")
	offlineCleanup := f.Bool("offline-cleanup", false, "run fixed cleanup-derived on a private copy; no hop authority")
	inputDigest := f.String("input-digest", "", "expected complete input manifest digest")
	workspace := f.String("operation-workspace", "", "new private operation workspace; parent must be 0700")
	inspectID := f.String("inspect-offline-operation", "", "inspect completed operation diagnostics; never trusted")
	recoverID := f.String("recover-offline-operation", "", "mark an abandoned operation interrupted; never rerun")

	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errors.New("invalid legacy artifact arguments; use --help")
	}
	if f.NArg() != 0 || *timeout <= 0 {
		return errors.New("invalid arguments or non-positive timeout; no positional arguments")
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	modes := 0
	for _, selected := range []bool{*executeHop, *verifyReceipt != "", *offlineCleanup, *inspectID != "", *recoverID != ""} {
		if selected {
			modes++
		}
	}
	if modes > 1 {
		return errors.New("select one legacy artifact operation mode")
	}

	if *executeHop {
		return legacyartifact.ErrExecutionNotQualified
	}

	if *verifyReceipt != "" {
		return legacyartifact.ErrValidationNotQualified
	}
	if *offlineCleanup {
		report, err := legacyartifact.RunOfflineCleanup(ctx, legacyartifact.OfflineCleanupOptions{ArchivePath: *archive, Asset: *asset, InputBundle: *inputBundle, ExpectedInputDigest: *inputDigest, Workspace: *workspace, Timeout: *timeout})
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(report)
	}
	if *inspectID != "" {
		report, err := legacyartifact.InspectOfflineCleanup(ctx, *workspace, *inspectID)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(report)
	}
	if *recoverID != "" {
		if err := legacyartifact.RecoverOfflineCleanup(*workspace, *recoverID); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(struct {
			Status            string `json:"status"`
			OperationID       string `json:"operationId"`
			UpgradeAuthorized bool   `json:"upgradeAuthorized"`
		}{Status: "interrupted-offline-operation", OperationID: *recoverID})
	}

	// Mode 3: verify artifact archive bytes (existing behavior)
	if *archive == "" || *asset == "" {
		return errors.New("explicit --archive, --asset and positive --timeout are required; no positional arguments")
	}
	report, err := legacyartifact.Verify(ctx, *archive, *asset)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(report)
}

func IsHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }
