package usagearchive

import (
	"context"
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
)

func TestValidateDataPlaneSchemaDoesNotExecuteStatements(t *testing.T) {
	db := openArchiveTestDB(t)
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := usageevent.New(db).InsertBatch(ctx, archiveTestEvents()); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	if err := New(db).ValidateDataPlaneSchema(ctx); err != nil {
		t.Fatalf("schema compilation must work without executing writes: %v", err)
	}
	if err := db.QueryRow("SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("schema compilation changed data: %d -> %d", before, after)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := New(db).ValidateDataPlaneSchema(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("compilation must propagate context cancellation: %v", err)
	}
}
