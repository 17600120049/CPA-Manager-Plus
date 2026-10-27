package freshstore

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func journalStore(t *testing.T) (Store, *sql.DB, bootstrap.FreshIdentity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := New(db)
	id := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: "installation-gateway-01", Generation: "gen", OperationID: "fresh-operation", DatabasePath: path}
	credential, err := security.NewAdminCredential("cpamp_GatewayAdmin0123456789!", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Create(t.Context(), id, &credential); err != nil {
		t.Fatal(err)
	}
	return store, db, id
}

func groupOperation(requestID, checksum string) ConfigGroupOperation {
	return ConfigGroupOperation{
		RequestID: requestID, Group: "retry", Target: "group", Prefix: "routing.retry",
		Baseline: checksum, State: "unknown", SourceID: "installation-gateway-01", Generation: "7",
		Edits: []ConfigGroupEdit{{Path: "routing.retry.request-retry", Action: "set", Value: 3}},
	}
}

// TestGroupJournalRoundTripsBesideLegacyFieldEntries proves both journals share
// one container without either side losing anything: a pre-cutover field entry
// stays byte-readable while the page records its own groups.
func TestGroupJournalRoundTripsBesideLegacyFieldEntries(t *testing.T) {
	store, _, id := journalStore(t)
	checksum := strings.Repeat("a", 64)
	requestID := strings.Repeat("ab", 16)
	fieldID := strings.Repeat("cd", 16)
	if _, err := store.Update(t.Context(), id, 1, func(v *Record) error {
		v.Config["routing.retry.max-retry-interval"] = &FieldJournal{
			Latest: fieldID,
			Operations: map[string]ConfigOperation{
				fieldID: {RequestID: fieldID, Field: "routing.retry.max-retry-interval", Value: 30,
					SourceID: "installation-gateway-01", Generation: "7", Revision: 1, State: "persisted", HTTP200: true},
			},
		}
		v.ConfigGroups["retry"] = &ConfigGroupJournal{
			Latest:     requestID,
			Operations: map[string]ConfigGroupOperation{requestID: groupOperation(requestID, checksum)},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ConfigGroupsInvalid {
		t.Fatal("healthy group journal reported as damaged")
	}
	if reloaded.Config["routing.retry.max-retry-interval"] == nil {
		t.Fatal("legacy field entry lost")
	}
	entry := reloaded.ConfigGroups["retry"]
	if entry == nil || entry.Latest != requestID || entry.Operations[requestID].State != "unknown" {
		t.Fatalf("group journal %+v", entry)
	}
	// An unknown original stays the named latest.
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		v.ConfigGroups["retry"].Latest = ""
		return nil
	}); err == nil {
		t.Fatal("unknown original detached from latest")
	}
	reloaded, err = store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The original is resolved only with its acknowledgement evidence.
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		operation := v.ConfigGroups["retry"].Operations[requestID]
		operation.HTTP200 = true
		operation.State = "persisted"
		v.ConfigGroups["retry"].Operations[requestID] = operation
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err = store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ConfigGroups["retry"].Operations[requestID].State != "persisted" {
		t.Fatal("resolution not durable")
	}
	// A resolved original is terminal: nothing may rewrite it.
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		operation := v.ConfigGroups["retry"].Operations[requestID]
		operation.Edits[0].Value = 4
		v.ConfigGroups["retry"].Operations[requestID] = operation
		return nil
	}); err == nil {
		t.Fatal("resolved operation rewritten")
	}
}

// TestDamagedGroupJournalLocksThePageOnly proves an entry this store cannot
// prove never makes the legacy field entries unreadable.
func TestDamagedGroupJournalLocksThePageOnly(t *testing.T) {
	store, db, id := journalStore(t)
	fieldID := strings.Repeat("cd", 16)
	if _, err := store.Update(t.Context(), id, 1, func(v *Record) error {
		v.Config["requests.passthrough-headers"] = &FieldJournal{
			Latest: fieldID,
			Operations: map[string]ConfigOperation{
				fieldID: {RequestID: fieldID, Field: "requests.passthrough-headers", Value: true,
					SourceID: "installation-gateway-01", Generation: "7", Revision: 1, State: "persisted", HTTP200: true},
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Only an older writer can leave a journal this store cannot prove; write it
	// exactly as such a row would look.
	container := map[string]any{
		"fields": map[string]any{},
		"groups": map[string]any{"retry": map[string]any{"latest": "not-hex", "operations": map[string]any{}}},
	}
	data, err := json.Marshal(container)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value, updated_at_ms=excluded.updated_at_ms", configJournalKey, string(data), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatalf("damaged group journal rejected the record: %v", err)
	}
	if !reloaded.ConfigGroupsInvalid {
		t.Fatal("damage not reported")
	}
	if len(reloaded.ConfigGroups) != 0 {
		t.Fatal("unproven group journal exposed")
	}
	// The write path refuses to touch a page it cannot prove.
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		v.ConfigGroups["retry"] = &ConfigGroupJournal{
			Latest: strings.Repeat("ab", 16), Operations: map[string]ConfigGroupOperation{strings.Repeat("ab", 16): groupOperation(strings.Repeat("ab", 16), strings.Repeat("a", 64))},
		}
		return nil
	}); err == nil {
		t.Fatal("page repaired through the store")
	}
}

// TestLegacyContainerWithoutGroupsStaysReadable covers a row written before the
// group journal existed: the document has no "groups" key at all.
func TestLegacyContainerWithoutGroupsStaysReadable(t *testing.T) {
	var doc configJournalDoc
	if err := json.Unmarshal([]byte(`{"fields":{}}`), &doc); err != nil {
		t.Fatal(err)
	}
	groups, damaged := loadConfigGroups(doc)
	if damaged || len(groups) != 0 {
		t.Fatalf("legacy container groups=%v damaged=%v", groups, damaged)
	}
}
