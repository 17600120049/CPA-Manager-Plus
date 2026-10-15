package src11

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Both retention variants exercise the same bounded production primitives.
func qualificationRetentionCatchUp(t *testing.T, ctx context.Context, st *store.Store, p profile) []any {
	migrationStates := []any{}
	for i := 0; i < 100; i++ {
		state, err := st.DiscoverUsageCacheAccounting(ctx)
		must(t, err)
		migrationStates = append(migrationStates, state)
		if state.Status == "completed" {
			break
		}
		batch, err := st.RunUsageCacheAccountingBatch(ctx, 2)
		must(t, err)
		migrationStates = append(migrationStates, batch)
		if i == 99 {
			t.Fatal("retention qualification cache migration did not complete")
		}
	}
	for i := 0; i < 100; i++ {
		batch, err := st.BackfillLegacyQuotaSnapshotsBatch(ctx, 2)
		must(t, err)
		migrationStates = append(migrationStates, batch)
		if batch.Completed {
			break
		}
		if i == 99 {
			t.Fatal("retention qualification quota migration did not complete")
		}
	}
	for i := 0; i < 100; i++ {
		changed, err := st.BackfillUsageResponseMetadata(ctx, 2)
		must(t, err)
		migrationStates = append(migrationStates, map[string]any{"responseMetadataChanged": changed})
		if changed == 0 {
			break
		}
		if i == 99 {
			t.Fatal("retention qualification response metadata migration did not complete")
		}
	}
	must(t, st.RunDerivedStartupMaintenance(ctx))
	for i := 0; i < 100; i++ {
		identity, err := st.CatchUpIdentityProjection(ctx, 2, p.ToMS)
		must(t, err)
		projection, err := st.CatchUpUsageMonitoringProjection(ctx, 2, p.ToMS)
		must(t, err)
		stats, err := st.CatchUpUsageMonitoringStats(ctx, 2, p.ToMS)
		must(t, err)
		metadata, err := st.CatchUpUsageMonitoringMetadata(ctx, 2, p.ToMS)
		must(t, err)
		legacyIdentity, err := st.CatchUpCodexLegacyIdentityEvidence(ctx, 2, p.ToMS)
		must(t, err)
		hourly, err := st.CatchUpUsageHourlyAggregate(ctx, 2, p.ToMS)
		must(t, err)
		pricing, err := st.CatchUpUsagePricing(ctx, 2, p.ToMS)
		must(t, err)
		_, err = st.CatchUpAccountHistoryRollups(ctx, 2, p.ToMS)
		must(t, err)
		migrationStates = append(migrationStates, map[string]any{
			"identity": identity, "monitoringProjection": projection,
			"monitoringStats": stats, "monitoringMetadata": metadata,
			"legacyIdentity": legacyIdentity, "hourly": hourly, "pricing": pricing,
		})
		if !identity.Pending && !projection.Pending && !stats.Pending && !metadata.Pending &&
			!legacyIdentity.Pending && !hourly.Pending && !pricing.Pending {
			break
		}
		if i == 99 {
			t.Fatal("retention qualification derived projections did not become ready")
		}
	}
	return migrationStates
}
