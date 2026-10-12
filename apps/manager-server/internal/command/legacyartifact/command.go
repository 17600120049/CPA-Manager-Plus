package legacyartifact

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"io"
	"time"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f := flag.NewFlagSet("legacy-writer-artifact", flag.ContinueOnError)
	// The standard parser echoes invalid values. Keep diagnostic inputs out of
	// logs, just as artifact-read failures omit paths and payloads.
	f.SetOutput(io.Discard)
	f.Usage = func() {
		_, _ = io.WriteString(stderr, "Usage: cpa-manager-plus legacy-writer-artifact --archive PATH --asset NAME [--timeout DURATION]\nVerifies fixed v1.14.4 artifact bytes only; never executes a writer or authorizes upgrade.\n")
	}
	archive := f.String("archive", "", "local published native archive (read-only)")
	asset := f.String("asset", "", "exact published v1.14.4 native asset name")
	timeout := f.Duration("timeout", 10*time.Second, "verification time budget")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errors.New("invalid legacy artifact arguments; use --help")
	}
	if *archive == "" || *asset == "" || f.NArg() != 0 || *timeout <= 0 {
		return errors.New("explicit --archive, --asset and positive --timeout are required; no positional arguments")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := legacyartifact.Verify(ctx, *archive, *asset)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(report)
}

func IsHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }
