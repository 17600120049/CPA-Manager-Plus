package legacyartifact

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f := flag.NewFlagSet("legacy-writer-artifact", flag.ContinueOnError)
	// The standard parser echoes invalid values. Keep diagnostic inputs out of
	// logs, just as artifact-read failures omit paths and payloads.
	f.SetOutput(io.Discard)
	f.Usage = func() {
		_, _ = io.WriteString(stderr, "Usage: cpa-manager-plus legacy-writer-artifact --archive PATH --asset NAME [--timeout DURATION]\n       cpa-manager-plus legacy-writer-artifact --verify-receipt PATH --bundle PATH [--data-dir PATH] [--timeout DURATION]\n       cpa-manager-plus legacy-writer-artifact --execute-hop --archive PATH --asset NAME --input-bundle PATH --output-bundle PATH [--data-dir PATH] [--timeout DURATION]\nVerifies fixed v1.14.4 artifact bytes or receipts; forward-hop execution is not qualified and is refused on all platforms. Never authorizes upgrade.\n")
	}
	archive := f.String("archive", "", "local published native archive (read-only)")
	asset := f.String("asset", "", "exact published v1.14.4 native asset name")
	timeout := f.Duration("timeout", 10*time.Second, "verification time budget")
	dataDir := f.String("data-dir", "data", "Manager server data directory containing private authority and journal")

	verifyReceipt := f.String("verify-receipt", "", "execution receipt JSON file to verify")
	bundleDir := f.String("bundle", "", "associated bundle directory to verify against receipt")

	executeHop := f.Bool("execute-hop", false, "unsupported until offline writer execution is qualified")
	f.String("input-bundle", "", "input legacy bundle directory")
	f.String("output-bundle", "", "output bundle directory")

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

	if *executeHop {
		return legacyartifact.ErrExecutionNotQualified
	}

	// Mode 1: verify receipt
	if *verifyReceipt != "" {
		if *bundleDir == "" {
			return errors.New("explicit --bundle is required when verifying a receipt")
		}
		auth, err := legacyartifact.LoadAuthority(legacyartifact.DefaultAuthorityKeyPath(*dataDir))
		if err != nil {
			return err
		}
		journal, err := legacyartifact.OpenJournal(legacyartifact.DefaultJournalPath(*dataDir))
		if err != nil {
			return err
		}

		bundleLayout := maintenancebundle.BundleLayout(*bundleDir)
		result, err := legacyartifact.ValidateHop(ctx, legacyartifact.ValidateOptions{
			ReceiptPath:  *verifyReceipt,
			BundleLayout: bundleLayout,
			Authority:    auth,
			Journal:      journal,
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
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
