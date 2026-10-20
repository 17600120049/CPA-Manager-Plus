package freshkeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const initialInstallSetting = "fresh_s1_initial_key_install_v1"

// A one-shot mutation journal, not a second secret or Key identity authority.
// It is committed before any network write. Unknown attempts are never removed.
type InitialInstall struct {
	KeyID              string `json:"keyId"`
	RunID              string `json:"runId"`
	Generation         string `json:"generation"`
	AttemptedAtMS      int64  `json:"attemptedAtMs"`
	ProviderSeedSHA256 string `json:"providerSeedSHA256,omitempty"`
}

func (r *Repository) ClaimInitialInstall(ctx context.Context, v InitialInstall) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do nothing`, initialInstallSetting, string(data), time.Now().UnixMilli())
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

func (r *Repository) InitialInstall(ctx context.Context) (*InitialInstall, error) {
	var data string
	err := r.db.QueryRowContext(ctx, `select value from settings where key=?`, initialInstallSetting).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v InitialInstall
	if json.Unmarshal([]byte(data), &v) != nil || v.KeyID == "" || v.RunID == "" || v.Generation == "" || v.AttemptedAtMS <= 0 {
		return nil, errors.New("invalid S1 mutation journal")
	}
	return &v, nil
}
