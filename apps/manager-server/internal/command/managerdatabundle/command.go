package managerdatabundle

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const usage = `Usage: cpa-manager-plus manager-data-bundle <create|recover-create|verify|restore-new|recover-publication> [flags]
create: --db PATH --data-key PATH --config PATH --archives PATH --bundle PATH --source-stopped
recover-create: same source flags as create; recover a rollback journal on a private copy
verify: --bundle PATH
restore-new: --bundle PATH --target PATH
recover-publication: --bundle DESTINATION (the create/restore target to reconcile)
All actions accept --timeout DURATION (positive; default 2m).
Create requires an existing Manager ownership marker and a held matching lock.
All source writers must actually be stopped or isolated. --source-stopped is an
operator assertion, not proof of external writer quiescence or upgrade authority.
Config and archives are required by default; --require-config=false and
--require-archives=false explicitly allow and record their absence.
Restore-new requires an absent destination in a caller-controlled existing parent.
No live data is replaced. Bundle manifests are integrity metadata, not receipts.
`

type summary struct {
	Status            string `json:"status"`
	Fence             string `json:"fence"`
	Files             int    `json:"files"`
	UpgradeAuthorized bool   `json:"upgradeAuthorized"`
}

type operationError struct{ cause error }

func (e operationError) Error() string {
	if errors.Is(e.cause, maintenancebundle.ErrPublicationUncertain) {
		return "Manager bundle publication outcome requires recover-publication; no upgrade authority was granted"
	}
	return "Manager bundle operation failed; no upgrade authority was granted"
}
func (e operationError) Unwrap() error { return e.cause }

// Run is an offline maintenance command. It neither resolves default runtime
// locations nor dispatches startup or creates keys. Explicit recover-create may
// open only a task-owned private database copy for SQLite journal recovery.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := io.WriteString(stderr, usage)
		return err
	}
	if len(args) == 0 {
		return errors.New("Manager bundle action required; use --help")
	}
	action := args[0]
	if action != "create" && action != "recover-create" && action != "verify" && action != "restore-new" && action != "recover-publication" {
		return errors.New("unsupported Manager bundle action")
	}
	fs := flag.NewFlagSet("manager-data-bundle "+action, flag.ContinueOnError)
	// The standard parser echoes unrecognized arguments and their values.
	// Keep parser diagnostics private; help and returned errors contain no input.
	fs.SetOutput(io.Discard)
	fs.Usage = func() { _, _ = io.WriteString(stderr, usage) }
	bundle := fs.String("bundle", "", "private bundle directory")
	timeout := fs.Duration("timeout", 2*time.Minute, "positive operation timeout")
	var layout maintenancebundle.Layout
	var stopped bool
	var target string
	isCreate := action == "create" || action == "recover-create"
	if isCreate {
		fs.StringVar(&layout.DatabasePath, "db", "", "existing Manager database")
		fs.StringVar(&layout.DataKeyPath, "data-key", "", "existing Manager key")
		fs.StringVar(&layout.ConfigPath, "config", "", "explicit source config path")
		fs.StringVar(&layout.ArchivesPath, "archives", "", "explicit source archive directory")
		fs.BoolVar(&layout.RequireConfig, "require-config", true, "require source config")
		fs.BoolVar(&layout.RequireArchives, "require-archives", true, "require source archives")
		fs.BoolVar(&stopped, "source-stopped", false, "operator asserts all source writers are stopped")
	} else if action == "restore-new" {
		fs.StringVar(&target, "target", "", "new restoration root")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid Manager bundle arguments; use --help")
	}
	if fs.NArg() != 0 || strings.TrimSpace(*bundle) == "" || *timeout <= 0 {
		return errors.New("explicit --bundle and positive --timeout required; no positional arguments")
	}
	if isCreate && (!stopped || strings.TrimSpace(layout.DatabasePath) == "" || strings.TrimSpace(layout.DataKeyPath) == "" || strings.TrimSpace(layout.ConfigPath) == "" || strings.TrimSpace(layout.ArchivesPath) == "") {
		return errors.New("create requires explicit --db, --data-key, --config, --archives and --source-stopped")
	}
	if action == "restore-new" && strings.TrimSpace(target) == "" {
		return errors.New("restore-new requires --target")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return operationError{err}
	}
	var m maintenancebundle.Manifest
	var err error
	var recoveredState string
	switch action {
	case "create", "recover-create":
		var lock *processlock.Lock
		lock, err = processlock.AcquireExisting(layout.DatabasePath)
		if err == nil {
			if action == "recover-create" {
				m, err = maintenancebundle.RecoverCreate(ctx, layout, *bundle, lock)
			} else {
				m, err = maintenancebundle.Create(ctx, layout, *bundle, lock)
			}
			err = errors.Join(err, lock.Close())
		}
	case "verify":
		m, err = maintenancebundle.Verify(ctx, *bundle)
	case "restore-new":
		m, err = maintenancebundle.Verify(ctx, *bundle)
		if err == nil {
			_, err = maintenancebundle.RestoreNew(ctx, *bundle, target)
		}
	case "recover-publication":
		recoveredState, err = maintenancebundle.RecoverPublication(ctx, *bundle)
	}
	if err != nil {
		return operationError{err}
	}
	files := 0
	for _, entry := range m.Entries {
		if entry.Present && !entry.Directory {
			files++
		}
	}
	status := map[string]string{"create": "created", "recover-create": "recovered-created", "verify": "verified", "restore-new": "restored"}[action]
	if action == "recover-publication" {
		status = "publication-" + recoveredState
		m.Fence = maintenancebundle.CooperatingFence
	}
	if err := json.NewEncoder(stdout).Encode(summary{Status: status, Fence: m.Fence, Files: files}); err != nil {
		return fmt.Errorf("Manager bundle %s; result output failed", status)
	}
	return nil
}
