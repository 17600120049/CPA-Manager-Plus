//go:build linux || darwin

package legacyartifact

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureOfflineProcess(cmd *exec.Cmd, lock *offlineLock) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{lock.file}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
