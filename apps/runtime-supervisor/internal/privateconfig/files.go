package privateconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

func number(info os.FileInfo, field string) uint64 {
	if info == nil || info.Sys() == nil {
		return ^uint64(0)
	}
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	f := v.FieldByName(field)
	if !f.IsValid() || !f.CanUint() {
		return ^uint64(0)
	}
	return f.Uint()
}
func owner(info os.FileInfo) int { return int(number(info, "Uid")) }
func fileID(info os.FileInfo) (uint64, uint64, uint64) {
	return number(info, "Dev"), number(info, "Ino"), number(info, "Nlink")
}
func regular(info os.FileInfo, uid int, mode os.FileMode) bool {
	if info == nil {
		return false
	}
	_, _, n := fileID(info)
	return info.Mode().IsRegular() && info.Mode().Perm() == mode && owner(info) == uid && n == 1
}
func checkParents(name string) error {
	if !filepath.IsAbs(name) {
		return ErrStop
	}
	for p := filepath.Dir(name); ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return ErrStop
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
func readPrivate(name string, uid int, mode os.FileMode, max int64) ([]byte, error) {
	if checkParents(name) != nil {
		return nil, ErrStop
	}
	info, e := os.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !regular(info, uid, mode) || info.Size() > max {
		return nil, ErrStop
	}
	f, e := os.Open(name)
	if e != nil {
		return nil, ErrStop
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, ErrStop
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, ErrStop
	}
	return b, nil
}
func (a *Authority) save(r *record) error {
	b, e := json.Marshal(r)
	if e != nil {
		return ErrStop
	}
	f, e := os.CreateTemp(a.root, ".record-")
	if e != nil {
		return ErrStop
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ErrStop
	}
	if os.Rename(name, filepath.Join(a.root, "record.json")) != nil {
		return ErrStop
	}
	return syncDirectory(a.root)
}
func syncDirectory(name string) error {
	d, e := os.Open(name)
	if e != nil {
		return ErrStop
	}
	e = errors.Join(d.Sync(), d.Close())
	if e != nil {
		return ErrStop
	}
	return nil
}
func (a *Authority) restore(g Grant, good []byte, expected string) error {
	// Never rename the active inode; bind mounts and the CPA writer refer to it.
	if _, e := a.loadGrant(); e != nil {
		return e
	}
	f, e := os.OpenFile(a.config, os.O_RDWR, 0)
	if e != nil {
		return ErrStop
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return ErrStop
	}
	dev, ino, links := fileID(info)
	if dev != g.Device || ino != g.Inode || links != 1 {
		return ErrStop
	}
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || digest(b) != expected {
		return ErrStop
	}
	if bytes.Equal(b, good) {
		if f.Sync() != nil {
			return ErrStop
		}
		return nil
	}
	if _, e = f.Seek(0, 0); e == nil {
		_, e = f.Write(good)
	}
	if e == nil {
		e = f.Truncate(int64(len(good)))
	}
	if e == nil {
		e = f.Sync()
	}
	if e != nil {
		return ErrStop
	}
	return nil
}

func (a *Authority) flushCurrent(g Grant, expected string) error {
	f, e := os.OpenFile(a.config, os.O_RDWR, 0)
	if e != nil {
		return ErrStop
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return ErrStop
	}
	dev, ino, links := fileID(info)
	if dev != g.Device || ino != g.Inode || links != 1 {
		return ErrStop
	}
	if f.Sync() != nil {
		return ErrStop
	}
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || digest(b) != expected {
		return ErrStop
	}
	return nil
}
