package freshstore

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestH1RetryEvidenceMonotonicAndTerminalSQLiteFailure(t *testing.T) {
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	st := New(db)
	id := bootstrap.FreshIdentity{AdmissionID: "admit", InstallationID: "install", Generation: "gen", DatabasePath: "isolated-test.sqlite", OperationID: "fresh-op"}
	r, e := st.Create(t.Context(), id, nil)
	if e != nil {
		t.Fatal(e)
	}
	o := RetryOperation{RequestID: strings.Repeat("a", 32), Value: 0, SourceID: "isolated-h1-source", Generation: "7", Revision: 4, SHA256: strings.Repeat("b", 64), State: "unknown", ParentID: "c:" + strings.Repeat("a", 32), ClaimRevision: 5, IntentDigest: strings.Repeat("c", 64), Attempted: true}
	r, e = st.Update(t.Context(), id, r.Operation.Revision, func(r *Record) error {
		r.Retry = &RetryJournal{Latest: o.RequestID, Operations: map[string]RetryOperation{o.RequestID: o}}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	update := func(next RetryOperation) error {
		t.Helper()
		current, e := st.Load(t.Context())
		if e != nil {
			return e
		}
		_, e = st.Update(t.Context(), id, current.Operation.Revision, func(r *Record) error { r.Retry.Operations[next.RequestID] = next; return nil })
		return e
	}
	o.HTTP200 = true
	o.BeginRevision = 6
	if e = update(o); e != nil {
		t.Fatal("durable HTTP success refused", e)
	}
	// Terminal failure does not erase the durable HTTP evidence. Runtime's
	// receipt remains owned by the parent until this original terminal succeeds.
	_, e = db.Exec(`CREATE TRIGGER fail_terminal BEFORE UPDATE ON settings WHEN NEW.key='fresh_config_retry_v1' BEGIN SELECT RAISE(FAIL,'injected terminal failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	terminal := o
	terminal.State = "persisted"
	terminal.Receipt = strings.Repeat("d", 64)
	if e = update(terminal); e == nil {
		t.Fatal("injected failure ignored")
	}
	r, e = st.Load(t.Context())
	if e != nil || r.Retry.Operations[o.RequestID] != o {
		t.Fatal("HTTP evidence lost", e)
	}
	if _, e = db.Exec("DROP TRIGGER fail_terminal"); e != nil {
		t.Fatal(e)
	}
	bad := o
	bad.HTTP200 = false
	bad.BeginRevision = 0
	if update(bad) == nil {
		t.Fatal("HTTP evidence rolled back")
	}
	bad = o
	bad.Value = 1
	if update(bad) == nil {
		t.Fatal("original value changed")
	}
	if e = update(terminal); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*RetryOperation){func(v *RetryOperation) { v.State = "unknown"; v.Receipt = "" }, func(v *RetryOperation) { v.Receipt = strings.Repeat("e", 64) }, func(v *RetryOperation) { v.Generation = "8" }} {
		bad = terminal
		change(&bad)
		if update(bad) == nil {
			t.Fatal("terminal rewritten")
		}
	}
}
