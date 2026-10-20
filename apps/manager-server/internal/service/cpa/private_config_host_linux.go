//go:build linux

package cpa

import (
	"os"
	"syscall"
)

func readConfigHostFile(name string) ([]byte, error) {
	i, e := os.Lstat(name)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || i.Size() > 8192 {
		return nil, ErrProofUnavailable
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Nlink != 1 {
		return nil, ErrProofUnavailable
	}
	f, e := os.Open(name)
	if e != nil {
		return nil, ErrProofUnavailable
	}
	defer f.Close()
	j, e := f.Stat()
	if e != nil || !os.SameFile(i, j) {
		return nil, ErrProofUnavailable
	}
	return readBounded(f, 8192)
}
