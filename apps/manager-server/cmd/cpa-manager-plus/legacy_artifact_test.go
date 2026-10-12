package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLegacyWriterArtifactDispatchNeverStartsManager(t *testing.T) {
	for _, kind := range []string{"forged", "help"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "usage.sqlite")
			archive := filepath.Join(dir, "archive")
			if err := os.WriteFile(archive, []byte("secret forged archive"), 0600); err != nil {
				t.Fatal(err)
			}
			before := managerSourceSnapshot(t, dir)
			args := []string{"legacy-writer-artifact", "--archive", archive, "--asset", "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"}
			if kind == "help" {
				args = []string{"legacy-writer-artifact", "--help"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestLegacyPreflightCLIHelperProcess$", "--"}, args...)...)
			cmd.Env = append(ownedPreflightEnvironment(dir, db), "CPAMP_PREFLIGHT_CLI_HELPER=1")
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err := cmd.Run()
			if ctx.Err() != nil || (kind == "forged" && err == nil) || (kind == "help" && err != nil) {
				t.Fatalf("CLI outcome %v", err)
			}
			if out.Len() != 0 || strings.Contains(stderr.String(), "listening on") || strings.Contains(stderr.String(), "secret forged") {
				t.Fatal("CLI leaked payload or entered Manager startup")
			}
			if !reflect.DeepEqual(before, managerSourceSnapshot(t, dir)) {
				t.Fatal("artifact dispatch wrote Manager data/config/key/lock")
			}
		})
	}
}
