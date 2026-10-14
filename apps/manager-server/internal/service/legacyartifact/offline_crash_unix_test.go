//go:build linux || darwin

package legacyartifact

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Independent processes exercise fd 3 surviving SIGKILL of the parent. These
// helpers never establish production executable provenance or authority.
func TestOfflineCrashHelper(t *testing.T) {
	mode := os.Getenv("CPAMP_OFFLINE_CRASH_HELPER")
	if mode == "" {
		return
	}
	root, id := os.Getenv("CPAMP_OFFLINE_CRASH_ROOT"), os.Getenv("CPAMP_OFFLINE_CRASH_ID")
	switch mode {
	case "parent":
		lock, err := acquireOfflineLock(filepath.Join(root, ".operation.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.close()
		asset := offlineRuntimeAsset()
		pin := nativePins[asset]
		intent := offlineIntent{Tool: offlineTool, OperationID: id, Release: Release, SourceCommit: SourceCommit, Asset: asset, ArchiveSHA256: pin.ArchiveSHA256, ExecutableSHA256: pin.ExecutableSHA256, InputBundle: filepath.Join(filepath.Dir(root), "input"), InputManifestDigest: offlineHash([]byte("input")), TimeoutMS: 60000}
		data, _ := offlineJSON(intent)
		if err := offlineWriteNew(filepath.Join(root, "intent.json"), data, 0400); err != nil {
			t.Fatal(err)
		}
		state := offlineState{OperationID: id, IntentDigest: offlineHash(data), Phase: "prepared"}
		if err := offlineSave(root, state); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestOfflineCrashHelper$")
		if runtime.GOOS == "linux" {
			// Exercise the production two-descriptor launch mapping too. The
			// sealed test binary is a lifecycle helper, never a pinned writer.
			body, err := os.ReadFile(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "helper-executable")
			if err := os.WriteFile(path, body, 0o700); err != nil {
				t.Fatal(err)
			}
			sealed, err := prepareOfflineExecutable(context.Background(), path, offlineHash(body))
			if err != nil {
				t.Fatal(err)
			}
			defer sealed.Close()
			cmd, err = pinnedOfflineCommand(context.Background(), sealed, "unused", lock)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Args = []string{cmd.Path, "-test.run=^TestOfflineCrashHelper$"}
		} else {
			configureOfflineProcess(cmd, lock)
		}
		cmd.Env = append(os.Environ(), "CPAMP_OFFLINE_CRASH_HELPER=child")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		state.Phase, state.Process.PID = "running", cmd.Process.Pid
		if err := offlineSave(root, state); err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	case "child":
		if err := os.WriteFile(filepath.Join(root, "child-ready"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(root, "release-child")); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("orphan release timed out")
	case "probe-busy":
		if _, err := InspectOfflineCleanup(context.Background(), root, id); !errors.Is(err, ErrOfflineBusy) {
			t.Fatalf("inspection not fenced: %v", err)
		}
		if err := RecoverOfflineCleanup(root, id); !errors.Is(err, ErrOfflineBusy) {
			t.Fatalf("recovery not fenced: %v", err)
		}
	case "recover":
		if err := RecoverOfflineCleanup(root, id); err != nil {
			t.Fatal(err)
		}
		if _, err := InspectOfflineCleanup(context.Background(), root, id); !errors.Is(err, ErrOfflineState) {
			t.Fatalf("interrupted workspace accepted: %v", err)
		}
	default:
		t.Fatal("unknown helper")
	}
}

func TestOfflineParentCrashOrphanFencesRestartUntilChildExit(t *testing.T) {
	if !offlineSupported() {
		t.Skip("Unix AMD64 diagnostic lifecycle")
	}
	root := filepath.Join(testTempDir(t), "operation")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	id, err := GenerateOperationID()
	if err != nil {
		t.Fatal(err)
	}
	helper := func(mode string) *exec.Cmd {
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestOfflineCrashHelper$")
		cmd.Env = append(os.Environ(), "CPAMP_OFFLINE_CRASH_HELPER="+mode, "CPAMP_OFFLINE_CRASH_ROOT="+root, "CPAMP_OFFLINE_CRASH_ID="+id)
		return cmd
	}
	parent := helper("parent")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		if !reaped {
			parent.Process.Kill()
			parent.Wait()
		}
		os.WriteFile(filepath.Join(root, "release-child"), nil, 0600)
	}()
	deadline := time.Now().Add(10 * time.Second)
	var state offlineState
	for {
		_, state, err = offlineLoad(root, id)
		_, readyErr := os.Stat(filepath.Join(root, "child-ready"))
		if err == nil && state.Phase == "running" && state.Process.PID > 0 && readyErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	child, err := os.FindProcess(state.Process.PID)
	if err != nil {
		t.Fatal(err)
	}
	childExited := false
	defer func() {
		if !childExited {
			child.Kill()
		}
	}()
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(); err == nil {
		t.Fatal("parent unexpectedly succeeded")
	}
	reaped = true
	// Parent is dead and reaped; only the orphan can retain the fence.
	if out, err := helper("probe-busy").CombinedOutput(); err != nil {
		t.Fatalf("restart probe: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "release-child"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		lock, err := acquireOfflineLock(filepath.Join(root, ".operation.lock"))
		if err == nil {
			lock.close()
			childExited = true
			break
		}
		if !errors.Is(err, ErrOfflineBusy) || time.Now().After(deadline) {
			t.Fatalf("child did not release fence: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if out, err := helper("recover").CombinedOutput(); err != nil {
		t.Fatalf("restart recovery: %v %s", err, out)
	}
	_, state, err = offlineLoad(root, id)
	if err != nil || state.Phase != "interrupted" || state.OutputManifestDigest != "" {
		t.Fatalf("recovery: %+v %v", state, err)
	}
}
