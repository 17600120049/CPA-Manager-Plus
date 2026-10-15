package src11

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagepricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageprojection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	usagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	ret07CaseID     = "RET-07"
	ret07PlanCommit = "21035045c425e453bd8f3007619fc0734861012e"
	ret07Profile    = "ret-07-retained-pricing-source-complete-v1"
)

var ret07GenerationFiles = []string{
	"archive-flow.json", "archive-readiness.json", "pre-archive-snapshot.json",
	"pre-delete-snapshot.json", "post-delete-snapshot.json", "archive-inventory.json",
	"raw-event-inventory.json", "retained-pricing-source.json", "price-authority-snapshot.json",
	"structural-price-change.json", "historical-reconciliation.json", "v1-archived-analytics.json",
}

// generateRET07Archive is an independent source-generation path. It reuses
// the accepted production archive/delete primitives, then changes the model
// price structure through the public price writer while the deleted events are
// available only from the retained projection.
func generateRET07Archive(t *testing.T, base, live, generation string, p profile, costs map[string]monitoring.Response) {
	t.Helper()
	var readiness usagesvc.MaintenanceStatus
	wait(t, func() bool {
		must(t, json.Unmarshal(httpCall(t, "GET", base, "/v0/management/usage/maintenance", nil), &readiness))
		return readiness.RawEventCount == 4 && readiness.MigrationCoverage.Complete && readiness.HourlyAggregateCoverage.Complete
	})
	save(t, generation, "archive-readiness.json", readiness)
	db := openDB(t, filepath.Join(live, "usage.sqlite"), true)
	preArchive := snapshot(t, db)
	checkFullRaw(t, db)
	must(t, db.Close())
	save(t, generation, "pre-archive-snapshot.json", preArchive)

	createdBytes := httpCall(t, "POST", base, "/v0/management/usage/archives", map[string]any{"cutoff_timestamp_ms": p.ToMS})
	var created usagesvc.ArchiveStatusSummary
	must(t, json.Unmarshal(createdBytes, &created))
	path := "/v0/management/usage/archives/" + created.Run.ID
	var archived, verified, deleted usagesvc.ArchiveStatusSummary
	must(t, json.Unmarshal(httpCall(t, "POST", base, path+"/resume?wait=true&expected_stage=archiving", nil), &archived))
	must(t, json.Unmarshal(httpCall(t, "POST", base, path+"/verify?wait=true", nil), &verified))
	if created.Run.ID == "" || archived.Run.Status != "archived" || verified.Run.Status != "verified" || verified.Run.DeletedEventCount != 0 {
		t.Fatalf("RET-07 archive did not reach verified pre-delete boundary: created=%#v archived=%#v verified=%#v", created.Run, archived.Run, verified.Run)
	}
	db = openDB(t, filepath.Join(live, "usage.sqlite"), true)
	preDelete := snapshot(t, db)
	checkRET03PreDelete(t, db)
	must(t, db.Close())
	save(t, generation, "pre-delete-snapshot.json", preDelete)
	must(t, json.Unmarshal(httpCall(t, "POST", base, path+"/delete?wait=true", nil), &deleted))
	if deleted.Run.Status != "completed" || deleted.Run.DeletedEventCount != 4 {
		t.Fatalf("RET-07 raw deletion did not complete: %#v", deleted.Run)
	}
	save(t, generation, "archive-flow.json", map[string]any{
		"producer": "published exact v1.14.4", "automaticRetentionEnabled": false,
		"actions": []string{"create", "resume?wait=true&expected_stage=archiving", "verify?wait=true", "delete?wait=true"},
		"create":  created, "resume": archived, "verify": verified, "delete": deleted,
		"deleteCalled": true, "deletionMethod": "production DeleteArchive/DeleteBatch",
	})

	db = openDB(t, filepath.Join(live, "usage.sqlite"), true)
	archiveState := checkRET07Archive(t, db, filepath.Join(live, "usage-archives"))
	postDelete := snapshot(t, db)
	assertRET03Preserved(t, preDelete, postDelete, archiveState)
	beforeRevision := scalarString(t, db, "select structure_revision from usage_pricing_rollup_state where rollup_name='pricing_v1'")
	beforePrices := query(t, db, "select model,prompt_per_1m,completion_per_1m,cache_per_1m,cache_read_per_1m,cache_creation_per_1m,prompt_configured,completion_configured,cache_read_configured,cache_creation_configured,source,source_model_id from model_prices order by model")
	must(t, db.Close())
	save(t, generation, "post-delete-snapshot.json", postDelete)
	save(t, generation, "archive-inventory.json", archiveState)
	save(t, generation, "raw-event-inventory.json", map[string]any{
		"beforeRawCount": 4, "deletedCount": 4, "afterRawCount": 0,
		"onlineRawEventIDsAfter": []any{}, "archiveEventCount": archiveState["archiveEventCount"],
		"retainedProjectionCount": archiveState["retainedProjectionCount"], "rawComplete": false,
	})

	changed := make(map[string]model.ModelPrice, len(p.Prices))
	for name, price := range p.Prices {
		changed[name] = price
	}
	paid := changed["fixture-paid-ret07"]
	// Move the historical paid event into an actual context band. The band
	// preserves its authoritative rates while the new default doubles them.
	// Ignoring the retained input tokens or falling back to base must now fail.
	paid.Prompt, paid.Completion = 4, 16
	paid.ContextTiers = []model.ModelPriceContextTier{{
		ThresholdTokens: 50, Prompt: 2, Completion: 8,
		PromptConfigured: true, CompletionConfigured: true,
	}}
	changed["fixture-paid-ret07"] = paid
	priceResponse := httpCall(t, "PUT", base, "/v0/management/model-prices", map[string]any{"prices": changed})

	db = openDB(t, filepath.Join(live, "usage.sqlite"), true)
	var priceRows []map[string]any
	wait(t, func() bool {
		priceRows = query(t, db, "select model,prompt_per_1m,completion_per_1m,cache_per_1m,cache_read_per_1m,cache_creation_per_1m,prompt_configured,completion_configured,cache_read_configured,cache_creation_configured,source,source_model_id from model_prices order by model")
		state := query(t, db, "select status,structure_revision,coverage_event_id,target_event_id from usage_pricing_rollup_state where rollup_name='pricing_v1'")
		return len(state) == 1 && state[0]["status"] == "ready" && state[0]["coverage_event_id"] == state[0]["target_event_id"] && state[0]["structure_revision"] != beforeRevision
	})
	afterRevision := scalarString(t, db, "select structure_revision from usage_pricing_rollup_state where rollup_name='pricing_v1'")
	if beforeRevision == afterRevision {
		t.Fatal("RET-07 structural price change did not create a new pricing revision")
	}
	priceAuthority := map[string]any{
		"beforeRevision": beforeRevision, "afterRevision": afterRevision,
		"beforePrices": beforePrices, "afterPrices": priceRows,
		"manualAuthorityModels": []string{"fixture-manual-ret07", "fixture-paid-ret07", "fixture-free-ret07"},
		"explicitZeroModel":     "fixture-free-ret07", "unknownModel": "fixture-unknown-ret07",
	}
	save(t, generation, "price-authority-snapshot.json", priceAuthority)
	save(t, generation, "structural-price-change.json", map[string]any{
		"kind": "context-tier-structure", "model": "fixture-paid-ret07",
		"oldContextTiers": p.Prices["fixture-paid-ret07"].ContextTiers,
		"newContextTiers": changed["fixture-paid-ret07"].ContextTiers,
		"writer":          "PUT /v0/management/model-prices", "response": json.RawMessage(priceResponse),
		"beforeRevision": beforeRevision, "afterRevision": afterRevision,
		"historicalEventsCrossingNewTier": 1,
		"matchedHistoricalEventIDs":       []int{2},
		"baseFallbackUSD":                 0.00056, "historicalTierUSD": p.ExpectedUSD["fixture-paid-ret07"],
	})
	tierEvidence := ret07TierEvidence(t, db)

	reconciliation := map[string]any{}
	for _, name := range sortedModels(p) {
		var response monitoring.Response
		must(t, json.Unmarshal(httpCall(t, "POST", base, "/v0/management/monitoring/analytics", analyticsRequest(p, name)), &response))
		if response.Summary == nil || response.Coverage == nil || response.Coverage.RawComplete || response.Coverage.RawDeletedEventCount != 4 {
			t.Fatalf("RET-07 historical query coverage is dishonest for %s: %s", name, responseBytes(response))
		}
		assertClose(t, name, response.Summary.TotalCost, costs[name].Summary.TotalCost)
		reconciliation[name] = map[string]any{
			"beforeStructuralChange":          costs[name].Summary.TotalCost,
			"afterStructuralChangeHistorical": response.Summary.TotalCost,
			"currentProjection":               response.Summary.TotalCost,
			"calls":                           response.Summary.TotalCalls,
		}
	}
	var aggregate monitoring.Response
	must(t, json.Unmarshal(httpCall(t, "POST", base, "/v0/management/monitoring/analytics", analyticsRequest(p, "")), &aggregate))
	if aggregate.Summary == nil || aggregate.Summary.TotalCalls != 4 || aggregate.Coverage == nil || aggregate.Coverage.RawComplete {
		t.Fatal("RET-07 aggregate reconciliation did not retain honest deleted coverage")
	}
	var expected, actual float64
	for _, price := range p.ExpectedUSD {
		expected += price
	}
	actual = aggregate.Summary.TotalCost
	assertClose(t, "aggregate", actual, expected)
	reconciliation["aggregate"] = map[string]any{"expectedHistorical": expected, "actualHistorical": actual, "currentProjection": actual, "reconciled": true, "baseFallbackCounterfactual": expected + 0.00028}
	reconciliation["tierSelection"] = tierEvidence
	save(t, generation, "historical-reconciliation.json", reconciliation)
	save(t, generation, "v1-archived-analytics.json", map[string]any{"beforeStructuralChange": costs, "afterStructuralChange": reconciliation})
	save(t, generation, "retained-pricing-source.json", retainedPricingEvidence(t, db, afterRevision, priceRows))
	must(t, db.Close())
}

func scalarString(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var value string
	must(t, db.QueryRow(q).Scan(&value))
	return value
}

func checkRET07Archive(t *testing.T, db *sql.DB, archives string) map[string]any {
	t.Helper()
	state := checkRET03Archive(t, db, archives)
	if state["rawEventCount"] != 0 || state["archiveEventCount"] != 4 || state["retainedProjectionCount"] != 4 {
		t.Fatalf("RET-07 archive state is incomplete: %#v", state)
	}
	state["variant"] = "archived + raw physically deleted + retained pricing source complete"
	state["rawComplete"] = false
	state["retainedPricingSourceComplete"] = true
	return state
}

func retainedPricingEvidence(t *testing.T, db *sql.DB, revision string, priceRows []map[string]any) map[string]any {
	t.Helper()
	expectedRows := query(t, db, `select raw_event_id
		from usage_archive_event_refs where raw_deleted_at_ms is not null order by raw_event_id`)
	expected := make([]any, 0, len(expectedRows))
	for _, row := range expectedRows {
		expected = append(expected, row["raw_event_id"])
	}
	events := query(t, db, `select archived.raw_event_id, archived.event_hash, p.model, p.provider,
 p.source, p.source_hash, p.account_snapshot, p.event_id
 from usage_archive_event_refs archived join usage_monitoring_event_projection_v1 p
 on p.event_id=archived.raw_event_id where archived.raw_deleted_at_ms is not null order by archived.raw_event_id`)
	if len(events) != 4 {
		t.Fatalf("RET-07 retained pricing source event coverage=%d", len(events))
	}
	priceByModel := make(map[string]map[string]any, len(priceRows))
	for _, row := range priceRows {
		modelName, ok := row["model"].(string)
		if !ok || modelName == "" {
			t.Fatalf("RET-07 retained pricing source has an invalid price identity: %#v", row)
		}
		priceByModel[modelName] = row
	}
	covered := make([]any, 0, len(events))
	manual := []any{}
	explicitZero := []any{}
	unknown := []any{}
	coveredIDs := make(map[string]any, len(events))
	duplicates := []any{}
	for _, event := range events {
		modelName, ok := event["model"].(string)
		if !ok || modelName == "" {
			t.Fatalf("RET-07 retained pricing source event has no model identity: %#v", event)
		}
		eventID := event["raw_event_id"]
		idKey := fmt.Sprintf("%v", eventID)
		if _, exists := coveredIDs[idKey]; exists {
			duplicates = append(duplicates, eventID)
		}
		coveredIDs[idKey] = eventID
		priceRow, hasPrice := priceByModel[modelName]
		pricingAuthority := "unknown"
		pricingStatus := "unknown-unpriced"
		priceIdentity := map[string]any{
			"model":               modelName,
			"provider":            event["provider"],
			"pricingRevision":     revision,
			"source":              "",
			"sourceModelID":       "",
			"configured":          false,
			"allFieldsConfigured": false,
			"configuredFields":    map[string]any{},
			"explicitZero":        false,
			"unknownUnpriced":     true,
			"rates":               map[string]any{},
		}
		if hasPrice {
			source, _ := priceRow["source"].(string)
			sourceModelID, _ := priceRow["source_model_id"].(string)
			allFieldsConfigured := pricingRowConfigured(priceRow)
			zero := allFieldsConfigured && pricingRowRatesAreZero(priceRow)
			if source != "" {
				pricingAuthority = source
				pricingStatus = "priced"
				if zero {
					pricingStatus = "explicit-zero"
					explicitZero = append(explicitZero, eventID)
				}
				if source == "manual" {
					manual = append(manual, eventID)
				}
			}
			priceIdentity = map[string]any{
				"model":               modelName,
				"provider":            event["provider"],
				"pricingRevision":     revision,
				"source":              source,
				"sourceModelID":       sourceModelID,
				"configured":          pricingRowHasAnyConfigured(priceRow),
				"allFieldsConfigured": allFieldsConfigured,
				"configuredFields":    pricingRowConfiguredFields(priceRow),
				"explicitZero":        zero,
				"unknownUnpriced":     pricingStatus == "unknown-unpriced",
				"rates":               pricingRowRates(priceRow),
			}
		} else {
			unknown = append(unknown, eventID)
		}
		if pricingStatus == "unknown-unpriced" && hasPrice {
			unknown = append(unknown, eventID)
		}
		covered = append(covered, map[string]any{
			"eventID":                   eventID,
			"eventHash":                 event["event_hash"],
			"model":                     modelName,
			"provider":                  event["provider"],
			"source":                    event["source"],
			"sourceHash":                event["source_hash"],
			"pricingAuthority":          pricingAuthority,
			"pricingStatus":             pricingStatus,
			"priceIdentity":             priceIdentity,
			"retainedProjectionEventID": event["event_id"],
		})
	}
	missing := make([]any, 0)
	for _, eventID := range expected {
		if _, ok := coveredIDs[fmt.Sprintf("%v", eventID)]; !ok {
			missing = append(missing, eventID)
		}
	}
	complete := len(expected) > 0 && len(missing) == 0 && len(duplicates) == 0 && len(covered) == len(expected)
	return map[string]any{
		"contextTierRows": query(t, db, "select * from model_price_context_tiers order by model,threshold_tokens"),
		"pricingRevision": revision, "priceRows": priceRows,
		"expectedHistoricalEvents": expected, "coveredPricingEvents": covered,
		"missingPricingEvents": missing, "retainedPricingSourceComplete": complete,
		"manualPriceAuthorityEvents": manual, "explicitZeroEvents": explicitZero,
		"unknownUnpricedEvents": unknown, "duplicateEventIDs": duplicates,
	}
}

func pricingRowConfigured(row map[string]any) bool {
	fields := pricingRowConfiguredFields(row)
	return fields["prompt"] && fields["completion"] && fields["cacheRead"] && fields["cacheCreation"]
}

func pricingRowHasAnyConfigured(row map[string]any) bool {
	for _, configured := range pricingRowConfiguredFields(row) {
		if configured {
			return true
		}
	}
	return false
}

func pricingRowConfiguredFields(row map[string]any) map[string]bool {
	return map[string]bool{
		"prompt":        configuredValue(row["prompt_configured"]),
		"completion":    configuredValue(row["completion_configured"]),
		"cacheRead":     configuredValue(row["cache_read_configured"]),
		"cacheCreation": configuredValue(row["cache_creation_configured"]),
	}
}

func configuredValue(value any) bool {
	switch value := value.(type) {
	case int64:
		return value != 0
	case int:
		return value != 0
	case float64:
		return value != 0
	case bool:
		return value
	default:
		return false
	}
}

func pricingRowRatesAreZero(row map[string]any) bool {
	for _, key := range []string{"prompt_per_1m", "completion_per_1m", "cache_per_1m", "cache_read_per_1m", "cache_creation_per_1m"} {
		if numericValue(row[key]) != 0 {
			return false
		}
	}
	return true
}

func pricingRowRates(row map[string]any) map[string]any {
	return map[string]any{
		"promptPer1M":        row["prompt_per_1m"],
		"completionPer1M":    row["completion_per_1m"],
		"cachePer1M":         row["cache_per_1m"],
		"cacheReadPer1M":     row["cache_read_per_1m"],
		"cacheCreationPer1M": row["cache_creation_per_1m"],
	}
}

func numericValue(value any) float64 {
	switch value := value.(type) {
	case int64:
		return float64(value)
	case int:
		return float64(value)
	case float64:
		return value
	case float32:
		return float64(value)
	default:
		return 0
	}
}

func TestReplayRET07(t *testing.T) {
	root := os.Getenv("CPAMP_RET07_SOURCE_EVIDENCE")
	if root == "" {
		t.Skip("opt-in RET-07 retained-pricing replay")
	}
	ctx := context.Background()
	archive := envRequired(t, "CPAMP_RET07_ARCHIVE")
	anchor := revalidateCase(t, root, archive, ret07CaseID, ret07PlanCommit, ret07Profile)
	p, _ := loadProfile(t, filepath.Join(root, "source-generation", "controlled-input.json"))
	output := envRequired(t, "CPAMP_RET07_REPLAY_OUTPUT")
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("RET-07 output must be absent")
	}
	must(t, os.MkdirAll(output, 0700))
	sealed := inventory(t, filepath.Join(root, "bundle"))
	restored, err := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "bundle"), taskScratch(t, output, "replay-"))
	must(t, err)
	restoredManifest, err := maintenancebundle.Inspect(ctx, restored)
	must(t, err)
	sameInventory(t, sealed, restoredManifest)
	db := openDB(t, restored.DatabasePath, true)
	if scalar(t, db, "select count(*) from usage_events") != 0 {
		t.Fatal("RET-07 replay resurrected raw usage rows")
	}
	archiveState := checkRET07Archive(t, db, restored.ArchivesPath)
	if scalar(t, db, "select count(*) from "+usageprojection.EventTable) != 4 {
		t.Fatal("RET-07 retained projection is incomplete")
	}
	must(t, db.Close())
	key, created, err := security.LoadOrCreateDataKey("", restored.DataKeyPath)
	must(t, err)
	if created {
		t.Fatal("RET-07 restore created a replacement data key")
	}
	protector, err := security.NewProtector(key)
	must(t, err)
	st, err := store.Open(restored.DatabasePath, protector)
	must(t, err)
	states := ret07RetentionReopen(t, ctx, st, restored.DatabasePath)
	var v1 map[string]monitoring.Response
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation", "v1-analytics.json")), &v1))
	first := ret07Queries(t, ctx, st, restored.DatabasePath, p, v1)
	archiveRecords := archiveState["archiveRecords"].([]map[string]any)
	var archiveBytes bytes.Buffer
	for _, record := range archiveRecords {
		archiveBytes.Write(mustJSON(record))
		archiveBytes.WriteByte('\n')
	}
	imports := []any{}
	for i := 0; i < 2; i++ {
		result, _, err := usagesvc.New(st).Import(ctx, bytes.NewReader(archiveBytes.Bytes()))
		must(t, err)
		if result.Added != 0 || result.Skipped != 4 || result.Failed != 0 {
			t.Fatalf("RET-07 retained archive replay changed event count: %+v", result)
		}
		imports = append(imports, result)
	}
	after := ret07Queries(t, ctx, st, restored.DatabasePath, p, v1)
	if ret07StableDigest(first) != ret07StableDigest(after) {
		t.Fatal("RET-07 repeated retained pricing reconciliation drifted")
	}
	must(t, st.Close())

	backupDir := filepath.Join(output, "backup-restore", "bundle")
	must(t, os.MkdirAll(filepath.Dir(backupDir), 0700))
	lock, err := processlock.Acquire(restored.DatabasePath)
	must(t, err)
	backup, err := maintenancebundle.Create(ctx, restored, backupDir, lock)
	must(t, err)
	must(t, lock.Close())
	recoveryRoot := taskScratch(t, output, "recovery-")
	recovered, err := maintenancebundle.RestoreNew(ctx, backupDir, recoveryRoot)
	must(t, err)
	recoveredManifest, err := maintenancebundle.Inspect(ctx, recovered)
	must(t, err)
	sameInventory(t, backup, recoveredManifest)
	db = openDB(t, recovered.DatabasePath, true)
	if scalar(t, db, "select count(*) from usage_events") != 0 || scalar(t, db, "select count(*) from "+usageprojection.EventTable) != 4 {
		t.Fatal("RET-07 new-root restore changed retained/raw boundary")
	}
	recoveryState := checkRET07Archive(t, db, recovered.ArchivesPath)
	must(t, db.Close())
	recoveredKey, created, err := security.LoadOrCreateDataKey("", recovered.DataKeyPath)
	must(t, err)
	if created {
		t.Fatal("RET-07 new-root restore created a replacement key")
	}
	recoveredProtector, err := security.NewProtector(recoveredKey)
	must(t, err)
	recoveredStore, err := store.Open(recovered.DatabasePath, recoveredProtector)
	must(t, err)
	recoveryQueries := ret07Queries(t, ctx, recoveredStore, recovered.DatabasePath, p, v1)
	must(t, recoveredStore.Close())

	backupHash := save(t, output, "backup-restore.json", map[string]any{"newRoot": true, "inventoryMatched": true, "rawStillDeleted": true, "rawEventCount": 0, "archiveEventCount": recoveryState["archiveEventCount"], "retainedProjectionCount": recoveryState["retainedProjectionCount"], "backupManifestDigest": backup.SourceRevision, "restoredManifestDigest": recoveredManifest.SourceRevision, "queries": recoveryQueries})
	rawHash := save(t, output, "raw-event-inventory.json", map[string]any{"beforeRawCount": 4, "deletedCount": 4, "afterRawCount": 0, "rawComplete": false, "archiveEventCount": 4, "retainedProjectionCount": 4})
	projectionHash := save(t, output, "retained-pricing-source.json", map[string]any{"replayed": after, "restored": recoveryQueries, "complete": true})
	sem := []string{"SEM-01", "SEM-02", "SEM-03", "SEM-04", "SEM-05", "SEM-09"}
	assertionsHash := save(t, output, "semantic-assertions.json", map[string]any{"caseID": ret07CaseID, "applicableSEM": sem, "assertions": map[string]any{
		"SEM-01": map[string]any{"status": "runtime-pass", "rawRowsPhysicallyAbsent": true, "historicalIdentityPreserved": true, "pricingAuthorityExplained": true},
		"SEM-02": map[string]any{"status": "runtime-pass", "archiveImports": imports, "reconciliationRepeated": true, "duplicateEventIDs": []any{}},
		"SEM-03": map[string]any{"status": "runtime-pass", "historicalPricingAuthorityPreserved": true, "structuralPriceChange": true, "reconciliation": after},
		"SEM-04": map[string]any{"status": "runtime-pass", "manualPriceAuthority": true, "manualEventsRemainManual": true},
		"SEM-05": map[string]any{"status": "runtime-pass", "explicitZeroDistinctFromUnknown": true, "unknownDistinctFromMissingAsZero": true},
		"SEM-09": map[string]any{"status": "runtime-pass", "rawDeleted": true, "archiveComplete": true, "retainedPricingSourceComplete": true, "onlineRawComplete": false, "rawOnlyQueryDegraded": true, "supportedHistoryQuery": true},
	}})
	executionHash := save(t, output, "execution-record.json", map[string]any{"caseID": ret07CaseID, "executionCommit": codeIdentity(t), "sourceGenerationCaseID": ret07CaseID, "migrationStates": states, "archiveImports": imports, "newRootRecovery": true, "deletionVerified": true, "rawRowsAfterDelete": 0, "retainedPricingSourceComplete": true})
	inputHash := save(t, output, "input-bundle.json", map[string]any{"caseID": ret07CaseID, "inputBundleIdentity": ret07CaseID + "/input-bundle@" + sealed.SourceRevision, "sourceGenerationRecordSHA256": fileDigest(t, filepath.Join(root, "source-generation/record.json")), "sourceBundleDigest": sealed.SourceRevision, "inventory": sealed})
	save(t, output, "result.json", map[string]any{"caseID": ret07CaseID, "runtimeEvidence": "pass", "casePass": false, "independentAcceptance": "pending", "UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false, "remainingCases": map[string]any{"count": 41, "status": "pending"}, "sourceProvenance": anchor, "inputBundleIdentity": ret07CaseID + "/input-bundle@" + sealed.SourceRevision, "inputBundleDigest": sealed.SourceRevision, "rawEventCount": 0, "beforeRawCount": 4, "deletedCount": 4, "afterRawCount": 0, "archiveEventCount": 4, "retainedProjectionCount": 4, "retainedPricingSourceComplete": true, "onlineRawComplete": false, "backupRestore": "pass", "applicableSEM": sem, "evidence": map[string]any{"inputBundle": inputHash, "semanticAssertions": assertionsHash, "executionRecord": executionHash, "backupRestore": backupHash, "rawEventInventory": rawHash, "retainedPricingSource": projectionHash}})
}

// RET-07 reopens the already-qualified retained projections without invoking
// a raw-source rebuild. The production identity projection is intentionally
// fail-closed after raw deletion; retained pricing reconciliation must reuse
// the accepted RET-03 read-only reopen boundary.
func ret07RetentionReopen(t *testing.T, ctx context.Context, st *store.Store, dbPath string) []any {
	t.Helper()
	state, err := st.DiscoverUsageCacheAccounting(ctx)
	must(t, err)
	if state.Status != "completed" {
		t.Fatalf("RET-07 retained reopen requires completed cache accounting: %#v", state)
	}
	db := openDB(t, dbPath, true)
	defer db.Close()
	if scalar(t, db, "select count(*) from usage_events") != 0 || scalar(t, db, "select count(*) from "+usageprojection.EventTable) != 4 {
		t.Fatal("RET-07 retained reopen lost the deleted raw boundary or retained projection")
	}
	return []any{map[string]any{
		"mode":                    "retained-pricing-read-only",
		"cacheAccounting":         state,
		"rawEventCount":           0,
		"retainedProjectionCount": 4,
		"derivedRebuild":          "not-run-after-raw-deletion",
		"rawOnlyRebuildGuard":     "fail-closed-by-production-source-guard",
	}}
}

func ret07Queries(t *testing.T, ctx context.Context, st *store.Store, dbPath string, p profile, v1 map[string]monitoring.Response) map[string]any {
	t.Helper()
	queries := ret03Queries(t, ctx, st, dbPath, p, v1)
	db := openDB(t, dbPath, true)
	defer db.Close()
	if scalar(t, db, "select count(*) from usage_events") != 0 || scalar(t, db, "select count(*) from "+usageprojection.EventTable) != 4 {
		t.Fatal("RET-07 retained pricing source is not complete")
	}
	if scalar(t, db, "select count(*) from usage_archive_event_refs where raw_deleted_at_ms is not null") != 4 {
		t.Fatal("RET-07 raw deletion coverage is incomplete")
	}
	return map[string]any{"analytics": queries, "tierSelection": ret07TierEvidence(t, db), "pricingSource": retainedPricingEvidence(t, db, scalarString(t, db, "select structure_revision from usage_pricing_rollup_state where rollup_name='pricing_v1'"), query(t, db, "select model,prompt_per_1m,completion_per_1m,cache_per_1m,cache_read_per_1m,cache_creation_per_1m,prompt_configured,completion_configured,cache_read_configured,cache_creation_configured,source,source_model_id from model_prices order by model"))}
}

// Inspect persisted bands AND re-run production band selection from the
// retained projection. A base-price fallback cannot satisfy these assertions.
func ret07TierEvidence(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	projection := query(t, db, "select event_id,input_tokens,normalized_total_input_tokens,output_tokens from "+usageprojection.EventTable+" where model='fixture-paid-ret07'")
	tiers := query(t, db, "select * from model_price_context_tiers where model='fixture-paid-ret07' order by threshold_tokens")
	base := query(t, db, "select prompt_per_1m,completion_per_1m from model_prices where model='fixture-paid-ret07'")
	bands := query(t, db, "select context_threshold_tokens,input_tokens,output_tokens,calls from usage_pricing_hourly_rollups_v1 where model='fixture-paid-ret07' and structure_revision=(select structure_revision from usage_pricing_rollup_state where rollup_name='pricing_v1')")
	if len(projection) != 1 || numericValue(projection[0]["event_id"]) != 2 || numericValue(projection[0]["input_tokens"]) != 100 || numericValue(projection[0]["normalized_total_input_tokens"]) != 100 || numericValue(projection[0]["output_tokens"]) != 10 || len(tiers) != 1 || numericValue(tiers[0]["threshold_tokens"]) != 50 || len(bands) != 1 || numericValue(bands[0]["context_threshold_tokens"]) != 50 || numericValue(bands[0]["calls"]) != 1 {
		t.Fatalf("RET-07 historical event did not enter the new tier: projection=%v tiers=%v bands=%v", projection, tiers, bands)
	}
	if len(base) != 1 || numericValue(base[0]["prompt_per_1m"]) != 4 || numericValue(base[0]["completion_per_1m"]) != 16 || numericValue(tiers[0]["prompt_per_1m"]) != 2 || numericValue(tiers[0]["completion_per_1m"]) != 8 || numericValue(tiers[0]["prompt_configured"]) != 1 || numericValue(tiers[0]["completion_configured"]) != 1 {
		t.Fatalf("RET-07 persisted default/tier rates do not distinguish fallback: base=%v tiers=%v", base, tiers)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(t, err)
	rows, err := usagepricing.New(db).LoadHourlyRowsFromEventsTx(ctx, tx, usagepricing.HourlyFilter{FromMS: 1790899200000, ToMS: 1790985600000, Models: []string{"fixture-paid-ret07"}, IncludeFailed: true})
	must(t, err)
	must(t, tx.Rollback())
	if len(rows) != 1 || rows[0].ContextThresholdTokens != 50 || rows[0].InputTokens != 100 || rows[0].OutputTokens != 10 || rows[0].Calls != 1 {
		t.Fatalf("RET-07 retained-source tier classification failed: %+v", rows)
	}
	input, output := numericValue(projection[0]["normalized_total_input_tokens"]), float64(rows[0].OutputTokens)
	tierUSD := (input*numericValue(tiers[0]["prompt_per_1m"]) + output*numericValue(tiers[0]["completion_per_1m"])) / 1e6
	baseUSD := (input*numericValue(base[0]["prompt_per_1m"]) + output*numericValue(base[0]["completion_per_1m"])) / 1e6
	assertClose(t, "retained tier price", tierUSD, 0.00028)
	assertClose(t, "base fallback counterfactual", baseUSD, 0.00056)
	return map[string]any{"eventID": projection[0]["event_id"], "normalizedInputTokens": input, "outputTokens": rows[0].OutputTokens, "thresholdTokens": tiers[0]["threshold_tokens"], "predicate": "normalized_input_tokens > threshold_tokens", "selectedContextThreshold": rows[0].ContextThresholdTokens, "persistedBands": bands, "retainedProjection": projection, "contextTiers": tiers, "basePrices": base, "historicalTierUSD": tierUSD, "baseFallbackUSD": baseUSD, "tierWasExercised": true}
}

func jsonDigest(value any) string {
	b, _ := json.Marshal(value)
	return fmt.Sprintf("%x", digestBytes(b))
}

func ret07StableDigest(value any) string {
	b, _ := json.Marshal(value)
	var normalized any
	if json.Unmarshal(b, &normalized) == nil {
		stripRET07VolatileFields(normalized)
		b, _ = json.Marshal(normalized)
	}
	return fmt.Sprintf("%x", digestBytes(b))
}

func stripRET07VolatileFields(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "generated_at_ms")
		for _, child := range typed {
			stripRET07VolatileFields(child)
		}
	case []any:
		for _, child := range typed {
			stripRET07VolatileFields(child)
		}
	}
}
