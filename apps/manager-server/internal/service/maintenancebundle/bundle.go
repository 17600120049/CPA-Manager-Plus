// Package maintenancebundle inventories and copies complete offline Manager
// file sets. Its manifests establish content integrity, never release identity,
// writer provenance, or upgrade authorization.
package maintenancebundle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	_ "modernc.org/sqlite"
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
	Version        int              `json:"version"`
	Fence          string           `json:"fence"`
	SourceRevision string           `json:"sourceRevision,omitempty"`
	Entries        map[string]Entry `json:"entries"`
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

func computeSourceRevision(entries map[string]Entry) string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		e := entries[k]
		fmt.Fprintf(h, "%s:%t:%t:%d:%s\n", k, e.Present, e.Directory, e.Size, e.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
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
	m.SourceRevision = computeSourceRevision(m.Entries)
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
	if expected.SourceRevision != "" && current.SourceRevision != expected.SourceRevision {
		return errors.New("Manager bundle source revision mismatch")
	}
	return nil
}

// RevalidateSource verifies that the source files still match the authorized
// bundle manifest. A changed source invalidates the previous authorization.
func RevalidateSource(ctx context.Context, layout Layout, expected Manifest) error {
	return Revalidate(ctx, layout, expected)
}

func validateWALHeader(walPath string, expectedPageSize uint32) error {
	wal, err := os.Open(walPath)
	if err != nil {
		return err
	}
	defer wal.Close()
	var header [32]byte
	if _, err := io.ReadFull(wal, header[:]); err != nil {
		return err
	}
	magic := binary.BigEndian.Uint32(header[:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		return errors.New("invalid WAL header magic")
	}
	if binary.BigEndian.Uint32(header[4:8]) != 3007000 {
		return errors.New("unsupported WAL file format version")
	}
	walPageSize := binary.BigEndian.Uint32(header[8:12])
	if walPageSize != expectedPageSize {
		return fmt.Errorf("WAL page size %d does not match database page size %d", walPageSize, expectedPageSize)
	}
	var order binary.ByteOrder = binary.LittleEndian
	if magic&1 != 0 {
		order = binary.BigEndian
	}
	var s1, s2 uint32
	for i := 0; i < 24; i += 8 {
		s1 += order.Uint32(header[i:i+4]) + s2
		s2 += order.Uint32(header[i+4:i+8]) + s1
	}
	if s1 != binary.BigEndian.Uint32(header[24:28]) || s2 != binary.BigEndian.Uint32(header[28:32]) {
		return errors.New("invalid WAL header checksum")
	}
	return nil
}

func sqliteURI(path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS != "windows" && !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	dsn := &url.URL{Scheme: "file", Path: clean}
	q := dsn.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	dsn.RawQuery = q.Encode()
	return dsn.String()
}

// ValidateMatchingEvidence verifies cross-file coherence:
// 1. Database is a valid SQLite database (header magic, page size, PRAGMA quick_check).
// 2. If WAL exists, WAL header and checksum match database page size.
// 3. Data key is a valid 32-byte key.
// 4. Data key successfully decrypts any encrypted envelopes in the database settings table.
// 5. Config is valid JSON if present.
// 6. Usage archive segments recorded in the database exist on disk if archives are present.
func ValidateMatchingEvidence(ctx context.Context, l Layout) error {
	if strings.TrimSpace(l.DatabasePath) == "" {
		return errors.New("database path is required")
	}
	info, err := os.Lstat(l.DatabasePath)
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("database is not a regular file")
	}
	if info.Size() < 100 {
		return errors.New("database is too small to be a valid SQLite file")
	}
	f, err := os.Open(l.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	var header [100]byte
	_, err = io.ReadFull(f, header[:])
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("read database header: %w", err)
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		return errors.New("database is not a valid SQLite database file")
	}
	pageSize := uint32(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		return errors.New("database header contains invalid page size")
	}

	walPath := l.DatabasePath + "-wal"
	if walInfo, err := os.Lstat(walPath); err == nil && walInfo.Mode().IsRegular() && walInfo.Size() > 0 {
		if err := validateWALHeader(walPath, pageSize); err != nil {
			return fmt.Errorf("database WAL validation failed: %w", err)
		}
	}

	var rawKey []byte
	if strings.TrimSpace(l.DataKeyPath) != "" {
		if keyInfo, err := os.Lstat(l.DataKeyPath); err == nil && keyInfo.Mode().IsRegular() {
			k, _, err := security.LoadOrCreateDataKey("", l.DataKeyPath)
			if err != nil {
				return fmt.Errorf("invalid data key: %w", err)
			}
			if len(k) != 32 {
				return errors.New("data key must be 32 bytes")
			}
			rawKey = k
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect data key: %w", err)
		}
	}

	if strings.TrimSpace(l.ConfigPath) != "" {
		if info, err := os.Lstat(l.ConfigPath); err == nil && info.Mode().IsRegular() {
			data, err := os.ReadFile(l.ConfigPath)
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}
			var js any
			if err := json.Unmarshal(data, &js); err != nil {
				return fmt.Errorf("config is not valid JSON: %w", err)
			}
			if _, ok := js.(map[string]any); !ok {
				return errors.New("config JSON root must be an object")
			}
		} else if l.RequireConfig {
			return errors.New("required config is missing")
		}
	}

	// Stage SQLite files into a dedicated scratch directory for driver inspection
	// so opening SQLite driver never touches or mutates the layout paths or WAL.
	scratch, err := os.MkdirTemp("", "cpamp-matching-staging-")
	if err != nil {
		return fmt.Errorf("create matching inspection scratch: %w", err)
	}
	defer os.RemoveAll(scratch)

	scratchDB := filepath.Join(scratch, "database")
	if err := copyFile(ctx, l.DatabasePath, scratchDB); err != nil {
		return fmt.Errorf("stage database for inspection: %w", err)
	}
	if _, err := os.Lstat(walPath); err == nil {
		if err := copyFile(ctx, walPath, scratchDB+"-wal"); err != nil {
			return fmt.Errorf("stage WAL for inspection: %w", err)
		}
	}
	shmPath := l.DatabasePath + "-shm"
	if _, err := os.Lstat(shmPath); err == nil {
		if err := copyFile(ctx, shmPath, scratchDB+"-shm"); err != nil {
			return fmt.Errorf("stage SHM for inspection: %w", err)
		}
	}

	dsn := sqliteURI(scratchDB)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var quickCheck string
	row := db.QueryRowContext(ctx, "PRAGMA quick_check(1)")
	if err := row.Scan(&quickCheck); err != nil {
		return fmt.Errorf("SQLite integrity check query failed: %w", err)
	}
	if quickCheck != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", quickCheck)
	}

	if len(rawKey) > 0 {
		protector, err := security.NewProtector(rawKey)
		if err != nil {
			return fmt.Errorf("create key protector: %w", err)
		}
		var hasSettings int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='settings'").Scan(&hasSettings); err == nil && hasSettings > 0 {
			rows, err := db.QueryContext(ctx, "SELECT * FROM settings LIMIT 200")
			if err == nil {
				cols, colErr := rows.Columns()
				if colErr == nil {
					for rows.Next() {
						values := make([]any, len(cols))
						valuePtrs := make([]any, len(cols))
						for i := range values {
							valuePtrs[i] = &values[i]
						}
						if err := rows.Scan(valuePtrs...); err == nil {
							for _, v := range values {
								var strVal string
								switch s := v.(type) {
								case string:
									strVal = s
								case []byte:
									strVal = string(s)
								}
								if security.IsValidProtectedEnvelope(strVal) {
									if _, err := protector.UnprotectString(strVal); err != nil {
										_ = rows.Close()
										return fmt.Errorf("data key does not match database settings encryption: %w", err)
									}
								}
							}
						}
					}
				}
				_ = rows.Close()
			}
		}
	}

	if strings.TrimSpace(l.ArchivesPath) != "" {
		var hasArchiveSegments int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='usage_archive_segments'").Scan(&hasArchiveSegments); err == nil && hasArchiveSegments > 0 {
			rows, err := db.QueryContext(ctx, "SELECT file_name FROM usage_archive_segments WHERE status IN ('archived', 'verified', 'completed')")
			if err == nil {
				for rows.Next() {
					var fileName string
					if err := rows.Scan(&fileName); err == nil && strings.TrimSpace(fileName) != "" {
						segPath := filepath.Join(l.ArchivesPath, filepath.FromSlash(fileName))
						if _, err := os.Lstat(segPath); os.IsNotExist(err) {
							_ = rows.Close()
							return fmt.Errorf("required archive segment recorded in database is missing from usage-archives: %s", fileName)
						}
					}
				}
				_ = rows.Close()
			}
		}
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
	if err := ValidateMatchingEvidence(ctx, l); err != nil {
		return Manifest{}, fmt.Errorf("source matching evidence invalid: %w", err)
	}
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
	if err := ValidateMatchingEvidence(ctx, BundleLayout(tmp)); err != nil {
		return Manifest{}, fmt.Errorf("bundle matching evidence invalid: %w", err)
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
	if m.Version != Version || m.Fence != CooperatingFence || m.SourceRevision == "" {
		return Manifest{}, errors.New("unsupported Manager bundle manifest")
	}
	if expectedRev := computeSourceRevision(m.Entries); m.SourceRevision != expectedRev {
		return Manifest{}, errors.New("bundle manifest source revision mismatch")
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
			if strings.HasPrefix(e.Name(), ".") || strings.Contains(e.Name(), "unpublished") {
				return Manifest{}, errors.New("unexpected temporary bundle entry")
			}
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
	if err := ValidateMatchingEvidence(ctx, BundleLayout(tmp)); err != nil {
		return Layout{}, fmt.Errorf("restore matching evidence failed: %w", err)
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
