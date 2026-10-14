//go:build !linux

package legacyartifact

import (
	"context"
	"os"
	"os/exec"
)

func prepareOfflineExecutable(context.Context, string, string) (*os.File, error) {
	return nil, ErrOfflinePlatform
}

func pinnedOfflineCommand(context.Context, *os.File, string, *offlineLock) (*exec.Cmd, error) {
	return nil, ErrOfflinePlatform
}
