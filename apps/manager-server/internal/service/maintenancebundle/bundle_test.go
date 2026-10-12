package maintenancebundle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	_ "modernc.org/sqlite"
)

func fixture(t *testing.T) (Layout, string, *processlock.Lock) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := Layout{DatabasePath: filepath.Join(root, "usage.sqlite"), DataKeyPath: filepath.Join(root, "data.key"), ConfigPath: filepath.Join(root, "manager.json"), ArchivesPath: filepath.Join(root, "usage-archives"), RequireConfig: true, RequireArchives: true}
	for name, path := range l.paths() {
		if name != "usage-archives" {
			put(t, path, []byte("source-"+name))
		}
	}
	if err := os.MkdirAll(filepath.Join(l.ArchivesPath, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.ArchivesPath, "2026", "10"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(l.ArchivesPath, "2026", "10", "history.jsonl.gz"), []byte("opaque archive bytes\x00\xff"))
	lock, err := processlock.Acquire(l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	return l, root, lock
}

func put(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustCreate(t *testing.T, l Layout, root string, lock *processlock.Lock) (string, Manifest) {
	t.Helper()
	bundle := filepath.Join(root, "bundle")
	m, err := Create(context.Background(), l, bundle, lock)
	if err != nil {
		t.Fatal(err)
	}
	return bundle, m
}

func noPublished(t *testing.T, root, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("failure published destination: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "unpublished") {
			t.Fatalf("temporary artifact retained: %s", entry.Name())
		}
	}
}

func TestCompleteBundleRoundTrip(t *testing.T) {
	l, root, lock := fixture(t)
	before, err := Inspect(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	bundle, m := mustCreate(t, l, root, lock)
	if m.Fence != CooperatingFence || !reflect.DeepEqual(before.Entries, m.Entries) {
		t.Fatal("bundle does not match full source inventory")
	}
	if !m.Entries["usage-archives/empty"].Directory {
		t.Fatal("empty archive directory was lost")
	}
	if len(m.Entries) != 11 {
		t.Fatalf("unexpected inventory: %+v", m.Entries)
	}
	if _, err := Verify(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreNew(context.Background(), bundle, filepath.Join(root, "restored"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Revalidate(context.Background(), restored, before); err != nil {
		t.Fatal(err)
	}
	if err := Revalidate(context.Background(), l, before); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	for name, path := range restored.paths() {
		if name == "usage-archives" {
			continue
		}
		a, err := os.ReadFile(l.paths()[name])
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("unmatched restoration: %s", name)
		}
	}
}

func TestRealSQLiteWALAndMatchingEncryptedKeyRestore(t *testing.T) {
	l, root, lock := fixture(t)
	if err := os.Remove(l.DataKeyPath); err != nil {
		t.Fatal(err)
	}
	key, _, err := security.LoadOrCreateDataKey("", l.DataKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewProtector(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := protector.ProtectString("fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(root, "seed.sqlite")
	seed, err := sql.Open("sqlite", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed.SetMaxOpenConns(1)
	defer seed.Close()
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE usage_events (id INTEGER PRIMARY KEY, raw_json TEXT)", "CREATE TABLE settings (value TEXT)"} {
		if _, err := seed.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seed.Exec("INSERT INTO usage_events VALUES (1, ?)", `{"immutable-history":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec("INSERT INTO settings VALUES (?)", encrypted); err != nil {
		t.Fatal(err)
	}
	// Clone actual committed WAL bytes to an offline input. The seed writer never
	// uses this clone, and closing it afterward cannot checkpoint the clone.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(seedPath + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if suffix == "-wal" && len(data) <= 32 {
			t.Fatal("fixture has no committed WAL frames")
		}
		put(t, l.DatabasePath+suffix, data)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(l.DatabasePath + "-journal"); err != nil {
		t.Fatal(err)
	}
	before, err := Inspect(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	bundle, _ := mustCreate(t, l, root, lock)
	restored, err := RestoreNew(context.Background(), bundle, filepath.Join(root, "restored"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Revalidate(context.Background(), restored, before); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(restored.DatabasePath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw, ciphertext, integrity string
	if err := db.QueryRow("SELECT raw_json FROM usage_events WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `{"immutable-history":true}` {
		t.Fatal("immutable usage row changed")
	}
	if err := db.QueryRow("SELECT value FROM settings").Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	restoredKey, _, err := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredProtector, err := security.NewProtector(restoredKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := restoredProtector.UnprotectString(ciphertext)
	if err != nil || plaintext != "fixture-secret" {
		t.Fatalf("DB/key mismatch: %v", err)
	}
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("restored integrity: %q %v", integrity, err)
	}
	if err := Revalidate(context.Background(), l, before); err != nil {
		t.Fatalf("backup modified source: %v", err)
	}
}

func TestMissingEntriesAreExplicitAndRestoreAbsent(t *testing.T) {
	l, root, lock := fixture(t)
	l.RequireConfig, l.RequireArchives = false, false
	for _, path := range []string{l.DatabasePath + "-wal", l.DatabasePath + "-shm", l.DatabasePath + "-journal", l.ConfigPath, l.ArchivesPath} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	bundle, m := mustCreate(t, l, root, lock)
	if len(m.Entries) != 7 {
		t.Fatalf("missing fixed inventory: %+v", m.Entries)
	}
	for _, name := range []string{"database-wal", "database-shm", "database-journal", "config", "usage-archives"} {
		if e, ok := m.Entries[name]; !ok || e.Present {
			t.Fatalf("missing entry not explicit: %s", name)
		}
	}
	restored, err := RestoreNew(context.Background(), bundle, filepath.Join(root, "restored"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Revalidate(context.Background(), restored, m); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredInputsCannotBeOmitted(t *testing.T) {
	for _, name := range []string{"database", "data-key", "config", "usage-archives"} {
		t.Run(name, func(t *testing.T) {
			l, root, lock := fixture(t)
			if err := os.RemoveAll(l.paths()[name]); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(root, "bundle")
			if _, err := Create(context.Background(), l, dest, lock); err == nil {
				t.Fatal("missing required input accepted")
			}
			noPublished(t, root, dest)
		})
	}
}

func TestSourceChangesPreventPublication(t *testing.T) {
	for _, change := range []string{"content-with-same-stat", "sidecar-added", "sidecar-deleted", "archive-added", "archive-deleted", "key-changed", "config-changed", "lock-replaced"} {
		t.Run(change, func(t *testing.T) {
			l, root, lock := fixture(t)
			if change == "sidecar-added" {
				if err := os.Remove(l.DatabasePath + "-wal"); err != nil {
					t.Fatal(err)
				}
			}
			hook := func() {
				switch change {
				case "content-with-same-stat":
					info, err := os.Stat(l.DatabasePath)
					if err != nil {
						t.Fatal(err)
					}
					put(t, l.DatabasePath, []byte(strings.Repeat("X", int(info.Size()))))
					if err := os.Chtimes(l.DatabasePath, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "sidecar-added":
					put(t, l.DatabasePath+"-wal", []byte("added"))
				case "sidecar-deleted":
					if err := os.Remove(l.DatabasePath + "-shm"); err != nil {
						t.Fatal(err)
					}
				case "archive-added":
					put(t, filepath.Join(l.ArchivesPath, "new.gz"), []byte("new"))
				case "archive-deleted":
					if err := os.RemoveAll(filepath.Join(l.ArchivesPath, "2026")); err != nil {
						t.Fatal(err)
					}
				case "key-changed":
					put(t, l.DataKeyPath, []byte("new key"))
				case "config-changed":
					put(t, l.ConfigPath, []byte("new config"))
				case "lock-replaced":
					if err := os.Rename(lock.Path(), lock.Path()+".old"); err != nil {
						t.Skipf("platform cannot rename held lock: %v", err)
					}
					put(t, lock.Path(), []byte("replacement"))
				}
			}
			dest := filepath.Join(root, "bundle")
			if _, err := create(context.Background(), l, dest, lock, createHooks{beforeRevalidate: hook}); err == nil {
				t.Fatal("source change accepted")
			}
			noPublished(t, root, dest)
		})
	}
}

func TestOwnershipFenceRequiredAndBoundToDatabase(t *testing.T) {
	l, root, lock := fixture(t)
	wrong, err := processlock.Acquire(filepath.Join(root, "other.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	for _, held := range []*processlock.Lock{nil, wrong} {
		if _, err := Create(context.Background(), l, filepath.Join(root, "bundle"), held); err == nil {
			t.Fatal("unbound lock accepted")
		}
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), l, filepath.Join(root, "bundle"), lock); err == nil {
		t.Fatal("closed lock accepted")
	}
}

func TestCancellationAndDiskFailureDoNotPublish(t *testing.T) {
	for _, action := range []string{"create", "restore"} {
		for _, failure := range []string{"canceled", "disk-full"} {
			t.Run(action+"/"+failure, func(t *testing.T) {
				l, root, lock := fixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				h := createHooks{beforeRevalidate: cancel}
				if failure == "disk-full" {
					h = createHooks{copy: func(context.Context, string, string) error { return syscall.ENOSPC }}
				}
				dest := filepath.Join(root, "result")
				var err error
				if action == "create" {
					_, err = create(ctx, l, dest, lock, h)
				} else {
					bundle, _ := mustCreate(t, l, root, lock)
					_, err = restoreNew(ctx, bundle, dest, h)
				}
				if err == nil {
					t.Fatal("failure accepted")
				}
				if failure == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatalf("wrong cancellation: %v", err)
				}
				if failure == "disk-full" && !errors.Is(err, syscall.ENOSPC) {
					t.Fatalf("wrong disk failure: %v", err)
				}
				noPublished(t, root, dest)
			})
		}
	}
}

func TestTamperedBundlesCannotBeRestored(t *testing.T) {
	for _, change := range []string{"database", "data-key", "config", "archive", "archive-extra", "root-extra", "missing-sidecar", "missing-entry", "truncated-manifest", "manifest-symlink"} {
		t.Run(change, func(t *testing.T) {
			l, root, lock := fixture(t)
			bundle, m := mustCreate(t, l, root, lock)
			switch change {
			case "database", "data-key", "config":
				put(t, filepath.Join(bundle, change), []byte("tampered"))
			case "archive":
				put(t, filepath.Join(bundle, "usage-archives", "2026", "10", "history.jsonl.gz"), []byte("tampered"))
			case "archive-extra":
				put(t, filepath.Join(bundle, "usage-archives", "extra"), []byte("extra"))
			case "root-extra":
				put(t, filepath.Join(bundle, "extra"), []byte("extra"))
			case "missing-sidecar":
				if err := os.Remove(filepath.Join(bundle, "database-wal")); err != nil {
					t.Fatal(err)
				}
			case "missing-entry":
				delete(m.Entries, "database-shm")
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(bundle, "manifest.json"), data)
			case "truncated-manifest":
				put(t, filepath.Join(bundle, "manifest.json"), []byte(`{"version":1`))
			case "manifest-symlink":
				if err := os.Remove(filepath.Join(bundle, "manifest.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(l.ConfigPath, filepath.Join(bundle, "manifest.json")); err != nil {
					t.Skip(err)
				}
			}
			dest := filepath.Join(root, "restored")
			if _, err := RestoreNew(context.Background(), bundle, dest); err == nil {
				t.Fatal("tampered bundle accepted")
			}
			noPublished(t, root, dest)
		})
	}
}

func TestRestoreSourceChangeDuringStagingFailsClosed(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	dest := filepath.Join(root, "restored")
	_, err := restoreNew(context.Background(), bundle, dest, createHooks{beforeRevalidate: func() { put(t, filepath.Join(bundle, "data-key"), []byte("replaced key")) }})
	if err == nil {
		t.Fatal("concurrent bundle change accepted")
	}
	noPublished(t, root, dest)
}

func TestRestoreCoherentBundleReplacementDuringStagingFailsClosed(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	dest := filepath.Join(root, "restored")
	_, err := restoreNew(context.Background(), bundle, dest, createHooks{beforeRevalidate: func() {
		put(t, filepath.Join(bundle, "data-key"), []byte("replacement key"))
		current, err := Inspect(context.Background(), BundleLayout(bundle))
		if err != nil {
			t.Fatal(err)
		}
		current.Fence = CooperatingFence
		data, err := json.Marshal(current)
		if err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(bundle, "manifest.json"), data)
		if _, err := Verify(context.Background(), bundle); err != nil {
			t.Fatalf("replacement is not coherent: %v", err)
		}
	}})
	if err == nil {
		t.Fatal("coherent source replacement accepted")
	}
	noPublished(t, root, dest)
}

func TestPathAliasesAndExistingRootsRejected(t *testing.T) {
	for _, kind := range []string{"overlap-key", "overlap-archive", "destination-in-archive", "destination-contains-source", "existing-destination", "source-symlink", "ancestor-symlink", "archive-hardlink", "archive-symlink"} {
		t.Run(kind, func(t *testing.T) {
			l, root, lock := fixture(t)
			dest := filepath.Join(root, "bundle")
			switch kind {
			case "overlap-key":
				l.DataKeyPath = l.DatabasePath
			case "overlap-archive":
				l.ArchivesPath = root
			case "destination-in-archive":
				dest = filepath.Join(l.ArchivesPath, "bundle")
			case "destination-contains-source":
				dest = root
			case "existing-destination":
				if err := os.Mkdir(dest, 0o700); err != nil {
					t.Fatal(err)
				}
			case "source-symlink":
				alias := filepath.Join(root, "key-link")
				if err := os.Symlink(l.DataKeyPath, alias); err != nil {
					t.Skip(err)
				}
				l.DataKeyPath = alias
			case "ancestor-symlink":
				alias := filepath.Join(root, "archive-link")
				if err := os.Symlink(l.ArchivesPath, alias); err != nil {
					t.Skip(err)
				}
				l.ArchivesPath = filepath.Join(alias, "2026")
			case "archive-hardlink":
				if err := os.Link(l.ConfigPath, filepath.Join(l.ArchivesPath, "alias")); err != nil {
					t.Skip(err)
				}
			case "archive-symlink":
				if err := os.Symlink(l.ConfigPath, filepath.Join(l.ArchivesPath, "alias")); err != nil {
					t.Skip(err)
				}
			}
			if _, err := Create(context.Background(), l, dest, lock); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	if _, err := RestoreNew(context.Background(), bundle, root); err == nil {
		t.Fatal("existing restore root accepted")
	}
}

func TestUnrepresentableArchiveNameDoesNotPublish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames do not admit invalid UTF-8 bytes")
	}
	l, root, lock := fixture(t)
	if err := os.WriteFile(filepath.Join(l.ArchivesPath, string([]byte{0xff})), []byte("archive"), 0o600); err != nil {
		if errors.Is(err, syscall.EILSEQ) {
			t.Skip("filesystem rejects invalid UTF-8 filenames")
		}
		t.Fatal(err)
	}
	dest := filepath.Join(root, "bundle")
	if _, err := Create(context.Background(), l, dest, lock); err == nil {
		t.Fatal("unrepresentable archive name accepted")
	}
	noPublished(t, root, dest)
}

func TestFilesystemCaseAliasesCannotOverlapSourcePaths(t *testing.T) {
	l, root, lock := fixture(t)
	alias := filepath.Join(root, strings.ToUpper(filepath.Base(l.DatabasePath)))
	if _, err := os.Stat(alias); os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive")
	} else if err != nil {
		t.Fatal(err)
	}
	l.DataKeyPath = alias
	dest := filepath.Join(root, "bundle")
	if _, err := Create(context.Background(), l, dest, lock); err == nil {
		t.Fatal("physical case alias of database accepted as key")
	}
	noPublished(t, root, dest)
}
