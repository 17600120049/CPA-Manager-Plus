package freshstore

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func testStore(t *testing.T) (Store, *sql.DB, bootstrap.FreshIdentity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := New(db)
	id := bootstrap.FreshIdentity{AdmissionID: "admit", InstallationID: "install", Generation: "gen", DatabasePath: path, OperationID: "fresh-op"}
	if _, err = store.Create(t.Context(), id, nil); err != nil {
		t.Fatal(err)
	}
	return store, db, id
}

// h1FieldOperation builds a reserved H1 operation for one allowlisted field.
func h1FieldOperation(path string, value any) ConfigOperation {
	id := strings.Repeat("a", 32)
	return ConfigOperation{
		RequestID: id, Field: path, Value: value, SourceID: "isolated-h1-source",
		Generation: "7", Revision: 4, SHA256: strings.Repeat("b", 64), State: "unknown",
		ParentID: "c:" + id, ClaimRevision: 5, IntentDigest: strings.Repeat("c", 64), Attempted: true,
	}
}

func saveJournal(t *testing.T, store Store, id bootstrap.FreshIdentity, path string, operation ConfigOperation) Record {
	t.Helper()
	r, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r, err = store.Update(t.Context(), id, r.Operation.Revision, func(r *Record) error {
		if r.Config == nil {
			r.Config = map[string]*FieldJournal{}
		}
		r.Config[path] = &FieldJournal{Latest: operation.RequestID, Operations: map[string]ConfigOperation{operation.RequestID: operation}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestH1FieldEvidenceMonotonicAndTerminalSQLiteFailure(t *testing.T) {
	store, db, id := testStore(t)
	operation := h1FieldOperation("routing.retry.request-retry", 0)
	saveJournal(t, store, id, "routing.retry.request-retry", operation)
	update := func(next ConfigOperation) error {
		t.Helper()
		current, err := store.Load(t.Context())
		if err != nil {
			return err
		}
		_, err = store.Update(t.Context(), id, current.Operation.Revision, func(r *Record) error {
			r.Config["routing.retry.request-retry"].Operations[next.RequestID] = next
			return nil
		})
		return err
	}
	withHTTP := operation
	withHTTP.HTTP200 = true
	withHTTP.BeginRevision = 6
	if err := update(withHTTP); err != nil {
		t.Fatal("durable HTTP success refused", err)
	}
	// Terminal failure does not erase the durable HTTP evidence. Runtime's
	// receipt remains owned by the parent until this original terminal succeeds.
	_, err := db.Exec(`CREATE TRIGGER fail_terminal BEFORE UPDATE ON settings WHEN NEW.key='fresh_config_fields_v1' BEGIN SELECT RAISE(FAIL,'injected terminal failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	terminal := withHTTP
	terminal.State = "persisted"
	terminal.Receipt = strings.Repeat("d", 64)
	if err = update(terminal); err == nil {
		t.Fatal("injected failure ignored")
	}
	r, err := store.Load(t.Context())
	if err != nil || r.Config["routing.retry.request-retry"].Operations[operation.RequestID] != withHTTP {
		t.Fatal("HTTP evidence lost", err)
	}
	if _, err = db.Exec("DROP TRIGGER fail_terminal"); err != nil {
		t.Fatal(err)
	}
	bad := withHTTP
	bad.HTTP200 = false
	bad.BeginRevision = 0
	if update(bad) == nil {
		t.Fatal("HTTP evidence rolled back")
	}
	bad = withHTTP
	bad.Value = 1
	if update(bad) == nil {
		t.Fatal("original value changed")
	}
	if err = update(terminal); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ConfigOperation){
		func(v *ConfigOperation) { v.State = "unknown"; v.Receipt = "" },
		func(v *ConfigOperation) { v.Receipt = strings.Repeat("e", 64) },
		func(v *ConfigOperation) { v.Generation = "8" },
	} {
		bad = terminal
		change(&bad)
		if update(bad) == nil {
			t.Fatal("terminal rewritten")
		}
	}
}

func TestDamagedFieldJournalLocksOnlyThatField(t *testing.T) {
	store, db, id := testStore(t)
	good := h1FieldOperation("routing.retry.request-retry", 0)
	other := h1FieldOperation("requests.passthrough-headers", true)
	saveJournal(t, store, id, "routing.retry.request-retry", good)
	saveJournal(t, store, id, "requests.passthrough-headers", other)

	var raw string
	if err := db.QueryRow("select value from settings where key=?", configJournalKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	fields := doc["fields"].(map[string]any)
	/* corrupt exactly one field journal: wrong scalar type for its allowlist */
	broken := fields["requests.passthrough-headers"].(map[string]any)
	operations := broken["operations"].(map[string]any)
	operations[other.RequestID].(map[string]any)["value"] = "not-a-bool"
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("update settings set value=? where key=?", string(data), configJournalKey); err != nil {
		t.Fatal(err)
	}
	r, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !r.ConfigInvalid["requests.passthrough-headers"] {
		t.Fatal("corruption was not isolated to its field")
	}
	if r.ConfigInvalid["routing.retry.request-retry"] || r.Config["routing.retry.request-retry"] == nil {
		t.Fatal("healthy field journal was locked")
	}
	// A damaged field cannot be erased or repaired through a CAS callback, and an
	// unrelated update still works.
	if _, err = store.Update(t.Context(), id, r.Operation.Revision, func(r *Record) error {
		r.Config["requests.passthrough-headers"] = &FieldJournal{Latest: other.RequestID, Operations: map[string]ConfigOperation{other.RequestID: other}}
		return nil
	}); err != ErrConflict {
		t.Fatal("corruption repaired", err)
	}
	if r, err = store.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(t.Context(), id, r.Operation.Revision, func(r *Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = db.QueryRow("select value from settings where key=?", configJournalKey).Scan(&after); err != nil || after != string(data) {
		t.Fatal("damaged bytes overwritten", err)
	}
}

func TestCorruptJournalContainerRejectsTheRecord(t *testing.T) {
	_, db, _ := testStore(t)
	if _, err := db.Exec("insert into settings(key,value,updated_at_ms) values(?,?,0)", configJournalKey, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db).Load(t.Context()); err != ErrNotFresh {
		t.Fatal("corrupt container admitted", err)
	}
}

func TestLegacyRetryJournalsMigratePerField(t *testing.T) {
	store, db, _ := testStore(t)
	retry := h1FieldOperation("routing.retry.request-retry", 3)
	retry.Field = ""
	interval := h1FieldOperation("routing.retry.max-retry-interval", 120)
	interval.Field = "max-retry-interval"
	for key, journal := range map[string]*FieldJournal{
		legacyRetryKey:    {Latest: retry.RequestID, Operations: map[string]ConfigOperation{retry.RequestID: retry}},
		legacyIntervalKey: {Latest: interval.RequestID, Operations: map[string]ConfigOperation{interval.RequestID: interval}},
	} {
		data, err := json.Marshal(journal)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("insert into settings(key,value,updated_at_ms) values(?,?,0)", key, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ConfigInvalid) != 0 {
		t.Fatal("legacy journals rejected", r.ConfigInvalid)
	}
	if got := r.Config["routing.retry.request-retry"]; got == nil || got.Operations[retry.RequestID].Field != "routing.retry.request-retry" || got.Operations[retry.RequestID].Value != 3 {
		t.Fatal("legacy request-retry journal not migrated", got)
	}
	if got := r.Config["routing.retry.max-retry-interval"]; got == nil || got.Operations[interval.RequestID].Field != "routing.retry.max-retry-interval" {
		t.Fatal("legacy interval journal not migrated", got)
	}
	// The first update persists the migrated container and never rewrites the
	// legacy rows, so an old Manager can still read them.
	header := h1FieldOperation("requests.passthrough-headers", true)
	if _, err = store.Update(t.Context(), r.Operation.Identity, r.Operation.Revision, func(record *Record) error {
		record.Config["requests.passthrough-headers"] = &FieldJournal{Latest: header.RequestID, Operations: map[string]ConfigOperation{header.RequestID: header}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var migrated string
	if err = db.QueryRow("select value from settings where key=?", configJournalKey).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(migrated, "routing.retry.max-retry-interval") {
		t.Fatal("migration not persisted", migrated)
	}
}

// configJournalFields is the raw per-field map of the current container.
func configJournalFields(t *testing.T, db *sql.DB) map[string]json.RawMessage {
	t.Helper()
	var raw string
	if err := db.QueryRow("select value from settings where key=?", configJournalKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Fields
}

// writeConfigJournalFields replaces the container with exactly these field bytes.
func writeConfigJournalFields(t *testing.T, db *sql.DB, fields map[string]json.RawMessage) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("insert into settings(key,value,updated_at_ms) values(?,?,0) on conflict(key) do update set value=excluded.value", configJournalKey, string(data)); err != nil {
		t.Fatal(err)
	}
}

// TestUndecodableFieldEntryLocksOnlyItsField covers a field entry that does not
// even decode as a journal: it must lock only that field, never the record.
func TestUndecodableFieldEntryLocksOnlyItsField(t *testing.T) {
	store, db, id := testStore(t)
	good := h1FieldOperation("routing.retry.request-retry", 0)
	saveJournal(t, store, id, "routing.retry.request-retry", good)
	fields := configJournalFields(t, db)
	fields["requests.passthrough-headers"] = json.RawMessage(`{"latest":5}`)
	writeConfigJournalFields(t, db, fields)
	r, err := store.Load(t.Context())
	if err != nil {
		t.Fatalf("one undecodable field failed the whole record: %v", err)
	}
	if !r.ConfigInvalid["requests.passthrough-headers"] {
		t.Fatal("the undecodable field was not locked", r.ConfigInvalid)
	}
	if r.ConfigInvalid["routing.retry.request-retry"] || r.Config["routing.retry.request-retry"] == nil {
		t.Fatal("a healthy field was locked with it", r.ConfigInvalid)
	}
	// The damaged entry is not a journal, so it can never be read or rewritten.
	if _, err = store.Update(t.Context(), id, r.Operation.Revision, func(v *Record) error {
		v.Config["requests.passthrough-headers"] = &FieldJournal{Latest: good.RequestID, Operations: map[string]ConfigOperation{good.RequestID: good}}
		return nil
	}); err != ErrConflict {
		t.Fatal("a damaged field entry was repaired", err)
	}
}

// TestDamagedFieldBytesSurviveAnotherFieldWrite is the "never discard or replay"
// rule: saving a healthy field rewrites the container, and the damaged field's
// bytes must be written back exactly as they were found.
func TestDamagedFieldBytesSurviveAnotherFieldWrite(t *testing.T) {
	store, db, id := testStore(t)
	good := h1FieldOperation("routing.retry.request-retry", 0)
	saveJournal(t, store, id, "routing.retry.request-retry", good)
	damaged := json.RawMessage(`{"latest":5}`)
	fields := configJournalFields(t, db)
	fields["requests.passthrough-headers"] = damaged
	writeConfigJournalFields(t, db, fields)
	r, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !r.ConfigInvalid["requests.passthrough-headers"] {
		t.Fatal("damaged field not isolated", r.ConfigInvalid)
	}
	// Another field's save rewrites the container.
	other := h1FieldOperation("requests.streaming.keepalive-seconds", 30)
	if _, err = store.Update(t.Context(), id, r.Operation.Revision, func(v *Record) error {
		v.Config["requests.streaming.keepalive-seconds"] = &FieldJournal{Latest: other.RequestID, Operations: map[string]ConfigOperation{other.RequestID: other}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := configJournalFields(t, db)
	if string(after["requests.passthrough-headers"]) != string(damaged) {
		t.Fatalf("damaged bytes were rewritten: %s", after["requests.passthrough-headers"])
	}
	if j, ok := after["requests.streaming.keepalive-seconds"]; !ok || !strings.Contains(string(j), other.RequestID) {
		t.Fatal("the healthy save was not written", string(j))
	}
	if before, ok := after["routing.retry.request-retry"]; !ok || !strings.Contains(string(before), good.RequestID) {
		t.Fatal("the earlier healthy field was dropped", string(before))
	}
	// The damaged field is still locked and the others still work.
	if r, err = store.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !r.ConfigInvalid["requests.passthrough-headers"] || r.Config["requests.streaming.keepalive-seconds"] == nil {
		t.Fatal("isolation lost after the save", r.ConfigInvalid)
	}
}
