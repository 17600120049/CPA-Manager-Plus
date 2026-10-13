package monitoring

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagepricing"
	usageservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestHistoricalDataSafetyLifecycle_Phase4A05(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sqliterepo.Open(filepath.Join(t.TempDir(), "safety.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	db := store.New(sqlDB)
	t.Cleanup(func() { _ = db.Close() })

	// 1. 初始化定价规则：包含 manual base price、context tiers、service tiers 与 explicit zero
	baseTime := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	hour := time.Hour.Milliseconds()

	initialPrices := map[string]store.ModelPrice{
		"tiered-model": {
			Prompt: 1.0, Completion: 2.0, Cache: 0.1,
			ContextTiers: []store.ModelPriceContextTier{
				{ThresholdTokens: 100_000, Prompt: 3.0, PromptConfigured: true},
			},
		},
		"service-model": {
			Prompt: 2.0, Completion: 4.0,
			ServiceTiers: []store.ModelPriceServiceTier{
				{Mode: "fast", ServiceTier: "priority", Prompt: 7.0, PromptConfigured: true},
			},
		},
		"zero-model": {
			Prompt: 0.0, Completion: 0.0, Cache: 0.0,
			PromptConfigured: true, CompletionConfigured: true,
			CacheReadConfigured: true, CacheCreationConfigured: true,
		},
	}
	if err := db.SaveModelPrices(ctx, initialPrices); err != nil {
		t.Fatalf("save initial model prices: %v", err)
	}

	latency := int64(250)
	genTrue := true
	streamFalse := false

	// Account A: coverage-a.json, index_a
	// Account B: coverage-b.json, index_b
	events := []usage.Event{
		// Account A events
		{
			EventHash:            testCanonicalHash("hash-a-1"),
			TimestampMS:          baseTime + 10_000,
			Timestamp:            time.UnixMilli(baseTime + 10_000).UTC().Format(time.RFC3339),
			Model:                "tiered-model",
			AnalyticsModel:       "tiered-model",
			Endpoint:             "POST /v1/chat/completions",
			AuthFileSnapshot:     "coverage-a.json",
			AuthIndex:            "index_a",
			AuthProviderSnapshot: "openai",
			AccountSnapshot:      "acc_a@example.com",
			InputTokens:          150_000,
			OutputTokens:         100,
			CachedTokens:         10,
			CacheReadTokens:      10,
			TotalTokens:          150_100,
			SessionID:            "sess-a-1",
			ParentSessionID:      "parent-a-1",
			ResponseModel:        "tiered-model-resp",
			AccessTokenSHA256:    "sha-token-a-1",
			Generate:             &genTrue,
			Stream:               &streamFalse,
			LatencyMS:            &latency,
		},
		{
			EventHash:            testCanonicalHash("hash-a-zero"),
			TimestampMS:          baseTime + 20_000,
			Timestamp:            time.UnixMilli(baseTime + 20_000).UTC().Format(time.RFC3339),
			Model:                "zero-model",
			AnalyticsModel:       "zero-model",
			Endpoint:             "POST /v1/chat/completions",
			AuthFileSnapshot:     "coverage-a.json",
			AuthIndex:            "index_a",
			AuthProviderSnapshot: "openai",
			AccountSnapshot:      "acc_a@example.com",
			InputTokens:          50_000,
			OutputTokens:         50,
			TotalTokens:          50_050,
			SessionID:            "sess-a-2",
			Generate:             &genTrue,
			Stream:               &streamFalse,
			LatencyMS:            &latency,
		},
		// Account B events
		{
			EventHash:            testCanonicalHash("hash-b-priority"),
			TimestampMS:          baseTime + hour + 10_000,
			Timestamp:            time.UnixMilli(baseTime + hour + 10_000).UTC().Format(time.RFC3339),
			Model:                "service-model",
			AnalyticsModel:       "service-model",
			ServiceTier:          "priority",
			Endpoint:             "POST /v1/chat/completions",
			AuthFileSnapshot:     "coverage-b.json",
			AuthIndex:            "index_b",
			AuthProviderSnapshot: "openai",
			AccountSnapshot:      "acc_b@example.com",
			InputTokens:          200_000,
			OutputTokens:         200,
			TotalTokens:          200_200,
			SessionID:            "sess-b-1",
			Generate:             &genTrue,
			Stream:               &streamFalse,
			LatencyMS:            &latency,
		},
		{
			EventHash:            testCanonicalHash("hash-b-standard"),
			TimestampMS:          baseTime + hour + 20_000,
			Timestamp:            time.UnixMilli(baseTime + hour + 20_000).UTC().Format(time.RFC3339),
			Model:                "service-model",
			AnalyticsModel:       "service-model",
			Endpoint:             "POST /v1/chat/completions",
			AuthFileSnapshot:     "coverage-b.json",
			AuthIndex:            "index_b",
			AuthProviderSnapshot: "openai",
			AccountSnapshot:      "acc_b@example.com",
			InputTokens:          80_000,
			OutputTokens:         100,
			TotalTokens:          80_100,
			SessionID:            "sess-b-2",
			Generate:             &genTrue,
			Stream:               &streamFalse,
			LatencyMS:            &latency,
		},
	}

	for i := range events {
		events[i].Provider = "openai"
		events[i].NormalizedTotalInputTokens = events[i].InputTokens
		events[i].NormalizedUncachedInputTokens = events[i].InputTokens - events[i].CacheReadTokens
		events[i].NormalizedCacheReadTokens = events[i].CacheReadTokens
		events[i].NormalizedCacheCreationTokens = 0
		events[i].CacheInputMode = usage.CacheInputModeIncluded
	}

	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert initial events: %v", err)
	}
	// Expected costs are computed from the fixture rates, independently of the
	// pricing implementation: A uses the context tier; B uses priority + base.
	const wantCostA = (149_990*3.0 + 10*0.1 + 100*2.0) / 1_000_000
	const wantCostB = (200_000*7.0 + 200*4.0 + 80_000*2.0 + 100*4.0) / 1_000_000
	assertCost := func(t *testing.T, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-12 {
			t.Fatalf("cost=%v, want %.12f", got, want)
		}
	}
	var fullRawHistory []AccountHistoryItem
	assertHistoricalTotals := func(t *testing.T, got []AccountHistoryItem) {
		t.Helper()
		before := append([]AccountHistoryItem(nil), fullRawHistory...)
		after := append([]AccountHistoryItem(nil), got...)
		// Latest/recent request details are served from live raw rows. Retained
		// history guarantees lifetime totals, not raw-deleted request details.
		for i := range before {
			before[i].LatestRequest = nil
			before[i].RecentRequests = nil
		}
		for i := range after {
			after[i].LatestRequest = nil
			after[i].RecentRequests = nil
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("historical totals changed: before=%+v after=%+v", before, after)
		}
	}

	// ------------------------------------------------------------------------
	// State 1: Full Raw (在线 raw 完整)
	// ------------------------------------------------------------------------
	t.Run("State 1 - Full Raw Invariants", func(t *testing.T) {
		// 验证 event identity / dedupe：重新插入相同 event_hash 的事件不产生重复
		dupEvent := events[0]
		inserted, err := db.InsertEvents(ctx, []usage.Event{dupEvent})
		if err != nil {
			t.Fatalf("insert duplicate event must not fatal: %v", err)
		}
		if inserted.Inserted != 0 || inserted.Skipped != 1 {
			t.Fatalf("inserted duplicate event count = %d, skipped = %d, want 0, 1 (dedupe must hold)", inserted.Inserted, inserted.Skipped)
		}

		// 同步所有 rollups 与 pricing
		catchUpMonitoringArchiveDeleteReadiness(t, ctx, db)
		pricingResult, err := db.CatchUpUsagePricing(ctx, 100, time.Now().UnixMilli())
		if err != nil {
			t.Fatalf("catch up usage pricing: %v", err)
		}
		if pricingResult.Pending {
			t.Fatalf("pricing catchup pending: %#v", pricingResult)
		}

		// 检查 AccountHistory
		accReq := AccountHistoryRequest{Accounts: []AccountHistoryTarget{
			{RowKey: "a", AuthFileSnapshot: "coverage-a.json", AuthIndex: "index_a", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_a@example.com"},
			{RowKey: "b", AuthFileSnapshot: "coverage-b.json", AuthIndex: "index_b", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_b@example.com"},
		}}
		hist, err := New(db).AccountHistory(ctx, accReq)
		if err != nil || len(hist.Items) != 2 {
			t.Fatalf("account history query failed: %#v, %v", hist, err)
		}

		// Account A: 2 requests, 150_100 + 50_050 = 200_150 tokens
		itemA := hist.Items[0]
		if itemA.TotalRequests != 2 || itemA.TotalTokens != 200_150 {
			t.Fatalf("item A stats mismatch: %#v", itemA)
		}
		assertCost(t, itemA.TotalCost, wantCostA)

		// 验证 explicit zero 保持：zero-model 的 cost 绝不为 nil 也绝不把整体成本搞坏
		// Account B: 2 requests, 200_200 + 80_100 = 280_300 tokens
		itemB := hist.Items[1]
		if itemB.TotalRequests != 2 || itemB.TotalTokens != 280_300 {
			t.Fatalf("item B stats mismatch: %#v", itemB)
		}
		assertCost(t, itemB.TotalCost, wantCostB)
		fullRawHistory = hist.Items

		// AccountHistory exposes latest/recent request timestamps, not session metadata.
		if itemA.LatestRequest == nil || itemA.LatestRequest.TimestampMS != baseTime+20_000 {
			t.Fatalf("item A latest request metadata missing or timestamp mismatch: %#v", itemA.LatestRequest)
		}
		if len(itemA.RecentRequests) != 2 {
			t.Fatalf("item A recent requests count = %d, want 2", len(itemA.RecentRequests))
		}

		// 验证 Analytics 查询
		analyticsReq := Request{
			FromMS: baseTime, ToMS: baseTime + 2*hour, NowMS: baseTime + 2*hour, TimeZone: "UTC",
			Include: Include{Summary: true, Timeline: true, ModelStats: true, Granularity: "hour"},
		}
		analyticsResp, err := New(db, true).Analytics(ctx, analyticsReq)
		if err != nil || analyticsResp.Summary == nil {
			t.Fatalf("analytics failed: %#v, %v", analyticsResp, err)
		}
		if analyticsResp.Summary.TotalCalls != 4 {
			t.Fatalf("analytics total calls = %d, want 4", analyticsResp.Summary.TotalCalls)
		}
		assertCost(t, &analyticsResp.Summary.TotalCost, wantCostA+wantCostB)
		zeroModelFound := false
		for _, stat := range analyticsResp.ModelStats {
			if stat.Model == "zero-model" {
				zeroModelFound = true
				if stat.Calls != 1 || stat.Cost != 0 {
					t.Fatalf("explicit zero pricing changed: %+v", stat)
				}
			}
		}
		if !zeroModelFound {
			t.Fatal("explicit zero model is missing from analytics")
		}

		// 验证 pricing rebuild 确定性与幂等性
		if _, err := sqlDB.ExecContext(ctx, `delete from usage_pricing_hourly_rollups_v1;
			delete from usage_pricing_account_rollups_v1;
			update usage_pricing_rollup_state set status = 'rebuilding', coverage_event_id = 0, backfill_last_event_id = 0`); err != nil {
			t.Fatal(err)
		}
		rebuiltResult, err := db.CatchUpUsagePricing(ctx, 100, time.Now().UnixMilli())
		if err != nil || !rebuiltResult.Rebuilt {
			t.Fatalf("pricing rebuild failed: %#v, %v", rebuiltResult, err)
		}
		histAfter, err := New(db).AccountHistory(ctx, accReq)
		if err != nil || !reflect.DeepEqual(hist.Items, histAfter.Items) {
			t.Fatalf("rebuilt history differs: before=%#v after=%#v", hist.Items, histAfter.Items)
		}
	})

	// ------------------------------------------------------------------------
	// State 2: Archived but Raw Not Deleted (归档存在，但 raw 依然完整)
	// ------------------------------------------------------------------------
	var archiveRunID string
	archiveDir := filepath.Join(t.TempDir(), "usage-archives")
	archiveService := usageservice.New(db, usageservice.WithArchive(usageservice.ArchiveConfig{
		Directory:             archiveDir,
		SegmentEventLimit:     1,
		DeleteBatchSize:       1,
		AggregateReadsEnabled: true,
	}))

	t.Run("State 2 - Archived but Raw Not Deleted Invariants", func(t *testing.T) {
		// 创建并验证归档，但不调用 DeleteArchive
		created, err := archiveService.CreateArchive(ctx, baseTime+hour)
		if err != nil {
			t.Fatalf("create archive: %v", err)
		}
		archiveRunID = created.Run.ID
		if _, err := archiveService.ResumeArchive(ctx, archiveRunID); err != nil {
			t.Fatalf("resume archive: %v", err)
		}
		verified, err := archiveService.VerifyArchive(ctx, archiveRunID)
		if err != nil || verified.Run.Status != "verified" {
			t.Fatalf("verify archive: %#v, %v", verified, err)
		}

		// 断言：raw 数据没有被删除，raw_deleted_at_ms 为 NULL
		var deletedRefCount int64
		if err := sqlDB.QueryRowContext(ctx, `select count(*) from usage_archive_event_refs where raw_deleted_at_ms is not null`).Scan(&deletedRefCount); err != nil {
			t.Fatal(err)
		}
		if deletedRefCount != 0 {
			t.Fatalf("raw_deleted_at_ms must be NULL in State 2, got %d deleted refs", deletedRefCount)
		}

		var rawCount int64
		if err := sqlDB.QueryRowContext(ctx, `select count(*) from usage_events`).Scan(&rawCount); err != nil {
			t.Fatal(err)
		}
		if rawCount != 4 {
			t.Fatalf("raw usage_events count = %d, want 4 (online raw must be intact)", rawCount)
		}

		// 断言：不错误使用 archive 替代在线数据，绝不产生重复统计
		accReq := AccountHistoryRequest{Accounts: []AccountHistoryTarget{
			{RowKey: "a", AuthFileSnapshot: "coverage-a.json", AuthIndex: "index_a", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_a@example.com"},
			{RowKey: "b", AuthFileSnapshot: "coverage-b.json", AuthIndex: "index_b", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_b@example.com"},
		}}
		hist, err := New(db).AccountHistory(ctx, accReq)
		if err != nil || len(hist.Items) != 2 {
			t.Fatalf("account history query failed: %#v, %v", hist, err)
		}
		if hist.Items[0].TotalRequests != 2 || hist.Items[1].TotalRequests != 2 {
			t.Fatalf("double-counting detected! calls = %d, %d, want 2, 2", hist.Items[0].TotalRequests, hist.Items[1].TotalRequests)
		}
		assertCost(t, hist.Items[0].TotalCost, wantCostA)
		assertCost(t, hist.Items[1].TotalCost, wantCostB)
		if !reflect.DeepEqual(hist.Items, fullRawHistory) {
			t.Fatalf("archiving changed historical results: before=%+v after=%+v", fullRawHistory, hist.Items)
		}

		// 即使重新执行 pricing rebuild，权威来源依然是 usage_events，不会双重计数
		if _, err := sqlDB.ExecContext(ctx, `delete from usage_pricing_hourly_rollups_v1;
			delete from usage_pricing_account_rollups_v1;
			update usage_pricing_rollup_state set status = 'rebuilding', coverage_event_id = 0, backfill_last_event_id = 0`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CatchUpUsagePricing(ctx, 100, time.Now().UnixMilli()); err != nil {
			t.Fatalf("pricing rebuild in state 2 failed: %v", err)
		}
		histAfter, err := New(db).AccountHistory(ctx, accReq)
		if err != nil || !reflect.DeepEqual(hist.Items, histAfter.Items) {
			t.Fatalf("rebuilt history differs in state 2: before=%#v after=%#v", hist.Items, histAfter.Items)
		}
	})

	// ------------------------------------------------------------------------
	// State 3: Archived + Raw Deleted (在线 raw 部分删除，retained projection 完整)
	// ------------------------------------------------------------------------
	t.Run("State 3 - Archived and Raw Deleted Invariants", func(t *testing.T) {
		// 执行 DeleteArchive：删除归档时间段（Account A）的 raw 数据
		delResult, err := archiveService.DeleteArchive(ctx, archiveRunID)
		if err != nil {
			t.Fatalf("delete archive: %v", err)
		}
		if delResult.Run.DeletedEventCount != 2 {
			t.Fatalf("deleted events = %d, want 2", delResult.Run.DeletedEventCount)
		}

		// 验证：在线 raw 已经不完整（Account A 的事件从 usage_events 移除了）
		var remainingRaw int64
		if err := sqlDB.QueryRowContext(ctx, `select count(*) from usage_events`).Scan(&remainingRaw); err != nil {
			t.Fatal(err)
		}
		if remainingRaw != 2 {
			t.Fatalf("remaining raw events = %d, want 2", remainingRaw)
		}

		// 验证：retained projection 完整保留了被删除的事件（受 #945 保护）
		var projectionCount int64
		if err := sqlDB.QueryRowContext(ctx, `select count(*) from usage_monitoring_event_projection_v1`).Scan(&projectionCount); err != nil {
			t.Fatal(err)
		}
		if projectionCount < 2 {
			t.Fatalf("retained projection count = %d, expected at least 2", projectionCount)
		}

		// 验证：当 pricing rollups 丢失需要 rebuild 时，系统能从 live raw + retained projection 联合重建
		if _, err := sqlDB.ExecContext(ctx, `delete from usage_pricing_hourly_rollups_v1;
			delete from usage_pricing_account_rollups_v1;
			update usage_pricing_rollup_state set status = 'rebuilding', coverage_event_id = 0, backfill_last_event_id = 0`); err != nil {
			t.Fatal(err)
		}
		rebuiltResult, err := db.CatchUpUsagePricing(ctx, 100, time.Now().UnixMilli())
		if err != nil || !rebuiltResult.Rebuilt {
			t.Fatalf("pricing rebuild from retained projection failed: %#v, %v", rebuiltResult, err)
		}

		// 查询 AccountHistory：Account A（已 raw-deleted）和 Account B（仍为 live raw）的 cost 均精确完整！
		accReq := AccountHistoryRequest{Accounts: []AccountHistoryTarget{
			{RowKey: "a", AuthFileSnapshot: "coverage-a.json", AuthIndex: "index_a", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_a@example.com"},
			{RowKey: "b", AuthFileSnapshot: "coverage-b.json", AuthIndex: "index_b", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_b@example.com"},
		}}
		hist, err := New(db).AccountHistory(ctx, accReq)
		if err != nil || len(hist.Items) != 2 {
			t.Fatalf("account history query in state 3 failed: %#v, %v", hist, err)
		}
		assertCost(t, hist.Items[0].TotalCost, wantCostA)
		assertCost(t, hist.Items[1].TotalCost, wantCostB)
		assertHistoricalTotals(t, hist.Items)
		if hist.Items[0].LatestRequest != nil || len(hist.Items[0].RecentRequests) != 0 {
			t.Fatal("raw-deleted request details should be unavailable")
		}
		if !reflect.DeepEqual(hist.Items[1].RecentRequests, fullRawHistory[1].RecentRequests) {
			t.Fatal("live raw request details changed after archive deletion")
		}
		if hist.Items[0].TotalRequests != 2 || hist.Items[1].TotalRequests != 2 {
			t.Fatalf("requests mismatch: %d, %d, want 2, 2", hist.Items[0].TotalRequests, hist.Items[1].TotalRequests)
		}
	})

	// ------------------------------------------------------------------------
	// State 4: Raw Deleted + Required Retained Source Missing (关键保留证据缺失)
	// ------------------------------------------------------------------------
	t.Run("State 4 - Raw Deleted with Missing Retained Source Fails Closed", func(t *testing.T) {
		// 模拟最后一份证据被破坏：删除 Account A 在 usage_monitoring_event_projection_v1 中的投影
		res, err := sqlDB.ExecContext(ctx, `delete from usage_monitoring_event_projection_v1 where auth_index = 'index_a'`)
		if err != nil {
			t.Fatal(err)
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			t.Fatal("expected at least one row deleted from projection for Account A")
		}

		// 断言 1: Structural price change 必须 block (Fail Closed)
		// 引入结构性价格变更（新增模型或修改 context tier 阈值），触发 revision 变化；
		// 由于 raw 已删除且 retained projection 缺失，必须 fail closed，返回 ErrStructureChangeAfterRawDeletion
		mutatedPrices := map[string]store.ModelPrice{
			"tiered-model": {
				Prompt: 1.0, Completion: 2.0, Cache: 0.1,
				ContextTiers: []store.ModelPriceContextTier{
					{ThresholdTokens: 200_000, Prompt: 4.0, PromptConfigured: true}, // threshold 变更
				},
			},
			"service-model": {
				Prompt: 2.0, Completion: 4.0,
			},
			"zero-model": {
				Prompt: 0.0, Completion: 0.0, Cache: 0.0,
			},
		}
		err = db.SaveModelPrices(ctx, mutatedPrices)
		if err == nil || !errors.Is(err, modelprice.ErrStructureChangeAfterRawDeletion) {
			t.Fatalf("SaveModelPrices must fail closed with ErrStructureChangeAfterRawDeletion, got err=%v", err)
		}

		// 单模型/增量结构变更（UpsertSynced）同样必须 fail closed
		_, singleSyncErr := db.ModelPrices.UpsertSynced(ctx, map[string]store.ModelPrice{
			"tiered-model": {
				Prompt: 1.0, Completion: 2.0, Cache: 0.1,
				ContextTiers: []store.ModelPriceContextTier{
					{ThresholdTokens: 300_000, Prompt: 5.0, PromptConfigured: true},
				},
			},
		})
		if singleSyncErr == nil || !errors.Is(singleSyncErr, modelprice.ErrStructureChangeAfterRawDeletion) {
			t.Fatalf("UpsertSynced must fail closed with ErrStructureChangeAfterRawDeletion, got err=%v", singleSyncErr)
		}

		// 断言 2: Destructive rebuild 必须 block (Fail Closed)
		// Keep the valid rows. Request the destructive clearing phase and prove
		// source verification fails before any row or checkpoint is modified.
		if _, err := sqlDB.ExecContext(ctx, `update usage_pricing_rollup_state set status = 'clearing'`); err != nil {
			t.Fatal(err)
		}
		snapshotPricing := func() [][][]any {
			t.Helper()
			var snapshot [][][]any
			for _, table := range []string{"usage_pricing_hourly_rollups_v1", "usage_pricing_account_rollups_v1", "usage_pricing_rollup_state"} {
				rows, err := sqlDB.QueryContext(ctx, `select * from `+table+` order by rowid`)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				var entries [][]any
				for rows.Next() {
					values := make([]any, len(columns))
					targets := make([]any, len(columns))
					for i := range values {
						targets[i] = &values[i]
					}
					if err := rows.Scan(targets...); err != nil {
						_ = rows.Close()
						t.Fatal(err)
					}
					entries = append(entries, values)
				}
				if err := rows.Err(); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if len(entries) == 0 {
					t.Fatalf("missing pre-existing pricing rows in %s", table)
				}
				snapshot = append(snapshot, entries)
			}
			return snapshot
		}
		beforeFailedRebuild := snapshotPricing()
		// 此时如果尝试重算，必须失败，不能清空或伪造历史
		_, catchUpErr := db.CatchUpUsagePricing(ctx, 100, time.Now().UnixMilli())
		if !errors.Is(catchUpErr, usagepricing.ErrRetainedPricingHistoryIncomplete) {
			t.Fatalf("CatchUpUsagePricing error=%v, want retained history incomplete", catchUpErr)
		}
		if after := snapshotPricing(); !reflect.DeepEqual(beforeFailedRebuild, after) {
			t.Fatal("failed rebuild modified existing pricing rows or checkpoint")
		}

		// 断言 3: 受影响账号的历史成本为 unavailable / indeterminate (TotalCost == nil)，绝不能返回 0！
		// 断言 4: 未受影响的账号（Account B）继续正常工作，拥有准确的 TotalCost！
		// 断言 5: request/token 等非成本指标继续可用！
		accReq := AccountHistoryRequest{Accounts: []AccountHistoryTarget{
			{RowKey: "a", AuthFileSnapshot: "coverage-a.json", AuthIndex: "index_a", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_a@example.com"},
			{RowKey: "b", AuthFileSnapshot: "coverage-b.json", AuthIndex: "index_b", AuthProviderSnapshot: "openai", AccountSnapshot: "acc_b@example.com"},
		}}
		hist, err := New(db).AccountHistory(ctx, accReq)
		if err != nil || len(hist.Items) != 2 {
			t.Fatalf("account history query failed: %#v, %v", hist, err)
		}

		itemA := hist.Items[0]
		itemB := hist.Items[1]

		// Account A: 历史定价缺失，TotalCost 必须为 nil（绝不能伪造成 0.0）！
		if itemA.TotalCost != nil {
			t.Fatalf("Account A total cost must be nil (unavailable), but got: %v", *itemA.TotalCost)
		}
		// 但核心指标（请求数、tokens）仍然来自 core rollups，继续可用！
		if itemA.TotalRequests != 2 || itemA.TotalTokens != 200_150 {
			t.Fatalf("Account A core stats corrupted: requests=%d tokens=%d", itemA.TotalRequests, itemA.TotalTokens)
		}

		// Account B: 证据完整，TotalCost 正常计算并返回，绝不被 Account A 污染！
		assertCost(t, itemB.TotalCost, wantCostB)
		if itemB.TotalRequests != 2 || itemB.TotalTokens != 280_300 {
			t.Fatalf("Account B stats corrupted: requests=%d tokens=%d", itemB.TotalRequests, itemB.TotalTokens)
		}

		// 断言 6: Analytics 在缺失必要 pricing 历史时 fail closed，绝不返回伪造的 0
		analyticsReq := Request{
			FromMS: baseTime, ToMS: baseTime + 2*hour, NowMS: baseTime + 2*hour, TimeZone: "UTC",
			Include: Include{Summary: true, Timeline: true, ModelStats: true, Granularity: "hour"},
		}
		analyticsResp, err := New(db, true).Analytics(ctx, analyticsReq)
		if err == nil || !errors.Is(err, store.ErrUsagePricingCoverageIncomplete) {
			t.Fatalf("Analytics must fail closed with ErrUsagePricingCoverageIncomplete, got err=%v", err)
		}
		if analyticsResp.Summary != nil {
			t.Fatalf("Analytics summary must be nil on failure, got: %#v", analyticsResp.Summary)
		}
	})
}
