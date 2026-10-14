//go:build !linux && !darwin

package legacyartifact

import "os/exec"

func configureOfflineProcess(*exec.Cmd, *offlineLock) {}
