package legacyartifact

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// 2. Removed flags (--authority-key, --executable) must be rejected
	for _, args := range [][]string{
		{"--verify-receipt", filepath.Join(dir, "r.json"), "--bundle", dir, "--authority-key", "deadbeef"},
		{"--execute-hop", "--executable", "/tmp/fake"},
	} {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		if err == nil {
			t.Fatalf("expected error on disallowed flag: %v", args)
		}
	}
}
