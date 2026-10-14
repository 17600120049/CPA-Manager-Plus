//go:build linux

package legacyartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOfflineSealedExecutableIgnoresStagingReplacementAndInPlaceWrites(t *testing.T) {
	for _, mutation := range []string{"rename", "in-place"} {
		t.Run(mutation, func(t *testing.T) {
			root, lock, state := offlineProcessFixture(t)
			defer lock.close()
			exe := filepath.Join(root, "writer")
			authentic := []byte("#!/bin/sh\nexit 0\n")
			if err := os.WriteFile(exe, authentic, 0700); err != nil {
				t.Fatal(err)
			}
			sealed, err := prepareOfflineExecutable(context.Background(), exe, offlineHash(authentic))
			if err != nil {
				t.Fatal(err)
			}
			defer sealed.Close()
			if _, err := sealed.WriteAt([]byte("tamper"), 0); !errors.Is(err, unix.EPERM) {
				t.Fatalf("sealed bytes writable: %v", err)
			}
			if err := sealed.Truncate(0); !errors.Is(err, unix.EPERM) {
				t.Fatalf("sealed object resizable: %v", err)
			}
			if mutation == "rename" {
				if err := os.Rename(exe, exe+".original"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
				t.Fatal(err)
			}
			// Launch after a deterministic attack at exactly the old hash/exec boundary.
			// Success distinguishes the sealed authentic bytes from the malicious path.
			if err := offlineRunPinnedProcess(context.Background(), sealed, root, filepath.Join(root, "database"), lock, state); err != nil {
				t.Fatal(err)
			}
			if !state.Reaped || state.Process.ExitCode != 0 {
				t.Fatalf("unbound launch: %+v", state)
			}
		})
	}
}

func TestOfflineSealedExecutableRejectsDigestAndUnsealedObject(t *testing.T) {
	root, lock, state := offlineProcessFixture(t)
	defer lock.close()
	exe := filepath.Join(root, "writer")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if f, err := prepareOfflineExecutable(context.Background(), exe, offlineHash([]byte("wrong"))); !errors.Is(err, ErrExecutableMismatch) || f != nil {
		t.Fatalf("digest mismatch: %v", err)
	}
	f, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := offlineRunPinnedProcess(context.Background(), f, root, "database", lock, state); !errors.Is(err, ErrExecutableMismatch) || state.Process.PID != 0 {
		t.Fatalf("unsealed launch: %v", err)
	}
}
