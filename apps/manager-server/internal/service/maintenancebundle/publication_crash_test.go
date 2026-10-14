package maintenancebundle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
)

func TestBoundaryJournalInternalCrashRestart(t *testing.T) {
	for _, transition := range []string{"initial", "preparing", "manifest", "committed", "aborted"} {
		for _, window := range []string{"next-synced", "replaced"} {
			t.Run(transition+"/"+window, func(t *testing.T) {
				ctx := context.Background()
				l, root, lock := fixture(t)
				baseline, err := Inspect(ctx, l)
				if err != nil {
					t.Fatal(err)
				}
				dest := filepath.Join(root, "result")
				if transition == "preparing" {
					p, err := beginPublication(ctx, dest, l, createHooks{})
					if err != nil {
						t.Fatal(err)
					}
					if err := p.finish(); err != nil {
						t.Fatal(err)
					}
				}
				if err := lock.Close(); err != nil {
					t.Fatal(err)
				}
				mode := "crash-create"
				if transition == "aborted" {
					mode = "crash-create-abort"
				}
				phase := "journal-" + transition + "-" + window
				killAtBoundary(t, boundaryCommand(t, l, mode, "CPAMP_BOUNDARY_PHASE="+phase, "CPAMP_BOUNDARY_DEST="+dest))
				_, nextErr := os.Lstat(filepath.Join(operationRoot(dest), "state.next"))
				if (window == "next-synced" && nextErr != nil) || (window == "replaced" && !os.IsNotExist(nextErr)) {
					t.Fatalf("wrong journal crash window: %v", nextErr)
				}
				want := "aborted"
				if transition == "committed" {
					want = "committed"
				} else if _, err := Verify(ctx, dest); err == nil {
					t.Fatal("unfinished publication accepted")
				}
				if window == "next-synced" && want == "aborted" && transition != "initial" {
					// Retry must reconcile interrupted intent itself, without
					// requiring a successful explicit recovery call first.
					p, err := beginPublication(ctx, dest, l, createHooks{})
					if err != nil {
						t.Fatalf("direct retry: %v", err)
					}
					if err := p.finish(); err != nil {
						t.Fatal(err)
					}
				}
				for attempt := 0; attempt < 2; attempt++ {
					state, err := RecoverPublication(ctx, dest)
					if err != nil || state != want {
						t.Fatalf("recovery %d: state=%s want=%s err=%v", attempt, state, want, err)
					}
					assertNoStaging(t, dest)
				}
				if want == "aborted" {
					// Recovery must leave the same destination reusable.
					held, err := processlock.AcquireExisting(l.DatabasePath)
					if err != nil {
						t.Fatal(err)
					}
					_, createErr := Create(ctx, l, dest, held)
					closeErr := held.Close()
					if createErr != nil || closeErr != nil {
						t.Fatalf("same-target retry: create=%v close=%v", createErr, closeErr)
					}
					assertNoStaging(t, dest)
				}
				if _, err := Verify(ctx, dest); err != nil {
					t.Fatal(err)
				}
				if err := RevalidateSource(ctx, l, baseline); err != nil {
					t.Fatalf("source changed: %v", err)
				}
			})
		}
	}
}

func TestBoundaryRecoveryRefusesTargetWhileStagingExists(t *testing.T) {
	ctx := context.Background()
	l, root, lock := fixture(t)
	bundle, m := mustCreate(t, l, root, lock)
	dest := filepath.Join(root, "result")
	p, err := beginPublication(ctx, dest, l, createHooks{})
	if err != nil {
		t.Fatal(err)
	}
	p.record.Manifest = &m
	if err := p.writeRecord(); err != nil {
		t.Fatal(err)
	}
	stage := p.staging()
	// Preserve complete staging and an identical, independently copied target.
	for _, target := range []string{stage, dest} {
		if err := filepath.WalkDir(bundle, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(bundle, path)
			if err != nil {
				return err
			}
			to := filepath.Join(target, rel)
			if entry.IsDir() {
				return os.MkdirAll(to, 0o700)
			}
			return copyFile(ctx, path, to)
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := p.record
	if err := p.lock.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := RecoverPublication(ctx, dest); err == nil {
			t.Fatal("adopted untracked target while recorded staging still exists")
		}
		if !reflect.DeepEqual(operationRecord(t, dest), before) {
			t.Fatal("rejected recovery changed record")
		}
		if _, err := verify(ctx, stage, true); err != nil {
			t.Fatalf("staging changed: %v", err)
		}
		if _, err := Verify(ctx, dest); err != nil {
			t.Fatalf("target changed: %v", err)
		}
	}
}

func TestBoundaryInterruptedJournalRejectsInvalidTransitions(t *testing.T) {
	for _, kind := range []string{"malformed", "foreign-id", "committed-without-manifest", "unknown-artifact"} {
		t.Run(kind, func(t *testing.T) {
			l, root, lock := fixture(t)
			dest := filepath.Join(root, "result")
			p, err := beginPublication(context.Background(), dest, l, createHooks{})
			if err != nil {
				t.Fatal(err)
			}
			before := p.record
			next := before
			switch kind {
			case "foreign-id":
				next.ID = "0123456789abcdef0123456789abcdef"
			case "committed-without-manifest":
				next.State = "committed"
			case "unknown-artifact":
				put(t, filepath.Join(p.root, "foreign.tmp"), []byte("preserve"))
			}
			data, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "malformed" {
				data = []byte(`{"version":1`)
			}
			path := filepath.Join(p.root, "state.next")
			put(t, path, data)
			if err := p.lock.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(context.Background(), l, dest, lock); err == nil {
				t.Fatal("retry accepted invalid next journal")
			}
			if _, err := RecoverPublication(context.Background(), dest); err == nil {
				t.Fatal("recovery accepted invalid next journal")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(data) || !reflect.DeepEqual(operationRecord(t, dest), before) {
				t.Fatalf("rejected recovery changed journal: %v", err)
			}
		})
	}
}
