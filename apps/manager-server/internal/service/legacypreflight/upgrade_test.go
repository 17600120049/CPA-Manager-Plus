package legacypreflight

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const upgradeTestKey = "OWNED_SECRET_existing_admin_only"

func upgradeFixture(t *testing.T, ref string) (sqlite.InspectionPaths, maintenancebundle.Layout, UpgradeBackupOptions) {
	t.Helper()
	var paths sqlite.InspectionPaths
	for _, fixture := range loadLegacyProfileFixtures(t) {
		if fixture.Ref == ref {
			paths = buildLegacyProfileFixture(t, fixture)
		}
	}
	if paths.Database == "" {
		t.Fatal("fixture missing")
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(paths.Database))
	if err != nil {
		t.Fatal(err)
	}
	paths = DefaultPaths(filepath.Join(root, "usage.sqlite"))
	paths.Config = filepath.Join(root, "config.json")
	credential, err := security.NewAdminCredential(upgradeTestKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(credential)
	execLegacyFixture(t, paths.Database, `insert into settings values('admin_credential_v1',?,0)`, string(raw))
	execLegacyFixture(t, paths.Database, `insert into usage_data_migrations(name,status) values('usage_cache_accounting_v2','completed'),('quota_snapshot_lifecycle_v1','completed')`)
	if err := os.WriteFile(paths.DataKey, []byte(base64.RawStdEncoding.EncodeToString(make([]byte, 32))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Config, []byte(`{"untrusted_version":"v1.14.4"}`), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := processlock.Acquire(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	layout := maintenancebundle.Layout{DatabasePath: paths.Database, DataKeyPath: paths.DataKey, ConfigPath: paths.Config, ArchivesPath: paths.Archives, RequireConfig: true}
	return paths, layout, UpgradeBackupOptions{AdminKey: upgradeTestKey, SourceStopped: true}
}

func upgradeBackup(t *testing.T, layout maintenancebundle.Layout, options *UpgradeBackupOptions) {
	t.Helper()
	lock, err := processlock.AcquireExisting(layout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	options.BundleDirectory = filepath.Join(root, "backup")
	manifest, err := maintenancebundle.Create(context.Background(), layout, options.BundleDirectory, lock)
	if err != nil {
		t.Fatal(err)
	}
	options.ExpectedSourceRevision = manifest.SourceRevision
}

func checkUpgrade(t *testing.T, ctx context.Context, paths sqlite.InspectionPaths, options UpgradeBackupOptions, reason string) Report {
	t.Helper()
	before := sourceState(t, filepath.Dir(paths.Database))
	report, err := CheckUpgradeBackup(ctx, paths, options)
	if !errors.Is(err, ErrUpgradePreconditionsBlocked) || !slices.Contains(report.Blocked, reason) || report.UpgradeAuthorized || report.UpgradePreconditions.TrustedHopReceipt {
		t.Fatalf("expected %s, got %+v / %v", reason, report, err)
	}
	if err := report.UpgradePreconditions.Admission.Validate(); err != nil {
		t.Fatal("invalid existing contract", err)
	}
	if !reflect.DeepEqual(before, sourceState(t, filepath.Dir(paths.Database))) {
		t.Fatal("source bytes/metadata changed")
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), "OWNED_SECRET") {
		t.Fatal("credential escaped")
	}
	return report
}

func TestUpgradeBackupContentProofCannotMintSourceOrMigrationAuthority(t *testing.T) {
	paths, layout, options := upgradeFixture(t, "v1.14.4")
	upgradeBackup(t, layout, &options)
	report := checkUpgrade(t, context.Background(), paths, options, "exact-source-provenance-unverified")
	p := report.UpgradePreconditions
	if !p.AdminVerified || p.Admission.Shape != bootstrap.LegacySchemaC || p.Admission.PlannedCandidate != bootstrap.ExactV1144Candidate() ||
		p.Admission.EvidenceStatus != bootstrap.SourceUnverified || p.Backup == nil || !p.Backup.IntegrityVerified || !p.Backup.MatchingEvidenceVerified ||
		p.Backup.SourceRevision != options.ExpectedSourceRevision || !slices.Contains(report.Blocked, "external-writer-isolation-unverified") {
		t.Fatalf("incorrect proof boundary: %+v", p)
	}
	lock, err := processlock.AcquireExisting(paths.Database)
	if err != nil {
		t.Fatal("source fence was not released", err)
	}
	defer lock.Close()
}

func TestUpgradeAdminFailuresReturnNoInventoryAndNeverBecomeFresh(t *testing.T) {
	for _, kind := range []string{"missing-key", "wrong-key", "rotated-key", "expired-session", "missing-credential", "malformed-credential", "missing-db"} {
		t.Run(kind, func(t *testing.T) {
			paths, _, options := upgradeFixture(t, "v1.14.4")
			reason := "existing-admin-unverifiable-use-stopped-host-recovery"
			switch kind {
			case "missing-key":
				options.AdminKey = ""
				reason = "existing-admin-authorization-required"
			case "wrong-key":
				options.AdminKey = "OWNED_SECRET_wrong_key"
			case "rotated-key":
				current, err := security.NewAdminCredential("OWNED_SECRET_rotated_key", "test")
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(current)
				execLegacyFixture(t, paths.Database, `update settings set value=? where key='admin_credential_v1'`, string(data))
			case "expired-session":
				options.AdminKey = "expired-browser-session"
			case "missing-credential":
				execLegacyFixture(t, paths.Database, `delete from settings where key='admin_credential_v1'`)
			case "malformed-credential":
				execLegacyFixture(t, paths.Database, `update settings set value='{"version":99}' where key='admin_credential_v1'`)
			case "missing-db":
				if err := os.Remove(paths.Database); err != nil {
					t.Fatal(err)
				}
			}
			report := checkUpgrade(t, context.Background(), paths, options, reason)
			if report.SourceKind != "unknown" || len(report.Observed.Files) != 0 || len(report.Observed.Tables) != 0 || report.UpgradePreconditions.AdminVerified || report.UpgradePreconditions.Backup != nil {
				t.Fatal("unauthorized inventory or Fresh fallback", report)
			}
		})
	}
}

func TestUpgradeNativeV2CannotAcquireLegacyAdmission(t *testing.T) {
	paths, _, options := upgradeFixture(t, "v1.14.4")
	// Setup only: produce the actual native-v2 schema on the disposable test DB.
	// The check itself must not run this writable production opener.
	db, err := sqlite.Open(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report := checkUpgrade(t, context.Background(), paths, options, "upgrade-source-profile-unresolved")
	if report.SourceKind != "native-v2" || report.UpgradePreconditions.Backup != nil {
		t.Fatal("wrong generation treated as an Upgrade candidate", report)
	}
}

func TestUpgradeSourceAndOwnershipStops(t *testing.T) {
	for _, kind := range []string{"older", "metadata-only", "unknown-column", "wrong-generation", "unasserted-stop", "missing-lock", "live-manager", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			ref := "v1.14.4"
			if kind == "older" {
				ref = "v1.12.12"
			}
			paths, _, options := upgradeFixture(t, ref)
			ctx := context.Background()
			reason := "upgrade-source-profile-unresolved"
			switch kind {
			case "older":
				reason = "qualified-forward-hop-required"
			case "metadata-only":
				execLegacyFixture(t, paths.Database, `insert into settings values('untrusted_version','v1.14.7',0)`)
				reason = "exact-source-provenance-unverified"
			case "unknown-column":
				execLegacyFixture(t, paths.Database, `alter table usage_events add column unknown_future text`)
			case "wrong-generation":
				execLegacyFixture(t, paths.Database, `create table gateway_api_key_identities(id text primary key)`)
			case "unasserted-stop":
				options.SourceStopped = false
				reason = "source-quiescence-unproven"
			case "missing-lock":
				if err := os.Remove(paths.Database + ".manager.lock"); err != nil {
					t.Fatal(err)
				}
				reason = "source-ownership-unavailable"
			case "live-manager":
				lock, err := processlock.AcquireExisting(paths.Database)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				reason = "source-ownership-unavailable"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				reason = "inspection-interrupted"
			}
			checkUpgrade(t, ctx, paths, options, reason)
		})
	}
}

func TestUpgradeBackupMismatchOmissionAndCorruption(t *testing.T) {
	for _, kind := range []string{"missing", "stale-revision", "source-key-missing", "source-config-missing", "backup-key-missing", "backup-config-missing", "backup-corrupt", "backup-foreign", "unpublished", "source-drift"} {
		t.Run(kind, func(t *testing.T) {
			paths, layout, options := upgradeFixture(t, "v1.14.4")
			upgradeBackup(t, layout, &options)
			reason := "backup-integrity-or-publication-unverified"
			switch kind {
			case "missing":
				options.BundleDirectory = ""
				reason = "verified-backup-and-expected-revision-required"
			case "stale-revision":
				options.ExpectedSourceRevision = strings.Repeat("0", 64)
				reason = "source-revision-mismatch"
			case "source-key-missing", "source-config-missing", "backup-key-missing", "backup-config-missing":
				file := map[string]string{"source-key-missing": paths.DataKey, "source-config-missing": paths.Config,
					"backup-key-missing": filepath.Join(options.BundleDirectory, "data-key"), "backup-config-missing": filepath.Join(options.BundleDirectory, "config")}[kind]
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(kind, "source-") {
					reason = "source-backup-inventory-incomplete"
				}
			case "backup-corrupt":
				if err := os.WriteFile(filepath.Join(options.BundleDirectory, "database"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "backup-foreign":
				if err := os.WriteFile(paths.Config, []byte(`{"changed":true}`), 0600); err != nil {
					t.Fatal(err)
				}
				m, err := maintenancebundle.Inspect(context.Background(), layout)
				if err != nil {
					t.Fatal(err)
				}
				options.ExpectedSourceRevision = m.SourceRevision
				reason = "backup-source-mismatch"
			case "unpublished":
				if err := os.Remove(filepath.Join(options.BundleDirectory, "manifest.json")); err != nil {
					t.Fatal(err)
				}
			case "source-drift":
				execLegacyFixture(t, paths.Database, `insert into settings values('changed','true',0)`)
				reason = "source-revision-mismatch"
			}
			checkUpgrade(t, context.Background(), paths, options, reason)
		})
	}
}

func TestUpgradeRealWALSnapshotAndOmittedWAL(t *testing.T) {
	paths, layout, options := upgradeFixture(t, "v1.14.4")
	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma journal_mode=wal; pragma wal_autocheckpoint=0; update settings set updated_at_ms=123`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	// Capture a bounded, stationary WAL file set, then close the writer and
	// restore those exact bytes as a cold source fixture (never a live DB).
	files := map[string][]byte{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(paths.Database + suffix)
		if err != nil {
			t.Fatal(err)
		}
		files[suffix] = data
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for suffix, data := range files {
		if err := os.WriteFile(paths.Database+suffix, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	upgradeBackup(t, layout, &options)
	report := checkUpgrade(t, context.Background(), paths, options, "exact-source-provenance-unverified")
	if report.UpgradePreconditions.Backup == nil || !report.UpgradePreconditions.Backup.MatchingEvidenceVerified {
		t.Fatal("real cold WAL backup not validated", report.Blocked)
	}
	if err := os.Remove(filepath.Join(options.BundleDirectory, "database-wal")); err != nil {
		t.Fatal(err)
	}
	checkUpgrade(t, context.Background(), paths, options, "backup-integrity-or-publication-unverified")
}

func TestUpgradeArchiveAndRawDeletedEvidenceRemainBounded(t *testing.T) {
	for _, kind := range []string{"complete", "raw-deleted", "missing-backup-segment", "missing-source-archive", "missing-both-segments"} {
		t.Run(kind, func(t *testing.T) {
			paths, layout, options := upgradeFixture(t, "v1.14.4")
			// Reuse the existing real v1 archive snapshot. Test-owned copies only;
			// installing a test credential is not production source attestation.
			root := "../../../../../tests/fixtures/phase4a-evidence/bak-02/bundle"
			for _, pair := range [][2]string{{"database", paths.Database}, {"data-key", paths.DataKey}, {"config", paths.Config}} {
				data, err := os.ReadFile(filepath.Join(root, pair[0]))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pair[1], data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.CopyFS(paths.Archives, os.DirFS(filepath.Join(root, "usage-archives"))); err != nil {
				t.Fatal(err)
			}
			// Git/CopyFS does not preserve the production archive's private mode.
			// Restore it only on this test-owned copy, never the fixed evidence.
			if err := filepath.WalkDir(paths.Archives, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				return os.Chmod(path, 0600)
			}); err != nil {
				t.Fatal(err)
			}
			credential, err := security.NewAdminCredential(upgradeTestKey, "test")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(credential)
			execLegacyFixture(t, paths.Database, `update settings set value=? where key='admin_credential_v1'`, string(raw))
			if kind == "raw-deleted" {
				// A retained coverage witness without a complete historical proof
				// must never be promoted by byte-valid archive/backup metadata.
				execLegacyFixture(t, paths.Database, `insert into usage_archive_deleted_coverage_daily(utc_day,deleted_event_count,min_timestamp_ms,max_timestamp_ms) values(1790812800000,1,1790812800000,1790812800000)`)
			}
			upgradeBackup(t, layout, &options)
			reason := "exact-source-provenance-unverified"
			segment := "0b61c6371e8ffbdf715aa493bf9ca9dd/segment-000001-00000000000000000001-00000000000000000004.jsonl.gz"
			switch kind {
			case "raw-deleted":
				reason = "retained-history-coverage-unverified"
			case "missing-backup-segment":
				if err := os.Remove(filepath.Join(options.BundleDirectory, "usage-archives", segment)); err != nil {
					t.Fatal(err)
				}
				reason = "backup-integrity-or-publication-unverified"
			case "missing-source-archive":
				if err := os.RemoveAll(paths.Archives); err != nil {
					t.Fatal(err)
				}
				reason = "source-revision-mismatch"
			case "missing-both-segments":
				if err := os.Remove(filepath.Join(paths.Archives, segment)); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(options.BundleDirectory, "usage-archives", segment)); err != nil {
					t.Fatal(err)
				}
				// Recompute ordinary integrity metadata using the maintained
				// inventory; SQLite's original segment reference still rejects it.
				manifest, err := maintenancebundle.Inspect(context.Background(), layout)
				if err != nil {
					t.Fatal(err)
				}
				manifest.Fence = maintenancebundle.CooperatingFence
				data, _ := json.Marshal(manifest)
				if err := os.WriteFile(filepath.Join(options.BundleDirectory, "manifest.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				options.ExpectedSourceRevision = manifest.SourceRevision
				reason = "backup-sqlite-key-config-or-archive-evidence-invalid"
			}
			report := checkUpgrade(t, context.Background(), paths, options, reason)
			if (kind == "complete" || kind == "raw-deleted") && (report.UpgradePreconditions.Backup == nil || !report.UpgradePreconditions.Backup.MatchingEvidenceVerified) {
				t.Fatal("complete file evidence was not verified", report.Blocked)
			}
		})
	}
}
