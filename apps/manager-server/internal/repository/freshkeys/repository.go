// Package freshkeys stores staged client secrets and their existing canonical
// identities atomically. It creates no runtime source bindings or acknowledgments.
package freshkeys

import (
	"context"
	"database/sql"
	"errors"
	"math"

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

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) List(ctx context.Context) ([]Record, error) {
	rows, err := r.db.QueryContext(ctx, `select k.request_id, i.id, i.revision, i.lifecycle, i.created_at_ms, i.updated_at_ms,
		k.secret_ciphertext, k.secret_hash, k.masked_preview, k.name, k.name_revision from fresh_client_api_keys k
		join gateway_api_key_identities i on i.id=k.api_key_id order by i.created_at_ms, i.id`)
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
func (r *Repository) Create(ctx context.Context, v Record) (bool, error) {
	if v.Identity.Validate() != nil || !security.IsValidProtectedEnvelope(v.Ciphertext) || !identity.IsValidSHA256Hex(v.Hash) {
		return false, errors.New("invalid staged client key")
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
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
