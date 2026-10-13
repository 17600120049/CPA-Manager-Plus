package managerdatabundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	_ "modernc.org/sqlite"
)

func source(t *testing.T) (maintenancebundle.Layout, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "source")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	l := maintenancebundle.BundleLayout(data)
	seedPath := filepath.Join(root, "seed.sqlite")
	seed, err := sql.Open("sqlite", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	if _, err := seed.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE test_cmd (id INTEGER PRIMARY KEY, secret TEXT); INSERT INTO test_cmd VALUES (1, 'private-fixture-secret-db');"); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(seedPath + suffix)
		if err == nil {
			if err := os.WriteFile(l.DatabasePath+suffix, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = seed.Close()
	if _, _, err := security.LoadOrCreateDataKey("", l.DataKeyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.ConfigPath, []byte(`{"secret":"private-fixture-secret-config"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.ArchivesPath, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.ArchivesPath, "history.gz"), []byte("opaque archive private-fixture-secret-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := processlock.Acquire(l.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	return l, root
}

func createArgs(l maintenancebundle.Layout, bundle string) []string {
	return []string{"create", "--db", l.DatabasePath, "--data-key", l.DataKeyPath, "--config", l.ConfigPath, "--archives", l.ArchivesPath, "--bundle", bundle, "--source-stopped"}
}

type witness struct {
	Mode   os.FileMode
	Digest [32]byte
}

func sourceWitness(t *testing.T, root string) map[string]witness {
	t.Helper()
	out := map[string]witness{}
	if err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		r, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := witness{Mode: info.Mode()}
		if !e.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Digest = sha256.Sum256(data)
		}
		out[r] = item
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func run(t *testing.T, args []string, status string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var got summary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != status || got.Fence != maintenancebundle.CooperatingFence || got.Files != 6 || got.UpgradeAuthorized {
		t.Fatalf("unexpected summary: %+v", got)
	}
	if strings.Contains(stdout.String(), "private-fixture-secret") || strings.Contains(stdout.String(), "sha256") || stderr.Len() != 0 {
		t.Fatal("sensitive output")
	}
}

func TestCommandCreateVerifyRestorePreservesSource(t *testing.T) {
	l, root := source(t)
	bundle := filepath.Join(root, "bundle")
	before := sourceWitness(t, filepath.Dir(l.DatabasePath))
	run(t, createArgs(l, bundle), "created")
	run(t, []string{"verify", "--bundle", bundle}, "verified")
	dest := filepath.Join(root, "restored")
	run(t, []string{"restore-new", "--bundle", bundle, "--target", dest}, "restored")
	if after := sourceWitness(t, filepath.Dir(l.DatabasePath)); !reflect.DeepEqual(before, after) {
		t.Fatal("command modified source files/bytes/modes")
	}
	manifest, err := maintenancebundle.Inspect(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if err := maintenancebundle.Revalidate(context.Background(), maintenancebundle.BundleLayout(dest), manifest); err != nil {
		t.Fatal(err)
	}
}

func TestCommandFailsWithoutOwnershipAndNeverCreatesSourceMarker(t *testing.T) {
	for _, kind := range []string{"marker-missing", "lock-held", "database-missing"} {
		t.Run(kind, func(t *testing.T) {
			l, root := source(t)
			switch kind {
			case "marker-missing":
				if err := os.Remove(l.DatabasePath + ".manager.lock"); err != nil {
					t.Fatal(err)
				}
			case "lock-held":
				lock, err := processlock.Acquire(l.DatabasePath)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "database-missing":
				if err := os.Remove(l.DatabasePath); err != nil {
					t.Fatal(err)
				}
			}
			before := sourceWitness(t, filepath.Dir(l.DatabasePath))
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), createArgs(l, filepath.Join(root, "bundle")), &stdout, &stderr)
			if err == nil || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("failure reported success: %v %s %s", err, stdout.String(), stderr.String())
			}
			if after := sourceWitness(t, filepath.Dir(l.DatabasePath)); !reflect.DeepEqual(before, after) {
				t.Fatal("failure changed source")
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "private-fixture-secret") {
				t.Fatal("failure leaked source")
			}
		})
	}
}

func TestCommandRequiresCompleteExplicitArguments(t *testing.T) {
	l, root := source(t)
	args := createArgs(l, filepath.Join(root, "bundle"))
	for _, flagName := range []string{"--db", "--data-key", "--config", "--archives", "--bundle", "--source-stopped"} {
		t.Run(flagName, func(t *testing.T) {
			var modified []string
			for i := 0; i < len(args); i++ {
				if args[i] == flagName {
					if flagName != "--source-stopped" {
						i++
					}
					continue
				}
				modified = append(modified, args[i])
			}
			var stdout, stderr bytes.Buffer
			if err := Run(context.Background(), modified, &stdout, &stderr); err == nil || stdout.Len() != 0 {
				t.Fatal("incomplete command accepted")
			}
		})
	}
	for _, invalid := range [][]string{{"verify", "--bundle", "x", "--timeout", "0"}, {"verify", "--bundle", "x", "--timeout", "-1s"}, {"restore-new", "--bundle", "x"}, {"verify", "--bundle", "x", "unexpected-private-fixture-secret"}, {"verify", "--private-fixture-secret", "secret"}, {"create", "--timeout", "private-fixture-secret"}} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), invalid, &stdout, &stderr)
		if err == nil || strings.Contains(err.Error(), "private-fixture-secret") || strings.Contains(stderr.String(), "private-fixture-secret") {
			t.Fatal("invalid command leaked or accepted")
		}
	}
}

func TestCommandMissingOptionalRootsRequireExplicitOverride(t *testing.T) {
	l, root := source(t)
	if err := os.Remove(l.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(l.ArchivesPath); err != nil {
		t.Fatal(err)
	}
	before := sourceWitness(t, filepath.Dir(l.DatabasePath))
	args := createArgs(l, filepath.Join(root, "bundle"))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), args, &stdout, &stderr); err == nil || stdout.Len() != 0 {
		t.Fatal("missing required config/archive accepted")
	}
	args = append(args, "--require-config=false", "--require-archives=false")
	if err := Run(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	m, err := maintenancebundle.Verify(context.Background(), filepath.Join(root, "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Entries["config"].Present || m.Entries["usage-archives"].Present {
		t.Fatal("absence not recorded")
	}
	if after := sourceWitness(t, filepath.Dir(l.DatabasePath)); !reflect.DeepEqual(before, after) {
		t.Fatal("optional source changed")
	}
}

func TestCommandCorruptionAndExistingRestoreRootFailWithoutMutations(t *testing.T) {
	l, root := source(t)
	bundle := filepath.Join(root, "bundle")
	run(t, createArgs(l, bundle), "created")
	before := sourceWitness(t, filepath.Dir(l.DatabasePath))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"restore-new", "--bundle", bundle, "--target", filepath.Dir(l.DatabasePath)}, &stdout, &stderr); err == nil || stdout.Len() != 0 {
		t.Fatal("live-root restore accepted")
	}
	if err := os.WriteFile(filepath.Join(bundle, "data-key"), []byte("tamper-private-fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"verify", "--bundle", bundle}, {"restore-new", "--bundle", bundle, "--target", filepath.Join(root, "restored")}} {
		err := Run(context.Background(), args, &stdout, &stderr)
		if err == nil || stdout.Len() != 0 || strings.Contains(err.Error(), root) {
			t.Fatal("corrupted bundle accepted/leaked")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "restored")); !os.IsNotExist(err) {
		t.Fatal("partial restore published")
	}
	if after := sourceWitness(t, filepath.Dir(l.DatabasePath)); !reflect.DeepEqual(before, after) {
		t.Fatal("failed restore modified source")
	}
}

func TestCommandCancellationAndHelp(t *testing.T) {
	l, root := source(t)
	before := sourceWitness(t, filepath.Dir(l.DatabasePath))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	if err := Run(ctx, createArgs(l, filepath.Join(root, "bundle")), &stdout, &stderr); !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong cancellation: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatal("cancellation emitted success")
	}
	args := append(createArgs(l, filepath.Join(root, "bundle")), "--timeout", "1ns")
	if err := Run(context.Background(), args, &stdout, &stderr); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wrong timeout: %v", err)
	}
	if err := Run(context.Background(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "not proof of external writer quiescence") {
		t.Fatal("help obscures assertion boundary")
	}
	if after := sourceWitness(t, filepath.Dir(l.DatabasePath)); !reflect.DeepEqual(before, after) {
		t.Fatal("cancellation/help changed source")
	}
}
