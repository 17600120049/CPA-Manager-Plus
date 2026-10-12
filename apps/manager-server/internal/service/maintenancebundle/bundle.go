// Package maintenancebundle inventories and copies complete offline Manager
// file sets. Its manifests establish content integrity, never release identity,
// writer provenance, or upgrade authorization.
package maintenancebundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
)

const Version = 1
const CooperatingFence = "cooperating-manager-lock"
const maxManifestBytes = 16 * 1024 * 1024
const maxEntries = 10000

// Layout makes external config and archive requirements explicit. Database and
// key must exist. Optional missing config/archive roots still get manifest entries.
type Layout struct {
	DatabasePath    string
	DataKeyPath     string
	ConfigPath      string
	ArchivesPath    string
	RequireConfig   bool
	RequireArchives bool
}

type Entry struct {
	Present   bool   `json:"present"`
	Directory bool   `json:"directory,omitempty"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type Manifest struct {
	Version int              `json:"version"`
	Fence   string           `json:"fence"`
	Entries map[string]Entry `json:"entries"`
}

// BundleLayout returns storage paths shared by backup, restore and maintenance
// execution. The database filename is deliberately independent of source paths.
func BundleLayout(root string) Layout {
	return Layout{DatabasePath: filepath.Join(root, "database"), DataKeyPath: filepath.Join(root, "data-key"), ConfigPath: filepath.Join(root, "config"), ArchivesPath: filepath.Join(root, "usage-archives")}
}

func (l Layout) paths() map[string]string {
	return map[string]string{"database": l.DatabasePath, "database-wal": l.DatabasePath + "-wal", "database-shm": l.DatabasePath + "-shm", "database-journal": l.DatabasePath + "-journal", "data-key": l.DataKeyPath, "config": l.ConfigPath, "usage-archives": l.ArchivesPath}
}

func normalize(l Layout) (Layout, error) {
	paths := []*string{&l.DatabasePath, &l.DataKeyPath, &l.ConfigPath, &l.ArchivesPath}
	for _, p := range paths {
		if strings.TrimSpace(*p) == "" {
			return Layout{}, errors.New("all Manager bundle paths must be explicit")
		}
		a, err := filepath.Abs(*p)
		if err != nil {
			return Layout{}, err
		}
		*p = a
		if err := safePath(a); err != nil {
			return Layout{}, err
		}
	}
	items := l.paths()
	for name, a := range items {
		for other, b := range items {
			if name == other {
				continue
			}
			physical, err := physicalOverlap(a, b)
			if err != nil {
				return Layout{}, err
			}
			if overlaps(a, b) || physical {
				return Layout{}, fmt.Errorf("Manager bundle paths overlap: %s and %s", name, other)
			}
		}
	}
	return l, nil
}

// String paths alone miss case aliases on case-insensitive filesystems. Compare
// an existing root with the other path and its ancestors without inferring any
// source identity from this filesystem witness.
func physicalOverlap(a, b string) (bool, error) {
	info, err := os.Lstat(a)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for p := b; ; p = filepath.Dir(p) {
		other, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if err == nil && os.SameFile(info, other) {
			return true, nil
		}
		if p == filepath.Dir(p) {
			return false, nil
		}
	}
}

func overlaps(a, b string) bool {
	if runtime.GOOS == "windows" {
		a = strings.ToLower(a)
		b = strings.ToLower(b)
	}
	if samePath(a, b) {
		return true
	}
	r, err := filepath.Rel(a, b)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Inspect reads every byte, including all nested archive files. Directory and
// missing entries make additions/deletions part of revalidation. It does not
// itself establish a snapshot or a quiescence boundary.
func Inspect(ctx context.Context, layout Layout) (Manifest, error) {
	l, err := normalize(layout)
	if err != nil {
		return Manifest{}, err
	}
	return inspect(ctx, l)
}

func inspect(ctx context.Context, l Layout) (Manifest, error) {
	m := Manifest{Version: Version, Entries: map[string]Entry{}}
	for name, p := range l.paths() {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			if name == "database" || name == "data-key" || (name == "config" && l.RequireConfig) || (name == "usage-archives" && l.RequireArchives) {
				return Manifest{}, fmt.Errorf("required Manager bundle entry missing: %s", name)
			}
			m.Entries[name] = Entry{}
			continue
		}
		if err != nil {
			return Manifest{}, err
		}
		if name != "usage-archives" {
			e, err := digestFile(ctx, p, nil)
			if err != nil {
				return Manifest{}, fmt.Errorf("inspect %s: %w", name, err)
			}
			m.Entries[name] = e
			continue
		}
		if !info.IsDir() {
			return Manifest{}, errors.New("usage-archives must be a directory")
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(m.Entries) >= maxEntries {
				return errors.New("Manager bundle archive inventory exceeds supported bound")
			}
			if err := safePath(path); err != nil {
				return err
			}
			r, err := filepath.Rel(p, path)
			if err != nil {
				return err
			}
			if !utf8.ValidString(r) {
				return errors.New("archive filename cannot be represented in the bundle manifest")
			}
			name := "usage-archives"
			if r != "." {
				name += "/" + filepath.ToSlash(r)
			}
			if d.IsDir() {
				m.Entries[name] = Entry{Present: true, Directory: true}
				return nil
			}
			e, err := digestFile(ctx, path, nil)
			if err != nil {
				return err
			}
			m.Entries[name] = e
			return nil
		})
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect archives: %w", err)
		}
	}
	return m, nil
}

func Revalidate(ctx context.Context, layout Layout, expected Manifest) error {
	current, err := Inspect(ctx, layout)
	if err != nil {
		return err
	}
	if expected.Version != Version || !reflect.DeepEqual(current.Entries, expected.Entries) {
		return errors.New("Manager bundle contents changed")
	}
	return nil
}

type createHooks struct {
	beforeRevalidate func()
	copy             func(context.Context, string, string) error
}

// Create requires a held lock bound to this database. The fence excludes
// cooperating Manager writers only. The owner must stop/isolate external
// writers; matching hashes cannot establish that an external writer is absent.
func Create(ctx context.Context, layout Layout, bundleDir string, lock *processlock.Lock) (Manifest, error) {
	return create(ctx, layout, bundleDir, lock, createHooks{})
}

func create(ctx context.Context, layout Layout, bundleDir string, lock *processlock.Lock, h createHooks) (Manifest, error) {
	l, err := normalize(layout)
	if err != nil {
		return Manifest{}, err
	}
	validateLock := func() error {
		if lock == nil || !samePath(lock.DatabasePath(), l.DatabasePath) {
			return errors.New("Manager bundle requires its database ownership lock")
		}
		return lock.Validate()
	}
	if err := validateLock(); err != nil {
		return Manifest{}, err
	}
	dest, err := prepareDestination(bundleDir, l)
	if err != nil {
		return Manifest{}, err
	}
	before, err := inspect(ctx, l)
	if err != nil {
		return Manifest{}, err
	}
	before.Fence = CooperatingFence
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".manager-bundle-unpublished-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(tmp)
	copyFn := h.copy
	if copyFn == nil {
		copyFn = copyFile
	}
	if err := materialize(ctx, l, tmp, before, copyFn); err != nil {
		return Manifest{}, err
	}
	if h.beforeRevalidate != nil {
		h.beforeRevalidate()
	}
	if err := validateLock(); err != nil {
		return Manifest{}, err
	}
	if err := Revalidate(ctx, l, before); err != nil {
		return Manifest{}, err
	}
	if err := Revalidate(ctx, BundleLayout(tmp), before); err != nil {
		return Manifest{}, err
	}
	data, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if len(data)+1 > maxManifestBytes {
		return Manifest{}, errors.New("Manager bundle manifest exceeds supported bound")
	}
	if err := writeFile(filepath.Join(tmp, "manifest.json"), append(data, '\n')); err != nil {
		return Manifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err := validateLock(); err != nil {
		return Manifest{}, err
	}
	if err := absent(dest); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, err
	}
	return before, nil
}

func materialize(ctx context.Context, l Layout, dest string, m Manifest, copyFn func(context.Context, string, string) error) error {
	// Directories must precede files, independent of map iteration order.
	for name, e := range m.Entries {
		if e.Present && e.Directory {
			if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(name)), 0o700); err != nil {
				return err
			}
		}
	}
	for name, e := range m.Entries {
		if !e.Present || e.Directory {
			continue
		}
		source := l.paths()[name]
		if strings.HasPrefix(name, "usage-archives/") {
			source = filepath.Join(l.ArchivesPath, filepath.FromSlash(strings.TrimPrefix(name, "usage-archives/")))
		}
		if err := copyFn(ctx, source, filepath.Join(dest, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return nil
}

// Verify rejects missing, extra and changed files. An editable manifest remains
// an integrity description; it is not an authenticated maintenance receipt.
func Verify(ctx context.Context, bundleDir string) (Manifest, error) {
	root, err := filepath.Abs(bundleDir)
	if err != nil {
		return Manifest{}, err
	}
	if err := safePath(root); err != nil {
		return Manifest{}, err
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if err := safePath(manifestPath); err != nil {
		return Manifest{}, err
	}
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return Manifest{}, errors.New("bundle manifest is not a bounded regular file")
	}
	f, err := os.Open(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return Manifest{}, err
	}
	if !os.SameFile(info, opened) {
		return Manifest{}, errors.New("bundle manifest changed while opening")
	}
	if err := rejectMultipleLinks(f); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(io.LimitReader(f, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("bundle manifest has trailing content")
	}
	if m.Version != Version || m.Fence != CooperatingFence {
		return Manifest{}, errors.New("unsupported Manager bundle manifest")
	}
	if err := Revalidate(ctx, BundleLayout(root), m); err != nil {
		return Manifest{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Manifest{}, err
	}
	for _, e := range entries {
		if e.Name() != "manifest.json" {
			if _, ok := m.Entries[e.Name()]; !ok {
				return Manifest{}, errors.New("unexpected bundle entry")
			}
		}
	}
	return m, nil
}

// RestoreNew restores the matching file set into a new owner-controlled root.
// Existing roots are rejected. No running/live Manager file set is replaced.
func RestoreNew(ctx context.Context, bundleDir, newRoot string) (Layout, error) {
	return restoreNew(ctx, bundleDir, newRoot, createHooks{})
}

func restoreNew(ctx context.Context, bundleDir, newRoot string, h createHooks) (Layout, error) {
	m, err := Verify(ctx, bundleDir)
	if err != nil {
		return Layout{}, err
	}
	l := BundleLayout(bundleDir)
	dest, err := prepareDestination(newRoot, l)
	if err != nil {
		return Layout{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".manager-restore-unpublished-")
	if err != nil {
		return Layout{}, err
	}
	defer os.RemoveAll(tmp)
	copyFn := h.copy
	if copyFn == nil {
		copyFn = copyFile
	}
	if err := materialize(ctx, l, tmp, m, copyFn); err != nil {
		return Layout{}, err
	}
	if h.beforeRevalidate != nil {
		h.beforeRevalidate()
	}
	if err := Revalidate(ctx, BundleLayout(tmp), m); err != nil {
		return Layout{}, err
	}
	current, err := Verify(ctx, bundleDir)
	if err != nil {
		return Layout{}, err
	}
	if !reflect.DeepEqual(current, m) {
		return Layout{}, errors.New("Manager bundle changed during restoration")
	}
	if err := ctx.Err(); err != nil {
		return Layout{}, err
	}
	if err := absent(dest); err != nil {
		return Layout{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Layout{}, err
	}
	return BundleLayout(dest), nil
}

func prepareDestination(path string, l Layout) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("bundle destination is required")
	}
	dest, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := safePath(dest); err != nil {
		return "", err
	}
	for _, p := range l.paths() {
		a, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		physical, err := physicalOverlap(a, dest)
		if err != nil {
			return "", err
		}
		if overlaps(a, dest) || overlaps(dest, a) || physical {
			return "", errors.New("bundle destination overlaps source")
		}
	}
	if err := absent(dest); err != nil {
		return "", err
	}
	// The parent must already exist; callers own placement and permissions.
	info, err := os.Stat(filepath.Dir(dest))
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("bundle destination parent is not a directory")
	}
	return dest, nil
}

func absent(path string) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("bundle destination already exists")
}
