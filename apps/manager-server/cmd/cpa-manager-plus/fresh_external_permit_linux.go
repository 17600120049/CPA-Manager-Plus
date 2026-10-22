package main

import (
	"encoding/json"
	"io"
	"os"
	"runtime"
	"syscall"
)

// The private Host issuer provides a regular 0600 root-owned, read-only bind
// mount. A flag or file manufactured by the browser has no authority.
func loadPrivateFreshExternalPermit(path string) (freshExternalPermit, bool) {
	if runtime.GOARCH != "amd64" {
		return freshExternalPermit{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return freshExternalPermit{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		info.Size() <= 0 || info.Size() > 4096 {
		return freshExternalPermit{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return freshExternalPermit{}, false
	}
	link, err := os.Lstat(path)
	if err != nil || link.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, link) {
		return freshExternalPermit{}, false
	}
	var p freshExternalPermit
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(&struct{}{}) != io.EOF {
		return freshExternalPermit{}, false
	}
	return p, true
}
