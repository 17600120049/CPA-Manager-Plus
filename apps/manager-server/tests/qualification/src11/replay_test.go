package src11

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	usagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type generationRecord struct {
	CaseID                         string `json:"caseID"`
	SourceRelease                  string
	SourceCommit                   string
	SourceProducerKind             string
	SourceProducerArchiveSHA256    string
	SourceProducerExecutableSHA256 string
	SourceProducerBuildCodeCommit  string
	GeneratorCodeCommit            string
	PlanCommit                     string
	ControlledInputSHA256          string
	GeneratedBundleSHA256          string
	GenerationEvidenceFiles        map[string]string
	BundleInventory                maintenancebundle.Manifest `json:"bundleInventory"`
	Process                        struct {
		PID       int       `json:"pid"`
		ExitCode  int       `json:"exitCode"`
		Reaped    bool      `json:"reaped"`
		Started   time.Time `json:"startedAt"`
		Finished  time.Time `json:"finishedAt"`
		Delivered int       `json:"deliveredPayloads"`
	} `json:"process"`
	GenerationSuccess bool `json:"generationSuccess"`
	CasePass          bool `json:"casePass"`
	UpgradeAuthorized bool
	TrustedHopReceipt bool
}

// Revalidation is qualification integrity checking, never an authority grant.
// An independent verifier must also compare the committed runner/procedure and
// actual source-generation execution log (or regenerate from the pinned asset).
func revalidate(t *testing.T, root, archive string) generationRecord {
	t.Helper()
	ctx := context.Background()
	var index struct {
		Location     string `json:"location"`
		SHA256       string `json:"SHA256"`
		BundleDigest string `json:"bundleDigest"`
	}
	must(t, json.Unmarshal(read(t, filepath.Join(root, "generation-index.json")), &index))
	if index.Location != "source-generation/record.json" || fileDigest(t, filepath.Join(root, index.Location)) != index.SHA256 {
		t.Fatal("generation evidence digest mismatch")
	}
	var r generationRecord
	must(t, json.Unmarshal(read(t, filepath.Join(root, index.Location)), &r))
	if r.CaseID != "SRC-11" || r.SourceRelease != "v1.14.4" || r.SourceCommit != sourceCommit || r.SourceProducerBuildCodeCommit != sourceCommit || r.SourceProducerKind != "published-artifact" || r.PlanCommit != planCommit || len(r.GeneratorCodeCommit) != 40 || r.CasePass || r.UpgradeAuthorized || r.TrustedHopReceipt || !r.GenerationSuccess {
		t.Fatal("invalid source-generation chain/boundary")
	}
	if !r.Process.Reaped || r.Process.PID <= 0 || r.Process.ExitCode != 0 || !r.Process.Finished.After(r.Process.Started) || r.Process.Delivered != 5 {
		t.Fatal("generation process/exit evidence incomplete")
	}
	expected := []string{"controlled-input.json", "release-info.json", "producer-build.json", "producer.log", "config-write.json", "price-write.json", "quota-write.json", "v1-analytics.json", "v1-export.jsonl", "source-snapshot.json"}
	if len(r.GenerationEvidenceFiles) != len(expected) {
		t.Fatal("generation evidence inventory incomplete")
	}
	for _, name := range expected {
		if fileDigest(t, filepath.Join(root, "source-generation", name)) != r.GenerationEvidenceFiles[name] {
			t.Fatalf("generation evidence %s changed", name)
		}
	}
	if fileDigest(t, filepath.Join(root, "source-generation", "release-info.json")) != releaseInfoDigest {
		t.Fatal("release-info mismatch")
	}
	pin, e := legacyartifact.Verify(ctx, archive, asset)
	must(t, e)
	if r.SourceProducerArchiveSHA256 != pin.ArchiveSHA256 || r.SourceProducerExecutableSHA256 != pin.ExecutableSHA256 {
		t.Fatal("archive/executable identities mismatch")
	}
	p, input := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	_, contract := loadProfile(t, envRequired(t, "CPAMP_SRC11_PROFILE"))
	if digestBytes(input) != digestBytes(contract) || r.ControlledInputSHA256 != digestBytes(contract) || p.Profile == "" {
		t.Fatal("input does not match committed generation profile")
	}
	m, e := maintenancebundle.Verify(ctx, filepath.Join(root, "bundle"))
	must(t, e)
	sameInventory(t, m, r.BundleInventory)
	if m.SourceRevision != r.GeneratedBundleSHA256 || index.BundleDigest != m.SourceRevision {
		t.Fatal("bundle/generation binding mismatch")
	}
	must(t, maintenancebundle.ValidateMatchingEvidence(ctx, maintenancebundle.BundleLayout(filepath.Join(root, "bundle"))))
	return r
}

func TestReplayExactSource(t *testing.T) {
	root := os.Getenv("CPAMP_SRC11_EVIDENCE")
	if root == "" {
		t.Skip("opt-in sealed exact-source fixture replay")
	}
	r := revalidate(t, root, envRequired(t, "CPAMP_SRC11_ARCHIVE"))
	code := codeIdentity(t)
	p, _ := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	ctx := context.Background()
	sealed := inventory(t, filepath.Join(root, "bundle"))
	output := envRequired(t, "CPAMP_SRC11_REPLAY_OUTPUT")
	if _, e := os.Lstat(output); !os.IsNotExist(e) {
		t.Fatal("replay output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))
	// Restore into a NEW task-owned root. Never open/migrate the sealed fixture.
	restored, e := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), filepath.Join(t.TempDir(), "replay"))
	must(t, e)
	restoredBefore, e := maintenancebundle.Inspect(ctx, restored)
	must(t, e)
	sameInventory(t, sealed, restoredBefore)
	db := openDB(t, restored.DatabasePath, true)
	before := snapshot(t, db)
	checkFullRaw(t, db)
	must(t, db.Close())
	rawSnapshot := read(t, filepath.Join(root, "source-generation", "source-snapshot.json"))
	var original any
	must(t, json.Unmarshal(rawSnapshot, &original))
	b, e := json.Marshal(before)
	must(t, e)
	var observed any
	must(t, json.Unmarshal(b, &observed))
	b, e = json.Marshal(observed)
	must(t, e)
	want, e := json.Marshal(original)
	must(t, e)
	if !bytes.Equal(b, want) {
		t.Fatal("sealed bundle differs from generation logical snapshot")
	}
	key, created, e := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	must(t, e)
	if created {
		t.Fatal("replay must use source data.key")
	}
	protector, e := security.NewProtector(key)
	must(t, e)
	st, e := store.Open(restored.DatabasePath, protector)
	must(t, e)
	defer st.Close()
	// Exercise existing data migration with bounded committed batches. No public
	// Upgrade API, writable startup admission or trusted receipt is introduced.
	states := []any{}
	for i := 0; i < 100; i++ {
		state, e := st.DiscoverUsageCacheAccounting(ctx)
		must(t, e)
		states = append(states, state)
		if state.Status == "completed" {
			break
		}
		batch, e := st.RunUsageCacheAccountingBatch(ctx, 2)
		must(t, e)
		states = append(states, batch)
		if i == 99 {
			t.Fatal("cache migration did not complete")
		}
	}
	for i := 0; i < 100; i++ {
		b, e := st.BackfillLegacyQuotaSnapshotsBatch(ctx, 2)
		must(t, e)
		if b.Completed {
			break
		}
		if i == 99 {
			t.Fatal("quota migration did not complete")
		}
	}
	for i := 0; i < 100; i++ {
		n, e := st.BackfillUsageResponseMetadata(ctx, 2)
		must(t, e)
		if n == 0 {
			break
		}
		if i == 99 {
			t.Fatal("metadata migration did not complete")
		}
	}
	must(t, st.RunDerivedStartupMaintenance(ctx))
	// Reuse the production retained, hourly and pricing projections until caught up.
	for i := 0; i < 100; i++ {
		a, e := st.CatchUpUsageMonitoringProjection(ctx, 2, p.ToMS)
		must(t, e)
		b, e := st.CatchUpUsageHourlyAggregate(ctx, 2, p.ToMS)
		must(t, e)
		c, e := st.CatchUpUsagePricing(ctx, 2, p.ToMS)
		must(t, e)
		_, e = st.CatchUpAccountHistoryRollups(ctx, 2, p.ToMS)
		must(t, e)
		if !a.Pending && !b.Pending && !c.Pending {
			break
		}
		if i == 99 {
			t.Fatal("derived projections did not become ready")
		}
	}
	db = openDB(t, restored.DatabasePath, true)
	after := snapshot(t, db)
	assertPreserved(t, before, after)
	checkFullRaw(t, db)
	if scalar(t, db, "select count(*) from usage_events") != scalar(t, db, "select count(distinct event_hash) from usage_events") {
		t.Fatal("event dedupe unstable")
	}
	devin := query(t, db, "select input_tokens,cache_read_tokens,cache_creation_tokens,total_tokens,cache_input_mode,normalized_uncached_input_tokens,normalized_total_input_tokens,normalized_cache_read_tokens,normalized_cache_creation_tokens from usage_events where request_id='src11-devin-mixed'")
	if len(devin) != 1 {
		t.Fatal("Devin evidence missing")
	}
	d := devin[0]
	for k, want := range map[string]any{"input_tokens": int64(1000), "cache_read_tokens": int64(900), "cache_creation_tokens": int64(50), "total_tokens": int64(1150), "cache_input_mode": "read_included_creation_separate", "normalized_uncached_input_tokens": int64(100), "normalized_total_input_tokens": int64(1050), "normalized_cache_read_tokens": int64(900), "normalized_cache_creation_tokens": int64(50)} {
		if d[k] != want {
			t.Fatalf("Devin revision2 %s=%v != %v", k, d[k], want)
		}
	}
	if scalar(t, db, "select count(*) from usage_events where session_id='src11-session'") < 2 || scalar(t, db, "select count(*) from usage_events where response_metadata_json is not null and response_metadata_json != ''") < 2 {
		t.Fatal("metadata fixture vacuous")
	}
	if scalar(t, db, "select count(*) from account_quota_snapshots") < 2 {
		t.Fatal("quota fixture vacuous")
	}
	must(t, db.Close())
	var v1 map[string]monitoring.Response
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "v1-analytics.json")), &v1))
	svc := monitoring.New(st, true)
	v2 := map[string]monitoring.Response{}
	for _, name := range sortedModels(p) {
		response, e := svc.Analytics(ctx, analyticsRequest(p, name))
		must(t, e)
		if response.Summary == nil || response.Summary.TotalCalls != 1 || v1[name].Summary == nil {
			t.Fatal("price evidence missing")
		}
		assertClose(t, name, response.Summary.TotalCost, p.ExpectedUSD[name])
		assertClose(t, name, response.Summary.TotalCost, v1[name].Summary.TotalCost)
		v2[name] = response
	}
	// A real sync attempt must preserve historical manual pricing, including zero
	// and context tiers. Synthetic candidates here test authority, not provenance.
	prices, e := st.LoadModelPrices(ctx)
	must(t, e)
	candidates := map[string]store.ModelPrice{}
	for k := range prices {
		candidates[k] = store.ModelPrice{Prompt: 999, Completion: 999, Source: "qualification-sync-candidate"}
	}
	syncResult, e := st.UpsertSyncedModelPrices(ctx, candidates)
	must(t, e)
	pricesAfter, e := st.LoadModelPrices(ctx)
	must(t, e)
	a, e := json.Marshal(prices)
	must(t, e)
	b, e = json.Marshal(pricesAfter)
	must(t, e)
	if !bytes.Equal(a, b) || syncResult.Imported != 0 {
		t.Fatal("manual prices lost authority")
	}
	var exported bytes.Buffer
	usageService := usagesvc.New(st, usagesvc.WithArchive(usagesvc.ArchiveConfig{Directory: restored.ArchivesPath, SegmentEventLimit: 2, DeleteBatchSize: 2, AggregateReadsEnabled: true}))
	must(t, usageService.WriteFullExport(ctx, &exported))
	for _, forbidden := range []string{"\"raw_json\"", "\"fail_body\"", "qualification-redaction-sentinel", "qualification local troubleshooting body"} {
		if strings.Contains(exported.String(), forbidden) {
			t.Fatalf("export exposed %s", forbidden)
		}
	}
	for _, row := range after["usage_events"] {
		if strings.Contains(row["raw_json"].(string), "qualification-redaction-sentinel") {
			t.Fatal("UsageEvents raw JSON was not sanitized")
		}
	}
	// Replay historical JSONL through the maintained importer; it must skip all
	// existing hashes and preserve the authoritative snapshots.
	imported, _, e := usageService.Import(ctx, bytes.NewReader(read(t, filepath.Join(root, "source-generation", "v1-export.jsonl"))))
	must(t, e)
	if imported.Added != 0 || imported.Skipped != 4 {
		t.Fatalf("duplicate replay: %#v", imported)
	}
	db = openDB(t, restored.DatabasePath, true)
	assertPreserved(t, before, snapshot(t, db))
	must(t, db.Close())
	save(t, output, "sem-full-raw.json", map[string]any{"SEM-01": "pass", "SEM-02": "pass", "SEM-03": "pass", "SEM-04": "pass", "SEM-05": "pass", "SEM-06": "pass", "SEM-07": "pass", "SEM-08": "pass", "before": before, "after": after, "devinRevision2": devin, "v1Costs": v1, "v2Costs": v2, "manualPriceSync": syncResult, "duplicateReplay": imported, "currency": "USD; this profile has no persisted conversion setting"})
	// Full-raw SOURCE stays sealed. SEM-09 is exercised by a clearly labelled v2
	// retention branch, with archive publication/verification before raw deletion.
	archiveStatus, e := usageService.CreateRetentionArchive(ctx, p.ToMS)
	must(t, e)
	verified, e := usageService.VerifyArchive(ctx, archiveStatus.Run.ID)
	must(t, e)
	deleted, e := usageService.DeleteArchive(ctx, archiveStatus.Run.ID)
	must(t, e)
	if verified.Run.Status != "verified" || deleted.Run.Status != "completed" || deleted.Run.DeletedEventCount != 4 {
		t.Fatalf("retention evidence incomplete: %#v", deleted.Run)
	}
	coverage, e := svc.Analytics(ctx, analyticsRequest(p, ""))
	must(t, e)
	if coverage.Coverage == nil || coverage.Coverage.RawComplete || coverage.Coverage.RawDeletedEventCount != 4 || coverage.Summary == nil || coverage.Summary.TotalCalls != 4 {
		t.Fatalf("SEM-09 raw coverage: %#v", coverage)
	}
	total := float64(0)
	for _, c := range p.ExpectedUSD {
		total += c
	}
	assertClose(t, "retained history", coverage.Summary.TotalCost, total)
	db = openDB(t, restored.DatabasePath, true)
	if scalar(t, db, "select count(*) from usage_events") != 0 || scalar(t, db, "select count(*) from usage_monitoring_event_projection_v1") != 4 {
		t.Fatal("retained history incomplete after raw deletion")
	}
	must(t, db.Close())
	save(t, output, "sem09-retention-branch.json", map[string]any{"SEM-09": "pass", "sourceVariantUnchanged": "full raw", "branch": "v2 primitive descendant of SRC-11; not RET-03 source-generation evidence", "archived": archiveStatus, "verified": verified, "deleted": deleted, "analytics": coverage})
	must(t, st.Close())
	db = openDB(t, restored.DatabasePath, false)
	checks := sqliteChecks(t, db, true)
	must(t, db.Close())
	afterMigration, e := maintenancebundle.Inspect(ctx, restored)
	must(t, e)
	lock, e := processlock.Acquire(restored.DatabasePath)
	must(t, e)
	backup, e := maintenancebundle.Create(ctx, restored, filepath.Join(output, "retention-backup"), lock)
	must(t, e)
	must(t, lock.Close())
	_, e = maintenancebundle.RestoreNew(ctx, filepath.Join(output, "retention-backup"), filepath.Join(t.TempDir(), "retention-restore"))
	must(t, e)
	// Wrong key, modified archive and added sidecars must not pass revalidation.
	testTamperedInventory(t, root)
	sameInventory(t, sealed, inventory(t, filepath.Join(root, "bundle")))
	revalidate(t, root, envRequired(t, "CPAMP_SRC11_ARCHIVE"))
	save(t, output, "result.json", map[string]any{"caseID": "SRC-11", "runtimeAssertions": "pass", "independentAcceptance": "pending", "SourceGeneratorCodeCommit": r.GeneratorCodeCommit, "VerifierCodeCommit": code, "SourceBundleDigest": sealed.SourceRevision, "BeforeDigest": restoredBefore.SourceRevision, "AfterDigest": afterMigration.SourceRevision, "SourceAfterDigest": inventory(t, filepath.Join(root, "bundle")).SourceRevision, "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation", "record.json")), "migrationStates": states, "SQLite": checks, "retentionBackupDigest": backup.SourceRevision, "platform": platformIdentity(), "UpgradeAuthorized": false, "TrustedHopReceipt": false, "other48Cases": "pending", "Phase4A07Complete": false, "Phase4AComplete": false})
}

func testTamperedInventory(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	l, e := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), filepath.Join(t.TempDir(), "tamper"))
	must(t, e)
	baseline, e := maintenancebundle.Inspect(ctx, l)
	must(t, e)
	key := read(t, l.DataKeyPath)
	key[0] ^= 1
	must(t, os.WriteFile(l.DataKeyPath, key, 0600))
	if maintenancebundle.Revalidate(ctx, l, baseline) == nil {
		t.Fatal("changed key accepted")
	}
	must(t, os.WriteFile(l.DataKeyPath, read(t, filepath.Join(root, "bundle", "data-key")), 0600))
	must(t, os.WriteFile(l.DatabasePath+"-journal", []byte("nonempty unqualified journal"), 0600))
	if maintenancebundle.ValidateMatchingEvidence(ctx, l) == nil {
		t.Fatal("nonempty journal accepted")
	}
}
