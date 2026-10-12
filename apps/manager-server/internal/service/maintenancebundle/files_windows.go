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
