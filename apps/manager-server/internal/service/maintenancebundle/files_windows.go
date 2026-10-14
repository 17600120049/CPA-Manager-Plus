//go:build windows

package maintenancebundle

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func rejectMultipleLinks(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return errors.New("Manager bundle file has multiple hard links")
	}
	return nil
}

// Windows has no portable directory fsync. File FlushFileBuffers and same-volume
// MoveFileEx WRITE_THROUGH are the available boundary, not proof of power-loss
// durability on every filesystem/storage stack.
func syncDirectory(string) error { return nil }

func moveFile(from, to string, flags uint32) error {
	a, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, flags|windows.MOVEFILE_WRITE_THROUGH)
}

// Never replace an existing restore/backup target.
func publishDirectory(from, to string) error { return moveFile(from, to, 0) }
func replaceState(from, to string) error {
	return moveFile(from, to, windows.MOVEFILE_REPLACE_EXISTING)
}
