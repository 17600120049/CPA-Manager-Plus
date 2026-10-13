package usagearchive

import (
	"context"
	"fmt"
	"strings"
)

// These statements are shared with Records and RecordSegment so offline schema
// qualification compiles the production data path rather than a column list.
const archiveRecordsSQL = `select
	e.id,
	e.event_hash,
	e.timestamp_ms,
	` + archiveRecordExpression + ` as payload
from usage_events e
where e.id > ? and e.id <= ? and e.timestamp_ms < ?
	and not exists (
		select 1 from usage_archive_event_refs archived
		where archived.event_hash = e.event_hash
	)
order by e.id asc
limit ?`

const recordSegmentLedgerInsertSQL = `insert or ignore into usage_event_identity_ledger (
	event_hash, raw_event_id, timestamp_ms, bucket_ms, aggregate_schema_version,
	first_seen_at_ms, updated_at_ms
)
select
	e.event_hash,
	e.id,
	e.timestamp_ms,
	e.timestamp_ms - (e.timestamp_ms % 3600000),
	0,
	case when e.created_at_ms > 0 then e.created_at_ms else ? end,
	?
from usage_events e
where e.id = ? and e.event_hash = ?`

const recordSegmentLedgerUpdateSQL = `update usage_event_identity_ledger set
	raw_event_id = ?,
	timestamp_ms = (select timestamp_ms from usage_events where id = ? and event_hash = ?),
	bucket_ms = (select timestamp_ms - (timestamp_ms % 3600000)
		from usage_events where id = ? and event_hash = ?),
	updated_at_ms = ?
where event_hash = ? and (raw_event_id is null or raw_event_id = ?)`

const recordSegmentRefsInsertSQL = `insert into usage_archive_event_refs (
	event_hash, run_id, segment_sequence, raw_event_id, timestamp_ms,
	archived_at_ms, raw_deleted_at_ms
)
select event_hash, ?, ?, raw_event_id, timestamp_ms, ?, null
from usage_event_identity_ledger
where event_hash = ? and raw_event_id = ?`

// ValidateDataPlaneSchema compiles the archive records/ref/ledger statements.
// EXPLAIN returns bytecode without reading event payloads or executing writes;
// this does not qualify coverage, ledger contents, or historical compatibility.
func (r *Repository) ValidateDataPlaneSchema(ctx context.Context) error {
	for _, query := range []struct{ name, sql string }{
		{"records", archiveRecordsSQL},
		{"identity ledger insert", recordSegmentLedgerInsertSQL},
		{"identity ledger update", recordSegmentLedgerUpdateSQL},
		{"event refs insert", recordSegmentRefsInsertSQL},
	} {
		// The shared statements use only anonymous positional placeholders.
		args := make([]any, strings.Count(query.sql, "?"))
		rows, err := r.db.QueryContext(ctx, "EXPLAIN "+query.sql, args...)
		if err != nil {
			return fmt.Errorf("compile archive %s: %w", query.name, err)
		}
		for rows.Next() {
			// Drain only EXPLAIN bytecode, never the underlying query results.
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return fmt.Errorf("read archive %s compilation: %w", query.name, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close archive %s compilation: %w", query.name, closeErr)
		}
	}
	return ctx.Err()
}
