package freshstore

import (
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestDamagedRetryJournalOnlyLocksConfigAndCannotBeErased(t *testing.T) {
	for _, raw := range []string{"null", "{", "{}", `{"latest":"missing","operations":{}}`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manager.sqlite")
			db, e := sqlite.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			st := New(db)
			id := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: "install", Generation: "gen", DatabasePath: path, OperationID: "fresh-operation"}
			if _, e = st.Create(t.Context(), id, nil); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec("insert into settings(key,value,updated_at_ms) values(?,?,0)", retryKey, raw); e != nil {
				t.Fatal(e)
			}
			r, e := st.Load(t.Context())
			if e != nil || !r.RetryInvalid || r.Retry != nil {
				t.Fatal(r, e)
			}
			// Another already-authorized domain retains its Fresh CAS. Invalid
			// bytes survive unchanged for trusted Host diagnosis.
			if _, e = st.Update(t.Context(), id, r.Operation.Revision, func(r *Record) error { return nil }); e != nil {
				t.Fatal(e)
			}
			var after string
			if e = db.QueryRow("select value from settings where key=?", retryKey).Scan(&after); e != nil || after != raw {
				t.Fatal("invalid journal overwritten", e)
			}
			r, _ = st.Load(t.Context())
			if _, e = st.Update(t.Context(), id, r.Operation.Revision, func(r *Record) error { r.RetryInvalid = false; return nil }); e != ErrConflict {
				t.Fatal("corruption reset", e)
			}
		})
	}
}
