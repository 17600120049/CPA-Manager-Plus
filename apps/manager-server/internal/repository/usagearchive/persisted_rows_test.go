package usagearchive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
)

func TestValidatePersistedRowsDoesNotStopAtListPage(t *testing.T) {
	db := openArchiveTestDB(t)
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := usageevent.New(db).InsertBatch(ctx, archiveTestEvents()); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	// Exceed the default page and its one-row lookahead.
	for i := 1; i <= 22; i++ {
		id := fmt.Sprintf("%032x", i)
		if _, err := repo.CreateRun(ctx, id, 2500, int64(10000+i*2)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CancelRun(ctx, id, int64(10001+i*2)); err != nil {
			t.Fatal(err)
		}
	}
	var before, after int64
	if err := db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	if err := repo.ValidatePersistedRows(ctx); err != nil {
		t.Fatalf("production rows must decode on a read-only connection: %v", err)
	}
	if err := db.QueryRow("SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatalf("row inspection changed data: %d -> %d, %v", before, after, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.ValidatePersistedRows(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("row inspection must propagate context cancellation: %v", err)
	}
	if _, err := db.Exec(`PRAGMA query_only = OFF;
		UPDATE usage_archive_runs SET event_count = 'garbage' WHERE id = '00000000000000000000000000000001'`); err != nil {
		t.Fatal(err)
	}
	if page, err := repo.ListRuns(ctx, RunListFilter{}); err != nil || len(page.Runs) != 20 || !page.HasMore {
		t.Fatalf("default page must miss the corrupt oldest row: %#v %v", page, err)
	}
	if err := repo.ValidatePersistedRows(ctx); err == nil || !strings.Contains(err.Error(), "event_count") {
		t.Fatalf("full inspection must reject the row outside the default page: %v", err)
	}
}
