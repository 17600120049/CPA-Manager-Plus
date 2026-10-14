//go:build windows

package maintenancebundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func windowsNoDeleteHandle(t *testing.T, path string, directory bool) windows.Handle {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	flags := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if directory {
		flags = windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestWindowsBundleOpenDirectoryBlocksPublicationAndAllowsRecovery(t *testing.T) {
	l, root, lock := fixture(t)
	dest := filepath.Join(root, "result")
	var held windows.Handle
	opened := false
	_, err := create(context.Background(), l, dest, lock, createHooks{at: func(phase string) {
		if phase == "before-publication" {
			r := operationRecord(t, dest)
			held = windowsNoDeleteHandle(t, filepath.Join(operationRoot(dest), stagingPrefix+r.ID), true)
			opened = true
		}
	}})
	if opened {
		if closeErr := windows.CloseHandle(held); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if !opened || err == nil {
		t.Fatalf("Windows open directory must block rename: opened=%v err=%v", opened, err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("blocked rename published target: %v", err)
	}
	state, err := RecoverPublication(context.Background(), dest)
	if err != nil || state != "aborted" {
		t.Fatalf("recovery after handle release: %s %v", state, err)
	}
	assertNoStaging(t, dest)
	if _, err := Create(context.Background(), l, dest, lock); err != nil {
		t.Fatalf("retry after Windows handle release: %v", err)
	}
}

func TestWindowsBundleDeleteReplacementAndWALHandles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"database", "database-wal", "database-shm", "database-journal", "state.json"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			put(t, path, []byte("original"))
			held := windowsNoDeleteHandle(t, path, false)
			if err := os.Remove(path); err == nil {
				windows.CloseHandle(held)
				t.Fatal("delete ignored non-sharing Windows handle")
			}
			next := filepath.Join(root, name+".next")
			put(t, next, []byte("replacement"))
			if err := replaceState(next, path); err == nil {
				windows.CloseHandle(held)
				t.Fatal("replacement ignored non-sharing Windows handle")
			}
			if err := windows.CloseHandle(held); err != nil {
				t.Fatal(err)
			}
			if err := replaceState(next, path); err != nil {
				t.Fatalf("replacement after release: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatalf("delete after release: %v", err)
			}
		})
	}
	from, to := filepath.Join(root, "from"), filepath.Join(root, "existing")
	if err := os.Mkdir(from, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(to, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishDirectory(from, to); err == nil {
		t.Fatal("directory publication replaced existing target")
	}
}
