//go:build !linux

package freshkeyapply

import "os"

func rootOwned(os.FileInfo) bool { return false }
