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
	if damaged != nil || len(groups) != 0 {
		t.Fatalf("legacy container groups=%v damaged=%v", groups, damaged)
	}
}

// TestNumberEditSurvivesTheJournalBoundary is the regression for a group save
// that carries a number. A journal entry is written from the in-memory operation
// the service built (Value is int) but every later write — the acknowledgement,
// the resolution, CPA's own rejection — compares it against the same entry as it
// came back from SQLite, where encoding/json produced a float64. If that
// comparison is type-sensitive, CPA has already applied the PATCH while the
// Manager answers a conflict, and the group stays locked with no way back.
func TestNumberEditSurvivesTheJournalBoundary(t *testing.T) {
	store, _, id := journalStore(t)
	checksum := strings.Repeat("a", 64)
	requestID := strings.Repeat("ab", 16)
	operation := groupOperation(requestID, checksum)
	if _, err := store.Update(t.Context(), id, 1, func(v *Record) error {
		v.ConfigGroups["retry"] = &ConfigGroupJournal{
			Latest:     requestID,
			Operations: map[string]ConfigGroupOperation{requestID: operation},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The acknowledgement and the resolution both write the service's own copy of
	// the operation, so the entry comes back across the JSON boundary and is
	// compared with the value the service still holds.
	operation.HTTP200, operation.State = true, "persisted"
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		v.ConfigGroups["retry"].Operations[requestID] = operation
		return nil
	}); err != nil {
		t.Fatalf("number edit rejected on write-back: %v", err)
	}
	resolved, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entry := resolved.ConfigGroups["retry"].Operations[requestID]
	if entry.State != "persisted" || !entry.HTTP200 {
		t.Fatalf("resolution not durable: %+v", entry)
	}
	if len(entry.Edits) != 1 || entry.Edits[0].Value != float64(3) && entry.Edits[0].Value != 3 {
		t.Fatalf("number edit lost: %+v", entry.Edits)
	}
}

// TestWideIntegerRuleParameterSurvivesTheJournal is the review's #13d regression:
// a rule parameter wider than 2^53 must come back as the same integer. Decoding
// the config document into float64 rounds such a value, so the page would show one
// number and send a different one back on the next family save.
func TestWideIntegerRuleParameterSurvivesTheJournal(t *testing.T) {
	const literal = "9007199254740993"
	store, _, id := journalStore(t)
	checksum := strings.Repeat("a", 64)
	requestID := strings.Repeat("cd", 16)
	operation := ConfigGroupOperation{
		RequestID: requestID, Group: "rules", Target: "family", Prefix: "requests.payload",
		Baseline: checksum, State: "unknown", SourceID: "installation-gateway-01", Generation: "7",
		Rules: []any{map[string]any{"id": "big", "size": json.Number(literal)}},
	}
	if _, err := store.Update(t.Context(), id, 1, func(v *Record) error {
		v.ConfigGroups["rules"] = &ConfigGroupJournal{
			Latest:     requestID,
			Operations: map[string]ConfigGroupOperation{requestID: operation},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entry := reloaded.ConfigGroups["rules"].Operations[requestID]
	rules, ok := entry.Rules.([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("rules did not survive: %+v", entry.Rules)
	}
	rule, ok := rules[0].(map[string]any)
	if !ok {
		t.Fatalf("rule did not survive: %+v", rules[0])
	}
	encoded, err := json.Marshal(rule["size"])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != literal {
		t.Fatalf("wide integer rounded across the journal: %s", encoded)
	}
	// Re-writing the entry must not round it either: the page's own copy and the
	// reloaded one are the same operation.
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		current := v.ConfigGroups["rules"].Operations[requestID]
		if canonical := current.Clone(); canonical.Rules == nil {
			return ErrConflict
		}
		current.State, current.HTTP200 = "persisted", true
		v.ConfigGroups["rules"].Operations[requestID] = current
		return nil
	}); err != nil {
		t.Fatalf("wide-integer entry rejected on write-back: %v", err)
	}
	final, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(final.ConfigGroups["rules"].Operations[requestID].Rules)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), literal) {
		t.Fatalf("wide integer changed on the second write: %s", encoded)
	}
}

// TestDamagedGroupJournalSurvivesAnotherWrite proves a group journal this store
// cannot prove is carried through verbatim. The damaged entry may hold an
// unresolved original operation, so rewriting the record without it would unlock
// a page whose last write is still unknown.
func TestDamagedGroupJournalSurvivesAnotherWrite(t *testing.T) {
	store, db, id := journalStore(t)
	damagedEntry := `{"latest":"not-hex","operations":{}}`
	container := `{"fields":{},"groups":{"retry":` + damagedEntry + `}}`
	if _, err := db.ExecContext(t.Context(), "insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value, updated_at_ms=excluded.updated_at_ms", configJournalKey, container, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.ConfigGroupsInvalid || len(reloaded.ConfigGroupsDamaged) != 1 {
		t.Fatalf("damage not preserved in the record: %+v", reloaded.ConfigGroupsDamaged)
	}
	// A legacy field operation is still refinable; the damaged group bytes must
	// ride along unchanged.
	fieldID := strings.Repeat("cd", 16)
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		v.Config["requests.passthrough-headers"] = &FieldJournal{
			Latest: fieldID,
			Operations: map[string]ConfigOperation{
				fieldID: {RequestID: fieldID, Field: "requests.passthrough-headers", Value: true,
					SourceID: "installation-gateway-01", Generation: "7", Revision: 1, State: "unknown", HTTP200: true},
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = db.QueryRowContext(t.Context(), "select value from settings where key=?", configJournalKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var doc configJournalDoc
	if err = json.Unmarshal([]byte(stored), &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc.Groups["retry"]) != damagedEntry {
		t.Fatalf("damaged group journal rewritten: %s", doc.Groups["retry"])
	}
	if doc.Fields["requests.passthrough-headers"] == nil {
		t.Fatal("field write lost while preserving damage")
	}
}

// TestValidGroupSurvivesADamagedSibling is the round-2 #10 regression: a valid
// group journal holding an unresolved original sat next to a damaged one, and the
// next legacy field write erased it — the page then reported no unknown operation
// and unlocked a group whose last write is still unresolved. Valid groups and the
// damaged bytes must both survive a write of another section.
func TestValidGroupSurvivesADamagedSibling(t *testing.T) {
	store, db, id := journalStore(t)
	checksum := strings.Repeat("a", 64)
	requestID := strings.Repeat("ab", 16)
	if _, err := store.Update(t.Context(), id, 1, func(v *Record) error {
		v.ConfigGroups["retry"] = &ConfigGroupJournal{
			Latest:     requestID,
			Operations: map[string]ConfigGroupOperation{requestID: groupOperation(requestID, checksum)},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// An older writer leaves a second group this store cannot prove.
	var stored string
	if err := db.QueryRowContext(t.Context(), "select value from settings where key=?", configJournalKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var doc configJournalDoc
	if err := json.Unmarshal([]byte(stored), &doc); err != nil {
		t.Fatal(err)
	}
	damagedEntry := `{"latest":"not-hex","operations":{}}`
	container := `{"fields":` + string(mustJSON(t, doc.Fields)) + `,"groups":{"retry":` + string(doc.Groups["retry"]) + `,"cooldown":` + damagedEntry + `}}`
	if _, err := db.ExecContext(t.Context(), "insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value, updated_at_ms=excluded.updated_at_ms", configJournalKey, container, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatalf("damaged sibling rejected the record: %v", err)
	}
	if !reloaded.ConfigGroupsInvalid {
		t.Fatal("damage not reported")
	}
	// The provable group is still there, with its unresolved original.
	valid := reloaded.ConfigGroups["retry"]
	if valid == nil || valid.Operations[requestID].State != "unknown" {
		t.Fatalf("valid group journal dropped beside damage: %+v", valid)
	}
	if len(reloaded.ConfigGroupsDamaged) != 1 || string(reloaded.ConfigGroupsDamaged["cooldown"]) != damagedEntry {
		t.Fatalf("damaged bytes %+v", reloaded.ConfigGroupsDamaged)
	}
	// The page is locked, so no group write may proceed through the store.
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		j := v.ConfigGroups["retry"]
		j.Latest = strings.Repeat("cd", 16)
		return nil
	}); err == nil {
		t.Fatal("page with a damaged group accepted a group write")
	}
	// A legacy field write of another section is still allowed, and must carry
	// BOTH the valid group journal and the damaged bytes through unchanged.
	fieldID := strings.Repeat("cd", 16)
	if _, err = store.Update(t.Context(), id, reloaded.Operation.Revision, func(v *Record) error {
		v.Config["requests.passthrough-headers"] = &FieldJournal{
			Latest: fieldID,
			Operations: map[string]ConfigOperation{
				fieldID: {RequestID: fieldID, Field: "requests.passthrough-headers", Value: true,
					SourceID: "installation-gateway-01", Generation: "7", Revision: 1, State: "unknown", HTTP200: true},
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(t.Context(), "select value from settings where key=?", configJournalKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	doc = configJournalDoc{}
	if err = json.Unmarshal([]byte(stored), &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc.Groups["cooldown"]) != damagedEntry {
		t.Fatalf("damaged group rewritten: %s", doc.Groups["cooldown"])
	}
	if doc.Groups["retry"] == nil {
		t.Fatal("valid group journal erased by another section's write")
	}
	var kept ConfigGroupJournal
	if err = json.Unmarshal(doc.Groups["retry"], &kept); err != nil {
		t.Fatal(err)
	}
	if kept.Operations[requestID].State != "unknown" || kept.Operations[requestID].HTTP200 {
		t.Fatalf("unresolved original lost or changed: %+v", kept.Operations[requestID])
	}
	if doc.Fields["requests.passthrough-headers"] == nil {
		t.Fatal("field write lost")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
