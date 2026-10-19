package freshkeys

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	identityrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/adapters/sqlite/identitystore"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func TestPersistRestartReconcileAndConcurrentCreation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	p, _ := security.NewProtector([]byte("isolated-test-protector"))
	service := New(repo.New(db), p)
	requestID := strings.Repeat("a", 32)
	result, created, err := service.Create(ctx, requestID)
	if err != nil || !created || len(result.CreatedSecret) != 73 {
		t.Fatalf("create failed: %v", err)
	}
	canonical, err := identityrepo.New(db).LoadAPIKeyByID(ctx, result.Key.ID)
	if err != nil || canonical.ID != result.Key.ID {
		t.Fatal("canonical identity unavailable", err)
	}
	var stored string
	if err = db.QueryRow("select secret_ciphertext from fresh_client_api_keys").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !security.IsValidProtectedEnvelope(stored) || strings.Contains(stored, result.CreatedSecret) {
		t.Fatal("unprotected secret")
	}
	clear, err := p.UnprotectString(stored)
	if err != nil || clear != result.CreatedSecret {
		t.Fatal("stored secret not bound to created result")
	}
	var bindings int
	if err = db.QueryRow("select count(*) from gateway_api_key_source_bindings").Scan(&bindings); err != nil || bindings != 0 {
		t.Fatal("fabricated runtime binding")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	service = New(repo.New(db), p)
	replayed, created, err := service.Create(ctx, requestID)
	if err != nil || created || replayed.CreatedSecret != "" || replayed.Key != result.Key {
		t.Fatal("restart/replay changed identity or exposed secret", err)
	}
	const concurrent = 12
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)
	results := make(chan Result, concurrent)
	for range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, e := service.Create(ctx, strings.Repeat("b", 32))
			errs <- e
			results <- v
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var concurrentKey Key
	secretReplies := 0
	for v := range results {
		if concurrentKey.ID == "" {
			concurrentKey = v.Key
		}
		if v.Key != concurrentKey {
			t.Fatal("duplicate request generated different canonical identities")
		}
		if v.CreatedSecret != "" {
			secretReplies++
		}
	}
	if secretReplies != 1 || concurrentKey.ID == result.Key.ID {
		t.Fatal("create result count or independent identity invalid")
	}
	keys, err := service.List(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatal("duplicate persisted key", err)
	}
	wrong, _ := security.NewProtector([]byte("wrong-data-key"))
	if _, err = New(repo.New(db), wrong).List(ctx); err == nil {
		t.Fatal("wrong protector accepted")
	}
	if _, _, err = New(repo.New(db), wrong).Create(ctx, strings.Repeat("c", 32)); err == nil {
		t.Fatal("mixed protector write accepted")
	}
	if _, err = service.Lookup(ctx, strings.Repeat("c", 32)); err != ErrNotFound {
		t.Fatal("unknown result misreported", err)
	}
	if _, _, err = service.Create(ctx, "invalid"); err != ErrRequest {
		t.Fatal("invalid request accepted")
	}
	if _, err = db.Exec("update fresh_client_api_keys set secret_ciphertext='plaintext'"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.List(ctx); err == nil {
		t.Fatal("plaintext fallback accepted")
	}
}
