package freshkeys

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func openSourceService(t *testing.T, options ...Option) (*Service, *sql.DB) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(db); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewProtector([]byte("fresh key source binding test"))
	if err != nil {
		t.Fatal(err)
	}
	return New(repo.New(db), protector, options...), db
}

func sourceBindings(t *testing.T, db *sql.DB, keyID string) []string {
	t.Helper()
	rows, err := db.Query(`select runtime_identity || ':' || api_key_hash from gateway_api_key_source_bindings where api_key_id = ?`, keyID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCreateUsesTheCommittedRuntimeAsTheSourceIdentity(t *testing.T) {
	service, db := openSourceService(t, WithSourceRuntime(func(context.Context) (string, error) {
		return "runtime-managed-1", nil
	}))
	result, created, err := service.Create(context.Background(), strings.Repeat("a", 32))
	if err != nil || !created {
		t.Fatalf("create = %v err = %v", created, err)
	}
	bindings := sourceBindings(t, db, string(result.Key.ID))
	if len(bindings) != 1 || !strings.HasPrefix(bindings[0], "runtime-managed-1:") {
		t.Fatalf("bindings = %+v", bindings)
	}
	// The binding names the hash, never the secret.
	if strings.Contains(bindings[0], result.CreatedSecret) {
		t.Fatal("binding leaked the created secret")
	}
	// A replay never adds a second attribution window.
	if _, created, err = service.Create(context.Background(), strings.Repeat("a", 32)); err != nil || created {
		t.Fatalf("replay created = %v err = %v", created, err)
	}
	if bindings = sourceBindings(t, db, string(result.Key.ID)); len(bindings) != 1 {
		t.Fatalf("replay bindings = %+v", bindings)
	}
}

func TestCreateDefersAttributionWhenTheRuntimeCannotBeRead(t *testing.T) {
	failing, errDB := openSourceService(t, WithSourceRuntime(func(context.Context) (string, error) {
		return "", errors.New("checkpoint unavailable")
	}))
	result, created, err := failing.Create(context.Background(), strings.Repeat("b", 32))
	if err != nil || !created {
		t.Fatalf("create with failing source = %v err = %v", created, err)
	}
	if bindings := sourceBindings(t, errDB, string(result.Key.ID)); len(bindings) != 0 {
		t.Fatalf("failing source still bound = %+v", bindings)
	}

	empty, _ := openSourceService(t)
	if _, created, err = empty.Create(context.Background(), strings.Repeat("c", 32)); err != nil || !created {
		t.Fatalf("create without source = %v err = %v", created, err)
	}
}

func TestIdentityIDsListCanonicalKeysOnly(t *testing.T) {
	service, _ := openSourceService(t)
	first, _, err := service.Create(context.Background(), strings.Repeat("d", 32))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.Create(context.Background(), strings.Repeat("e", 32))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := service.IdentityIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != first.Key.ID || ids[1] != second.Key.ID {
		t.Fatalf("identity ids = %+v", ids)
	}
}
