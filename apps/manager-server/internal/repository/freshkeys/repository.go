// Package freshkeys stores staged client secrets, their canonical identities and
// the runtime source binding that lets usage events be attributed to them, all
// in one transaction. It creates no runtime acknowledgments.
package freshkeys

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

type Record struct {
	RequestID    string
	Identity     identity.APIKeyIdentity
	Ciphertext   string
	Hash         string
	Preview      string
	Name         string
	NameRevision int64
}

var ErrConflict = errors.New("staged key name revision conflict")

// ErrSourceOwned means the staged key's source hash already has an active
// binding to another identity, so no second attribution may be created.
var ErrSourceOwned = errors.New("staged key source is bound to another identity")

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

// List returns every staged key in creation order. Keys created inside the
// same millisecond share created_at_ms, so the staged row's insertion order
// breaks the tie; ordering by the random identity would silently reorder them.
func (r *Repository) List(ctx context.Context) ([]Record, error) {
	rows, err := r.db.QueryContext(ctx, `select k.request_id, i.id, i.revision, i.lifecycle, i.created_at_ms, i.updated_at_ms,
		k.secret_ciphertext, k.secret_hash, k.masked_preview, k.name, k.name_revision from fresh_client_api_keys k
		join gateway_api_key_identities i on i.id=k.api_key_id order by i.created_at_ms, k.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var v Record
		if err = rows.Scan(&v.RequestID, &v.Identity.ID, &v.Identity.Revision, &v.Identity.Lifecycle, &v.Identity.CreatedAtMS, &v.Identity.UpdatedAtMS, &v.Ciphertext, &v.Hash, &v.Preview, &v.Name, &v.NameRevision); err != nil {
			return nil, err
		}
		if v.Identity.Validate() != nil || v.NameRevision < 1 || !security.IsValidProtectedEnvelope(v.Ciphertext) {
			return nil, errors.New("invalid staged client key")
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// Rename changes only display metadata. A duplicate/stale submission cannot
// overwrite a newer edit, even if the first response was lost.
func (r *Repository) Rename(ctx context.Context, id identity.APIKeyID, name string, revision int64) error {
	if revision < 1 || revision >= math.MaxInt64 {
		return ErrConflict
	}
	result, err := r.db.ExecContext(ctx, `update fresh_client_api_keys set name=?, name_revision=name_revision+1
		where api_key_id=? and name_revision=?`, name, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Create returns false on an already committed request; the replay never
// returns the old secret. A uniqueness collision cannot create a second identity.
//
// When the committed installation's runtime identity is known, the source
// binding is written in the same transaction: the Canonical Key and its
// attribution window become durable together, so a later passive snapshot can
// never mint a second Canonical ID for the same secret hash.
func (r *Repository) Create(ctx context.Context, v Record, runtimeIdentity string) (bool, error) {
	if v.Identity.Validate() != nil || !security.IsValidProtectedEnvelope(v.Ciphertext) || !identity.IsValidSHA256Hex(v.Hash) {
		return false, errors.New("invalid staged client key")
	}
	if runtimeIdentity != "" && (strings.TrimSpace(runtimeIdentity) != runtimeIdentity || v.Identity.CreatedAtMS <= 0) {
		return false, errors.New("invalid staged client key source")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "select count(*) from fresh_client_api_keys where request_id=?", v.RequestID).Scan(&exists); err != nil {
		return false, err
	}
	if exists != 0 {
		return false, nil
	}
	i := v.Identity
	if _, err = tx.ExecContext(ctx, "insert into gateway_api_key_identities(id,revision,lifecycle,created_at_ms,updated_at_ms) values(?,?,?,?,?)", i.ID, i.Revision, i.Lifecycle, i.CreatedAtMS, i.UpdatedAtMS); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, "insert into fresh_client_api_keys(request_id,api_key_id,secret_ciphertext,secret_hash,masked_preview) values(?,?,?,?,?)", v.RequestID, i.ID, v.Ciphertext, v.Hash, v.Preview); err != nil {
		return false, err
	}
	if runtimeIdentity != "" {
		if err = bindSource(ctx, tx, i.ID, runtimeIdentity, v.Hash, i.CreatedAtMS); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// bindSource records one attribution window. An active binding of the same
// source hash that already belongs to another identity is a conflict: one hash
// has exactly one Canonical Key.
func bindSource(ctx context.Context, tx *sql.Tx, id identity.APIKeyID, runtimeIdentity, hash string, nowMS int64) error {
	if id.Validate() != nil || !identity.IsValidSHA256Hex(hash) || runtimeIdentity == "" ||
		strings.TrimSpace(runtimeIdentity) != runtimeIdentity || nowMS <= 0 {
		return errors.New("invalid staged client key source")
	}
	var owned bool
	if err := tx.QueryRowContext(ctx, `select exists(
		select 1 from gateway_api_key_source_bindings
		where api_key_hash = ? and retired_at_ms is null)`, hash).Scan(&owned); err != nil {
		return err
	}
	if owned {
		return ErrSourceOwned
	}
	_, err := tx.ExecContext(ctx, `insert into gateway_api_key_source_bindings (
		api_key_id, runtime_identity, api_key_hash, observed_runtime_generation,
		first_seen_at_ms, last_seen_at_ms, retired_at_ms
	) values (?, ?, ?, null, ?, ?, null)`, string(id), runtimeIdentity, hash, nowMS, nowMS)
	return err
}

// BindFreshKeySources binds staged keys that have no attribution yet to the
// runtime identity of the now committed installation. It is idempotent and
// never rebinds a source hash that an active binding already owns.
//
// The attribution window opens at the key's own creation instant, not at this
// call: a staged key can only produce traffic after its secret existed, and a
// later startup must not erase what an earlier one could already attribute.
func (r *Repository) BindFreshKeySources(ctx context.Context, runtimeIdentity string, nowMS int64) (int64, error) {
	if runtimeIdentity == "" || strings.TrimSpace(runtimeIdentity) != runtimeIdentity || nowMS <= 0 {
		return 0, errors.New("invalid staged client key source")
	}
	result, err := r.db.ExecContext(ctx, `insert into gateway_api_key_source_bindings (
		api_key_id, runtime_identity, api_key_hash, observed_runtime_generation,
		first_seen_at_ms, last_seen_at_ms, retired_at_ms
	)
	select k.api_key_id, ?, k.secret_hash, null, i.created_at_ms, ?, null
	from fresh_client_api_keys k
	join gateway_api_key_identities i on i.id = k.api_key_id
	where i.lifecycle = 'active'
	  and i.created_at_ms > 0
	  and not exists (
		select 1 from gateway_api_key_source_bindings b
		where b.api_key_hash = k.secret_hash and b.retired_at_ms is null
	  )`, runtimeIdentity, nowMS)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
