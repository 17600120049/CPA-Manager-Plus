//go:build !windows

package legacyartifact

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type fileLock struct {
	file *os.File
}

func acquireFileLock(path string) (*fileLock, error) {
	if err := checkPrivateLockPath(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := checkOpenedPrivateLock(f, path); err != nil {
		_ = f.Close()
		return nil, err
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &fileLock{file: f}, nil
}

func (l *fileLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(err, closeErr)
}
