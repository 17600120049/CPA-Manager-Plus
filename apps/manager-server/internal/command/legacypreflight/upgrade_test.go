package legacypreflight

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	service "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestUpgradeCommandRequiresCurrentAdminAndNeverPrintsCredentials(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := security.NewAdminCredential("OWNED_SECRET_current", "test")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(credential)
	if _, err := db.Exec(`create table settings(key text primary key,value text); create table usage_events(id integer primary key); insert into settings values('admin_credential_v1',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err := processlock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "OWNED_SECRET_stale", "expired-session", strings.Repeat("x", 4097), "OWNED_SECRET_current\n"} {
		t.Run(key[:min(len(key), 12)], func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := run(context.Background(), []string{"--db", path, "--source-stopped", "--upgrade-check", "--backup", filepath.Join(dir, "missing-backup")}, strings.NewReader(key), &out, &errOut)
			if err == nil {
				t.Fatal("Upgrade command granted admission")
			}
			if strings.Contains(out.String()+errOut.String()+err.Error(), "OWNED_SECRET") {
				t.Fatal("administrator key escaped")
			}
			if len(key) <= 4096 {
				if !errors.Is(err, ErrBlocked) {
					t.Fatal(err)
				}
				var report service.Report
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.UpgradeAuthorized || report.SourceKind == "fresh" || report.UpgradePreconditions == nil || report.UpgradePreconditions.TrustedHopReceipt {
					t.Fatal("unsafe command result", report)
				}
				authorized := strings.TrimSpace(key) == "OWNED_SECRET_current"
				if report.UpgradePreconditions.AdminVerified != authorized || (!authorized && len(report.Observed.Files) != 0) {
					t.Fatal("inventory authorization failed")
				}
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Upgrade command changed source")
	}
}

func TestUpgradeCommandFlagsAndInputFailure(t *testing.T) {
	for _, args := range [][]string{
		{"--db", "absent", "--backup", "bundle"},
		{"--db", "absent", "--expected-source-revision", "revision"},
	} {
		var out, errOut bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out, &errOut); err == nil || out.Len() != 0 {
			t.Fatal("orphan Upgrade arguments accepted")
		}
	}
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"--db", "absent", "--upgrade-check"}, failingReader{}, &out, &errOut); err == nil || out.Len() != 0 {
		t.Fatal("failed key input accepted")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("OWNED_SECRET_input_error") }
