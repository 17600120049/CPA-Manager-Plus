package legacypreflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

var ErrBlocked = errors.New("source inspection is blocked; see JSON report")

// Run only resolves source locations and inspects them. It never dispatches the
// server, creates defaults, generates a key or opens the writable Store.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return run(ctx, args, os.Stdin, stdout, stderr)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("legacy-preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("db", "", "explicit source Manager SQLite path (required)")
	configFile := flags.String("config", "", "existing source config; omitted means not-inspected")
	dataKey := flags.String("data-key", "", "source key path; defaults beside source database")
	archives := flags.String("archives", "", "source archive directory; defaults beside source database")
	stopped := flags.Bool("source-stopped", false, "assert that all source writers are stopped; never authorizes upgrade")
	upgrade := flags.Bool("upgrade-check", false, "host-only Upgrade prerequisites; existing admin key is read from stdin (EOF required)")
	operatorSummary := flags.Bool("summary", false, "show a safe, human-readable host preflight result instead of the detailed JSON report")
	bundle := flags.String("backup", "", "existing manager-data-bundle to validate with --upgrade-check")
	revision := flags.String("expected-source-revision", "", "expected bundle sourceRevision with --upgrade-check")
	timeout := flags.Duration("timeout", 10*time.Second, "inspection time budget")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*database) == "" || *timeout <= 0 {
		return errors.New("explicit --db and a positive --timeout are required; no positional arguments")
	}
	if !*upgrade && (*bundle != "" || *revision != "") {
		return errors.New("backup/revision require --upgrade-check")
	}
	writeReport := func(report legacypreflight.Report, inspectionErr error) error {
		if *operatorSummary {
			return writeOperatorSummary(stdout, report, inspectionErr)
		}
		return json.NewEncoder(stdout).Encode(report)
	}
	paths := legacypreflight.DefaultPaths(*database)
	paths.Config = *configFile
	if *configFile != "" {
		cfg, err := config.ResolveInspectionLocations(*configFile, *database)
		if err != nil {
			_ = writeReport(legacypreflight.Report{SourceKind: "unknown", Blocked: []string{"source-config-unreadable"}, Unknown: []string{"source-configuration"}}, err)
			return errors.New("source config could not be inspected")
		}
		paths.DataKey, paths.Archives = cfg.DataKeyPath, cfg.UsageArchiveDir
	}
	if *dataKey != "" {
		paths.DataKey = *dataKey
	}
	if *archives != "" {
		paths.Archives = *archives
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var report legacypreflight.Report
	var inspectionErr error
	if *upgrade {
		if ctx.Err() != nil {
			return ErrBlocked
		}
		key, err := io.ReadAll(io.LimitReader(stdin, 4097))
		if err != nil || len(key) > 4096 {
			return errors.New("existing administrator key input unavailable or too large")
		}
		report, inspectionErr = legacypreflight.CheckUpgradeBackup(ctx, paths, legacypreflight.UpgradeBackupOptions{
			AdminKey: string(key), SourceStopped: *stopped, BundleDirectory: *bundle, ExpectedSourceRevision: *revision,
		})
	} else {
		report, inspectionErr = legacypreflight.Inspect(ctx, paths, legacypreflight.Options{SourceStopped: *stopped})
	}
	if err := writeReport(report, inspectionErr); err != nil {
		return err
	}
	if inspectionErr != nil {
		if errors.Is(inspectionErr, legacypreflight.ErrUpgradePreconditionsBlocked) {
			return ErrBlocked
		}
		return inspectionErr
	}
	if len(report.Blocked) != 0 || report.SourceKind == "unknown" || report.SourceKind == "missing-database" {
		return ErrBlocked
	}
	return nil
}

func IsHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }

// writeOperatorSummary only uses trusted classification labels; it deliberately
// excludes source paths, inspected values, credentials and raw issue messages.
// A successful diagnostic command is never an Upgrade/Adopt authorization.
func writeOperatorSummary(w io.Writer, report legacypreflight.Report, inspectionErr error) error {
	const noGrant = "NOT GRANTED"
	var source, next string
	switch report.SourceKind {
	case "fresh":
		source = "No existing Manager database observed"
		next = "If this is an intended new install, obtain a separate Host-authorized Fresh Handoff. Do not infer authorization from this inspection."
	case "legacy-manager":
		source = "Existing legacy Manager database"
		next = "Preserve the original data and verify source release, stopped writers, backup and upgrade eligibility before any migration."
	case "native-v2":
		source = "Existing native v2 Manager database"
		next = "Preserve this installation and use its existing authorized management or recovery flow; do not run Fresh initialization over it."
	case "missing-database":
		source = "Specified Manager database not found"
		next = "Verify the explicit source path and its mount on the Host. Missing data alone does not authorize a new installation."
	case "partial-source", "empty-database":
		source = "Incomplete or empty Manager source"
		next = "Stop and preserve the source; resolve the incomplete database state with the Host before any initialization or migration."
	default:
		source = "Manager source classification unavailable"
		next = "Keep the source unchanged and resolve inspection, ownership or consistency failures using a Host-controlled diagnostic."
	}
	if inspectionErr != nil || len(report.Blocked) != 0 {
		// A blocked/unstable observation cannot be presented as an actionable
		// Fresh or Upgrade classification, even if SourceKind looks familiar.
		next = "Inspection blocked or incomplete. Preserve source data; resolve Host ownership, integrity and prerequisites before any write."
	}
	_, err := fmt.Fprintf(w, "Source: %s\nUpgrade/Adopt authorization: %s\nNext step: %s\n", source, noGrant, next)
	return err
}
