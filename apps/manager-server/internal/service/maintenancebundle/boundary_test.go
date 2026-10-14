package maintenancebundle

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
)

// All helpers run exclusively on task-owned test files. No helper acquires a
// Manager fence when mutating: the SQL writer really ignores that protocol.
func TestBoundarySubprocess(t *testing.T) {
	mode := os.Getenv("CPAMP_BOUNDARY_MODE")
	if mode == "" {
		return
	}
	var l Layout
	if err := json.Unmarshal([]byte(os.Getenv("CPAMP_BOUNDARY_LAYOUT")), &l); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "mutate":
		name, action := os.Getenv("CPAMP_BOUNDARY_PATH"), os.Getenv("CPAMP_BOUNDARY_ACTION")
		if action == "sql" {
			db, err := sql.Open("sqlite", l.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec("INSERT INTO test_table VALUES (2, 'external writer')")
			if err := errors.Join(err, db.Close()); err != nil {
				t.Fatal(err)
			}
		} else if action == "delete" {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(name, []byte("non-cooperative mutation"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	case "crash-create", "crash-restore", "crash-recover":
		h := createHooks{at: func(phase string) {
			if phase == os.Getenv("CPAMP_BOUNDARY_PHASE") {
				fmt.Println("BOUNDARY-READY")
				var command [1]byte
				_, _ = os.Stdin.Read(command[:]) // parent holds the pipe until Kill + Wait
				os.Exit(93)                      // must never return to graceful cleanup
			}
		}}
		dest := os.Getenv("CPAMP_BOUNDARY_DEST")
		var err error
		if mode == "crash-restore" {
			_, err = restoreNew(context.Background(), os.Getenv("CPAMP_BOUNDARY_BUNDLE"), dest, h)
		} else {
			lock, lockErr := processlock.AcquireExisting(l.DatabasePath)
			if lockErr != nil {
				t.Fatal(lockErr)
			}
			defer lock.Close()
			_, err = createBundle(context.Background(), l, dest, lock, h, mode == "crash-recover")
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash checkpoint was not reached")
	default:
		t.Fatal("unknown helper mode")
	}
}

func boundaryCommand(t *testing.T, l Layout, mode string, env ...string) *exec.Cmd {
	t.Helper()
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBoundarySubprocess$", "-test.timeout=1m")
	cmd.Env = append(os.Environ(), "CPAMP_BOUNDARY_MODE="+mode, "CPAMP_BOUNDARY_LAYOUT="+string(data))
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

func externalMutation(t *testing.T, l Layout, path, action string) {
	t.Helper()
	cmd := boundaryCommand(t, l, "mutate", "CPAMP_BOUNDARY_PATH="+path, "CPAMP_BOUNDARY_ACTION="+action)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external mutation/reap: %v %s", err, output)
	}
}

func operationRecord(t *testing.T, dest string) publicationRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(operationRoot(dest), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r publicationRecord
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func assertNoStaging(t *testing.T, dest string) {
	t.Helper()
	entries, err := os.ReadDir(operationRoot(dest))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" && e.Name() != "state.json.manager.lock" {
			t.Fatalf("unhandled operation artifact %s", e.Name())
		}
	}
}

func TestBoundaryExternalMutationsFailClosed(t *testing.T) {
	for _, phase := range []string{"copying", "matching-validated", "before-publication"} {
		for _, mutation := range []string{"database", "database-wal", "database-shm", "journal-add", "wal-add", "wal-delete", "shm-add", "shm-delete", "archive-change", "archive-add", "archive-delete", "config", "data-key", "sql-writer"} {
			t.Run(phase+"/"+mutation, func(t *testing.T) {
				l, root, lock := fixture(t)
				path, action := l.paths()[mutation], "change"
				switch mutation {
				case "journal-add":
					path = l.DatabasePath + "-journal"
				case "wal-add", "shm-add":
					suffix := "-wal"
					if mutation == "shm-add" {
						suffix = "-shm"
					}
					path = l.DatabasePath + suffix
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "wal-delete":
					path, action = l.DatabasePath+"-wal", "delete"
				case "shm-delete":
					path, action = l.DatabasePath+"-shm", "delete"
				case "archive-change", "archive-delete":
					path = filepath.Join(l.ArchivesPath, "2026", "10", "history.jsonl.gz")
					if mutation == "archive-delete" {
						action = "delete"
					}
				case "archive-add":
					path = filepath.Join(l.ArchivesPath, "new.gz")
				case "sql-writer":
					action = "sql"
				}
				// Removing WAL from this fixture loses its CREATE TABLE. Replace
				// it with a checkpointed database before testing optional addition.
				if mutation == "wal-add" {
					seed, err := sql.Open("sqlite", l.DatabasePath)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := seed.Exec("CREATE TABLE IF NOT EXISTS stable (id INTEGER)"); err != nil {
						t.Fatal(err)
					}
					if err := seed.Close(); err != nil {
						t.Fatal(err)
					}
				}
				baseline, err := Inspect(context.Background(), l)
				if err != nil {
					t.Fatal(err)
				}
				mutated := false
				h := createHooks{at: func(at string) {
					if at == phase && !mutated {
						mutated = true
						externalMutation(t, l, path, action)
					}
				}}
				h.copy = func(ctx context.Context, from, to string) error {
					if err := copyFile(ctx, from, to); err != nil {
						return err
					}
					h.checkpoint("copying")
					return nil
				}
				dest := filepath.Join(root, "result")
				if _, err := create(context.Background(), l, dest, lock, h); err == nil {
					t.Fatal("moving source accepted")
				}
				if !mutated {
					t.Fatal("test did not reach external writer")
				}
				if err := RevalidateSource(context.Background(), l, baseline); err == nil {
					t.Fatal("stale source evidence remained valid after reaped writer")
				}
				if _, err := Verify(context.Background(), dest); err == nil {
					t.Fatal("false consistent output")
				}
				if r := operationRecord(t, dest); r.State != "aborted" {
					t.Fatalf("not aborted: %+v", r)
				}
				assertNoStaging(t, dest)
			})
		}
	}
}

func TestBoundaryCancellationCommitPointAndRetry(t *testing.T) {
	for _, action := range []string{"create", "restore"} {
		for _, phase := range []string{"staging-created", "copying", "manifest-written", "before-publication", "renamed"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				l, root, lock := fixture(t)
				bundle, _ := mustCreate(t, l, root, lock)
				baseline, err := Inspect(context.Background(), l)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				h := createHooks{at: func(at string) {
					if at == phase {
						cancel()
					}
				}}
				h.copy = func(ctx context.Context, from, to string) error {
					if err := copyFile(ctx, from, to); err != nil {
						return err
					}
					h.checkpoint("copying")
					return nil
				}
				dest := filepath.Join(root, "result")
				if action == "create" {
					_, err = create(ctx, l, dest, lock, h)
				} else {
					_, err = restoreNew(ctx, bundle, dest, h)
				}
				if phase == "renamed" {
					if err != nil {
						t.Fatalf("cancel after commit must finish publication: %v", err)
					}
					if _, err := Verify(context.Background(), dest); err != nil {
						t.Fatal(err)
					}
				} else {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancellation: %v", err)
					}
					if _, err := os.Lstat(dest); !os.IsNotExist(err) {
						t.Fatalf("cancel published output: %v", err)
					}
					assertNoStaging(t, dest)
					if action == "create" {
						_, err = Create(context.Background(), l, dest, lock)
					} else {
						_, err = RestoreNew(context.Background(), bundle, dest)
					}
					if err != nil {
						t.Fatalf("retry poisoned by cancellation: %v", err)
					}
				}
				if err := RevalidateSource(context.Background(), l, baseline); err != nil {
					t.Fatalf("source changed: %v", err)
				}
				assertNoStaging(t, dest)
			})
		}
	}
}

func killAtBoundary(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "BOUNDARY-READY" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	var reached bool
	select {
	case reached = <-ready:
	case <-time.After(30 * time.Second):
	}
	_ = cmd.Process.Kill()
	err = cmd.Wait() // process must be reaped before inspecting its files
	if !reached || err == nil {
		t.Fatalf("crash/reap fixture failed: reached=%v err=%v stderr=%s", reached, err, stderr.String())
	}
}

func TestBoundaryCrashRestartPublication(t *testing.T) {
	for _, action := range []string{"create", "restore"} {
		for _, phase := range []string{"staging-created", "manifest-written", "before-publication", "renamed", "publication-synced", "committed"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				l, root, lock := fixture(t)
				bundle, _ := mustCreate(t, l, root, lock)
				baseline, err := Inspect(context.Background(), l)
				if err != nil {
					t.Fatal(err)
				}
				if err := lock.Close(); err != nil {
					t.Fatal(err)
				}
				dest := filepath.Join(root, "result")
				cmd := boundaryCommand(t, l, "crash-"+action, "CPAMP_BOUNDARY_PHASE="+phase, "CPAMP_BOUNDARY_DEST="+dest, "CPAMP_BOUNDARY_BUNDLE="+bundle)
				killAtBoundary(t, cmd)
				r := operationRecord(t, dest)
				stage := filepath.Join(operationRoot(dest), stagingPrefix+r.ID)
				if _, err := Verify(context.Background(), stage); err == nil {
					t.Fatal("unpublished staging accepted as committed")
				}
				committed := phase == "renamed" || phase == "publication-synced" || phase == "committed"
				if committed {
					if _, err := Verify(context.Background(), dest); err != nil {
						t.Fatalf("rename exposed incomplete output: %v", err)
					}
				} else if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Fatalf("crash before rename published output: %v", err)
				}
				state, err := RecoverPublication(context.Background(), dest)
				want := "aborted"
				if committed {
					want = "committed"
				}
				if err != nil || state != want {
					t.Fatalf("restart state=%s want=%s error=%v", state, want, err)
				}
				assertNoStaging(t, dest)
				if !committed {
					if action == "create" {
						held, err := processlock.AcquireExisting(l.DatabasePath)
						if err != nil {
							t.Fatal(err)
						}
						_, err = Create(context.Background(), l, dest, held)
						closeErr := held.Close()
						if err := errors.Join(err, closeErr); err != nil {
							t.Fatalf("restart retry: %v", err)
						}
					} else if _, err := RestoreNew(context.Background(), bundle, dest); err != nil {
						t.Fatal(err)
					}
				}
				if err := RevalidateSource(context.Background(), l, baseline); err != nil {
					t.Fatalf("crash changed source: %v", err)
				}
			})
		}
	}
}

func TestBoundarySyncFailureBeforeAndAfterRename(t *testing.T) {
	for _, afterRename := range []bool{false, true} {
		t.Run(fmt.Sprint(afterRename), func(t *testing.T) {
			l, root, lock := fixture(t)
			dest := filepath.Join(root, "result")
			renamed := false
			h := createHooks{at: func(phase string) {
				if phase == "renamed" {
					renamed = true
				}
			}, syncDirectory: func(string) error {
				if renamed == afterRename {
					return syscall.EIO
				}
				return nil
			}}
			_, err := create(context.Background(), l, dest, lock, h)
			if !errors.Is(err, syscall.EIO) || errors.Is(err, ErrPublicationUncertain) != afterRename {
				t.Fatalf("wrong durability outcome: %v", err)
			}
			state, err := RecoverPublication(context.Background(), dest)
			want := "aborted"
			if afterRename {
				want = "committed"
			}
			if err != nil || state != want {
				t.Fatalf("sync failure recovery %s/%s: %v", state, want, err)
			}
			assertNoStaging(t, dest)
		})
	}
}

func TestBoundaryRecoveryNeverAdoptsOrDeletesForeignTarget(t *testing.T) {
	l, root, lock := fixture(t)
	dest := filepath.Join(root, "result")
	ctx, cancel := context.WithCancel(context.Background())
	_, _ = create(ctx, l, dest, lock, createHooks{at: func(phase string) {
		if phase == "manifest-written" {
			cancel()
		}
	}})
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dest, "foreign")
	put(t, foreign, []byte("retain"))
	if _, err := RecoverPublication(context.Background(), dest); err == nil {
		t.Fatal("foreign target adopted")
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "retain" {
		t.Fatal("foreign target changed")
	}
	unknown := filepath.Join(root, "unknown")
	if _, err := RecoverPublication(context.Background(), unknown); err == nil {
		t.Fatal("unknown publication adopted")
	}
	if _, err := os.Lstat(operationRoot(unknown)); !os.IsNotExist(err) {
		t.Fatal("recovery created unknown operation")
	}
}

func TestBoundaryUnknownOperationArtifactsFailClosed(t *testing.T) {
	l, root, lock := fixture(t)
	dest := filepath.Join(root, "result")
	p, err := beginPublication(context.Background(), dest, l, createHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.lock.Close(); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(operationRoot(dest), "state.next")
	put(t, unknown, []byte("foreign operation artifact"))
	if _, err := Create(context.Background(), l, dest, lock); err == nil {
		t.Fatal("retry adopted an unknown operation artifact")
	}
	if _, err := os.Lstat(unknown); err != nil {
		t.Fatalf("retry removed unknown artifact: %v", err)
	}
	if _, err := RecoverPublication(context.Background(), dest); err == nil {
		t.Fatal("recovery adopted an unknown operation artifact")
	}
}

func TestBoundaryRestoreActiveTargetAndSeparateRoot(t *testing.T) {
	l, root, lock := fixture(t)
	bundle, _ := mustCreate(t, l, root, lock)
	db, err := sql.Open("sqlite", l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	reader, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var count int
	if err := reader.QueryRow("SELECT count(*) FROM test_table").Scan(&count); err != nil {
		t.Fatal(err)
	}
	writer, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer writer.ExecContext(context.Background(), "ROLLBACK")
	before, err := Inspect(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreNew(context.Background(), bundle, root); err == nil {
		t.Fatal("active target replaced")
	}
	if _, err := RestoreNew(context.Background(), bundle, filepath.Join(bundle, "nested")); err == nil {
		t.Fatal("source/output overlap allowed")
	}
	newRoot := filepath.Join(root, "separate")
	if _, err := RestoreNew(context.Background(), bundle, newRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), newRoot); err != nil {
		t.Fatal(err)
	}
	after, err := Inspect(context.Background(), l)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("restore touched active source: %v", err)
	}
	if err := reader.QueryRow("SELECT count(*) FROM test_table").Scan(&count); err != nil || count != 1 {
		t.Fatalf("active reader changed: %d %v", count, err)
	}
}

func TestBoundarySQLiteURIWithSpecialNames(t *testing.T) {
	l, root, lock := fixture(t)
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	old := l.DatabasePath
	l.DatabasePath = filepath.Join(root, "source # percent% 数据.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(old+suffix, l.DatabasePath+suffix); err != nil {
			t.Fatal(err)
		}
	}
	held, err := processlock.Acquire(l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	dest := filepath.Join(root, "backup # percent% 数据")
	if _, err := Create(context.Background(), l, dest, held); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreNew(context.Background(), dest, filepath.Join(root, "restore # 数据")); err != nil {
		t.Fatal(err)
	}
}

func TestBoundaryManifestAndJournalTamperFailClosed(t *testing.T) {
	for _, kind := range []string{"staged-manifest", "journal-id", "journal-destination", "changed-committed-target"} {
		t.Run(kind, func(t *testing.T) {
			l, root, lock := fixture(t)
			dest := filepath.Join(root, "result")
			if kind == "staged-manifest" {
				_, err := create(context.Background(), l, dest, lock, createHooks{at: func(phase string) {
					if phase == "before-publication" {
						r := operationRecord(t, dest)
						put(t, filepath.Join(operationRoot(dest), stagingPrefix+r.ID, "manifest.json"), []byte("broken"))
					}
				}})
				if err == nil {
					t.Fatal("invalid staged manifest published")
				}
				assertNoStaging(t, dest)
				return
			}
			if kind == "changed-committed-target" {
				if _, err := Create(context.Background(), l, dest, lock); err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(dest, "config"), []byte(`{"changed":true}`))
				rewriteManifest(t, dest)
				before, err := Verify(context.Background(), dest)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := RecoverPublication(context.Background(), dest); err == nil {
					t.Fatal("coherently changed target adopted")
				}
				if err := Revalidate(context.Background(), BundleLayout(dest), before); err != nil {
					t.Fatal("rejected target modified")
				}
				return
			}
			p, err := beginPublication(context.Background(), dest, l, createHooks{})
			if err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(root, "foreign")
			put(t, foreign, []byte("retain"))
			if kind == "journal-id" {
				p.record.ID = "../../foreign"
			} else {
				p.record.Destination = "foreign"
			}
			if err := p.writeRecord(); err != nil {
				t.Fatal(err)
			}
			if err := p.lock.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := RecoverPublication(context.Background(), dest); err == nil {
				t.Fatal("invalid journal contract accepted")
			}
			if b, err := os.ReadFile(foreign); err != nil || string(b) != "retain" {
				t.Fatal("journal tamper affected foreign path")
			}
		})
	}
}

func TestBoundaryConcurrentPublisherRefused(t *testing.T) {
	l, root, lock := fixture(t)
	dest := filepath.Join(root, "result")
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := create(context.Background(), l, dest, lock, createHooks{at: func(phase string) {
			if phase == "staging-created" {
				close(entered)
				<-release
			}
		}})
		finished <- err
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("first publisher failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("publisher did not enter staging")
	}
	_, err := Create(context.Background(), l, dest, lock)
	close(release)
	firstErr := <-finished
	if !errors.Is(err, processlock.ErrLocked) {
		t.Fatalf("concurrent publication was not fenced: %v", err)
	}
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if _, err := Verify(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
}
