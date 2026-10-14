package maintenancebundle

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func TestRecoveryJournalWriter(t *testing.T) {
	path := os.Getenv("CPAMP_RECOVERY_DB")
	if path == "" {
		return
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL;
		PRAGMA cache_size=2; PRAGMA cache_spill=ON; BEGIN IMMEDIATE;
		UPDATE recovery_test SET note=CAST(hex(zeroblob(2000)) AS BLOB)`); err != nil {
		t.Fatal(err)
	}
	// SQLite leaves a genuine hot journal and spilled dirty pages. This process
	// intentionally exits without rollback/Close; parent must Wait before reuse.
	os.Exit(0)
}

func hotJournalFixture(t *testing.T) (Layout, string, *processlock.Lock) {
	t.Helper()
	l, root, lock := archiveLifecycleFixture(t, "verified", "")
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
	saveConnections(t, db, protector, "both")
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL;
		CREATE TABLE recovery_test (id INTEGER PRIMARY KEY, note BLOB);
		WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<64)
		INSERT INTO recovery_test SELECT n, zeroblob(4000) FROM seq`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRecoveryJournalWriter$")
	child.Env = append(os.Environ(), "CPAMP_RECOVERY_DB="+l.DatabasePath)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("journal writer/reap: %v %s", err, output)
	}
	dirty, err := os.ReadFile(l.DatabasePath)
	if err != nil || bytes.Equal(dirty, committed) {
		t.Fatalf("no dirty spill: %v", err)
	}
	journal, err := os.ReadFile(l.DatabasePath + "-journal")
	if err != nil || len(journal) < 28 || !bytes.Equal(journal[:8], []byte{0xd9, 0xd5, 0x05, 0xf9, 0x20, 0xa1, 0x63, 0xd7}) {
		t.Fatalf("no hot journal: %v", err)
	}
	return l, root, lock
}

func checkRecoveredRows(t *testing.T, root string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteURI(BundleLayout(root).DatabasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM recovery_test WHERE note=zeroblob(4000)").Scan(&count); err != nil || count != 64 {
		t.Fatalf("rollback did not restore all committed rows: %d %v", count, err)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("recovered foreign key violation")
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryRollbackJournalResealsPrivateCopy(t *testing.T) {
	l, root, lock := hotJournalFixture(t)
	before, err := Inspect(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), l, filepath.Join(root, "ordinary"), lock); err == nil {
		t.Fatal("ordinary Create bypassed journal gate")
	}
	dest := filepath.Join(root, "recovered")
	m, err := RecoverCreate(context.Background(), l, dest, lock)
	if err != nil {
		t.Fatal(err)
	}
	if m.Recovery == nil || m.Recovery.SourceRevision != before.SourceRevision || m.Recovery.Method != rollbackRecoveryMethod || m.SourceRevision == before.SourceRevision {
		t.Fatalf("missing pre/post recovery revisions: %+v", m.Recovery)
	}
	if _, err := Verify(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	checkRecoveredRows(t, dest)
	restored := filepath.Join(root, "restored")
	if _, err := RestoreNew(context.Background(), dest, restored); err != nil {
		t.Fatal(err)
	}
	checkRecoveredRows(t, restored)
	if err := RevalidateSource(context.Background(), l, before); err != nil {
		t.Fatalf("source was recovered or changed: %v", err)
	}
	// The recovered manifest describes different bytes; it cannot authorize the
	// original dirty source by substituting its pre-recovery revision.
	if err := RevalidateSource(context.Background(), l, m); err == nil {
		t.Fatal("recovered snapshot authorized dirty source")
	}
	assertNoStaging(t, dest)
}

func TestRecoveryFailsClosedAndPreservesSource(t *testing.T) {
	for _, failure := range []string{"invalid-journal", "checksum", "truncated", "no-sync", "wrong-geometry", "bad-page", "mixed-wal", "missing-key", "wrong-key", "cancel", "source-after-recovery", "malformed-database"} {
		t.Run(failure, func(t *testing.T) {
			l, root, lock := hotJournalFixture(t)
			switch failure {
			case "invalid-journal":
				put(t, l.DatabasePath+"-journal", []byte("invalid journal that SQLite must not silently ignore"))
			case "checksum", "truncated", "no-sync", "wrong-geometry", "bad-page":
				journal, err := os.ReadFile(l.DatabasePath + "-journal")
				if err != nil {
					t.Fatal(err)
				}
				sector := int(binary.BigEndian.Uint32(journal[20:24]))
				page := int(binary.BigEndian.Uint32(journal[24:28]))
				switch failure {
				case "checksum":
					journal[sector+4+page] ^= 0x80
				case "truncated":
					journal = journal[:len(journal)-1]
				case "no-sync":
					binary.BigEndian.PutUint32(journal[8:12], 0xffffffff)
				case "wrong-geometry":
					binary.BigEndian.PutUint32(journal[24:28], 0)
				case "bad-page":
					binary.BigEndian.PutUint32(journal[sector:sector+4], 0)
				}
				put(t, l.DatabasePath+"-journal", journal)
			case "mixed-wal":
				put(t, l.DatabasePath+"-wal", []byte("ambiguous WAL input"))
			case "missing-key":
				if err := os.Remove(l.DataKeyPath); err != nil {
					t.Fatal(err)
				}
			case "wrong-key":
				put(t, l.DataKeyPath, bytes.Repeat([]byte{0x5a}, 32))
			case "malformed-database":
				put(t, l.DatabasePath, []byte("corrupt DB"))
			}
			// Missing required files cannot be inventoried. Witness all source
			// bytes separately so rejection still proves no source-side writes.
			before := sourceFiles(t, l)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := createHooks{at: func(phase string) {
				if phase == "matching-validated" {
					if failure == "cancel" {
						cancel()
					}
					if failure == "source-after-recovery" {
						externalMutation(t, l, l.ConfigPath, "change")
					}
				}
			}}
			dest := filepath.Join(root, "recovered")
			if _, err := createBundle(ctx, l, dest, lock, h, true); err == nil {
				t.Fatal("invalid recovery published")
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("recovery published target: %v", err)
			}
			if failure != "source-after-recovery" && !equalSourceFiles(before, sourceFiles(t, l)) {
				t.Fatal("recovery mutated source")
			}
		})
	}
}

func sourceFiles(t *testing.T, l Layout) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for name, path := range l.paths() {
		if name == "usage-archives" {
			continue
		}
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out[name] = b
	}
	return out
}
func equalSourceFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !bytes.Equal(v, w) {
			return false
		}
	}
	return true
}

func TestRecoveryCrashRestart(t *testing.T) {
	for _, phase := range []string{"matching-validated", "renamed"} {
		t.Run(phase, func(t *testing.T) {
			l, root, lock := hotJournalFixture(t)
			before, err := Inspect(context.Background(), l)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(root, "result")
			killAtBoundary(t, boundaryCommand(t, l, "crash-recover", "CPAMP_BOUNDARY_PHASE="+phase, "CPAMP_BOUNDARY_DEST="+dest))
			state, err := RecoverPublication(context.Background(), dest)
			want := "aborted"
			if phase == "renamed" {
				want = "committed"
			}
			if err != nil || state != want {
				t.Fatalf("recovery restart %s: %v", state, err)
			}
			if phase == "renamed" {
				checkRecoveredRows(t, dest)
			}
			if err := RevalidateSource(context.Background(), l, before); err != nil {
				t.Fatal(err)
			}
			assertNoStaging(t, dest)
		})
	}
}

func TestRecoveryMetadataMustBeValid(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, m := mustCreate(t, l, root, lock)
	for _, e := range []*RecoveryEvidence{{Method: "manual-delete", SourceRevision: strings.Repeat("a", 64)}, {Method: rollbackRecoveryMethod, SourceRevision: "invalid"}} {
		m.Recovery = e
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(bundle, "manifest.json"), data)
		if _, err := Verify(context.Background(), bundle); err == nil {
			t.Fatal("invalid recovery metadata accepted")
		}
	}
}
