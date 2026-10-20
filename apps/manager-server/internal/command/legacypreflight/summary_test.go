package legacypreflight

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	service "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func TestSummaryLegacySourceIsReadOnlyAndDoesNotGrantUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table settings(key text primary key,value text);
		create table usage_events(id integer primary key);
		insert into settings values('setup','{"managementKey":"OWNED_SECRET"}')`); err != nil {
		_ = db.Close()
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
	err = Run(context.Background(), []string{"--db", path, "--source-stopped", "--summary"}, &out, &errOut)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("partial legacy source must remain blocked: %v", err)
	}
	if !strings.Contains(out.String(), "Source: Manager source classification unconfirmed") ||
		strings.Contains(out.String(), "Existing legacy Manager database") ||
		!strings.Contains(out.String(), "Upgrade/Adopt authorization: NOT GRANTED") ||
		!strings.Contains(out.String(), "Inspection blocked or incomplete.") {
		t.Fatalf("unsafe or missing operator summary: %q", out.String())
	}
	if strings.Contains(out.String()+errOut.String(), "OWNED_SECRET") ||
		strings.Contains(out.String(), path) ||
		strings.Contains(out.String(), "managementKey") ||
		strings.HasPrefix(out.String(), "{") {
		t.Fatal("summary leaked raw inspection details")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("summary mutated source database")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("summary created extra source files: %v, %v", entries, err)
	}
}

func TestSummaryMissingSourceDoesNotSuggestFreshAdmission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-created", "usage.sqlite")
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"--db", path, "--summary"}, &out, &errOut)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("missing source unexpectedly admitted: %v", err)
	}
	if err.Error() != "source inspection is blocked; see preflight report" {
		t.Fatalf("summary mode reported wrong output format: %v", err)
	}
	if !strings.Contains(out.String(), "Specified Manager database not found") ||
		!strings.Contains(out.String(), "Missing data alone does not authorize") ||
		!strings.Contains(out.String(), "Upgrade/Adopt authorization: NOT GRANTED") {
		t.Fatalf("missing source summary = %q", out.String())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("summary created source directory: %v", err)
	}
}

func TestSummaryNeverTurnsBlockedOrUnknownIntoFreshPermission(t *testing.T) {
	for _, report := range []service.Report{
		{SourceKind: "fresh", Blocked: []string{"source-ownership-unavailable"}},
		{SourceKind: "native-v2"},
		{SourceKind: "partial-source"},
		{SourceKind: "unknown", Unknown: []string{"source"}},
	} {
		var out bytes.Buffer
		if err := writeOperatorSummary(&out, report, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Upgrade/Adopt authorization: NOT GRANTED") {
			t.Fatalf("unsafe grant for %s: %s", report.SourceKind, out.String())
		}
		if report.SourceKind == "fresh" && (!strings.Contains(out.String(), "Inspection blocked or incomplete.") ||
			!strings.Contains(out.String(), "Source: Manager source classification unconfirmed") ||
			strings.Contains(out.String(), "No existing Manager database observed")) {
			t.Fatalf("blocked Fresh still received setup advice or classification: %s", out.String())
		}
		if report.SourceKind == "native-v2" && !strings.Contains(out.String(), "Existing native v2 Manager database") {
			t.Fatalf("nonblocked native-v2 lost its source classification: %s", out.String())
		}
	}
	var out bytes.Buffer
	if err := writeOperatorSummary(&out, service.Report{SourceKind: "legacy-manager"}, errors.New("OWNED_SECRET_example")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "OWNED_SECRET") || !strings.Contains(out.String(), "Inspection blocked") ||
		!strings.Contains(out.String(), "Source: Manager source classification unconfirmed") ||
		strings.Contains(out.String(), "Existing legacy Manager database") {
		t.Fatal("raw error leaked or not treated as blocked")
	}
}

func TestSummaryUpgradeCheckRetainsHostAuthenticationAndNoSecretOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	var out, errOut bytes.Buffer
	err := run(context.Background(), []string{"--db", path, "--upgrade-check", "--summary"},
		strings.NewReader("OWNED_SECRET_admin"), &out, &errOut)
	if err == nil {
		t.Fatal("missing source qualified for upgrade")
	}
	if strings.Contains(out.String()+errOut.String()+err.Error(), "OWNED_SECRET") {
		t.Fatal("administrator key leaked")
	}
	if !strings.Contains(out.String(), "Upgrade/Adopt authorization: NOT GRANTED") {
		t.Fatalf("upgrade summary lacks explicit stop: %s", out.String())
	}
}

func TestSummaryPropagatesOutputFailure(t *testing.T) {
	err := writeOperatorSummary(failingWriter{}, service.Report{SourceKind: "legacy-manager"}, nil)
	if err == nil {
		t.Fatal("summary ignored output failure")
	}
}
