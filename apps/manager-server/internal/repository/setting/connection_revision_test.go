package setting

import (
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestPersistedCPAConnectionRevisionLifecycle(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "revision.sqlite"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	r := New(db)
	ctx := t.Context()
	load := func() string {
		t.Helper()
		v, found, err := r.LoadPersistedCPAConnectionRevision(ctx)
		if err != nil || !found || v == "" { t.Fatalf("revision: %q found=%v err=%v", v, found, err) }
		return v
	}
	if _, found, err := r.LoadPersistedCPAConnectionRevision(ctx); err != nil || found {
		t.Fatalf("legacy revision found=%v err=%v", found, err)
	}
	setup := model.Setup{CPAUpstreamURL: "https://cpa.example.test", ManagementKey: "secret-one"}
	cfg := model.ManagerConfig{}
	cfg.CPAConnection.CPABaseURL = setup.CPAUpstreamURL
	cfg.CPAConnection.ManagementKey = setup.ManagementKey
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	first := load()
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	if v := load(); v != first { t.Fatalf("no-op revision rotated") }
	cfg.Collector.Queue = "unrelated"
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	if v := load(); v != first { t.Fatalf("collector edit rotated revision") }
	setup.ManagementKey = "secret-two"
	cfg.CPAConnection.ManagementKey = setup.ManagementKey
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	second := load()
	if second == first { t.Fatalf("same-URL key rotation retained revision") }
	// Key bytes (including surrounding spaces) must not be normalized away
	// when deciding whether an old private revision can be reused.
	setup.ManagementKey = " secret-two "
	cfg.CPAConnection.ManagementKey = setup.ManagementKey
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	spaced := load()
	if spaced == second { t.Fatalf("different key bytes reused the old revision") }
	setup.ManagementKey = "secret-two"
	cfg.CPAConnection.ManagementKey = setup.ManagementKey
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	second = load()
	if second == spaced { t.Fatalf("key bytes restored without changing revision") }
	reopened := New(db)
	if v, found, err := reopened.LoadPersistedCPAConnectionRevision(ctx); err != nil || !found || v != second {
		t.Fatalf("repository restart: revision=%q found=%v err=%v", v, found, err)
	}
	setup.CPAUpstreamURL = "https://replacement.example.test"
	cfg.CPAConnection.CPABaseURL = setup.CPAUpstreamURL
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	if v := load(); v == second { t.Fatalf("URL replacement did not rotate") }
	if err := r.SaveSetup(ctx, model.Setup{CPAUpstreamURL: setup.CPAUpstreamURL, ManagementKey: "conflict"}); err != nil { t.Fatal(err) }
	if _, found, err := r.LoadPersistedCPAConnectionRevision(ctx); err != nil || found {
		t.Fatalf("conflicted setup must be unqualified: found=%v err=%v", found, err)
	}
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	if v := load(); v == second { t.Fatalf("repaired pair did not get revision") }
	if err := r.SaveManagerConfig(ctx, model.ManagerConfig{}); err != nil { t.Fatal(err) }
	if _, found, err := r.LoadPersistedCPAConnectionRevision(ctx); err != nil || found {
		t.Fatalf("partial manager config must not be qualified: found=%v err=%v", found, err)
	}
}

func TestPersistedCPAConnectionRevisionLegacyNormalizeDoesNotMint(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil { t.Fatal(err) }
	r := New(db, protector)
	ctx := t.Context()
	setup := model.Setup{CPAUpstreamURL: "https://cpa.test", ManagementKey: "key"}
	cfg := model.ManagerConfig{}
	cfg.CPAConnection.CPABaseURL, cfg.CPAConnection.ManagementKey = setup.CPAUpstreamURL, setup.ManagementKey
	if err := r.NormalizeLegacyConnectionStorage(ctx, cfg, true, setup, true); err != nil { t.Fatal(err) }
	if _, found, err := r.LoadPersistedCPAConnectionRevision(ctx); err != nil || found {
		t.Fatalf("legacy normalization minted revision: found=%v err=%v", found, err)
	}
	if err := r.SaveManagerConfigAndSetup(ctx, cfg, setup); err != nil { t.Fatal(err) }
	rev, found, err := r.LoadPersistedCPAConnectionRevision(ctx)
	if err != nil || !found { t.Fatalf("authorized write did not mint: %q %v %v", rev, found, err) }
	if err := r.NormalizeLegacyConnectionStorage(ctx, cfg, true, setup, true); err != nil { t.Fatal(err) }
	after, found, err := r.LoadPersistedCPAConnectionRevision(ctx)
	if err != nil || !found || after != rev { t.Fatalf("no-op normalization changed revision") }
}
