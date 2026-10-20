package freshkeys

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func TestRenameCanonicalPersistenceConflictsAndFailure(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "keys.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	p, _ := security.NewProtector([]byte("rename-test-data-key"))
	svc := New(repo.New(db), p)
	first, _, err := svc.Create(ctx, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := svc.Create(ctx, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	// Snapshot all security/identity columns; display metadata is orthogonal.
	snapshot := func() string {
		t.Helper()
		var value string
		err := db.QueryRow(`select json_group_array(json_array(i.id,i.revision,i.lifecycle,i.created_at_ms,i.updated_at_ms,k.request_id,k.secret_ciphertext,k.secret_hash,k.masked_preview))
    from fresh_client_api_keys k join gateway_api_key_identities i on i.id=k.api_key_id order by i.id`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	renamed, err := svc.Rename(ctx, string(first.Key.ID), "  工作 Key  ", 1)
	if err != nil || renamed.Name != "工作 Key" || renamed.NameRevision != 2 {
		t.Fatal("rename", renamed, err)
	}
	if snapshot() != before {
		t.Fatal("rename mutated key authority")
	}
	if _, err := svc.Rename(ctx, string(first.Key.ID), "stale", 1); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate accepted", err)
	}
	for _, id := range []string{string(first.Key.RequestID), strings.Repeat("c", 32)} {
		if _, err := svc.Rename(ctx, id, "wrong target", 1); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong ID accepted", err)
		}
	}
	for _, name := range []string{strings.Repeat("界", 121), "line\nbreak", string([]byte{0xff})} {
		if _, err := svc.Rename(ctx, string(first.Key.ID), name, 2); !errors.Is(err, ErrRequest) {
			t.Fatal("invalid name accepted", err)
		}
	}
	wrong, _ := security.NewProtector([]byte("wrong-rename-data-key"))
	if _, err := New(repo.New(db), wrong).Rename(ctx, string(first.Key.ID), "wrong protector", 2); err == nil {
		t.Fatal("wrong protector wrote")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.Rename(canceled, string(first.Key.ID), "canceled", 2); err == nil {
		t.Fatal("canceled write succeeded")
	}
	// A real SQLite write failure must leave the last confirmed value intact.
	if _, err := db.Exec(`create trigger fail_key_rename before update of name on fresh_client_api_keys begin select raise(abort,'test write failure'); end`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rename(ctx, string(first.Key.ID), "failed", 2); err == nil {
		t.Fatal("write failure hidden")
	}
	if _, err := db.Exec(`drop trigger fail_key_rename`); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Detail(ctx, string(first.Key.ID)); err != nil || got != renamed {
		t.Fatal("failed save changed state", got, err)
	}
	// Two administrators editing the same observed version: exactly one wins.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first writer", "second writer"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Rename(ctx, string(first.Key.ID), name, 2)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent updates not fenced")
	}
	final, err := svc.Detail(ctx, string(first.Key.ID))
	if err != nil || final.NameRevision != 3 {
		t.Fatal(final, err)
	}
	if snapshot() != before {
		t.Fatal("concurrent rename mutated identity/secret")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc = New(repo.New(db), p)
	if got, err := svc.Detail(ctx, string(first.Key.ID)); err != nil || got != final {
		t.Fatal("restart lost name", got, err)
	}
	if got, err := svc.Detail(ctx, string(second.Key.ID)); err != nil || got != second.Key {
		t.Fatal("another key changed", got, err)
	}
	if got, err := svc.Lookup(ctx, first.Key.RequestID); err != nil || got != final {
		t.Fatal("original request lost canonical metadata", got, err)
	}
	if replay, created, err := svc.Create(ctx, first.Key.RequestID); err != nil || created || replay.Key != final || replay.CreatedSecret != "" {
		t.Fatal("create replay regressed", err)
	}
	cleared, err := svc.Rename(ctx, string(first.Key.ID), " ", 3)
	if err != nil || cleared.Name != "" || cleared.NameRevision != 4 {
		t.Fatal("clear name", cleared, err)
	}
}
