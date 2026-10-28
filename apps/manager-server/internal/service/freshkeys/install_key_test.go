package freshkeys

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func installKeyStore(t *testing.T) (*sql.DB, *Service, *security.Protector, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	protector, err := security.NewProtector([]byte("isolated-install-key-protector"))
	if err != nil {
		t.Fatal(err)
	}
	return db, New(repo.New(db), protector), protector, path
}

// Every byte either names one of the 62 characters or is rejected, and each
// character is named by exactly four of the 256 values: the rejection rule
// carries no residue of the modulo bias a `% 62` would introduce.
func TestInstallKeyGeneratorRejectsTheTwoUnusedIndexValues(t *testing.T) {
	counts := map[byte]int{}
	for value := range 256 {
		character, ok := initialClientKeyChar(byte(value))
		if !ok {
			continue
		}
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z') {
			t.Fatalf("non-alphanumeric %q", character)
		}
		counts[character]++
	}
	if len(counts) != 62 {
		t.Fatalf("alphabet coverage %d", len(counts))
	}
	for character, count := range counts {
		if count != 4 {
			t.Fatalf("%q produced by %d byte values, want the uniform 4", character, count)
		}
	}
	for _, value := range []byte{62, 63, 126, 127, 190, 191, 254, 255} {
		if _, ok := initialClientKeyChar(value); ok {
			t.Fatalf("byte %d was folded instead of rejected", value)
		}
	}
	// The filler stops at the exact key length and never overruns it.
	buffer := make([]byte, 256)
	for i := range buffer {
		buffer[i] = byte(i)
	}
	filled := appendInitialClientKeyAlnum([]byte("sk-"), buffer)
	if len(filled) != model.InitialClientKeyLength || !model.ValidInitialClientKey(string(filled)) {
		t.Fatalf("filler produced %q", filled)
	}
}

func TestGenerateInstallKeyShapeAndEntropy(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		secret, err := GenerateInitialClientKey()
		if err != nil {
			t.Fatal(err)
		}
		if !model.ValidInitialClientKey(secret) || !strings.HasPrefix(secret, "sk-") {
			t.Fatalf("generated install key %q", secret)
		}
		if seen[secret] {
			t.Fatal("generator repeated a key")
		}
		seen[secret] = true
	}
}

// PrepareManagedInstall replays one canonical key across a restart, a replay and
// concurrent callers. The origin-unique record is the only durable copy.
func TestEnsureInitialKeyIsIdempotentAndCanonical(t *testing.T) {
	ctx := context.Background()
	db, service, protector, _ := installKeyStore(t)
	first, err := service.EnsureInitialKey(ctx)
	if err != nil || first.ID == "" || first.RequestID == "" || !model.ValidInitialClientKey(first.Secret) {
		t.Fatalf("ensure install key: %+v %v", first, err)
	}
	// Restart: a new service over the same store replays the identical key.
	restarted := New(repo.New(db), protector)
	second, err := restarted.EnsureInitialKey(ctx)
	if err != nil || second != first {
		t.Fatalf("restart minted a second install key: %+v %v", second, err)
	}
	const concurrent = 8
	var wg sync.WaitGroup
	results := make(chan InitialKey, concurrent)
	errs := make(chan error, concurrent)
	for range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, e := restarted.EnsureInitialKey(ctx)
			results <- key
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for key := range results {
		if key.ID != first.ID || key.Secret != first.Secret {
			t.Fatal("concurrent caller minted a second install key")
		}
	}

	// The key is a real canonical Key: listed, masked, named and marked as
	// installation-generated. Creation and installation never travel together.
	keys, err := service.List(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("canonical list: %+v %v", keys, err)
	}
	key := keys[0]
	if key.ID != first.ID || key.Origin != OriginInstaller || key.Name != InstallerOriginName ||
		key.MaskedPreview != model.MaskedClientKey(first.Secret) || strings.Contains(key.MaskedPreview, first.Secret) {
		t.Fatalf("install key projection: %+v", key)
	}
	// A workspace key keeps its own origin and never consumes the install slot.
	created, _, err := service.Create(ctx, strings.Repeat("a", 32))
	if err != nil || created.Key.Origin != OriginWorkspace || created.Key.ID == first.ID {
		t.Fatalf("workspace key origin: %+v %v", created.Key, err)
	}
	var installerRows int
	if err := db.QueryRow("select count(*) from fresh_client_api_keys where origin='installer'").Scan(&installerRows); err != nil || installerRows != 1 {
		t.Fatalf("installer rows = %d (%v)", installerRows, err)
	}
	var stored string
	if err := db.QueryRow("select secret_ciphertext from fresh_client_api_keys where origin='installer'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !security.IsValidProtectedEnvelope(stored) || strings.Contains(stored, first.Secret) {
		t.Fatal("install key stored in plaintext")
	}
	if clear, err := protector.UnprotectString(stored); err != nil || clear != first.Secret {
		t.Fatal("stored install secret is not the returned key")
	}
}

// Without a protector or repository there is no authority to mint a key, and no
// fallback secret may be invented.
func TestEnsureInitialKeyRequiresItsAuthority(t *testing.T) {
	_, service, _, _ := installKeyStore(t)
	if _, err := (&Service{repo: service.repo}).EnsureInitialKey(context.Background()); err == nil {
		t.Fatal("protector-less service minted an install key")
	}
	if _, err := (&Service{protector: service.protector}).EnsureInitialKey(context.Background()); err == nil {
		t.Fatal("store-less service minted an install key")
	}
	var nilService *Service
	if _, err := nilService.EnsureInitialKey(context.Background()); err == nil {
		t.Fatal("nil service minted an install key")
	}
}
