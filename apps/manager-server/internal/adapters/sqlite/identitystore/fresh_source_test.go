package identitystore_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	ports "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/ports/identitystore"
)

// seedStagedFreshKey reproduces the Fresh workspace shape: one Canonical Key
// with one staged secret and no runtime source binding yet.
func seedStagedFreshKey(t *testing.T, db *sql.DB, keyID, hash, lifecycle string) {
	t.Helper()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`insert into gateway_api_key_identities (id, revision, lifecycle, created_at_ms, updated_at_ms)
			values (?, 1, ?, 100, 100)`, []any{keyID, lifecycle}},
		{`insert into fresh_client_api_keys (request_id, api_key_id, secret_ciphertext, secret_hash, masked_preview)
			values (?, ?, 'cipher', ?, 'cpamp_sk_…0000')`, []any{sha256Hex(keyID)[:32], keyID, hash}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed staged key: %v", err)
		}
	}
}

func countAPIKeyIdentities(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`select count(*) from gateway_api_key_identities`).Scan(&count); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	return count
}

func TestApplyPassiveSnapshotBindsStagedFreshKeyInsteadOfCreatingSecondIdentity(t *testing.T) {
	ctx := t.Context()
	db, repo := setupTestDB(t)
	stagedID := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	hash := sha256Hex("staged-client-secret")
	seedStagedFreshKey(t, db, stagedID, hash, "active")

	result, err := repo.ApplyPassiveSnapshot(ctx, ports.ReconcileSnapshotParams{
		RuntimeIdentity:           "runtime-managed-1",
		ObservedRuntimeGeneration: 3,
		APIKeys:                   []ports.APIKeySnapshotItem{{APIKeyHash: hash}},
		NowMS:                     5000,
	})
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	if result.APIKeysBound != 1 || result.APIKeysCreated != 0 {
		t.Fatalf("result = %+v, want one bound staged key and no new identity", result)
	}
	entity, binding, err := repo.FindActiveAPIKeyBySource(ctx, "runtime-managed-1", hash)
	if err != nil {
		t.Fatalf("find bound source: %v", err)
	}
	if string(entity.ID) != stagedID {
		t.Fatalf("identity = %s, want the staged canonical key %s", entity.ID, stagedID)
	}
	if binding.FirstSeenAtMS != 5000 || binding.APIKeyHash != hash {
		t.Fatalf("binding = %+v", binding)
	}
	if identities := countAPIKeyIdentities(t, db); identities != 1 {
		t.Fatalf("identities = %d, want exactly the staged one", identities)
	}
}

func TestApplyPassiveSnapshotFailsClosedForTerminalStagedKey(t *testing.T) {
	ctx := t.Context()
	db, repo := setupTestDB(t)
	stagedID := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	hash := sha256Hex("revoked-client-secret")
	seedStagedFreshKey(t, db, stagedID, hash, "superseded")

	// The revoke/replace commit closes the superseded key's window; the binding
	// row survives as history.
	if _, err := db.Exec(`insert into gateway_api_key_source_bindings
		(api_key_id, runtime_identity, api_key_hash, observed_runtime_generation,
		 first_seen_at_ms, last_seen_at_ms, retired_at_ms)
		values (?, 'runtime-managed-1', ?, null, 100, 100, 2000)`, stagedID, hash); err != nil {
		t.Fatalf("seed retired binding: %v", err)
	}

	// A lagging snapshot that still reports the revoked hash must never mint a
	// second Canonical ID for it: attribution history stays with the staged key.
	_, err := repo.ApplyPassiveSnapshot(ctx, ports.ReconcileSnapshotParams{
		RuntimeIdentity:           "runtime-managed-1",
		ObservedRuntimeGeneration: 3,
		APIKeys:                   []ports.APIKeySnapshotItem{{APIKeyHash: hash}},
		NowMS:                     5000,
	})
	if !errors.Is(err, ports.ErrStagedSourceTerminal) {
		t.Fatalf("superseded staged hash error = %v, want ErrStagedSourceTerminal", err)
	}
	if identities := countAPIKeyIdentities(t, db); identities != 1 {
		t.Fatalf("identities = %d, want exactly the staged one", identities)
	}
	var bindings int
	if err := db.QueryRow(`select count(*) from gateway_api_key_source_bindings where api_key_hash = ? and retired_at_ms is null`, hash).Scan(&bindings); err != nil {
		t.Fatalf("count active bindings: %v", err)
	}
	if bindings != 0 {
		t.Fatalf("active bindings = %d, want none after the refused snapshot", bindings)
	}
	if _, err := repo.LoadAPIKeyByID(ctx, identity.APIKeyID(stagedID)); err != nil {
		t.Fatalf("staged history lost: %v", err)
	}
}

// A superseded staged key that was never bound (it was revoked before the
// installation committed) owns the hash just the same: fail closed, never mint.
func TestApplyPassiveSnapshotFailsClosedForUnboundSupersededStagedKey(t *testing.T) {
	ctx := t.Context()
	db, repo := setupTestDB(t)
	stagedID := "b1b2c3d4e5f60718293a4b5c6d7e8f91"
	hash := sha256Hex("revoked-before-binding")
	seedStagedFreshKey(t, db, stagedID, hash, "superseded")

	if _, err := repo.ApplyPassiveSnapshot(ctx, ports.ReconcileSnapshotParams{
		RuntimeIdentity:           "runtime-managed-1",
		ObservedRuntimeGeneration: 3,
		APIKeys:                   []ports.APIKeySnapshotItem{{APIKeyHash: hash}},
		NowMS:                     5000,
	}); !errors.Is(err, ports.ErrStagedSourceTerminal) {
		t.Fatalf("unbound superseded staged hash error = %v, want ErrStagedSourceTerminal", err)
	}
	if identities := countAPIKeyIdentities(t, db); identities != 1 {
		t.Fatalf("identities = %d, want exactly the staged one", identities)
	}
}
