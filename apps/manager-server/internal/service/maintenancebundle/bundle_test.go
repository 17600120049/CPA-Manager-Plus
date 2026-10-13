package maintenancebundle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/setting"
	managersqlite "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	_ "modernc.org/sqlite"
)

func fixture(t *testing.T) (Layout, string, *processlock.Lock) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := Layout{
		DatabasePath:    filepath.Join(root, "usage.sqlite"),
		DataKeyPath:     filepath.Join(root, "data.key"),
		ConfigPath:      filepath.Join(root, "manager.json"),
		ArchivesPath:    filepath.Join(root, "usage-archives"),
		RequireConfig:   true,
		RequireArchives: true,
	}
	seedPath := filepath.Join(root, "seed.sqlite")
	seed, err := sql.Open("sqlite", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	if _, err := seed.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE test_table (id INTEGER PRIMARY KEY, note TEXT); INSERT INTO test_table VALUES (1, 'initial');"); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(seedPath + suffix)
		if err == nil {
			put(t, l.DatabasePath+suffix, data)
		}
	}
	_ = seed.Close()
	if _, _, err := security.LoadOrCreateDataKey("", l.DataKeyPath); err != nil {
		t.Fatal(err)
	}
	put(t, l.ConfigPath, []byte(`{"dbPath":"`+filepath.ToSlash(l.DatabasePath)+`"}`))
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

func rewriteManifest(t *testing.T, bundle string) {
	t.Helper()
	m, err := Inspect(context.Background(), BundleLayout(bundle))
	if err != nil {
		t.Fatal(err)
	}
	m.Fence = CooperatingFence
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(bundle, "manifest.json"), data)
	if _, err := Verify(context.Background(), bundle); err != nil {
		t.Fatalf("rewritten inventory must pass Verify: %v", err)
	}
}

func saveConnections(t *testing.T, db *sql.DB, protector *security.Protector, which string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at_ms INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	repo := setting.New(db, protector)
	if which == "setup" || which == "both" {
		if err := repo.SaveSetup(context.Background(), model.Setup{CPAUpstreamURL: "http://localhost:8317", ManagementKey: "fixture-secret"}); err != nil {
			t.Fatal(err)
		}
	}
	if which == "manager_config_v1" || which == "both" {
		if err := repo.SaveManagerConfig(context.Background(), model.ManagerConfig{CPAConnection: model.ManagerCPAConnectionConfig{CPABaseURL: "http://localhost:8317", ManagementKey: "fixture-secret"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveBootstrapState(context.Background(), model.BootstrapState{ConnectionStorageMigrationVersion: 2}); err != nil {
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
	if m.SourceRevision == "" || m.SourceRevision != before.SourceRevision {
		t.Fatalf("source revision mismatch: %q vs %q", m.SourceRevision, before.SourceRevision)
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
		a, errA := os.ReadFile(l.paths()[name])
		b, errB := os.ReadFile(path)
		if os.IsNotExist(errA) && os.IsNotExist(errB) {
			continue
		}
		if errA != nil {
			t.Fatal(errA)
		}
		if errB != nil {
			t.Fatal(errB)
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
	seedPath := filepath.Join(root, "seed.sqlite")
	seed, err := sql.Open("sqlite", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed.SetMaxOpenConns(1)
	defer seed.Close()
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE usage_events (id INTEGER PRIMARY KEY, raw_json TEXT)"} {
		if _, err := seed.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seed.Exec("INSERT INTO usage_events VALUES (1, ?)", `{"immutable-history":true}`); err != nil {
		t.Fatal(err)
	}
	saveConnections(t, seed, protector, "both")
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
	if err := os.Remove(l.DatabasePath + "-journal"); err != nil && !os.IsNotExist(err) {
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
	var raw, integrity string
	if err := db.QueryRow("SELECT raw_json FROM usage_events WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `{"immutable-history":true}` {
		t.Fatal("immutable usage row changed")
	}
	restoredKey, _, err := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredProtector, err := security.NewProtector(restoredKey)
	if err != nil {
		t.Fatal(err)
	}
	repo := setting.New(db, restoredProtector)
	setup, found, err := repo.LoadSetup(context.Background())
	if err != nil || !found || setup.ManagementKey != "fixture-secret" {
		t.Fatalf("DB/key mismatch: %v", err)
	}
	cfg, found, err := repo.LoadManagerConfig(context.Background())
	if err != nil || !found || cfg.CPAConnection.ManagementKey != "fixture-secret" {
		t.Fatalf("manager config/key mismatch: %v", err)
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
				if err := os.Remove(l.DatabasePath + "-wal"); err != nil && !os.IsNotExist(err) {
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

func TestRestoreCorruptSQLite(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	// Tamper database in bundle with corrupt bytes that are not SQLite
	put(t, filepath.Join(bundle, "database"), []byte("not-a-sqlite-database-corrupted-bytes-payload-header"))
	// Re-inspect bundle layout to produce coherent manifest matching the tampered file
	// so that Verify's digest check passes, forcing the failure to come from matching evidence!
	tamperedManifest, err := Inspect(context.Background(), BundleLayout(bundle))
	if err != nil {
		t.Fatal(err)
	}
	tamperedManifest.Fence = CooperatingFence
	data, err := json.MarshalIndent(tamperedManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(bundle, "manifest.json"), append(data, '\n'))
	// Verify should pass inventory integrity because the manifest was recomputed.
	if _, err := Verify(context.Background(), bundle); err != nil {
		t.Fatalf("verify failed on coherent manifest: %v", err)
	}
	dest := filepath.Join(root, "restored")
	if _, err := RestoreNew(context.Background(), bundle, dest); err == nil {
		t.Fatal("corrupt SQLite accepted during restore")
	}
	noPublished(t, root, dest)
}

func TestRestoreWrongKey(t *testing.T) {
	for _, which := range []string{"setup", "manager_config_v1", "both"} {
		t.Run(which, func(t *testing.T) {
			l, root, lock := fixture(t)
			db, err := sql.Open("sqlite", l.DatabasePath)
			if err != nil {
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
			saveConnections(t, db, protector, which)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			bundle, _ := mustCreate(t, l, root, lock)

			// Replace data-key in bundle with a different valid 32-byte key
			otherKeyPath := filepath.Join(root, "other.key")
			if _, _, err := security.LoadOrCreateDataKey("", otherKeyPath); err != nil {
				t.Fatal(err)
			}
			otherKeyBytes, err := os.ReadFile(otherKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(bundle, "data-key"), otherKeyBytes)

			rewriteManifest(t, bundle)
			dest := filepath.Join(root, "restored")
			if _, err := RestoreNew(context.Background(), bundle, dest); err == nil || !strings.Contains(err.Error(), "data key") {
				t.Fatalf("wrong key must fail matching evidence: %v", err)
			}
			noPublished(t, root, dest)
		})
	}
}

func TestRestoreArchiveMatchingEvidence(t *testing.T) {
	for _, status := range []string{"published", "verified"} {
		for _, change := range []string{"none", "missing", "root-missing", "foreign-content", "wrong-size", "traversal", "absolute", "wrong-run", "backslash", "directory"} {
			t.Run(status+"/"+change, func(t *testing.T) {
				l, root, lock := fixture(t)
				fileName := "run-1/segment.jsonl.gz"
				segPath := filepath.Join(l.ArchivesPath, filepath.FromSlash(fileName))
				if err := os.MkdirAll(filepath.Dir(segPath), 0o700); err != nil {
					t.Fatal(err)
				}
				payload := []byte("instance A archive bytes")
				put(t, segPath, payload)
				digest := sha256.Sum256(payload)
				db, err := sql.Open("sqlite", l.DatabasePath)
				if err != nil {
					t.Fatal(err)
				}
				// These are the production columns consumed by bundle inspection.
				if _, err := db.Exec(`CREATE TABLE usage_archive_segments (
					run_id TEXT NOT NULL, sequence INTEGER NOT NULL, status TEXT NOT NULL,
					file_name TEXT NOT NULL, content_sha256 TEXT NOT NULL, compressed_bytes INTEGER NOT NULL,
					PRIMARY KEY (run_id, sequence));
					INSERT INTO usage_archive_segments VALUES ('run-1', 1, ?, ?, ?, ?)`,
					status, fileName, hex.EncodeToString(digest[:]), len(payload)); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				bundle, _ := mustCreate(t, l, root, lock)
				bundleSeg := filepath.Join(bundle, "usage-archives", filepath.FromSlash(fileName))
				switch change {
				case "root-missing":
					if err := os.RemoveAll(filepath.Join(bundle, "usage-archives")); err != nil {
						t.Fatal(err)
					}
				case "missing", "directory":
					if err := os.Remove(bundleSeg); err != nil {
						t.Fatal(err)
					}
					if change == "directory" {
						if err := os.Mkdir(bundleSeg, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				case "foreign-content":
					// Same path and byte count; only DB-recorded SHA-256 exposes the mix.
					put(t, bundleSeg, []byte("instance B archive bytes"))
				case "wrong-size", "traversal", "absolute", "wrong-run", "backslash":
					db, err := sql.Open("sqlite", filepath.Join(bundle, "database"))
					if err != nil {
						t.Fatal(err)
					}
					query, value := "UPDATE usage_archive_segments SET file_name = ?", "../database"
					switch change {
					case "wrong-size":
						query, value = "UPDATE usage_archive_segments SET compressed_bytes = ?", "999"
					case "absolute":
						value = filepath.ToSlash(bundleSeg)
					case "wrong-run":
						query, value = "UPDATE usage_archive_segments SET run_id = ?", "other-run"
					case "backslash":
						value = `run-1\segment.jsonl.gz`
					}
					if _, err := db.Exec(query, value); err != nil {
						t.Fatal(err)
					}
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
				}
				rewriteManifest(t, bundle)
				dest := filepath.Join(root, "restored")
				_, err = RestoreNew(context.Background(), bundle, dest)
				if change == "none" {
					if err != nil {
						t.Fatalf("matching archive must restore: %v", err)
					}
				} else {
					if err == nil || !strings.Contains(err.Error(), "archive") {
						t.Fatalf("invalid archive must fail matching evidence: %v", err)
					}
					noPublished(t, root, dest)
				}
			})
		}
	}
}

func archiveRunFixture(t *testing.T) (Layout, string, *processlock.Lock) {
	t.Helper()
	l, root, lock := fixture(t)
	db, err := managersqlite.Open(l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runDir := filepath.Join(l.ArchivesPath, "run-1")
	if err := os.Mkdir(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	segment := []byte("instance A archive bytes")
	manifest := []byte(`{"instance":"A"}`)
	put(t, filepath.Join(runDir, "segment.jsonl.gz"), segment)
	put(t, filepath.Join(runDir, "manifest.json"), manifest)
	segmentSHA, manifestSHA := sha256.Sum256(segment), sha256.Sum256(manifest)
	// Use the migrated production schema, including nullable run manifest fields.
	// Completed history has no remaining raw usage_events in this fixture.
	if _, err := db.Exec(`INSERT INTO usage_archive_runs (
		id, schema_version, format, status, cutoff_timestamp_ms, target_event_id,
		event_count, created_at_ms, updated_at_ms, manifest_file, manifest_sha256,
		last_deleted_event_id, deleted_event_count
	) VALUES ('run-1', 1, 'gzip-jsonl-v1', 'completed', 2, 1, 1, 1, 1,
		'run-1/manifest.json', ?, 1, 1)`, hex.EncodeToString(manifestSHA[:])); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO usage_archive_segments (
		run_id, sequence, status, file_name, first_event_id, last_event_id,
		min_timestamp_ms, max_timestamp_ms, event_count, uncompressed_bytes,
		compressed_bytes, content_sha256, event_hash_digest, created_at_ms
	) VALUES ('run-1', 1, 'verified', 'run-1/segment.jsonl.gz', 1, 1, 1, 1, 1, 1, ?, ?, 'fixture-event-digest', 1)`,
		len(segment), hex.EncodeToString(segmentSHA[:])); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return l, root, lock
}

func TestRestoreInvalidArchiveSegmentStatus(t *testing.T) {
	for _, status := range []string{"garbage", "archived", "completed", ""} {
		for _, change := range []string{"missing", "foreign-content"} {
			t.Run(status+"/"+change, func(t *testing.T) {
				l, root, lock := archiveRunFixture(t)
				bundle, _ := mustCreate(t, l, root, lock)
				db, err := sql.Open("sqlite", filepath.Join(bundle, "database"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("UPDATE usage_archive_segments SET status = ?", status); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				segPath := filepath.Join(bundle, "usage-archives", "run-1", "segment.jsonl.gz")
				if change == "missing" {
					if err := os.Remove(segPath); err != nil {
						t.Fatal(err)
					}
				} else {
					put(t, segPath, []byte("instance B archive bytes"))
				}
				rewriteManifest(t, bundle)
				dest := filepath.Join(root, "restored")
				if _, err := RestoreNew(context.Background(), bundle, dest); err == nil || !strings.Contains(err.Error(), "archive segment status") {
					t.Fatalf("invalid status must fail before a record can escape matching: %v", err)
				}
				noPublished(t, root, dest)
			})
		}
	}
}

func TestRestoreArchiveRunManifestMatchingEvidence(t *testing.T) {
	for _, change := range []string{"none", "missing", "foreign-content", "missing-sha", "missing-file", "invalid-sha", "traversal", "absolute", "backslash", "wrong-run", "empty", "oversize", "directory", "segments-table-absent"} {
		t.Run(change, func(t *testing.T) {
			l, root, lock := archiveRunFixture(t)
			bundle, _ := mustCreate(t, l, root, lock)
			manifestPath := filepath.Join(bundle, "usage-archives", "run-1", "manifest.json")
			switch change {
			case "missing", "directory", "segments-table-absent":
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
				if change == "directory" {
					if err := os.Mkdir(manifestPath, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "foreign-content":
				put(t, manifestPath, []byte(`{"instance":"B"}`))
			case "empty":
				put(t, manifestPath, nil)
			case "oversize":
				put(t, manifestPath, make([]byte, 4*1024*1024+1))
			}
			queries := map[string]string{
				"missing-sha":           "UPDATE usage_archive_runs SET manifest_sha256 = NULL",
				"missing-file":          "UPDATE usage_archive_runs SET manifest_file = ''",
				"invalid-sha":           "UPDATE usage_archive_runs SET manifest_sha256 = 'invalid'",
				"traversal":             "UPDATE usage_archive_runs SET manifest_file = '../database'",
				"backslash":             `UPDATE usage_archive_runs SET manifest_file = 'run-1\manifest.json'`,
				"wrong-run":             "UPDATE usage_archive_runs SET id = 'other-run'",
				"segments-table-absent": "DROP TABLE usage_archive_segments",
			}
			if query, ok := queries[change]; ok || change == "absolute" {
				db, err := sql.Open("sqlite", filepath.Join(bundle, "database"))
				if err != nil {
					t.Fatal(err)
				}
				if change == "absolute" {
					_, err = db.Exec("UPDATE usage_archive_runs SET manifest_file = ?", filepath.ToSlash(manifestPath))
				} else {
					_, err = db.Exec(query)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			rewriteManifest(t, bundle)
			dest := filepath.Join(root, "restored")
			_, err := RestoreNew(context.Background(), bundle, dest)
			if change == "none" {
				if err != nil {
					t.Fatalf("matching run manifest must restore: %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "archive") {
					t.Fatalf("invalid run manifest must fail matching evidence: %v", err)
				}
				noPublished(t, root, dest)
			}
		})
	}
}

func TestUnpublishedArchiveRunManifestFieldsAreOptional(t *testing.T) {
	for _, pair := range []string{"null", "empty"} {
		t.Run(pair, func(t *testing.T) {
			l, root, lock := fixture(t)
			db, err := managersqlite.Open(l.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`INSERT INTO usage_archive_runs (
				id, schema_version, format, status, cutoff_timestamp_ms, target_event_id,
				event_count, created_at_ms, updated_at_ms
			) VALUES ('run-1', 1, 'gzip-jsonl-v1', 'previewed', 2, 1, 1, 1, 1)`); err != nil {
				t.Fatal(err)
			}
			if pair == "empty" {
				if _, err := db.Exec("UPDATE usage_archive_runs SET manifest_file = '', manifest_sha256 = ''"); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			bundle, _ := mustCreate(t, l, root, lock)
			if _, err := RestoreNew(context.Background(), bundle, filepath.Join(root, "restored")); err != nil {
				t.Fatalf("unpublished runs need not have manifest evidence: %v", err)
			}
		})
	}
}

func TestRestoreInspectionErrorsFailClosed(t *testing.T) {
	cases := []struct {
		name, sql, want string
	}{
		{"settings-query", `CREATE TABLE settings (value TEXT)`, "inspect persisted CPA connection storage"},
		{"settings-view", `CREATE VIEW settings AS SELECT 'setup' AS key, '{}' AS value`, "not a table"},
		{"settings-scan", `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT); INSERT INTO settings VALUES ('setup', NULL)`, "scan encrypted CPA connection"},
		{"settings-json", `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT); INSERT INTO settings VALUES ('setup', '{')`, "invalid setup JSON"},
		{"bootstrap-json", `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT); INSERT INTO settings VALUES ('bootstrap_state_v1', '{')`, "invalid bootstrap state"},
		{"migrated-plaintext", `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
			INSERT INTO settings VALUES ('bootstrap_state_v1', '{"connectionStorageMigrationVersion":2}');
			INSERT INTO settings VALUES ('manager_config_v1', '{"cpaConnection":{"managementKey":"plaintext"}}')`, "not a valid encrypted envelope"},
		{"migrated-malformed-envelope", `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
			INSERT INTO settings VALUES ('bootstrap_state_v1', '{"connectionStorageMigrationVersion":2}');
			INSERT INTO settings VALUES ('setup', '{"managementKey":"enc:v1:broken"}')`, "not a valid encrypted envelope"},
		{"archive-query", `CREATE TABLE usage_archive_segments (file_name TEXT, status TEXT)`, "query archive matching evidence"},
		{"archive-view", `CREATE VIEW usage_archive_segments AS SELECT 'published' AS status`, "not a table"},
		{"archive-scan", `CREATE TABLE usage_archive_segments (run_id TEXT, file_name TEXT, content_sha256 TEXT, compressed_bytes INTEGER, status TEXT);
			INSERT INTO usage_archive_segments VALUES ('run-1', NULL, '', 1, 'published')`, "scan archive matching evidence"},
		{"archive-size-scan", `CREATE TABLE usage_archive_segments (run_id TEXT, file_name TEXT, content_sha256 TEXT, compressed_bytes INTEGER, status TEXT);
			INSERT INTO usage_archive_segments VALUES ('run-1', 'run-1/segment.gz', '', 'invalid', 'verified')`, "scan archive matching evidence"},
		{"archive-digest", `CREATE TABLE usage_archive_segments (run_id TEXT, file_name TEXT, content_sha256 TEXT, compressed_bytes INTEGER, status TEXT);
			INSERT INTO usage_archive_segments VALUES ('run-1', 'run-1/segment.gz', 'invalid', 1, 'verified')`, "invalid archive segment digest"},
		{"archive-run-query", `CREATE TABLE usage_archive_runs (id TEXT)`, "query archive run manifest evidence"},
		{"archive-run-view", `CREATE VIEW usage_archive_runs AS SELECT 'run-1' AS id`, "not a table"},
		{"archive-run-scan", `CREATE TABLE usage_archive_runs (id TEXT, manifest_file TEXT, manifest_sha256 TEXT);
			INSERT INTO usage_archive_runs VALUES (NULL, 'run-1/manifest.json', 'invalid')`, "scan archive run manifest evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, root, lock := fixture(t)
			bundle, _ := mustCreate(t, l, root, lock)
			db, err := sql.Open("sqlite", filepath.Join(bundle, "database"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			rewriteManifest(t, bundle)
			dest := filepath.Join(root, "restored")
			if _, err := RestoreNew(context.Background(), bundle, dest); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("inspection must fail closed with %q: %v", tc.want, err)
			}
			noPublished(t, root, dest)
		})
	}
}

func TestMatchingEvidenceMissingKeyDoesNotCreateReplacement(t *testing.T) {
	l, _, _ := fixture(t)
	if err := os.Remove(l.DataKeyPath); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMatchingEvidence(context.Background(), l); err == nil {
		t.Fatal("missing key must fail matching inspection")
	}
	if _, err := os.Lstat(l.DataKeyPath); !os.IsNotExist(err) {
		t.Fatalf("inspection generated a replacement data key: %v", err)
	}
}

func TestLegacyPlaintextConnectionStorageRemainsReadable(t *testing.T) {
	l, root, lock := fixture(t)
	db, err := sql.Open("sqlite", l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
		INSERT INTO settings VALUES ('setup', '{"managementKey":"legacy-plaintext"}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	bundle, _ := mustCreate(t, l, root, lock)
	if _, err := RestoreNew(context.Background(), bundle, filepath.Join(root, "restored")); err != nil {
		t.Fatalf("pre-migration legacy plaintext must retain existing guard semantics: %v", err)
	}
}

func TestRestoreCancellationDuringStaging(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dest := filepath.Join(root, "restored")
	if _, err := restoreNew(ctx, bundle, dest, createHooks{beforeRevalidate: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("restore must return cancellation: %v", err)
	}
	noPublished(t, root, dest)
}

func TestManifestVersionAndRequiredRevision(t *testing.T) {
	for _, format := range []string{"legacy-v1", "v1-with-revision", "future-version", "v2-without-revision"} {
		t.Run(format, func(t *testing.T) {
			l, root, lock := fixture(t)
			bundle, m := mustCreate(t, l, root, lock)
			if m.Version != 2 || m.SourceRevision == "" {
				t.Fatal("new bundles must use format v2 with a source revision")
			}
			switch format {
			case "legacy-v1":
				m.Version, m.SourceRevision = 1, ""
			case "v1-with-revision":
				m.Version = 1
			case "future-version":
				m.Version = Version + 1
			case "v2-without-revision":
				m.SourceRevision = ""
			}
			if err := RevalidateSource(context.Background(), l, m); err == nil {
				t.Fatal("unsupported source authorization accepted")
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(bundle, "manifest.json"), data)
			if _, err := Verify(context.Background(), bundle); err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("unsupported format must be explicitly rejected: %v", err)
			}
			dest := filepath.Join(root, "restored")
			if _, err := RestoreNew(context.Background(), bundle, dest); err == nil {
				t.Fatal("unsupported format restored")
			}
			noPublished(t, root, dest)
		})
	}
}

func TestRestoreWrongConfig(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	put(t, filepath.Join(bundle, "config"), []byte("{invalid-json-content"))

	tamperedManifest, err := Inspect(context.Background(), BundleLayout(bundle))
	if err != nil {
		t.Fatal(err)
	}
	tamperedManifest.Fence = CooperatingFence
	data, err := json.MarshalIndent(tamperedManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(bundle, "manifest.json"), append(data, '\n'))

	dest := filepath.Join(root, "restored")
	if _, err := RestoreNew(context.Background(), bundle, dest); err == nil {
		t.Fatal("wrong config accepted during restore")
	}
	noPublished(t, root, dest)
}

func TestVerifyPartialBundle(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, m := mustCreate(t, l, root, lock)

	// 1. Missing manifest.json
	partialRoot := filepath.Join(root, "partial-no-manifest")
	if err := os.Mkdir(partialRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(partialRoot, "database"), []byte("some-data"))
	if _, err := Verify(context.Background(), partialRoot); err == nil {
		t.Fatal("missing manifest accepted")
	}

	// 2. Extra unexpected file in bundle root
	extraFile := filepath.Join(bundle, "unexpected.txt")
	put(t, extraFile, []byte("extra"))
	if _, err := Verify(context.Background(), bundle); err == nil {
		t.Fatal("extra file in bundle accepted")
	}
	_ = os.Remove(extraFile)

	// 3. Unpublished temporary directory in bundle root
	unpubDir := filepath.Join(bundle, ".manager-bundle-unpublished-12345")
	if err := os.Mkdir(unpubDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), bundle); err == nil {
		t.Fatal("unpublished temporary entry accepted")
	}
	_ = os.RemoveAll(unpubDir)

	// 4. Missing required entry in manifest
	corruptManifest := m
	corruptManifest.SourceRevision = "tampered-revision"
	data, err := json.MarshalIndent(corruptManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(bundle, "manifest.json"), append(data, '\n'))
	if _, err := Verify(context.Background(), bundle); err == nil {
		t.Fatal("manifest source revision mismatch accepted")
	}
}

func TestPartialOutputDirectoryCannotBeRestoredOrCreated(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)

	existingDest := filepath.Join(root, "existing-dest")
	if err := os.Mkdir(existingDest, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(existingDest, "leftover.file"), []byte("leftover"))

	// Create into existing destination fails
	if _, err := Create(context.Background(), l, existingDest, lock); err == nil {
		t.Fatal("create into existing destination accepted")
	}

	// RestoreNew into existing destination fails
	if _, err := RestoreNew(context.Background(), bundle, existingDest); err == nil {
		t.Fatal("restore into existing destination accepted")
	}
}

func TestSourceChangedSinceAuthorization(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, m := mustCreate(t, l, root, lock)

	// Before any mutation, RevalidateSource succeeds
	if err := RevalidateSource(context.Background(), l, m); err != nil {
		t.Fatalf("revalidate source before mutation: %v", err)
	}

	// Mutate source database by inserting a row
	db, err := sql.Open("sqlite", l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO test_table VALUES (2, 'new-row');"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// RevalidateSource MUST fail now because source changed
	if err := RevalidateSource(context.Background(), l, m); err == nil {
		t.Fatal("revalidate source succeeded after source database was mutated")
	}

	// Restore of the old bundle still works to a clean new root, but source is marked changed
	dest := filepath.Join(root, "restored-safe")
	restored, err := RestoreNew(context.Background(), bundle, dest)
	if err != nil {
		t.Fatalf("restore new failed: %v", err)
	}
	// Restored DB has the initial state, NOT the mutated row
	rdb, err := sql.Open("sqlite", restored.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	var count int
	if err := rdb.QueryRow("SELECT count(*) FROM test_table").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("restored count %d != 1", count)
	}
}
