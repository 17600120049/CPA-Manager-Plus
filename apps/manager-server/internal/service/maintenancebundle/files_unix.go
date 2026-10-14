//go:build !windows

package maintenancebundle

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func rejectMultipleLinks(f *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		return err
	}
	if stat.Nlink != 1 {
		return errors.New("Manager bundle file has multiple hard links")
	}
	return nil
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func publishDirectory(from, to string) error { return os.Rename(from, to) }
func replaceState(from, to string) error     { return os.Rename(from, to) }
