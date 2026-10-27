package freshkeys

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestH1KeyJournalAttemptsAndImmutableTerminal(t *testing.T) {
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = sqlite.Migrate(db); e != nil {
		t.Fatal(e)
	}
	r := New(db)
	id := strings.Repeat("1", 32)
	o := Operation{ID: id, TargetID: strings.Repeat("2", 32), KeeperID: strings.Repeat("3", 32), Mode: "replace", RunID: "isolated-h1-key-test", Generation: "7", Revision: 1, Outcome: "pending", ParentID: "k:" + id, SourceID: "isolated-h1-key-source", ClaimSHA256: strings.Repeat("a", 64), ClaimRevision: 5, IntentDigest: strings.Repeat("b", 64), CreatedAtMS: time.Now().UnixMilli(), Steps: []KeyStep{{RuntimeID: "k:" + id + ":0:add", Name: "add", State: "unattempted"}, {RuntimeID: "k:" + id + ":1:revoke", Name: "revoke", State: "unattempted"}}}
	if e = r.BeginOperation(t.Context(), o); e != nil {
		t.Fatal(e)
	}
	save := func() {
		t.Helper()
		if e = r.SaveOperation(t.Context(), o); e != nil {
			t.Fatal(e)
		}
		o.Revision++
	}
	o.Steps[0].State = "attempted"
	o.Outcome = "unknown"
	save()
	if r.H1Idle(t.Context()) == nil {
		t.Fatal("unknown Key journal admitted other domain")
	}
	bad := o
	bad.Steps = append([]KeyStep(nil), o.Steps...)
	bad.Steps[0].State = "unattempted"
	if r.SaveOperation(t.Context(), bad) == nil {
		t.Fatal("attempt reset")
	}
	bad = o
	bad.Generation = "8"
	if r.SaveOperation(t.Context(), bad) == nil {
		t.Fatal("generation retarget")
	}
	o.Steps[0].Returned = true
	o.Steps[0].HTTP200 = true
	o.Steps[0].BeginRevision = 6
	save()
	o.Steps[0].Receipt = strings.Repeat("c", 64)
	o.Steps[0].State = "observed"
	o.Outcome = "pending"
	save()
	o.Steps[1].State = "attempted"
	o.Outcome = "unknown"
	save()
	o.Steps[1].Returned = true
	o.Steps[1].HTTP200 = true
	o.Steps[1].BeginRevision = 9
	save()
	_, e = db.Exec("insert into gateway_api_key_identities(id,revision,lifecycle,created_at_ms,updated_at_ms) values(?,1,'active',1,1)", o.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TRIGGER fail_key_terminal BEFORE UPDATE ON settings WHEN NEW.key='fresh_key_operation_v1:` + id + `' BEGIN SELECT RAISE(FAIL,'injected Key terminal failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	o.Steps[1].Receipt = strings.Repeat("d", 64)
	o.Steps[1].State = "observed"
	o.Outcome = "committed"
	o.CompletedAtMS = time.Now().UnixMilli()
	if r.SaveOperation(t.Context(), o) == nil {
		t.Fatal("terminal failure hidden")
	}
	var life string
	if e = db.QueryRow("select lifecycle from gateway_api_key_identities where id=?", o.TargetID).Scan(&life); e != nil || life != "active" {
		t.Fatal("canonical transition escaped failed transaction", life, e)
	}
	if _, e = db.Exec("DROP TRIGGER fail_key_terminal"); e != nil {
		t.Fatal(e)
	}
	save()
	if r.SaveOperation(t.Context(), o) == nil {
		t.Fatal("business terminal rewritten")
	}
	if e = r.H1Idle(t.Context()); e != nil {
		t.Fatal(e)
	}
}

// TestH1IdleChecksPreAllowlistConfigJournals keeps the read-only pre-allowlist
// journal rows inside the admission checkpoint: an unknown or damaged legacy
// write still forbids a new H1 claim, exactly as before the allowlist cutover.
func TestH1IdleChecksPreAllowlistConfigJournals(t *testing.T) {
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = sqlite.Migrate(db); e != nil {
		t.Fatal(e)
	}
	r := New(db)
	if e = r.H1Idle(t.Context()); e != nil {
		t.Fatal("an empty store is not idle:", e)
	}
	id := strings.Repeat("1", 32)
	row := func(field string, value int, state string) string {
		operation := map[string]any{"requestId": id, "value": value, "sourceId": "isolated-h1-source", "generation": "7", "revision": 4, "sha256": strings.Repeat("b", 64), "state": state}
		if field != "" {
			operation["field"] = field
		}
		b, err := json.Marshal(map[string]any{"latest": id, "operations": map[string]any{id: operation}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	put := func(key, value string) {
		t.Helper()
		if _, err := db.Exec("insert into settings(key,value,updated_at_ms) values(?,?,0) on conflict(key) do update set value=excluded.value", key, value); err != nil {
			t.Fatal(err)
		}
	}
	put("fresh_config_retry_v1", row("", 3, "unknown"))
	if r.H1Idle(t.Context()) == nil {
		t.Fatal("an unknown legacy retry journal admitted a new claim")
	}
	put("fresh_config_retry_v1", row("", 3, "persisted"))
	if e = r.H1Idle(t.Context()); e != nil {
		t.Fatal("a terminal legacy retry journal blocked a claim:", e)
	}
	put("fresh_config_retry_interval_v1", row("max-retry-interval", 120, "unknown"))
	if r.H1Idle(t.Context()) == nil {
		t.Fatal("an unknown legacy interval journal admitted a new claim")
	}
	put("fresh_config_retry_interval_v1", "{not a journal")
	if r.H1Idle(t.Context()) == nil {
		t.Fatal("a damaged legacy interval journal admitted a new claim")
	}
}
