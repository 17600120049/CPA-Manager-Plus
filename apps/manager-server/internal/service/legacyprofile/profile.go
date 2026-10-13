package legacyprofile

import (
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

// Profile is an inspected capability shape, never a release or upgrade grant.
type Profile struct {
	Status  string   `json:"status"`
	Family  string   `json:"family,omitempty"`
	Reasons []string `json:"reasons"`
}

const MetadataColumnSpecs = "response_model:text:0 session_id:text:0 parent_session_id:text:0 access_token_sha256:text:0 generate:integer:0 stream:integer:0"

var legacyMetadataColumns = strings.Fields(MetadataColumnSpecs)

// Classify is pure: it cannot open SQLite, touch source assets,
// read version strings, create backup receipts or alter startup admission.
func Classify(source sqlite.LegacyInspection) Profile {
	result := Profile{Status: "unknown", Reasons: []string{"source-not-safely-inspected"}}
	if source.SQLite != "observed" || len(source.Issues) != 0 {
		return result
	}
	switch source.Consistency {
	case "operator-asserted-cold", "manager-fenced-cold", "manager-fenced-recovery-copy":
	default:
		return result
	}
	tables := make(map[string]sqlite.InspectionTable, len(source.Tables))
	for _, table := range source.Tables {
		if _, exists := tables[table.Name]; exists {
			return Profile{Status: "conflicting", Reasons: []string{"duplicate-inspected-table"}}
		}
		tables[table.Name] = table
		if strings.HasPrefix(table.Name, "gateway_") {
			return Profile{Status: "conflicting", Reasons: []string{"partial-native-v2-schema"}}
		}
	}
	if _, settings := tables["settings"]; !settings {
		if _, events := tables["usage_events"]; !events {
			return Profile{Status: "unknown", Reasons: []string{"legacy-base-not-observed"}}
		}
	}
	metadataCount, archiveCount := 0, 0
	for _, col := range tables["usage_events"].Columns {
		for _, spec := range legacyMetadataColumns {
			if col.Name == strings.SplitN(spec, ":", 2)[0] {
				metadataCount++
			}
		}
	}
	for name := range legacyArchiveShapes {
		if _, exists := tables[name]; exists {
			archiveCount++
		}
	}
	if archiveCount != 0 && metadataCount != len(legacyMetadataColumns) {
		return Profile{Status: "conflicting", Reasons: []string{"archive-without-request-metadata"}}
	}
	if metadataCount != 0 && metadataCount != len(legacyMetadataColumns) {
		return Profile{Status: "partial", Reasons: []string{"request-metadata-incomplete"}}
	}
	if archiveCount != 0 && archiveCount != len(legacyArchiveShapes) {
		return Profile{Status: "partial", Reasons: []string{"archive-schema-incomplete"}}
	}
	expected := make(map[string]string, len(legacyCommonShapes)+len(legacyArchiveShapes))
	for name, shape := range legacyCommonShapes {
		expected[name] = shape
	}
	family := "LegacySchemaA"
	if metadataCount == len(legacyMetadataColumns) {
		family = "LegacySchemaB"
		expected["usage_events"] += " " + strings.Join(legacyMetadataColumns, " ")
	}
	if archiveCount == len(legacyArchiveShapes) {
		family = "LegacySchemaC"
		for name, shape := range legacyArchiveShapes {
			expected[name] = shape
		}
	}
	// Fixed ordering keeps reasons deterministic regardless of map iteration.
	for _, name := range []string{"settings", "usage_events", "usage_data_migrations", "usage_archive_runs", "usage_archive_segments", "usage_archive_event_refs", "usage_archive_deleted_coverage_daily", "usage_maintenance_locks"} {
		shape, required := expected[name]
		if !required {
			continue
		}
		table, exists := tables[name]
		if !exists {
			return Profile{Status: "partial", Reasons: []string{"legacy-required-table-missing"}}
		}
		state := matchLegacyTable(table, strings.Fields(shape))
		if state != "" {
			return Profile{Status: state, Reasons: []string{"legacy-required-shape-" + state}}
		}
	}
	return Profile{Status: "recognized", Family: family, Reasons: []string{"inspected-capability-shape-only"}}
}

func matchLegacyTable(table sqlite.InspectionTable, shape []string) string {
	columns := make(map[string]sqlite.InspectionColumn, len(table.Columns))
	for _, column := range table.Columns {
		if _, exists := columns[column.Name]; exists {
			return "conflicting"
		}
		columns[column.Name] = column
	}
	for _, spec := range shape {
		parts := strings.SplitN(spec, ":", 3)
		column, exists := columns[parts[0]]
		if !exists {
			return "partial"
		}
		pk, _ := strconv.Atoi(parts[2])
		if !strings.EqualFold(column.Type, parts[1]) || column.PrimaryKey != pk {
			return "conflicting"
		}
	}
	// Unreviewed columns cannot silently extend a known legacy family.
	if len(columns) != len(shape) {
		return "unknown"
	}
	return ""
}

// Fixed v1.12.12 common shape and v1.14.4 archive additions. Provenance and
// independent historical DDL fixtures live in testdata/legacy-profiles.
var legacyCommonShapes = map[string]string{
	"settings":              "key:text:1 value:text:0 updated_at_ms:integer:0",
	"usage_events":          "id:integer:1 request_id:text:0 event_hash:text:0 timestamp_ms:integer:0 timestamp:text:0 provider:text:0 executor_type:text:0 model:text:0 endpoint:text:0 method:text:0 path:text:0 client_ip:text:0 x_forwarded_for:text:0 user_agent:text:0 auth_type:text:0 auth_index:text:0 source:text:0 source_hash:text:0 api_key_hash:text:0 account_snapshot:text:0 auth_label_snapshot:text:0 auth_file_snapshot:text:0 auth_provider_snapshot:text:0 auth_account_id_snapshot:text:0 auth_project_id_snapshot:text:0 auth_snapshot_at_ms:integer:0 requested_model:text:0 resolved_model:text:0 reasoning_effort:text:0 service_tier:text:0 request_service_tier:text:0 response_service_tier:text:0 cache_input_mode:text:0 input_tokens:integer:0 output_tokens:integer:0 reasoning_tokens:integer:0 cached_tokens:integer:0 cache_tokens:integer:0 cache_read_tokens:integer:0 cache_creation_tokens:integer:0 normalized_uncached_input_tokens:integer:0 normalized_total_input_tokens:integer:0 normalized_cache_read_tokens:integer:0 normalized_cache_creation_tokens:integer:0 total_tokens:integer:0 latency_ms:integer:0 ttft_ms:integer:0 failed:integer:0 fail_status_code:integer:0 fail_summary:text:0 response_metadata_json:text:0 header_quota_recover_at_ms:integer:0 header_quota_used_percent:real:0 header_quota_plan_type:text:0 header_error_kind:text:0 header_error_code:text:0 header_trace_id:text:0 fail_body:text:0 raw_json:text:0 created_at_ms:integer:0",
	"usage_data_migrations": "name:text:1 status:text:0 last_event_id:integer:0 target_event_id:integer:0 processed_rows:integer:0 changed_rows:integer:0 applied_rows:integer:0 started_at_ms:integer:0 updated_at_ms:integer:0 finished_at_ms:integer:0 last_error:text:0",
}

var legacyArchiveShapes = map[string]string{
	"usage_archive_runs":                   "id:text:1 mode:text:0 schema_version:integer:0 format:text:0 status:text:0 resume_status:text:0 requested_stage:text:0 progress_phase:text:0 progress_current:integer:0 progress_total:integer:0 progress_unit:text:0 progress_updated_at_ms:integer:0 cutoff_timestamp_ms:integer:0 target_event_id:integer:0 event_count:integer:0 estimated_bytes:integer:0 last_archived_event_id:integer:0 archived_event_count:integer:0 archived_uncompressed_bytes:integer:0 archived_compressed_bytes:integer:0 archive_digest:text:0 manifest_file:text:0 manifest_sha256:text:0 last_deleted_event_id:integer:0 deleted_event_count:integer:0 created_at_ms:integer:0 updated_at_ms:integer:0 started_at_ms:integer:0 archived_at_ms:integer:0 verified_at_ms:integer:0 delete_started_at_ms:integer:0 completed_at_ms:integer:0 last_error:text:0",
	"usage_archive_segments":               "run_id:text:1 sequence:integer:2 status:text:0 file_name:text:0 first_event_id:integer:0 last_event_id:integer:0 min_timestamp_ms:integer:0 max_timestamp_ms:integer:0 event_count:integer:0 uncompressed_bytes:integer:0 compressed_bytes:integer:0 content_sha256:text:0 event_hash_digest:text:0 created_at_ms:integer:0 verified_at_ms:integer:0",
	"usage_archive_event_refs":             "event_hash:text:1 run_id:text:0 segment_sequence:integer:0 raw_event_id:integer:0 timestamp_ms:integer:0 archived_at_ms:integer:0 raw_deleted_at_ms:integer:0",
	"usage_archive_deleted_coverage_daily": "utc_day:integer:1 deleted_event_count:integer:0 min_timestamp_ms:integer:0 max_timestamp_ms:integer:0",
	"usage_maintenance_locks":              "name:text:1 run_id:text:0 operation:text:0 acquired_at_ms:integer:0 updated_at_ms:integer:0",
}
