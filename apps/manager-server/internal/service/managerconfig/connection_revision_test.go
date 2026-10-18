package managerconfig

import (
	"encoding/json"
	"strings"
	"testing"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	collectorservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestPersistedCPAConnectionRevisionManagerUpdate(t *testing.T) {
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	cpa := testutil.NewCPAMock(t)
	collector := collectorservice.New(collectorpkg.NewManager(cfg, st))
	svc := New(cfg, st, collector)
	submitted := store.ManagerConfig{
		CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: cpa.URL(), ManagementKey: cpa.ManagementKey},
		Collector:     store.ManagerCollectorConfig{Enabled: BoolPtr(false)},
	}
	revision := func() string {
		t.Helper()
		rev, found, err := st.Settings.LoadPersistedCPAConnectionRevision(t.Context())
		if err != nil || !found {
			t.Fatalf("revision found=%v err=%v", found, err)
		}
		return rev
	}
	if _, err := svc.Update(t.Context(), submitted); err != nil {
		t.Fatal(err)
	}
	first := revision()
	submitted.Collector.Queue = "unrelated-queue"
	if _, err := svc.Update(t.Context(), submitted); err != nil {
		t.Fatal(err)
	}
	if revision() != first {
		t.Fatal("collector update rotated revision")
	}
	cpa.ManagementKey = "rotated-key"
	submitted.CPAConnection.ManagementKey = cpa.ManagementKey
	if _, err := svc.Update(t.Context(), submitted); err != nil {
		t.Fatal(err)
	}
	if revision() == first {
		t.Fatal("same-URL key update retained revision")
	}
}

func TestPersistedCPAConnectionRevisionDoesNotQualifyEnvironment(t *testing.T) {
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	saved := store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{
		CPABaseURL: "https://db-cpa.test", ManagementKey: "db-key",
	}}
	if err := st.SaveManagerConfigAndSetup(t.Context(), saved, SetupFromManagerConfig(saved)); err != nil {
		t.Fatal(err)
	}
	revision, found, err := st.Settings.LoadPersistedCPAConnectionRevision(t.Context())
	if err != nil || !found {
		t.Fatalf("persisted revision found=%v err=%v", found, err)
	}
	for _, same := range []bool{true, false} {
		// Even byte-identical environment values retain their own source; a DB
		// revision's presence cannot turn an environment connection into SourceDB.
		cfg.CPAUpstreamURL, cfg.ManagementKey = saved.CPAConnection.CPABaseURL, saved.CPAConnection.ManagementKey
		if !same {
			cfg.CPAUpstreamURL, cfg.ManagementKey = "https://env-cpa.test", "env-key"
		}
		svc := New(cfg, st, nil)
		resolved, source, ok, err := svc.ResolveManagerConfigWithSource(t.Context())
		if err != nil || !ok || source != SourceEnv || resolved.CPAConnection.CPABaseURL != cfg.CPAUpstreamURL || resolved.CPAConnection.ManagementKey != cfg.ManagementKey {
			t.Fatalf("effective env source=%q ok=%v err=%v", source, ok, err)
		}
		_, setupSource, ok, err := svc.ResolveSetupWithSource(t.Context())
		if err != nil || !ok || setupSource != SourceEnv {
			t.Fatalf("setup env source=%q ok=%v err=%v", setupSource, ok, err)
		}
		if rev, found, err := st.Settings.LoadPersistedCPAConnectionRevision(t.Context()); err != nil || !found || rev != revision {
			t.Fatalf("env resolution changed persisted fact: found=%v err=%v", found, err)
		}
		public, err := json.Marshal(PublicConfig(resolved))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(public), revision) || strings.Contains(string(public), cfg.ManagementKey) || strings.Contains(string(public), "revision") {
			t.Fatal("public config exposed private revision or secret")
		}
		if _, err := svc.Update(t.Context(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{
			CPABaseURL: "https://replacement.test", ManagementKey: "replacement-key",
		}}); err == nil || !strings.Contains(err.Error(), "environment variables") {
			t.Fatalf("environment replacement error=%v", err)
		}
	}
}
