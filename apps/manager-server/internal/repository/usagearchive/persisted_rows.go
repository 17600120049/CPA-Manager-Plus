package usagearchive

import (
	"context"
	"database/sql"
	"fmt"
)

const archiveRunsSelectSQL = `select
	id, mode, schema_version, format, status, resume_status, requested_stage,
	progress_phase, progress_current, progress_total, progress_unit, progress_updated_at_ms, cutoff_timestamp_ms,
	target_event_id, event_count, estimated_bytes, last_archived_event_id,
	archived_event_count, archived_uncompressed_bytes, archived_compressed_bytes,
	archive_digest, manifest_file, manifest_sha256, last_deleted_event_id,
	deleted_event_count, created_at_ms, updated_at_ms, started_at_ms, archived_at_ms,
	verified_at_ms, delete_started_at_ms, completed_at_ms, last_error
from usage_archive_runs`

const archiveSegmentsSelectSQL = `select
	run_id, sequence, status, file_name, first_event_id, last_event_id,
	min_timestamp_ms, max_timestamp_ms, event_count, uncompressed_bytes,
	compressed_bytes, content_sha256, event_hash_digest, created_at_ms, verified_at_ms
from usage_archive_segments`

// ValidatePersistedRows streams every run and segment through the same SQL and
// scanners as the production readers. It checks decoding, not history coverage.
func (r *Repository) ValidatePersistedRows(ctx context.Context) error {
	for _, query := range []struct {
		name, sql string
		scan      func(*sql.Rows) error
	}{
		{"run", archiveRunsSelectSQL, func(rows *sql.Rows) error {
			_, err := scanRun(rows)
			return err
		}},
		{"segment", archiveSegmentsSelectSQL, func(rows *sql.Rows) error {
			_, err := scanSegment(rows)
			return err
		}},
	} {
		rows, err := r.db.QueryContext(ctx, query.sql)
		if err != nil {
			return fmt.Errorf("query persisted archive %s rows: %w", query.name, err)
		}
		for rows.Next() {
			if err := query.scan(rows); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan persisted archive %s row: %w", query.name, err)
			}
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return fmt.Errorf("read persisted archive %s rows: %w", query.name, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close persisted archive %s rows: %w", query.name, closeErr)
		}
	}
	return ctx.Err()
}
