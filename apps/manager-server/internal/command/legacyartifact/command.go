package legacyartifact

import (
	"context"
	"encoding/hex"
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
		_, _ = io.WriteString(stderr, "Usage: cpa-manager-plus legacy-writer-artifact --archive PATH --asset NAME [--timeout DURATION]\n       cpa-manager-plus legacy-writer-artifact --verify-receipt PATH --bundle PATH --authority-key HEX [--timeout DURATION]\n       cpa-manager-plus legacy-writer-artifact --execute-hop --asset NAME --input-bundle PATH --output-bundle PATH --authority-key HEX [--archive PATH] [--executable PATH] [--timeout DURATION]\nVerifies fixed v1.14.4 artifact bytes or forward-hop execution receipt; never authorizes upgrade.\n")
	}
	archive := f.String("archive", "", "local published native archive (read-only)")
	asset := f.String("asset", "", "exact published v1.14.4 native asset name")
	timeout := f.Duration("timeout", 10*time.Second, "verification time budget")

	verifyReceipt := f.String("verify-receipt", "", "execution receipt JSON file to verify")
	bundleDir := f.String("bundle", "", "associated bundle directory to verify against receipt")
	authorityKeyHex := f.String("authority-key", "", "authority key in hex")

	executeHop := f.Bool("execute-hop", false, "execute controlled forward-hop operation")
	inputBundleDir := f.String("input-bundle", "", "input legacy bundle directory")
	outputBundleDir := f.String("output-bundle", "", "output bundle directory")
	executablePath := f.String("executable", "", "path to verified executable")

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

	// Mode 1: verify receipt
	if *verifyReceipt != "" {
		if *bundleDir == "" || *authorityKeyHex == "" {
			return errors.New("explicit --bundle and --authority-key are required when verifying a receipt")
		}
		key, err := hex.DecodeString(*authorityKeyHex)
		if err != nil || len(key) < 32 {
			return errors.New("invalid authority key; must be valid hex of at least 32 bytes")
		}
		auth, err := legacyartifact.NewAuthority(key)
		if err != nil {
			return errors.New("cannot create authority from key")
		}
		bundleLayout := maintenancebundle.BundleLayout(*bundleDir)
		result, err := legacyartifact.ValidateHop(ctx, legacyartifact.ValidateOptions{
			ReceiptPath:  *verifyReceipt,
			BundleLayout: bundleLayout,
			Authority:    auth,
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(result)
	}

	// Mode 2: execute forward hop
	if *executeHop {
		if *asset == "" || *inputBundleDir == "" || *outputBundleDir == "" || *authorityKeyHex == "" {
			return errors.New("explicit --asset, --input-bundle, --output-bundle and --authority-key are required for forward-hop")
		}
		key, err := hex.DecodeString(*authorityKeyHex)
		if err != nil || len(key) < 32 {
			return errors.New("invalid authority key; must be valid hex of at least 32 bytes")
		}
		auth, err := legacyartifact.NewAuthority(key)
		if err != nil {
			return errors.New("cannot create authority from key")
		}
		inLayout := maintenancebundle.BundleLayout(*inputBundleDir)
		receipt, err := legacyartifact.ExecuteHop(ctx, legacyartifact.HopOptions{
			ArchivePath:    *archive,
			Asset:          *asset,
			ExecutablePath: *executablePath,
			InputLayout:    inLayout,
			OutputRoot:     *outputBundleDir,
			Authority:      auth,
			Timeout:        *timeout,
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(receipt)
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
