package legacyartifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	artifactservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
)

func TestCommandCannotClaimWriterOrDiscoverManagerData(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "archive")
	if err := os.WriteFile(p, []byte("secret forged v1.14.4 executable"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"--archive", p}, {"--asset", "unknown"}, {"--archive", p, "--asset", "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"}, {"--archive", p, "--asset", "unknown", "--timeout", "0s"}} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), dir) {
			t.Fatalf("invalid CLI evidence: %q %v", out.String(), err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("command touched Manager/source data")
	}
	var out, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &out, &stderr); !IsHelp(err) {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("help emitted evidence")
	}
}

func TestCommandRedactsInvalidArgumentValues(t *testing.T) {
	for _, args := range [][]string{{"--timeout", "secret-token"}, {"--secret-token=value"}, {"--archive", "--asset"}} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		if err == nil || out.Len() != 0 || strings.Contains(stderr.String(), "secret-token") || strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("argument input leaked: stderr=%q err=%v", stderr.String(), err)
		}
	}
}

func TestCommandVerifyReceiptValidAndInvalid(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 1. Missing flags
	for _, args := range [][]string{
		{"--verify-receipt", filepath.Join(dir, "r.json")},
		{"--verify-receipt", filepath.Join(dir, "r.json"), "--bundle", ""},
	} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		if err == nil || out.Len() != 0 {
			t.Fatalf("expected error on missing verify-receipt args: %v", err)
		}
	}

	// 2. Caller-selected authority and executable flags must be rejected
	for _, args := range [][]string{
		{"--verify-receipt", filepath.Join(dir, "r.json"), "--bundle", dir, "--authority-key", "deadbeef"},
		{"--execute-hop", "--executable", "/tmp/fake"},
		{"--verify-receipt", filepath.Join(dir, "r.json"), "--data-dir", dir},
	} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		if err == nil {
			t.Fatalf("expected error on disallowed flag: %v", args)
		}
	}
}

func TestCommandUnqualifiedExecutionHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	for _, asset := range []string{"cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz", "cpa-manager-plus_v1.14.4_windows_amd64.zip"} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), []string{"--execute-hop", "--archive", filepath.Join(dir, "archive"), "--asset", asset, "--input-bundle", filepath.Join(dir, "input"), "--output-bundle", filepath.Join(dir, "output")}, &out, &stderr)
		if !errors.Is(err, artifactservice.ErrExecutionNotQualified) || out.Len() != 0 {
			t.Fatalf("unqualified CLI execution: %q %v", out.String(), err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unqualified CLI touched files: %v %v", entries, err)
	}
}

func TestReceiptVerificationDoesNotCreateLostAuthority(t *testing.T) {
	dir := t.TempDir()
	var out, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--verify-receipt", filepath.Join(dir, "receipt"), "--bundle", filepath.Join(dir, "bundle")}, &out, &stderr)
	if !errors.Is(err, artifactservice.ErrValidationNotQualified) || out.Len() != 0 {
		t.Fatalf("unqualified receipt verification: %q %v", out.String(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("receipt verification initialized authority: %v %v", entries, err)
	}
}
