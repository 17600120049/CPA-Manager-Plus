package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	collectorservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestCollectorWorkerDoesNotStartWhenMonitoringDisabled(t *testing.T) {
	cfg := workerTestConfig(t)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	enabled := false
	err = db.SaveManagerConfig(context.Background(), store.ManagerConfig{
		CPAConnection: store.ManagerCPAConnectionConfig{
			CPABaseURL:    "http://cpa.local:8317",
			ManagementKey: "management-key",
		},
		Collector: store.ManagerCollectorConfig{
			Enabled:        &enabled,
			CollectorMode:  "http",
			Queue:          "usage",
			PopSide:        "right",
			BatchSize:      100,
			PollIntervalMS: 500,
		},
	})
	if err != nil {
		t.Fatalf("save manager config: %v", err)
	}

	manager := collectorpkg.NewManager(cfg, db)
	collectorService := collectorservice.New(manager)
	NewCollectorWorker(cfg, db, collectorService).Start(context.Background())

	if status := collectorService.Status(); status.Collector != "stopped" {
		t.Fatalf("collector status = %#v", status)
	}
}

func TestCollectorWorkerStartsFromEnvironmentConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/management/usage-queue" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	cfg := workerTestConfig(t)
	cfg.CPAUpstreamURL = upstream.URL
	cfg.ManagementKey = "management-key"
	cfg.CollectorMode = "http"
	cfg.PollInterval = time.Hour
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	manager := collectorpkg.NewManager(cfg, db)
	collectorService := collectorservice.New(manager)
	t.Cleanup(func() {
		_ = collectorService.Stop(context.Background())
	})

	NewCollectorWorker(cfg, db, collectorService).Start(context.Background())
	status := collectorService.Status()
	if status.Collector != "starting" && status.Collector != "running" {
		t.Fatalf("collector status = %#v", status)
	}
	if status.Upstream != upstream.URL || status.Mode != "http" || status.Queue != "usage" {
		t.Fatalf("collector status = %#v", status)
	}
}

func TestCollectorServiceRestartAndStop(t *testing.T) {
	cfg := workerTestConfig(t)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	manager := collectorpkg.NewManager(cfg, db)
	collectorService := collectorservice.New(manager)
	managerCfg := store.ManagerConfig{
		CPAConnection: store.ManagerCPAConnectionConfig{
			CPABaseURL:    "http://cpa.local:8317",
			ManagementKey: "management-key",
		},
		Collector: store.ManagerCollectorConfig{
			CollectorMode:  "http",
			Queue:          "usage",
			PopSide:        "right",
			BatchSize:      100,
			PollIntervalMS: int(time.Hour / time.Millisecond),
		},
	}

	if err := collectorService.Start(context.Background(), managerCfg); err != nil {
		t.Fatalf("start collector: %v", err)
	}
	if status := collectorService.Status(); status.Collector != "starting" {
		t.Fatalf("collector status after start = %#v", status)
	}
	managerCfg.Collector.PollIntervalMS = int((2 * time.Hour) / time.Millisecond)
	if err := collectorService.Restart(context.Background(), managerCfg); err != nil {
		t.Fatalf("restart collector: %v", err)
	}
	if status := collectorService.Status(); status.Collector != "starting" {
		t.Fatalf("collector status after restart = %#v", status)
	}
	if err := collectorService.Stop(context.Background()); err != nil {
		t.Fatalf("stop collector: %v", err)
	}
	if status := collectorService.Status(); status.Collector != "stopped" {
		t.Fatalf("collector status after stop = %#v", status)
	}
}

func workerTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		DBPath:        filepath.Join(t.TempDir(), "usage.sqlite"),
		CollectorMode: "auto",
		Queue:         "usage",
		PopSide:       "right",
		BatchSize:     100,
		PollInterval:  time.Hour,
	}
}

type collectorRuntimeConfigCapture struct {
	updates chan collectorpkg.RuntimeConfig
}

func (c collectorRuntimeConfigCapture) HandleUsageEvents(context.Context, collectorpkg.RuntimeConfig, []usage.Event) {
}

func (c collectorRuntimeConfigCapture) UpdateRuntimeConfig(_ context.Context, cfg collectorpkg.RuntimeConfig) {
	select {
	case c.updates <- cfg:
	default:
	}
}

func TestCollectorWorkerUsesCompletedManagedConnectionMirror(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fresh := freshstore.New(db)
	id := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: "installation", Generation: "generation", OperationID: "operation", DatabasePath: path}
	admin, err := security.NewAdminCredential("Cpamp_Collector_Mirror_Admin_839216", "host")
	if err != nil {
		t.Fatal(err)
	}
	record, err := fresh.Create(ctx, id, &admin)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewProtector([]byte("collector mirror data key"))
	if err != nil {
		t.Fatal(err)
	}
	const baseURL = "http://cpamp-runtime:8317"
	const managementKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ciphertext, err := protector.ProtectString(managementKey)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.NewManaged, Stage: "committed", TargetIdentity: "runtime", TargetGeneration: 7, TargetArtifactID: "sha256:" + strings.Repeat("a", 64), DestinationConnectionIdentity: freshstrategy.ManagedDestinationIdentity(baseURL)}
	connection := &model.ManagerCPAConnectionConfig{CPABaseURL: baseURL, ManagementKey: ciphertext}
	pending, err := fresh.Update(ctx, id, record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Strategy = checkpoint
		r.Connection = connection
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Update(ctx, id, pending.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Routing = bootstrap.FreshRoutingCheckpoint{OperationID: id.OperationID, Revision: 1, Target: "/console", Stage: bootstrap.PrefixCommitted, Committed: true}
		r.Operation.ProductComplete = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	managerStore := store.New(db, protector)
	managerCfg, ok, err := managerStore.LoadManagerConfig(ctx)
	if err != nil || !ok {
		t.Fatalf("manager config mirror unavailable: ok=%v err=%v", ok, err)
	}
	if managerCfg.CPAConnection.CPABaseURL != baseURL || managerCfg.CPAConnection.ManagementKey != managementKey {
		t.Fatalf("manager config mirror = %#v", managerCfg.CPAConnection)
	}
	enabled := true
	managerCfg.Collector.Enabled = &enabled
	if err := managerStore.SaveManagerConfigAndSetup(ctx, managerCfg, model.Setup{CPAUpstreamURL: baseURL, ManagementKey: managementKey}); err != nil {
		t.Fatal(err)
	}
	base := config.Config{CollectorMode: "poll", Queue: "queue", PopSide: "left", PollInterval: time.Second, BatchSize: 1}
	manager := collectorpkg.NewManager(base, managerStore)
	capture := collectorRuntimeConfigCapture{updates: make(chan collectorpkg.RuntimeConfig, 1)}
	manager.SetUsageEventHandler(capture)
	worker := NewCollectorWorker(base, managerStore, collectorservice.New(manager))
	worker.Start(ctx)
	select {
	case got := <-capture.updates:
		if got.CPAUpstreamURL != baseURL || got.ManagementKey != managementKey {
			t.Fatalf("collector received base=%q key=%q", got.CPAUpstreamURL, got.ManagementKey)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collector did not receive the completed managed connection")
	}
	worker.Stop(context.Background())
}
