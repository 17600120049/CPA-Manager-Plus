package legacyartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	artifactservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyprofile"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

// These ordinary on-disk fixtures test diagnostic CLI consumption, never
// production execution provenance. Published execution is a separate opt-in.
func offlineFixtureBundle(t *testing.T) (root, bundle string, digest string) {
	t.Helper()
	root = testTempDir(t)
	source := filepath.Join(root, "source")
	layout := createTestInputBundle(t, source, "LegacySchemaC")
	if err := os.Remove(layout.DataKeyPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := security.LoadOrCreateDataKey("", layout.DataKeyPath); err != nil {
		t.Fatal(err)
	}
	mockMigrateToV1144(t, layout.DatabasePath, "completed", "completed", false)
	// The published v1.14.4 cleanup-derived entry point expects the deferred
	// quota-snapshot lifecycle table to exist even when it has no pending rows.
	// This keeps the opt-in runtime smoke a valid controlled cleanup operation;
	// it does not turn the historical-schema fixture into a forward-hop proof.
	db, err := sql.Open("sqlite", layout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table account_quota_snapshots (
		id integer primary key autoincrement,
		observation_id integer,
		logical_window_id integer,
		activation_id integer,
		cycle_id integer,
		account_key text not null,
		provider text not null,
		provider_window_id text not null,
		window_kind text not null,
		window_mode text not null,
		model_scope_kind text not null,
		model_scope_key text,
		model_ids_json text,
		scope_fingerprint text not null default '',
		content_hash text not null default '',
		source text not null,
		source_observation_id text,
		observed_at_ms integer not null,
		boundary_accuracy text not null,
		cycle_start_ms integer,
		cycle_end_ms integer,
		duration_seconds integer,
		used_percent real,
		remaining_percent real,
		used_value real,
		limit_value real,
		quota_unit text,
		reset_credits_available integer,
		reset_credits_json text,
		plan_type text,
		created_at_ms integer not null
	)`)
	if err == nil {
		_, err = db.Exec(`create table usage_pricing_rollup_state (
			rollup_name text primary key,
			schema_version integer not null default 1,
			structure_revision text not null default '',
			status text not null default 'ready',
			backfill_last_event_id integer not null default 0,
			coverage_event_id integer not null default 0,
			target_event_id integer not null default 0,
			processed_events integer not null default 0,
			min_bucket_ms integer,
			max_bucket_ms integer,
			last_run_started_at_ms integer,
			updated_at_ms integer not null default 0,
			finished_at_ms integer,
			last_error text
		)`)
	}
	if err == nil {
		_, err = db.Exec(`create table usage_monitoring_rollup_state (
			rollup_name text primary key,
			schema_version integer not null default 1,
			structure_revision text not null default '',
			status text not null default 'ready',
			backfill_last_event_id integer not null default 0,
			coverage_event_id integer not null default 0,
			target_event_id integer not null default 0,
			processed_events integer not null default 0,
			last_run_started_at_ms integer,
			updated_at_ms integer not null default 0,
			finished_at_ms integer,
			last_error text
		)`)
	}
	if err == nil {
		_, err = db.Exec(`create table usage_monitoring_selector_daily_rollups_v1 (
			bucket_ms integer not null,
			model_format_revision text not null default ''
		)`)
	}
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("prepare published cleanup schema: %v %v", err, closeErr)
	}
	lock, err := processlock.Acquire(layout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	bundle = filepath.Join(root, "input-bundle")
	manifest, err := maintenancebundle.Create(context.Background(), layout, bundle, lock)
	_ = lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	digest, err = artifactservice.ComputeManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return root, bundle, digest
}

func testTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func createTestInputBundle(t *testing.T, root string, schemaFamily string) maintenancebundle.Layout {
	t.Helper()
	layout := maintenancebundle.BundleLayout(root)
	if err := os.MkdirAll(filepath.Dir(layout.DatabasePath), 0o700); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", layout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, ddl := range historicalDDL(t, schemaFamily) {
		if _, err := db.Exec(ddl.SQL); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO settings VALUES ('manager_config_v1', '{}', 1000)`,
		`INSERT INTO usage_events (id,event_hash,timestamp_ms,timestamp,model,raw_json,created_at_ms) VALUES (1,'event-1',1000,'1970-01-01T00:00:01Z','test','{}',1000)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	// Create fake data.key
	if err := os.WriteFile(layout.DataKeyPath, []byte("test-data-key-12345678901234567890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return layout
}

func mockMigrateToV1144(t *testing.T, dbPath string, cacheStatus, quotaStatus string, withLock bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Use historical DDL shapes; this simulated migration is not execution qualification.
	for _, ddl := range historicalDDL(t, "LegacySchemaC") {
		if _, err := db.Exec(ddl.SQL); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range strings.Fields(legacyprofile.MetadataColumnSpecs) {
		parts := strings.Split(spec, ":")
		var present int
		if err := db.QueryRow(`select count(*) from pragma_table_info('usage_events') where name=?`, parts[0]).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present == 0 {
			if _, err := db.Exec("alter table usage_events add column " + parts[0] + " " + parts[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	stmts := []string{
		`DELETE FROM usage_data_migrations`,
		`INSERT INTO usage_data_migrations (name,status) VALUES ('usage_cache_accounting_v2', '` + cacheStatus + `')`,
		`INSERT INTO usage_data_migrations (name,status) VALUES ('quota_snapshot_lifecycle_v1', '` + quotaStatus + `')`,
	}

	if withLock {
		stmts = append(stmts, `INSERT INTO usage_maintenance_locks (name,run_id,operation,acquired_at_ms,updated_at_ms) VALUES ('active_lock','test','test',1,1);`)
	} else {
		stmts = append(stmts, `DELETE FROM usage_maintenance_locks;`)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %s: %v", s, err)
		}
	}
}

type historicalTableDDL struct {
	Table string `json:"table"`
	SQL   string `json:"sql"`
}

var historicalFixturePath, historicalFixturePathErr = filepath.Abs("../../service/legacypreflight/testdata/legacy-profiles/schema-fixtures.json")

func historicalDDL(t *testing.T, family string) []historicalTableDDL {
	t.Helper()
	if historicalFixturePathErr != nil {
		t.Fatal(historicalFixturePathErr)
	}
	data, err := os.ReadFile(historicalFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixtureSet struct {
		Fixtures []struct {
			Family string               `json:"family"`
			DDL    []historicalTableDDL `json:"ddl"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &fixtureSet); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtureSet.Fixtures {
		if f.Family == family {
			return f.DDL
		}
	}
	t.Fatalf("missing historical family %s", family)
	return nil
}

type offlineIntent struct {
	Tool                string `json:"tool"`
	OperationID         string `json:"operationId"`
	Release             string `json:"release"`
	SourceCommit        string `json:"sourceCommit"`
	Asset               string `json:"asset"`
	ArchiveSHA256       string `json:"archiveSHA256"`
	ExecutableSHA256    string `json:"executableSHA256"`
	InputBundle         string `json:"inputBundle"`
	InputManifestDigest string `json:"inputManifestDigest"`
	TimeoutMS           int64  `json:"timeoutMs"`
}

type offlineState struct {
	OperationID          string                             `json:"operationId"`
	IntentDigest         string                             `json:"intentDigest"`
	Phase                string                             `json:"phase"`
	Process              artifactservice.ProcessEvidence    `json:"process"`
	Reaped               bool                               `json:"reaped"`
	OutputManifestDigest string                             `json:"outputManifestDigest,omitempty"`
	Checkpoints          artifactservice.CheckpointEvidence `json:"checkpoints"`
}

func cliJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}
func cliDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func cliWorkspace(t *testing.T, phase string) (string, string) {
	t.Helper()
	root, bundle, digest := offlineFixtureBundle(t)
	workspace := filepath.Join(root, "operation")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	layout, err := maintenancebundle.RestoreNew(context.Background(), bundle, filepath.Join(root, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := processlock.Acquire(layout.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := maintenancebundle.Create(context.Background(), layout, filepath.Join(workspace, "output"), lock)
	lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	outputDigest, err := artifactservice.ComputeManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	id, err := artifactservice.GenerateOperationID()
	if err != nil {
		t.Fatal(err)
	}
	asset := "cpa-manager-plus_v1.14.4_" + runtime.GOOS + "_amd64.tar.gz"
	archiveHash, exeHash := "eab3a90594cb70e0362b0a37f9737f7b249402c0971ff4c7134a53079de1e033", "0588b7f28010825cde26511dfecafbb4a70f6c26d35f4cc4998b2f90434b3a98"
	if runtime.GOOS == "darwin" {
		archiveHash, exeHash = "ea55a5e85ceeaf63f430d965727bff5de7c85c4305f6e4d3d1eb9c87af6410d1", "7c320f6109768a76c8ca93e2db62238fd99442cb1687f76c433b37b8a98ec4ce"
	}
	intent := offlineIntent{Tool: "manager-offline-cleanup/v1", OperationID: id, Release: artifactservice.Release, SourceCommit: artifactservice.SourceCommit, Asset: asset, ArchiveSHA256: archiveHash, ExecutableSHA256: exeHash, InputBundle: bundle, InputManifestDigest: digest, TimeoutMS: 60000}
	data := cliJSON(t, intent)
	state := offlineState{OperationID: id, IntentDigest: cliDigest(data), Phase: phase, Process: artifactservice.ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100}, Reaped: true, OutputManifestDigest: outputDigest, Checkpoints: artifactservice.CheckpointEvidence{CacheAccountingV2: "completed-observed", QuotaSnapshotLifecycleV1: "completed-observed", MaintenanceLock: "none-observed"}}
	if err := os.WriteFile(filepath.Join(workspace, "intent.json"), data, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "state.json"), cliJSON(t, state), 0600); err != nil {
		t.Fatal(err)
	}
	return workspace, id
}

func TestOfflineCommandModesAreExclusiveIncludingEmptyIDs(t *testing.T) {
	modes := [][]string{{"--offline-cleanup"}, {"--inspect-offline-operation", "id"}, {"--recover-offline-operation", "id"}, {"--execute-hop"}, {"--verify-receipt", "receipt"}, {"--inspect-offline-operation", ""}, {"--recover-offline-operation", ""}}
	for i, a := range modes {
		for j, b := range modes {
			if i >= j || a[0] == b[0] {
				continue
			}
			var out, stderr bytes.Buffer
			err := Run(context.Background(), append(append([]string{}, a...), b...), &out, &stderr)
			if err == nil || err.Error() != "select one legacy artifact operation mode" || out.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("mixed mode %v + %v: %q %v", a, b, out.String(), err)
			}
		}
	}
}

func TestOfflineCommandInvalidInputsAreRedactedAndHaveNoOutput(t *testing.T) {
	root := testTempDir(t)
	secret := filepath.Join(root, "secret-token")
	cases := [][]string{
		{"--offline-cleanup"},
		{"--offline-cleanup", "--archive", secret, "--asset", "wrong", "--input-bundle", secret, "--input-digest", "secret-token", "--operation-workspace", secret},
		{"--offline-cleanup", "--timeout", "11m"},
		{"--inspect-offline-operation", ""}, {"--recover-offline-operation", ""},
		{"--inspect-offline-operation", "secret-token", "--operation-workspace", secret},
		{"--recover-offline-operation", "secret-token", "--operation-workspace", secret},
		{"--inspect-offline-operation", strings.Repeat("a", 32)},
		{"--recover-offline-operation", strings.Repeat("a", 32)},
		{"--recover-offline-operation", strings.Repeat("a", 32), "--operation-workspace", root},
		{"--offline-cleanup", "--timeout", "secret-token"},
	}
	for _, args := range cases {
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "secret-token") || strings.Contains(stderr.String(), "secret-token") {
			t.Fatalf("invalid input leaked: %q %q %v", out.String(), stderr.String(), err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect/recover may create only the diagnostic lock; never authority/state.
	for _, entry := range entries {
		if entry.Name() != ".operation.lock" {
			t.Fatalf("invalid CLI wrote %s", entry.Name())
		}
	}
}

func TestOfflineInspectAndRecoverCommandJSONCannotClaimAuthority(t *testing.T) {
	if runtime.GOARCH != "amd64" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		t.Skip("Unix AMD64 diagnostic storage")
	}
	workspace, id := cliWorkspace(t, "sealed")
	var out, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--inspect-offline-operation", id, "--operation-workspace", workspace}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var got artifactservice.OfflineCleanupObservation
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["writerEvidence"] != false || fields["upgradeAuthorized"] != false {
		t.Fatalf("diagnostic JSON must explicitly deny authority: %s", out.String())
	}
	if got.Status != "observed-offline-cleanup-only" || got.OperationID != id || got.WriterEvidence || got.UpgradeAuthorized || got.InputManifestDigest == "" || got.OutputManifestDigest == "" {
		t.Fatalf("inspect authority: %+v", got)
	}
	for _, wrong := range []string{strings.Repeat("f", 32), "bad"} {
		out.Reset()
		if err := Run(context.Background(), []string{"--inspect-offline-operation", wrong, "--operation-workspace", workspace}, &out, &stderr); err == nil || out.Len() != 0 {
			t.Fatal("wrong ID accepted")
		}
	}
	workspace, id = cliWorkspace(t, "running")
	out.Reset()
	if err := Run(context.Background(), []string{"--recover-offline-operation", id, "--operation-workspace", workspace}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var recovery map[string]any
	if err := json.Unmarshal(out.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"status": "interrupted-offline-operation", "operationId": id, "upgradeAuthorized": false}
	if !reflect.DeepEqual(recovery, expected) {
		t.Fatalf("recovery authority: %v", recovery)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--inspect-offline-operation", id, "--operation-workspace", workspace}, &out, &stderr); err == nil || out.Len() != 0 {
		t.Fatal("interrupted inspect accepted")
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--recover-offline-operation", id, "--operation-workspace", workspace}, &out, &stderr); !errors.Is(err, artifactservice.ErrOfflineReuse) || out.Len() != 0 {
		t.Fatal("recovery replay accepted")
	}
}

func TestOfflineCleanupUnsupportedLaunchFailsClosed(t *testing.T) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		t.Skip("supported launch")
	}
	var out, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--offline-cleanup"}, &out, &stderr)
	if !errors.Is(err, artifactservice.ErrOfflinePlatform) || out.Len() != 0 {
		t.Fatalf("unsupported launch: %q %v", out.String(), err)
	}
	if runtime.GOARCH != "amd64" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		for _, mode := range []string{"--inspect-offline-operation", "--recover-offline-operation"} {
			err := Run(context.Background(), []string{mode, strings.Repeat("a", 32)}, &out, &stderr)
			if !errors.Is(err, artifactservice.ErrOfflinePlatform) || out.Len() != 0 {
				t.Fatalf("unsupported storage: %v", err)
			}
		}
	}
}

func TestPublishedOfflineCleanupCommandOptIn(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux AMD64 sealed executable launch")
	}
	archive := os.Getenv("CPAMP_LEGACY_OFFLINE_ARCHIVE")
	if archive == "" {
		t.Skip("set immutable Linux AMD64 archive")
	}
	root, bundle, digest := offlineFixtureBundle(t)
	workspace := filepath.Join(root, "published-operation")
	var out, stderr bytes.Buffer
	args := []string{"--offline-cleanup", "--archive", archive, "--asset", "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz", "--input-bundle", bundle, "--input-digest", digest, "--operation-workspace", workspace, "--timeout", "2m"}
	if err := Run(context.Background(), args, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var got artifactservice.OfflineCleanupObservation
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WriterEvidence || got.UpgradeAuthorized || !got.Reaped || got.Process.ExitCode != 0 || got.InputManifestDigest != digest {
		t.Fatalf("published CLI: %+v", got)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--inspect-offline-operation", got.OperationID, "--operation-workspace", workspace}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var inspected artifactservice.OfflineCleanupObservation
	if err := json.Unmarshal(out.Bytes(), &inspected); err != nil || !reflect.DeepEqual(inspected, got) {
		t.Fatalf("inspect mismatch: %v", err)
	}
	out.Reset()
	if err := Run(context.Background(), args, &out, &stderr); !errors.Is(err, artifactservice.ErrOfflineReuse) || out.Len() != 0 {
		t.Fatal("published replay accepted")
	}
}
