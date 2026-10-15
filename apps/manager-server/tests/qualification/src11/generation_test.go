package src11

import (
	"context"
	"database/sql"
	"debug/buildinfo"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
)

// No SQL writes or schema constructors generate historical data. The published
// executable creates the database and handles collector / HTTP inputs itself.
func TestGenerateExactSource(t *testing.T) {
	generateExactSource(t, "SRC-11", planCommit, "src-11-full-raw-revision2-v1")
}

func TestGenerateRET01(t *testing.T) {
	if os.Getenv("CPAMP_RET01_GENERATE") != "1" {
		t.Skip("opt-in RET-01 controlled generation")
	}
	generateExactSource(t, ret01CaseID, ret01PlanCommit, "ret-01-full-raw-revision2-v1")
}

func TestGenerateRET02(t *testing.T) {
	if os.Getenv("CPAMP_RET02_GENERATE") != "1" {
		t.Skip("opt-in RET-02 controlled generation")
	}
	generateExactSource(t, ret02CaseID, ret02PlanCommit, ret02Profile)
}

func TestGenerateMIG01(t *testing.T) {
	if os.Getenv("CPAMP_MIG01_GENERATE") != "1" {
		t.Skip("opt-in MIG-01 controlled generation")
	}
	generateExactSource(t, mig01CaseID, mig01PlanCommit, mig01Profile)
}

func TestGenerateBAK01(t *testing.T) {
	if os.Getenv("CPAMP_BAK01_GENERATE") != "1" {
		t.Skip("opt-in BAK-01 controlled generation")
	}
	generateExactSource(t, bak01CaseID, bak01PlanCommit, bak01Profile)
}

func generateExactSource(t *testing.T, caseID, plan, expectedProfile string) {
	if os.Getenv("CPAMP_SRC11_GENERATE") != "1" {
		t.Skip("opt-in exact historical executable execution")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("this generation profile requires isolated Linux AMD64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := envRequired(t, "CPAMP_SRC11_OUTPUT")
	if _, e := os.Lstat(root); !os.IsNotExist(e) {
		t.Fatal("output must be absent")
	}
	must(t, os.MkdirAll(root, 0700))
	archive := envRequired(t, "CPAMP_SRC11_ARCHIVE")
	releaseInfo := envRequired(t, "CPAMP_SRC11_RELEASE_INFO")
	report, e := legacyartifact.Verify(ctx, archive, asset)
	must(t, e)
	if fileDigest(t, releaseInfo) != releaseInfoDigest {
		t.Fatal("release-info pin mismatch")
	}
	var release struct {
		Release struct {
			Version string `json:"version"`
			Commit  string `json:"source_commit"`
		} `json:"release"`
	}
	must(t, json.Unmarshal(read(t, releaseInfo), &release))
	if release.Release.Version != "v1.14.4" || release.Release.Commit != sourceCommit {
		t.Fatal("release/source commit binding mismatch")
	}
	p, input := loadProfile(t, envRequired(t, "CPAMP_SRC11_PROFILE"))
	if p.Profile != expectedProfile {
		t.Fatal("case generation profile mismatch")
	}
	code := codeIdentity(t)
	generation := filepath.Join(root, "source-generation")
	must(t, os.MkdirAll(generation, 0700))
	must(t, os.WriteFile(filepath.Join(generation, "controlled-input.json"), input, 0600))
	must(t, os.WriteFile(filepath.Join(generation, "release-info.json"), read(t, releaseInfo), 0600))
	executable := filepath.Join(t.TempDir(), "source-producer")
	_, _, e = legacyartifact.ExtractExecutable(ctx, archive, asset, executable)
	must(t, e)
	exeBefore := fileDigest(t, executable)
	if exeBefore != report.ExecutableSHA256 {
		t.Fatal("actual executable pin mismatch")
	}
	bi, e := buildinfo.ReadFile(executable)
	must(t, e)
	var vcsRevision string
	var modified bool
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			vcsRevision = s.Value
		}
		if s.Key == "vcs.modified" {
			modified = s.Value != "false"
		}
	}
	if vcsRevision != sourceCommit || modified {
		t.Fatalf("producer build source mismatch: revision=%q modified=%v", vcsRevision, modified)
	}
	save(t, generation, "producer-build.json", bi)
	live := filepath.Join(t.TempDir(), "source")
	must(t, os.MkdirAll(live, 0700))
	cfg := map[string]any{"dataDir": ".", "dbPath": "usage.sqlite", "dataKeyPath": "data.key", "collectorMode": "http", "pollIntervalMs": 100, "usageArchiveRetentionEnabled": false, "accountActionsEnabled": false, "accountActionsAutoDisable": false}
	save(t, live, "config.json", cfg)
	var deliver, delivered atomic.Bool
	var disabled atomic.Bool
	var queueCalls atomic.Int64
	var preTransactionMainDigest string
	var preTransactionMainSize int64
	queue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer qualification-only-management-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/usage-queue":
			queueCalls.Add(1)
			if deliver.Load() && delivered.CompareAndSwap(false, true) {
				events := append([]json.RawMessage{}, p.Events...)
				events = append(events, p.Events[0])
				json.NewEncoder(w).Encode(events)
			} else {
				w.Write([]byte("[]"))
			}
		case "/v0/management/auth-files":
			json.NewEncoder(w).Encode(map[string]any{"files": []any{map[string]any{"id": "runtime-fixture-codex", "name": "fixture-codex.json", "auth_index": "fixture-codex", "provider": "codex", "disabled": disabled.Load()}}})
		case "/v0/management/config":
			w.Write([]byte(`{"usage-statistics-enabled":true,"redis-usage-queue-retention-seconds":60}`))
		case "/v0/management/usage-statistics-enabled":
			if r.Method != "PUT" {
				http.Error(w, "method", 405)
				return
			}
			w.Write([]byte(`{"ok":true}`))
		case "/v0/management/auth-files/status":
			if r.Method != "PATCH" {
				http.Error(w, "method", 405)
				return
			}
			disabled.Store(true)
			w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer queue.Close()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	must(t, e)
	addr := ln.Addr().String()
	must(t, ln.Close())
	base := "http://" + addr
	logFile, e := os.OpenFile(filepath.Join(generation, "producer.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(t, e)
	cmd := exec.CommandContext(ctx, executable)
	cmd.Dir = live
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Do not inherit host credentials, config discovery paths or real queues.
	cmd.Env = []string{"CPA_MANAGER_CONFIG=" + filepath.Join(live, "config.json"), "HTTP_ADDR=" + addr, "CPA_UPSTREAM_URL=" + queue.URL, "CPA_MANAGEMENT_KEY=qualification-only-management-key", "CPA_MANAGER_ADMIN_KEY=qualification-only-admin-key", "USAGE_COLLECTOR_MODE=http", "USAGE_POLL_INTERVAL_MS=100", "USAGE_QUOTA_COOLDOWN_ENABLED=true", "TZ=UTC"}
	started := time.Now()
	must(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			cmd.Process.Kill()
			<-done
		}
		logFile.Close()
	}()
	wait(t, func() bool {
		r, e := http.Get(base + "/usage-service/info")
		if e != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	// v1.14.4 reads the persisted Manager configuration for its collector.
	// The environment only supplies the bootstrap credential; this API write
	// records the exact disposable CPA binding through the old public handler.
	managerConfig := map[string]any{"config": map[string]any{
		"cpaConnection":        map[string]any{"cpaBaseUrl": queue.URL, "managementKey": "qualification-only-management-key"},
		"collector":            map[string]any{"enabled": true, "collectorMode": "http", "queue": "usage", "popSide": "right", "batchSize": 100, "pollIntervalMs": 100, "queryLimit": 50000},
		"externalUsageService": map[string]any{"enabled": false},
	}}
	configResponse := httpCall(t, "PUT", base, "/usage-service/config", managerConfig)
	must(t, os.WriteFile(filepath.Join(generation, "config-write.json"), configResponse, 0600))
	priceResponse := httpCall(t, "PUT", base, "/v0/management/model-prices", map[string]any{"prices": p.Prices})
	must(t, os.WriteFile(filepath.Join(generation, "price-write.json"), priceResponse, 0600))
	if caseID == bak01CaseID {
		// Capture the main-file state immediately before the controlled event
		// delivery. The committed event that follows must be supplied by WAL.
		transactionMain := filepath.Join(live, "usage.sqlite")
		preTransactionMainDigest = fileDigest(t, transactionMain)
		preTransactionMainSize = fileSize(t, transactionMain)
	}
	deliver.Store(true)
	wait(t, func() bool {
		r, e := http.Get(base + "/usage-service/info")
		if e != nil {
			return false
		}
		r.Body.Close()
		db := openDB(t, filepath.Join(live, "usage.sqlite"), true)
		defer db.Close()
		return scalar(t, db, "select count(*) from usage_events") == 4 && scalar(t, db, "select count(*) from usage_data_migrations where status != 'completed'") == 0
	})
	quotaResponse := httpCall(t, "POST", base, "/v0/management/quota-snapshots", p.Quota)
	wait(t, func() bool {
		db := openDB(t, filepath.Join(live, "usage.sqlite"), true)
		defer db.Close()
		return scalar(t, db, "select count(*) from quota_cooldowns") > 0
	})
	must(t, os.WriteFile(filepath.Join(generation, "quota-write.json"), quotaResponse, 0600))
	costs := map[string]monitoring.Response{}
	for _, name := range sortedModels(p) {
		b := httpCall(t, "POST", base, "/v0/management/monitoring/analytics", analyticsRequest(p, name))
		var response monitoring.Response
		must(t, json.Unmarshal(b, &response))
		if response.Summary == nil || response.Summary.TotalCalls != 1 {
			t.Fatalf("source analytics %s incomplete: %s", name, b)
		}
		assertClose(t, name, response.Summary.TotalCost, p.ExpectedUSD[name])
		costs[name] = response
	}
	save(t, generation, "v1-analytics.json", costs)
	if caseID == ret02CaseID {
		generateRET02Archive(t, base, live, generation, p, costs)
	}
	export := httpCall(t, "GET", base, "/v0/management/usage/export", nil)
	must(t, os.WriteFile(filepath.Join(generation, "v1-export.jsonl"), export, 0600))
	var bakReadDB *sql.DB
	var bakReadTx *sql.Tx
	if caseID == bak01CaseID {
		bakReadDB, e = sql.Open("sqlite", dbURI(filepath.Join(live, "usage.sqlite"), true))
		must(t, e)
		bakReadDB.SetMaxOpenConns(1)
		must(t, bakReadDB.PingContext(ctx))
		bakReadTx, e = bakReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		must(t, e)
		var count int64
		must(t, bakReadTx.QueryRowContext(ctx, "select count(*) from usage_events").Scan(&count))
		if count != 4 {
			t.Fatalf("BAK-01 read snapshot expected 4 committed events, got %d", count)
		}
	}
	must(t, cmd.Process.Signal(os.Interrupt))
	select {
	case err := <-done:
		reaped = true
		must(t, err)
	case <-ctx.Done():
		t.Fatal("producer did not reap")
	}
	finished := time.Now()
	must(t, logFile.Close())
	// Capture stopped files BEFORE SQLite checkpoint; checkpoint changes are
	// explicitly recorded and checked for logical history preservation.
	layout := maintenancebundle.Layout{DatabasePath: filepath.Join(live, "usage.sqlite"), DataKeyPath: filepath.Join(live, "data.key"), ConfigPath: filepath.Join(live, "config.json"), ArchivesPath: filepath.Join(live, "usage-archives"), RequireConfig: true}
	preCheckpoint, e := maintenancebundle.Inspect(ctx, layout)
	must(t, e)
	if caseID == bak01CaseID {
		generateBAK01SourceTail(t, bak01GenerationInput{
			ctx: ctx, root: root, generation: generation, live: live, layout: layout,
			preCheckpoint: preCheckpoint, profile: p, input: input, archive: archive,
			executableSHA256: exeBefore, producerBuildCommit: vcsRevision, generatorCommit: code,
			started: started, finished: finished, pid: cmd.Process.Pid, exitCode: cmd.ProcessState.ExitCode(),
			reaped: reaped, queueCalls: queueCalls.Load(), readDB: bakReadDB, readTx: bakReadTx,
			preTransactionMainDigest: preTransactionMainDigest, preTransactionMainSize: preTransactionMainSize,
		})
		return
	}
	db := openDB(t, layout.DatabasePath, false)
	before := snapshot(t, db)
	if caseID == ret02CaseID {
		checkRET02Archive(t, db, layout.ArchivesPath)
	} else {
		checkFullRaw(t, db)
	}
	if scalar(t, db, "select count(*) from account_quota_snapshots") < 2 {
		t.Fatal("required quota/reset evidence missing")
	}
	revision := query(t, db, "select value from settings where key='usage_cache_accounting_semantics_revision'")
	if len(revision) != 1 || revision[0]["value"] != "2" {
		t.Fatalf("historical revision 2 missing: %v", revision)
	}
	checks := sqliteChecks(t, db, true)
	assertPreserved(t, before, snapshot(t, db))
	must(t, db.Close())
	lock, e := processlock.AcquireExisting(layout.DatabasePath)
	must(t, e)
	sealed, e := maintenancebundle.Create(ctx, layout, filepath.Join(root, "bundle"), lock)
	must(t, e)
	must(t, lock.Close())
	sameInventory(t, sealed, inventory(t, filepath.Join(root, "bundle")))
	must(t, maintenancebundle.Revalidate(ctx, layout, sealed))
	after, e := legacyartifact.Verify(ctx, archive, asset)
	must(t, e)
	if after != report || fileDigest(t, executable) != exeBefore {
		t.Fatal("producer identity changed")
	}
	migrationState := querySourceState(t, layout.DatabasePath)
	save(t, generation, "source-snapshot.json", before)
	record := map[string]any{"caseID": caseID, "SourceRelease": "v1.14.4", "SourceCommit": sourceCommit, "SourceProducerKind": "published-artifact", "SourceProducerArtifact": map[string]any{"immutableReleaseID": 404941931, "assetID": 616064478, "asset": asset, "releaseInfoSHA256": releaseInfoDigest}, "SourceProducerArchiveSHA256": report.ArchiveSHA256, "SourceProducerExecutableSHA256": exeBefore, "SourceProducerBuildCodeCommit": vcsRevision, "GeneratorCodeCommit": code, "PlanCommit": plan, "GenerationProfile": p.Profile, "GenerationProcedure": "README.md: isolated Linux namespace; published executable; HTTP collector duplicate + price/quota API; SIGINT/reap; explicit checkpoint; maintenancebundle.Create; digest revalidation", "ControlledInputSHA256": digestBytes(input), "process": map[string]any{"pid": cmd.Process.Pid, "arguments": []string{}, "environment": safeEnvironment(cmd.Env), "startedAt": started.UTC(), "finishedAt": finished.UTC(), "exitCode": cmd.ProcessState.ExitCode(), "reaped": reaped, "queueCalls": queueCalls.Load(), "deliveredPayloads": 5, "expectedUniqueEvents": 4}, "producerBuildIdentity": "producer-build.json", "harnessToolchain": platformIdentity(), "StoppedBeforeCheckpointInventory": preCheckpoint, "SQLite": checks, "GeneratedBundleSHA256": sealed.SourceRevision, "bundleInventory": sealed, "migrationState": migrationState, "sourceVariant": "full raw; no archives and no deletion; cache/quota completed", "generationSuccess": true, "casePass": false, "UpgradeAuthorized": false, "TrustedHopReceipt": false}
	hashes := map[string]string{}
	if caseID == ret02CaseID {
		record["sourceVariant"] = "archived + no raw deletion; cache/quota completed"
		record["GenerationProcedure"] = "published exact v1.14.4 executable; isolated collector duplicate + price/quota API; archive HTTP create/resume/verify; no delete; SIGINT/reap; checkpoint; maintenancebundle.Create; digest revalidation"
		for _, name := range ret02GenerationFiles {
			hashes[name] = fileDigest(t, filepath.Join(generation, name))
		}
	}
	for _, name := range []string{"controlled-input.json", "release-info.json", "producer-build.json", "producer.log", "config-write.json", "price-write.json", "quota-write.json", "v1-analytics.json", "v1-export.jsonl", "source-snapshot.json"} {
		hashes[name] = fileDigest(t, filepath.Join(generation, name))
	}
	record["GenerationEvidenceFiles"] = hashes
	generationDigest := save(t, generation, "record.json", record)
	save(t, root, "generation-index.json", map[string]any{"location": "source-generation/record.json", "SHA256": generationDigest, "bundleDigest": sealed.SourceRevision, "generationSuccess": true, "casePass": false})
	t.Log("source generation succeeded; case acceptance remains pending")
}

func safeEnvironment(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			out = append(out, item)
			continue
		}
		value := "<redacted>"
		switch key {
		case "CPA_MANAGER_CONFIG":
			value = "<task-scratch>/source/config.json"
		case "HTTP_ADDR":
			value = "<private-listener>"
		case "CPA_UPSTREAM_URL":
			value = "<private-http-queue>"
		case "CPA_MANAGEMENT_KEY", "CPA_MANAGER_ADMIN_KEY":
			value = "<qualification-only-secret>"
		case "USAGE_COLLECTOR_MODE", "USAGE_POLL_INTERVAL_MS", "USAGE_QUOTA_COOLDOWN_ENABLED", "TZ":
			value = item[len(key)+1:]
		}
		out = append(out, key+"="+value)
	}
	return out
}

func querySourceState(t *testing.T, path string) any {
	db := openDB(t, path, true)
	defer db.Close()
	return query(t, db, "select * from usage_data_migrations order by name")
}
