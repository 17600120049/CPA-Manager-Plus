//go:build linux || darwin

package legacyartifact

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type offlineLock struct{ file *os.File }

func acquireOfflineLock(path string) (*offlineLock, error) {
	if err := checkPrivateLockPath(path); err != nil {
		return nil, ErrOfflineState
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, ErrOfflineState
	}
	if err := checkOpenedPrivateLock(f, path); err != nil {
		f.Close()
		return nil, ErrOfflineState
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil || st.Nlink != 1 {
		f.Close()
		return nil, ErrOfflineState
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrOfflineBusy
		}
		return nil, ErrOfflineState
	}
	return &offlineLock{file: f}, nil
}

// Close the parent's reference without LOCK_UN: an orphaned child must retain
// the shared flock until it exits. The last reference releases the OS lock.
func (l *offlineLock) close() error { return l.file.Close() }

func offlineSingleLink(f *os.File) bool {
	var st unix.Stat_t
	return unix.Fstat(int(f.Fd()), &st) == nil && st.Nlink == 1
}
