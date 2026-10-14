package legacyartifact

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

func offlineRuntimeAsset() string {
	return "cpa-manager-plus_v1.14.4_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
}

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
	digest, err = ComputeManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return root, bundle, digest
}

func offlineSealedFixture(t *testing.T) (root, workspace, operationID, inputBundle, inputDigest string) {
	t.Helper()
	root, inputBundle, inputDigest = offlineFixtureBundle(t)
	outputRoot := filepath.Join(root, "output-root")
	if _, err := maintenancebundle.RestoreNew(context.Background(), inputBundle, outputRoot); err != nil {
		t.Fatal(err)
	}
	workspace = filepath.Join(root, "operation")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	outputLock, err := processlock.Acquire(maintenancebundle.BundleLayout(outputRoot).DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	outputBundle := filepath.Join(workspace, "output")
	outputManifest, err := maintenancebundle.Create(context.Background(), maintenancebundle.BundleLayout(outputRoot), outputBundle, outputLock)
	_ = outputLock.Close()
	if err != nil {
		t.Fatal(err)
	}
	outputDigest, err := ComputeManifestDigest(outputManifest)
	if err != nil {
		t.Fatal(err)
	}
	operationID, err = GenerateOperationID()
	if err != nil {
		t.Fatal(err)
	}
	asset := offlineRuntimeAsset()
	pin := nativePins[asset]
	intent := offlineIntent{
		Tool: offlineTool, OperationID: operationID, Release: Release,
		SourceCommit: SourceCommit, Asset: asset,
		ArchiveSHA256: pin.ArchiveSHA256, ExecutableSHA256: pin.ExecutableSHA256,
		InputBundle: inputBundle, InputManifestDigest: inputDigest, TimeoutMS: 60_000,
	}
	intentData, err := offlineJSON(intent)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireOfflineLock(filepath.Join(workspace, ".operation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := offlineWriteNew(filepath.Join(workspace, "intent.json"), intentData, 0o400); err != nil {
		_ = lock.close()
		t.Fatal(err)
	}
	state := offlineState{
		OperationID: operationID, IntentDigest: offlineHash(intentData), Phase: "sealed",
		Process: ProcessEvidence{PID: 1, ExitCode: 0, StartedAtMS: 1000, FinishedAtMS: 1100, DurationMS: 100},
		Reaped:  true, OutputManifestDigest: outputDigest,
		Checkpoints: CheckpointEvidence{CacheAccountingV2: "completed-observed", QuotaSnapshotLifecycleV1: "completed-observed", MaintenanceLock: "none-observed"},
	}
	if err := offlineSave(workspace, state); err != nil {
		_ = lock.close()
		t.Fatal(err)
	}
	if err := lock.close(); err != nil {
		t.Fatal(err)
	}
	return root, workspace, operationID, inputBundle, inputDigest
}

func offlineScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "offline-writer")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func offlineProcessFixture(t *testing.T) (root string, lock *offlineLock, state *offlineState) {
	t.Helper()
	root = testTempDir(t)
	var err error
	lock, err = acquireOfflineLock(filepath.Join(root, ".operation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	state = &offlineState{OperationID: "0123456789abcdef0123456789abcdef", IntentDigest: offlineHash([]byte("intent")), Phase: "prepared"}
	if err := offlineSave(root, *state); err != nil {
		_ = lock.close()
		t.Fatal(err)
	}
	return root, lock, state
}

func TestOfflineProcessLifecycleReapsAndPersistsEvidence(t *testing.T) {
	root, lock, state := offlineProcessFixture(t)
	defer lock.close()
	if err := offlineRunProcess(context.Background(), offlineScript(t, "exit 0"), root, filepath.Join(root, "database"), lock, state); err != nil {
		t.Fatal(err)
	}
	if state.Phase != "reaped" || !state.Reaped || state.Process.PID <= 0 || state.Process.ExitCode != 0 {
		t.Fatalf("unexpected lifecycle state: %+v", state)
	}
	if err := state.Process.Validate(); err != nil {
		t.Fatalf("invalid persisted process evidence: %v", err)
	}
	var persisted offlineState
	if _, err := offlineRead(filepath.Join(root, "state.json"), 0o600, &persisted); err != nil || persisted.Phase != "reaped" || !persisted.Reaped {
		t.Fatalf("state was not persisted: %+v %v", persisted, err)
	}
}

func TestOfflineProcessFailureAndCancellationAreReaped(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		root, lock, state := offlineProcessFixture(t)
		defer lock.close()
		err := offlineRunProcess(context.Background(), offlineScript(t, "exit 7"), root, filepath.Join(root, "database"), lock, state)
		if !errors.Is(err, ErrProcessExecutionFailed) || !state.Reaped || state.Process.ExitCode != 7 {
			t.Fatalf("failure evidence = %+v, err=%v", state, err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		root, lock, state := offlineProcessFixture(t)
		defer lock.close()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := offlineRunProcess(ctx, offlineScript(t, "sleep 30"), root, filepath.Join(root, "database"), lock, state)
		if !errors.Is(err, ErrProcessExecutionFailed) || !state.Reaped {
			t.Fatalf("cancellation evidence = %+v, err=%v", state, err)
		}
	})
}

func TestOfflineWorkspaceConcurrentWriterRejected(t *testing.T) {
	root := testTempDir(t)
	first, err := acquireOfflineLock(filepath.Join(root, ".operation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := acquireOfflineLock(filepath.Join(root, ".operation.lock"))
	if !errors.Is(err, ErrOfflineBusy) || second != nil {
		t.Fatalf("concurrent lock result = %v %v", second, err)
	}
}

func TestOfflineInspectionBindsOperationAndInputOutputDigests(t *testing.T) {
	_, workspace, operationID, inputBundle, _ := offlineSealedFixture(t)
	if got, err := InspectOfflineCleanup(context.Background(), workspace, operationID); err != nil || got.Status != "observed-offline-cleanup-only" || got.OperationID != operationID || got.WriterEvidence || got.UpgradeAuthorized {
		t.Fatalf("valid observation = %+v, err=%v", got, err)
	}
	if _, err := InspectOfflineCleanup(context.Background(), workspace, operationID+"0"); !errors.Is(err, ErrOfflineState) {
		t.Fatalf("wrong operation ID accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inputBundle, "database"), []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectOfflineCleanup(context.Background(), workspace, operationID); !errors.Is(err, ErrOfflineInputs) {
		t.Fatalf("mutated input accepted: %v", err)
	}
}

func TestOfflineLegacyPathLaunchFormatRejected(t *testing.T) {
	if !offlineSupported() {
		t.Skip("Unix AMD64 diagnostic storage")
	}
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			_, workspace, id, _, _ := offlineSealedFixture(t)
			current, err := InspectOfflineCleanup(context.Background(), workspace, id)
			if err != nil || current == nil || current.Tool != "manager-offline-cleanup/v2" || current.WriterEvidence || current.UpgradeAuthorized {
				t.Fatalf("current format observation = %+v, err=%v", current, err)
			}
			in, state, err := offlineLoad(workspace, id)
			if err != nil {
				t.Fatal(err)
			}
			asset := "cpa-manager-plus_v1.14.4_" + platform + "_amd64.tar.gz"
			pin := nativePins[asset]
			// Freeze the exact intent layout from 06779fe5: no optional new
			// fields, and all artifact/input/state/output bindings remain valid.
			legacy := struct {
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
			}{"manager-offline-cleanup/v1", id, in.Release, in.SourceCommit, asset, pin.ArchiveSHA256, pin.ExecutableSHA256, in.InputBundle, in.InputManifestDigest, in.TimeoutMS}
			data, err := offlineJSON(legacy)
			if err != nil {
				t.Fatal(err)
			}
			// Test-only replacement of immutable intent, with a matching state
			// digest: rejection must come from the old contract identity.
			if err := os.Remove(filepath.Join(workspace, "intent.json")); err != nil {
				t.Fatal(err)
			}
			if err := offlineWriteNew(filepath.Join(workspace, "intent.json"), data, 0o400); err != nil {
				t.Fatal(err)
			}
			state.IntentDigest = offlineHash(data)
			if err := offlineSave(workspace, state); err != nil {
				t.Fatal(err)
			}
			if got, err := InspectOfflineCleanup(context.Background(), workspace, id); !errors.Is(err, ErrOfflineState) || got != nil {
				t.Fatalf("legacy path launch inspected: %+v, err=%v", got, err)
			}
			if err := RecoverOfflineCleanup(workspace, id); !errors.Is(err, ErrOfflineState) {
				t.Fatalf("legacy path launch recovered: %v", err)
			}
			var after offlineState
			if _, err := offlineRead(filepath.Join(workspace, "state.json"), 0o600, &after); err != nil || !reflect.DeepEqual(after, state) {
				t.Fatalf("legacy state was changed: %+v, err=%v", after, err)
			}
		})
	}
}

func TestOfflineOutputMutationAndRecoveryFailClosed(t *testing.T) {
	_, workspace, operationID, _, _ := offlineSealedFixture(t)
	if err := os.WriteFile(filepath.Join(workspace, "output", "database"), []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectOfflineCleanup(context.Background(), workspace, operationID); !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("mutated output accepted: %v", err)
	}

	_, workspace, operationID, _, _ = offlineSealedFixture(t)
	_, state, err := offlineLoad(workspace, operationID)
	if err != nil {
		t.Fatal(err)
	}
	state.Phase = "reaped"
	if err := offlineSave(workspace, state); err != nil {
		t.Fatal(err)
	}
	if err := RecoverOfflineCleanup(workspace, operationID); err != nil {
		t.Fatal(err)
	}
	_, state, err = offlineLoad(workspace, operationID)
	if err != nil || state.Phase != "interrupted" {
		t.Fatalf("recovery state = %+v, err=%v", state, err)
	}
	if _, err := InspectOfflineCleanup(context.Background(), workspace, operationID); !errors.Is(err, ErrOfflineState) {
		t.Fatalf("interrupted operation inspected as complete: %v", err)
	}
}

func TestOfflinePartialOutputAndExecutableMismatchFailClosed(t *testing.T) {
	_, workspace, operationID, _, _ := offlineSealedFixture(t)
	if err := os.Remove(filepath.Join(workspace, "output", "database")); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectOfflineCleanup(context.Background(), workspace, operationID); !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("partial output accepted: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not qualify the offline executable identity check")
	}
	root := testTempDir(t)
	exe := filepath.Join(root, "executable")
	if err := os.WriteFile(exe, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := offlineExecutable(context.Background(), exe, strings.Repeat("0", 64)); !errors.Is(err, ErrExecutableMismatch) {
		t.Fatalf("executable digest mismatch accepted: %v", err)
	}
}

func TestOfflineInvalidArtifactReplayAndTargetsFailClosed(t *testing.T) {
	root, inputBundle, digest := offlineFixtureBundle(t)
	workspace := filepath.Join(root, "new-operation")
	base := OfflineCleanupOptions{Asset: offlineRuntimeAsset(), InputBundle: inputBundle, ExpectedInputDigest: digest, Workspace: workspace, Timeout: time.Minute}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		if got, err := RunOfflineCleanup(context.Background(), base); !errors.Is(err, ErrOfflinePlatform) || got != nil {
			t.Fatalf("unsupported launch accepted: %v", err)
		}
		return
	}
	if _, err := RunOfflineCleanup(context.Background(), base); !errors.Is(err, ErrUnverified) {
		t.Fatalf("missing artifact accepted: %v", err)
	}
	if _, err := RunOfflineCleanup(context.Background(), OfflineCleanupOptions{ArchivePath: filepath.Join(root, "missing"), Asset: offlineRuntimeAsset(), InputBundle: inputBundle, ExpectedInputDigest: "bad", Workspace: filepath.Join(root, "new-operation-2"), Timeout: time.Minute}); !errors.Is(err, ErrOfflineInputs) {
		t.Fatalf("bad input digest accepted: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RunOfflineCleanup(context.Background(), OfflineCleanupOptions{ArchivePath: filepath.Join(root, "missing"), Asset: offlineRuntimeAsset(), InputBundle: inputBundle, ExpectedInputDigest: digest, Workspace: filepath.Join(root, "existing"), Timeout: time.Minute}); !errors.Is(err, ErrOfflineReuse) {
		t.Fatalf("existing workspace accepted: %v", err)
	}
	if _, err := RunOfflineCleanup(context.Background(), OfflineCleanupOptions{ArchivePath: filepath.Join(root, "missing"), Asset: "wrong", InputBundle: inputBundle, ExpectedInputDigest: digest, Workspace: filepath.Join(root, "new-operation-3"), Timeout: time.Minute}); !errors.Is(err, ErrOfflineInputs) {
		t.Fatalf("wrong target accepted: %v", err)
	}
}

func TestOfflineProductionEntrypointsRemainFailClosed(t *testing.T) {
	if _, err := ExecuteHop(context.Background(), HopOptions{}); !errors.Is(err, ErrExecutionNotQualified) {
		t.Fatalf("ExecuteHop opened unexpectedly: %v", err)
	}
	if _, err := ValidateHop(context.Background(), ValidateOptions{}); !errors.Is(err, ErrValidationNotQualified) {
		t.Fatalf("ValidateHop opened unexpectedly: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not qualify the offline lock")
	}
}

func TestPublishedOfflineCleanupOptIn(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("published offline execution requires Linux AMD64 sealed launch")
	}
	archive := strings.TrimSpace(os.Getenv("CPAMP_LEGACY_OFFLINE_ARCHIVE"))
	if archive == "" {
		t.Skip("set CPAMP_LEGACY_OFFLINE_ARCHIVE to run published writer qualification")
	}
	root, inputBundle, digest := offlineFixtureBundle(t)
	workspace := filepath.Join(root, "published-operation")
	got, err := RunOfflineCleanup(context.Background(), OfflineCleanupOptions{
		ArchivePath: archive, Asset: offlineRuntimeAsset(),
		InputBundle: inputBundle, ExpectedInputDigest: digest, Workspace: workspace,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tool != "manager-offline-cleanup/v2" || got.Status != "observed-offline-cleanup-only" || !got.Reaped || got.WriterEvidence || got.UpgradeAuthorized || got.Process.ExitCode != 0 {
		t.Fatalf("unexpected published offline observation: %+v", got)
	}
	if inspected, err := InspectOfflineCleanup(context.Background(), workspace, got.OperationID); err != nil || !reflect.DeepEqual(inspected, got) {
		t.Fatalf("published v2 inspection = %+v, err=%v", inspected, err)
	}
}
