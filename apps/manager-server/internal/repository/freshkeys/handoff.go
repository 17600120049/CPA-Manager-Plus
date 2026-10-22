package freshkeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
)

const h1Setting = "fresh_source_handoff_v2"

// Handoff is a bounded transport checkpoint in the existing Manager DB. It
// references, but never replaces or rewrites, immutable business terminals.
type Handoff struct {
	Version        int    `json:"version"`
	RunID          string `json:"runId"`
	SourceID       string `json:"sourceId"`
	Generation     string `json:"generation"`
	InitialDigest  string `json:"initialDigest"`
	Anchor         string `json:"anchor"`
	ParentID       string `json:"parentId"`
	IntentDigest   string `json:"intentDigest"`
	TerminalDigest string `json:"terminalDigest"`
	Receipts       string `json:"receipts"`
	Phase          string `json:"phase"`
}

func (r *Repository) Handoff(ctx context.Context) (*Handoff, error) {
	var raw string
	e := r.db.QueryRowContext(ctx, "select value from settings where key=?", h1Setting).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var h Handoff
	if json.Unmarshal([]byte(raw), &h) != nil || h.Version != 2 || h.RunID == "" || h.SourceID == "" || h.Generation == "" || len(h.InitialDigest) != 64 || len(h.Anchor) != 64 || (h.Phase != "free" && h.Phase != "held" && h.Phase != "terminal") || (h.Phase != "free" && (h.ParentID == "" || len(h.IntentDigest) != 64)) {
		return nil, ErrConflict
	}
	return &h, nil
}
func (r *Repository) SaveHandoff(ctx context.Context, old *Handoff, next Handoff) error {
	data, _ := json.Marshal(next)
	var result sql.Result
	var e error
	if old == nil {
		result, e = r.db.ExecContext(ctx, "insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do nothing", h1Setting, string(data), time.Now().UnixMilli())
	} else {
		before, _ := json.Marshal(old)
		result, e = r.db.ExecContext(ctx, "update settings set value=?,updated_at_ms=? where key=? and value=?", string(data), time.Now().UnixMilli(), h1Setting, string(before))
	}
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Both domains are checked before every claim. The Runtime claim remains the
// cross-process arbiter; this does not pretend SQLite and Runtime are atomic.
func (r *Repository) H1Idle(ctx context.Context) error {
	k, e := r.LatestOperation(ctx)
	if e != nil {
		return e
	}
	if k != nil && k.Outcome != "committed" {
		return ErrConflict
	}
	for _, entry := range []struct {
		key string
		valid func(*freshstore.RetryJournal) bool
	}{
		{"fresh_config_retry_v1", freshstore.ValidateRetryJournal},
		{"fresh_config_retry_interval_v1", freshstore.ValidateIntervalJournal},
	} {
		var raw string
		e = r.db.QueryRowContext(ctx, "select value from settings where key=?", entry.key).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) { continue }
		if e != nil { return e }
		var j freshstore.RetryJournal
		if json.Unmarshal([]byte(raw), &j) != nil || !entry.valid(&j) { return ErrConflict }
		for id, o := range j.Operations {
			if id != o.RequestID || (o.State != "persisted" && o.State != "recovered") { return ErrConflict }
		}
	}
	return nil
}
