package freshstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
)

// configJournalKey is the single settings row holding every per-field journal.
// One row keeps the store's key space fixed while entries stay independent:
// each field is validated, locked and rewritten on its own.
const configJournalKey = "fresh_config_fields_v1"

// Pre-allowlist journals. They are read once and migrated in memory into
// configJournalKey; the legacy rows are never written again.
const legacyRetryKey = "fresh_config_retry_v1"
const legacyIntervalKey = "fresh_config_retry_interval_v1"

var legacyJournalPaths = []struct {
	key  string
	path string
}{
	{legacyRetryKey, "routing.retry.request-retry"},
	{legacyIntervalKey, "routing.retry.max-retry-interval"},
}

// ConfigJournalSettingKey is the settings row an admission checkpoint reads.
func ConfigJournalSettingKey() string { return configJournalKey }

type configJournalDoc struct {
	Fields map[string]*FieldJournal `json:"fields"`
}

// loadConfigJournals returns the per-field journals plus the fields whose bytes
// are damaged. Damage is attributed per field so one corrupt journal locks only
// its own field; a corrupt container cannot be attributed and is reported
// separately by the caller.
func loadConfigJournals(ctx context.Context, q queryer) (map[string]*FieldJournal, map[string]bool, error) {
	journals := map[string]*FieldJournal{}
	invalid := map[string]bool{}
	var data string
	err := q.QueryRowContext(ctx, "select value from settings where key=?", configJournalKey).Scan(&data)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, nil, err
	default:
		var doc configJournalDoc
		if json.Unmarshal([]byte(data), &doc) != nil {
			return nil, nil, ErrNotFresh
		}
		for path, journal := range doc.Fields {
			normalizeFieldJournal(journal)
			if !validFieldJournal(path, journal) {
				invalid[path] = true
				continue
			}
			journals[path] = journal
		}
	}
	for _, legacy := range legacyJournalPaths {
		if _, migrated := journals[legacy.path]; migrated || invalid[legacy.path] {
			continue
		}
		var raw string
		err := q.QueryRowContext(ctx, "select value from settings where key=?", legacy.key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		var journal FieldJournal
		if json.Unmarshal([]byte(raw), &journal) != nil {
			invalid[legacy.path] = true
			continue
		}
		// The legacy journal predates the allowlist: it had no Field column and
		// stored either an int scalar or an explicit interval marker.
		for id, operation := range journal.Operations {
			operation.Field = legacy.path
			journal.Operations[id] = operation
		}
		normalizeFieldJournal(&journal)
		if !validFieldJournal(legacy.path, &journal) {
			invalid[legacy.path] = true
			continue
		}
		journals[legacy.path] = &journal
	}
	return journals, invalid, nil
}

// normalizeFieldJournal converts JSON numbers back into the exact scalar types
// the allowlist uses, so an in-memory comparison never depends on float64.
func normalizeFieldJournal(journal *FieldJournal) {
	if journal == nil {
		return
	}
	for id, operation := range journal.Operations {
		if number, ok := operationInt(operation.Value); ok {
			operation.Value = number
			journal.Operations[id] = operation
		}
	}
}

func operationInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		return int(number), float64(int(number)) == number
	default:
		return 0, false
	}
}

func hashHex(value string, size int) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == size && hex.EncodeToString(b) == value
}

// validFieldJournal is the structural contract for one field's journal. Scalar
// range and type are owned by the allowlist that services the field; the store
// only guarantees that a well-formed scalar and coherent H1 evidence are stored.
func validFieldJournal(path string, journal *FieldJournal) bool {
	if journal == nil {
		return false
	}
	if _, ok := journal.Operations[journal.Latest]; !ok {
		return false
	}
	for id, operation := range journal.Operations {
		if !hashHex(id, 16) || operation.RequestID != id || operation.Field != path ||
			len(operation.SourceID) < 16 || operation.Generation == "" || operation.Revision == 0 ||
			!hashHex(operation.SHA256, 32) {
			return false
		}
		switch operation.Value.(type) {
		case int, bool:
		default:
			return false
		}
		switch operation.State {
		case "unknown":
			if operation.Receipt != "" {
				return false
			}
		case "persisted", "recovered":
		default:
			return false
		}
		if !operation.HTTP200 && (operation.BeginRevision != 0 || operation.Receipt != "") {
			return false
		}
		if operation.ParentID == "" {
			if operation.Attempted || operation.ClaimRevision != 0 || operation.IntentDigest != "" || operation.HTTP200 {
				return false
			}
			continue
		}
		if operation.ParentID != "c:"+id || !operation.Attempted || operation.ClaimRevision <= operation.Revision ||
			!hashHex(operation.IntentDigest, 32) {
			return false
		}
		if operation.HTTP200 && operation.BeginRevision <= operation.ClaimRevision {
			return false
		}
		if operation.State != "unknown" && len(operation.Receipt) != 0 && !hashHex(operation.Receipt, 32) {
			return false
		}
	}
	return true
}

// ValidateConfigJournals is the shared admission-checkpoint view: malformed
// container bytes or any field whose original operation is still unknown blocks
// a new claim. A damaged field entry is skipped here; the owning repository has
// already isolated it to that field.
func ValidateConfigJournals(data []byte) error {
	var doc configJournalDoc
	if json.Unmarshal(data, &doc) != nil {
		return ErrConflict
	}
	for path, journal := range doc.Fields {
		normalizeFieldJournal(journal)
		if !validFieldJournal(path, journal) {
			continue
		}
		if journal.Operations[journal.Latest].State == "unknown" {
			return ErrConflict
		}
	}
	return nil
}

// validateConfigJournalUpdate enforces identity, monotonic H1 evidence and
// terminal immutability. A CAS callback may add an operation and advance an
// original operation; it can never erase, rewrite or replay one.
func validateConfigJournalUpdate(before, after map[string]*FieldJournal) error {
	for path, oldJournal := range before {
		if oldJournal == nil {
			continue
		}
		nextJournal := after[path]
		if nextJournal == nil {
			return ErrConflict
		}
		for id, old := range oldJournal.Operations {
			next, ok := nextJournal.Operations[id]
			if !ok {
				return ErrConflict
			}
			if err := validateConfigOperationUpdate(old, next); err != nil {
				return err
			}
		}
		if oldJournal.Latest != "" {
			if _, ok := nextJournal.Operations[oldJournal.Latest]; !ok {
				return ErrConflict
			}
			if oldJournal.Operations[oldJournal.Latest].State == "unknown" && nextJournal.Latest != oldJournal.Latest {
				return ErrConflict
			}
		}
	}
	return nil
}

func validateConfigOperationUpdate(old, next ConfigOperation) error {
	if next.RequestID != old.RequestID || next.Field != old.Field || next.ParentID != old.ParentID ||
		next.ClaimRevision != old.ClaimRevision || next.IntentDigest != old.IntentDigest ||
		next.Attempted != old.Attempted || next.SourceID != old.SourceID ||
		next.Generation != old.Generation || next.Revision != old.Revision || next.SHA256 != old.SHA256 ||
		!reflect.DeepEqual(next.Value, old.Value) {
		return ErrConflict
	}
	if next.BeginRevision != old.BeginRevision {
		if old.BeginRevision != 0 || !next.HTTP200 || next.BeginRevision <= old.ClaimRevision {
			return ErrConflict
		}
	}
	if old.HTTP200 && !next.HTTP200 {
		return ErrConflict
	}
	if next.Receipt != old.Receipt {
		if old.Receipt != "" || !next.HTTP200 || !hashHex(next.Receipt, 32) {
			return ErrConflict
		}
	}
	if next.State == old.State {
		return nil
	}
	if old.State != "unknown" || (next.State != "persisted" && next.State != "recovered") {
		return ErrConflict
	}
	if old.ParentID != "" {
		if !next.HTTP200 || !hashHex(next.Receipt, 32) {
			return ErrConflict
		}
		return nil
	}
	if next.HTTP200 || next.Receipt != "" {
		return ErrConflict
	}
	return nil
}

// writeConfigJournals persists the whole container only when it changed.
func writeConfigJournals(ctx context.Context, tx *sql.Tx, before, after map[string]*FieldJournal) error {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	return write(ctx, tx, configJournalKey, configJournalDoc{Fields: after})
}

// cloneConfigJournals deep-copies the journal map so a CAS callback can never
// alias the before-image it is validated against.
func cloneConfigJournals(source map[string]*FieldJournal) map[string]*FieldJournal {
	if source == nil {
		return map[string]*FieldJournal{}
	}
	out := make(map[string]*FieldJournal, len(source))
	for path, journal := range source {
		if journal == nil {
			out[path] = nil
			continue
		}
		copied := &FieldJournal{Latest: journal.Latest, Operations: make(map[string]ConfigOperation, len(journal.Operations))}
		for id, operation := range journal.Operations {
			copied.Operations[id] = operation
		}
		out[path] = copied
	}
	return out
}
