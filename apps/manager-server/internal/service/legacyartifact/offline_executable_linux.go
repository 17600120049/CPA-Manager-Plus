//go:build linux

package legacyartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

const offlineExecutableSeals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL

// Copy into an anonymous kernel object, make its bytes irrevocably immutable,
// then hash that object. Neither renaming nor editing the staging path can
// change what is launched. A normal opened inode alone is still writable.
func prepareOfflineExecutable(ctx context.Context, path, digest string) (*os.File, error) {
	source, err := offlineOpen(path, 0o700)
	if err != nil {
		return nil, ErrExecutableMismatch
	}
	defer source.Close()
	fd, err := unix.MemfdCreate("cpamp-offline-writer", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, ErrOfflinePlatform
	}
	f := os.NewFile(uintptr(fd), "sealed-offline-writer")
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	n, err := io.Copy(f, &contextReader{ctx: ctx, r: io.LimitReader(source, maxExecutableBytes+1)})
	if err != nil || n <= 0 || n > maxExecutableBytes {
		return nil, ErrExecutableMismatch
	}
	if err := f.Chmod(0o500); err != nil {
		return nil, ErrOfflinePlatform
	}
	if _, err := unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, offlineExecutableSeals); err != nil {
		return nil, ErrOfflinePlatform
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, ErrExecutableMismatch
	}
	h := sha256.New()
	if _, err := io.Copy(h, &contextReader{ctx: ctx, r: f}); err != nil || hex.EncodeToString(h.Sum(nil)) != digest {
		return nil, ErrExecutableMismatch
	}
	ok = true
	return f, nil
}

func pinnedOfflineCommand(ctx context.Context, executable *os.File, dbPath string, lock *offlineLock) (*exec.Cmd, error) {
	seals, err := unix.FcntlInt(executable.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals&offlineExecutableSeals != offlineExecutableSeals {
		return nil, ErrExecutableMismatch
	}
	// Go maps ExtraFiles to child descriptors before execve. Descriptor 3 is
	// the operation fence, 4 is the sealed object we hashed. No staging path
	// is resolved during launch. Missing /proc or denied memfd exec fails closed.
	cmd := exec.CommandContext(ctx, "/proc/self/fd/4", "cleanup-derived", "--db-path", dbPath)
	configureOfflineProcess(cmd, lock)
	cmd.ExtraFiles = append(cmd.ExtraFiles, executable)
	return cmd, nil
}
