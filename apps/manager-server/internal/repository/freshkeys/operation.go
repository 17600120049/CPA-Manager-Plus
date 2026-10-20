package freshkeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
)

const latestOperationSetting = "fresh_key_operation_latest_v1"
const operationPrefix = "fresh_key_operation_v1:"

// Operation records attempts, not a reusable live-auth grant. Secrets remain in
// the original encrypted canonical records; the S1 journal is never overwritten.
type Operation struct {
	ID                 string    `json:"id"`
	TargetID           string    `json:"targetId"`
	KeeperID           string    `json:"keeperId"`
	Mode               string    `json:"mode"`
	RunID              string    `json:"runId"`
	Generation         string    `json:"generation"`
	ProviderSeedSHA256 string    `json:"providerSeedSHA256"`
	Revision           int64     `json:"revision"`
	Outcome            string    `json:"outcome"`
	Steps              []KeyStep `json:"steps"`
	CreatedAtMS        int64     `json:"createdAtMs"`
	CompletedAtMS      int64     `json:"completedAtMs"`
}
type KeyStep struct {
	Name     string `json:"name"`
	State    string `json:"state"` // unattempted, attempted, observed
	Returned bool   `json:"returned"`
}

func readOperation(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (*Operation, error) {
	var data string
	err := q.QueryRowContext(ctx, "select value from settings where key=?", operationPrefix+id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o Operation
	if json.Unmarshal([]byte(data), &o) != nil || o.ID != id || o.Revision < 1 || !validOperation(o) {
		return nil, errors.New("invalid key operation journal")
	}
	return &o, nil
}
func validOperation(o Operation) bool {
	for _, id := range []string{o.ID, o.TargetID, o.KeeperID} {
		if _, err := identity.ParseAPIKeyID(id); err != nil {
			return false
		}
	}
	if o.TargetID == o.KeeperID || o.RunID == "" || o.Generation == "" || (o.Outcome != "pending" && o.Outcome != "unknown" && o.Outcome != "committed") {
		return false
	}
	names := []string{"revoke"}
	if o.Mode == "replace" {
		names = []string{"add", "revoke"}
	} else if o.Mode != "revoke" {
		return false
	}
	if len(o.Steps) != len(names) {
		return false
	}
	for i, s := range o.Steps {
		if s.Name != names[i] || (s.State != "unattempted" && s.State != "attempted" && s.State != "observed") {
			return false
		}
		if i > 0 && s.State != "unattempted" && o.Steps[i-1].State != "observed" {
			return false
		}
	}
	return o.Outcome != "committed" || o.Steps[len(o.Steps)-1].State == "observed"
}
func (r *Repository) Operation(ctx context.Context, id string) (*Operation, error) {
	return readOperation(ctx, r.db, id)
}
func (r *Repository) LatestOperation(ctx context.Context) (*Operation, error) {
	var id string
	err := r.db.QueryRowContext(ctx, "select value from settings where key=?", latestOperationSetting).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o, err := r.Operation(ctx, id)
	if err == nil && o == nil {
		return nil, errors.New("missing latest key operation journal")
	}
	return o, err
}

func (r *Repository) BeginOperation(ctx context.Context, o Operation) error {
	if !validOperation(o) || o.Revision != 1 {
		return ErrConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var latest string
	err = tx.QueryRowContext(ctx, "select value from settings where key=?", latestOperationSetting).Scan(&latest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if latest != "" {
		previous, e := readOperation(ctx, tx, latest)
		if e != nil {
			return e
		}
		if previous == nil || previous.Outcome != "committed" {
			return ErrConflict
		}
	}
	data, _ := json.Marshal(o)
	if _, err = tx.ExecContext(ctx, "insert into settings(key,value,updated_at_ms) values(?,?,?)", operationPrefix+o.ID, string(data), time.Now().UnixMilli()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value,updated_at_ms=excluded.updated_at_ms", latestOperationSetting, o.ID, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SaveOperation is a journal CAS, never a claim of upstream CAS. The final
// identity transition and operation result commit in the same SQLite transaction.
func (r *Repository) SaveOperation(ctx context.Context, o Operation) error {
	if !validOperation(o) {
		return ErrConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, err := readOperation(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	if previous == nil || previous.Revision != o.Revision || previous.Outcome == "committed" {
		return ErrConflict
	}
	o.Revision++
	data, _ := json.Marshal(o)
	old, _ := json.Marshal(previous)
	result, err := tx.ExecContext(ctx, "update settings set value=?,updated_at_ms=? where key=? and value=?", string(data), time.Now().UnixMilli(), operationPrefix+o.ID, string(old))
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
	if o.Outcome == "committed" {
		result, err = tx.ExecContext(ctx, "update gateway_api_key_identities set lifecycle='superseded',revision=revision+1,updated_at_ms=? where id=? and lifecycle='active'", o.CompletedAtMS, o.TargetID)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
	}
	return tx.Commit()
}
