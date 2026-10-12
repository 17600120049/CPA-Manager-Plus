package legacypreflight

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	service "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestCommandRequiresExplicitSourceAndDoesNotCreateDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent", "usage.sqlite")
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), nil, &out, &errOut); err == nil {
		t.Fatal("implicit source accepted")
	}
	if err := Run(context.Background(), []string{"--db", path}, &out, &errOut); err == nil {
		t.Fatal("uninspected config silently became Fresh")
	}
	var report service.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceKind != "missing-database" || report.UpgradeAuthorized {
		t.Fatalf("report=%+v", report)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("command created source directory")
	}
}

func TestCommandReportsLegacySchemaWithoutSecretsOrUpgradeGrant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table settings(key text primary key,value text);create table usage_events(id integer primary key);insert into settings values('setup','{"managementKey":"OWNED_SECRET"}')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"--db", path, "--source-stopped"}, &out, &errOut); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	var report service.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceKind != "legacy-manager" || report.UpgradeAuthorized || len(report.Unknown) < 5 {
		t.Fatalf("report=%+v", report)
	}
	if report.CompatibilityProfile == nil || report.CompatibilityProfile.Status != "partial" || report.ExactSourceRelease.Status != "unknown" {
		t.Fatalf("partial schema claimed a supported profile: %+v", report)
	}
	if strings.Contains(out.String()+errOut.String(), "OWNED_SECRET") {
		t.Fatal("credential escaped")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("source changed")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("command created artifacts: %v", files)
	}
}

func TestCommandMissingOrMalformedExplicitConfigFailsWithoutCreatingIt(t *testing.T) {
	for _, kind := range []string{"missing", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.json")
			if kind == "malformed" {
				if err := os.WriteFile(cfg, []byte(`{"managementKey":"OWNED_SECRET", broken}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			if err := Run(context.Background(), []string{"--db", filepath.Join(dir, "usage.sqlite"), "--config", cfg}, &out, &errOut); err == nil {
				t.Fatal("bad config accepted")
			}
			if strings.Contains(out.String()+errOut.String(), "OWNED_SECRET") {
				t.Fatal("config contents escaped")
			}
			if kind == "missing" {
				if _, err := os.Stat(cfg); !os.IsNotExist(err) {
					t.Fatal("config created")
				}
			}
		})
	}
}

func TestCommandHelpCancellationAndOutputFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &out, &errOut); !IsHelp(err) {
		t.Fatalf("help=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, []string{"--db", filepath.Join(t.TempDir(), "usage.sqlite")}, &out, &errOut); err == nil {
		t.Fatal("cancelled inspection succeeded")
	}
	if err := Run(context.Background(), []string{"--db", filepath.Join(t.TempDir(), "usage.sqlite")}, failingWriter{}, &errOut); err == nil {
		t.Fatal("output failure ignored")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrPermission }

func TestCommandHistoricalProfileContractAndRejectedPartialSource(t *testing.T) {
	data, err := os.ReadFile("../../service/legacypreflight/testdata/legacy-profiles/schema-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Fixtures []struct {
			Ref, Family string
			DDL         []struct{ SQL string }
		}
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range manifest.Fixtures {
		for _, partial := range []bool{false, true} {
			t.Run(fixture.Ref+map[bool]string{false: "/recognized", true: "/partial"}[partial], func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "usage.sqlite")
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				for _, ddl := range fixture.DDL {
					if _, err := db.Exec(ddl.SQL); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				if partial {
					if _, err := db.Exec("alter table usage_events drop column request_id"); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				if _, err := db.Exec(`insert into settings values('untrusted_version','v1.14.4 OWNED_SECRET_VERSION',0)`); err != nil {
					db.Close()
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var out, errOut bytes.Buffer
				runErr := Run(context.Background(), []string{"--db", path, "--source-stopped"}, &out, &errOut)
				if (partial && !errors.Is(runErr, ErrBlocked)) || (!partial && runErr != nil) {
					t.Fatalf("unexpected CLI exit: %v", runErr)
				}
				var report service.Report
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.UpgradeAuthorized || report.SourceKind != "legacy-manager" || report.ExactSourceRelease.Status != "unknown" {
					t.Fatalf("unsafe contract: %+v", report)
				}
				if partial {
					if report.PlannedBridgeCandidate != nil || bytes.Contains(out.Bytes(), []byte(`"plannedBridgeCandidate"`)) {
						t.Fatal("partial CLI source acquired a planned candidate")
					}
					if report.CompatibilityProfile.Status != "partial" || report.CompatibilityProfile.Family != "" {
						t.Fatal("partial gained family")
					}
				} else if report.CompatibilityProfile.Status != "recognized" || report.CompatibilityProfile.Family != fixture.Family {
					t.Fatal("recognized fixture lost family")
				}
				if !partial {
					assertPlannedCandidateCLIContract(t, report, out.Bytes())
				}
				if strings.Contains(out.String()+errOut.String(), "OWNED_SECRET") {
					t.Fatal("source setting leaked")
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("CLI changed source")
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatal("CLI created source assets")
				}
			})
		}
	}
}

// Historical DDL alone cannot exercise the states produced by v1.14.4
// datamigration/repository.go and quotasnapshot/repository.go at runtime.
func TestCommandHistoricalMigrationBlockersPreserveSource(t *testing.T) {
	data, err := os.ReadFile("../../service/legacypreflight/testdata/legacy-profiles/schema-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Fixtures []struct {
			Ref string
			DDL []struct{ SQL string }
		}
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var statements []string
	for _, fixture := range manifest.Fixtures {
		if fixture.Ref == "v1.14.4" {
			for _, ddl := range fixture.DDL {
				statements = append(statements, ddl.SQL)
			}
		}
	}
	if len(statements) == 0 {
		t.Fatal("historical v1.14.4 fixture absent")
	}
	for _, test := range []struct{ cache, quota, item string }{
		{"applying", "completed", "cache-accounting-v2"},
		{"clearing", "completed", "cache-accounting-v2"},
		{"completed", "pending", "quota-snapshot-lifecycle-v1"},
		{"completed", "running", "quota-snapshot-lifecycle-v1"},
		{"completed", "failed", "quota-snapshot-lifecycle-v1"},
		{"completed", "offline_required", "quota-snapshot-lifecycle-v1"},
	} {
		t.Run(test.cache+"/"+test.quota, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "usage.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range statements {
				if _, err := db.Exec(statement); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`insert into usage_data_migrations(name,status,last_error) values
				('usage_cache_accounting_v2',?,'OWNED_SECRET_ERROR'),
				('quota_snapshot_lifecycle_v1',?,'OWNED_SECRET_ERROR')`, test.cache, test.quota); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			if err := Run(context.Background(), []string{"--db", path, "--source-stopped"}, &out, &errOut); !errors.Is(err, ErrBlocked) {
				t.Fatalf("incomplete v1 migration did not block CLI: %v", err)
			}
			var report service.Report
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.CompatibilityProfile.Status != "recognized" || report.CompatibilityProfile.Family != "LegacySchemaC" || report.BridgeReadiness.Status != "blocked" || report.BridgeReadiness.Items[test.item].Status != "blocked" || report.UpgradeAuthorized || report.ExactSourceRelease.Status != "unknown" {
				t.Fatalf("migration/profile/grant contract: %+v", report)
			}
			if !strings.Contains(strings.Join(report.Blocked, ","), "legacy-bridge-blocked") {
				t.Fatal("Bridge blocker absent")
			}
			assertPlannedCandidateCLIContract(t, report, out.Bytes())
			if strings.Contains(out.String()+errOut.String(), "OWNED_SECRET") {
				t.Fatal("migration error leaked")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("CLI changed source bytes")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("CLI created source assets")
			}
		})
	}
}

func assertPlannedCandidateCLIContract(t *testing.T, report service.Report, data []byte) {
	t.Helper()
	candidate := report.PlannedBridgeCandidate
	if candidate == nil || candidate.Status != "planned" || candidate.Release != "v1.14.4" || candidate.SourceCommit != "166262365d506157b99968105c890f480cc7c07c" {
		t.Fatalf("CLI lost fixed planned candidate: %+v", candidate)
	}
	if report.ExactSourceRelease.Status != "unknown" || report.UpgradeAuthorized || !slices.Contains(report.Unknown, "exact-source-release") || !slices.Contains(report.Unknown, "forward-hop-evidence") {
		t.Fatal("CLI planned candidate concealed missing evidence or granted authority")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object["plannedBridgeCandidate"], &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["status"] == nil || fields["release"] == nil || fields["sourceCommit"] == nil {
		t.Fatal("planned target acquired extra evidence/authority fields")
	}
}
