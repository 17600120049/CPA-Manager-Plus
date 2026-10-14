package sqlite

import (
	"context"
	"database/sql"
)

// RequiredMigrationReadiness is read-only persisted evidence, not an upgrade
// grant. Historical completion does not use the current v2 worker's revision.
type RequiredMigrationReadiness struct {
	Cache               []MigrationCheckpoint `json:"cache"`
	Quota               []MigrationCheckpoint `json:"quota"`
	CacheRevision       *string               `json:"cacheRevision"`
	CacheStagingRows    *int64                `json:"cacheStagingRows"`
	QuotaUnattachedRows *int64                `json:"quotaUnattachedRows"`
}

type MigrationCheckpoint struct {
	Status       string `json:"status"`
	LastID       int64  `json:"lastId"`
	TargetID     int64  `json:"targetId"`
	Processed    int64  `json:"processed"`
	Changed      int64  `json:"changed"`
	Applied      int64  `json:"applied"`
	FinishedAtMS *int64 `json:"finishedAtMs"`
	HasError     bool   `json:"hasError"`
}

// Exact v1.14.4 may initialize completed cache rows with NULL finished_at_ms.
// Do not repair that value or infer completion from a timestamp.
func (s *RequiredMigrationReadiness) CompletedV1144() bool {
	if s == nil || s.CacheRevision == nil || *s.CacheRevision != "2" ||
		s.CacheStagingRows == nil || *s.CacheStagingRows != 0 ||
		s.QuotaUnattachedRows == nil || *s.QuotaUnattachedRows != 0 ||
		len(s.Cache) != 1 || len(s.Quota) != 1 {
		return false
	}
	c, q := s.Cache[0], s.Quota[0]
	return c.Status == "completed" && q.Status == "completed" && !c.HasError && !q.HasError &&
		c.LastID >= 0 && c.TargetID >= 0 && c.LastID == c.TargetID &&
		c.Processed >= 0 && c.Changed >= 0 && c.Applied == c.Changed && c.Processed >= c.Changed &&
		q.LastID >= 0 && q.TargetID >= 0 && q.Processed >= 0 && q.Changed >= 0 && q.Applied >= 0
	// Quota's historical checkpoint can precede later, already attached API
	// snapshots. The authoritative incomplete test is unattached rows, not max(id).
}

func inspectMigrationReadiness(ctx context.Context, db *sql.DB, result *LegacyInspection) error {
	if !inspectionHasColumns(result, "usage_data_migrations", "name", "status", "last_event_id", "target_event_id", "processed_rows", "changed_rows", "applied_rows", "finished_at_ms", "last_error") {
		return nil
	}
	s := &RequiredMigrationReadiness{Cache: []MigrationCheckpoint{}, Quota: []MigrationCheckpoint{}}
	for _, item := range []struct {
		name string
		dest *[]MigrationCheckpoint
	}{
		{"usage_cache_accounting_v2", &s.Cache}, {"quota_snapshot_lifecycle_v1", &s.Quota},
	} {
		rows, err := db.QueryContext(ctx, `select case when typeof(status) = 'text'
			and status collate binary in ('completed','pending','discovering','running','applying','clearing','paused','failed','offline_required')
			then status else 'unknown' end, last_event_id, target_event_id,
			processed_rows, changed_rows, applied_rows, finished_at_ms,
			case when last_error is null or last_error = '' then 0 else 1 end
			from usage_data_migrations where name collate binary = ? limit 2`, item.name)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c MigrationCheckpoint
			if err := rows.Scan(&c.Status, &c.LastID, &c.TargetID, &c.Processed, &c.Changed, &c.Applied, &c.FinishedAtMS, &c.HasError); err != nil {
				rows.Close()
				return err
			}
			*item.dest = append(*item.dest, c)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if inspectionHasColumns(result, "settings", "key", "value") {
		var revision string
		err := db.QueryRowContext(ctx, `select case when typeof(value) = 'text' and value collate binary in ('1','2','3')
			then value else 'unknown' end from settings where key collate binary = 'usage_cache_accounting_semantics_revision'`).Scan(&revision)
		if err == nil {
			s.CacheRevision = &revision
		} else if err != sql.ErrNoRows {
			return err
		}
	}
	for _, item := range []struct {
		table, column, query string
		dest                 **int64
	}{
		{"usage_cache_accounting_v2_changes", "event_id", "select count(*) from usage_cache_accounting_v2_changes", &s.CacheStagingRows},
		{"account_quota_snapshots", "observation_id", "select count(*) from account_quota_snapshots where observation_id is null", &s.QuotaUnattachedRows},
	} {
		// Supplementary tables are not schema-family fingerprints. Inspect their
		// exact column before querying; absent evidence remains unknown.
		var exists bool
		if err := db.QueryRowContext(ctx, `select exists(select 1 from pragma_table_info(?) where name = ?)`, item.table, item.column).Scan(&exists); err != nil {
			return err
		}
		if exists {
			var count int64
			if err := db.QueryRowContext(ctx, item.query).Scan(&count); err != nil {
				return err
			}
			*item.dest = &count
		}
	}
	result.RequiredMigrationReadiness = s
	return nil
}
