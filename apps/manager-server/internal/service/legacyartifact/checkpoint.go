package legacyartifact

import (
	"context"
	"errors"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

var (
	ErrCheckpointFailed         = errors.New("required migration checkpoints not satisfied")
	ErrMaintenanceLockActive    = errors.New("active maintenance lock detected on database")
	ErrPendingArchivesDetected  = errors.New("pending archive stage detected on database")
	ErrInspectionIssuesDetected = errors.New("source inspection issues detected on database")
)

// InspectCheckpoints runs a read-only legacy inspection on databasePath and verifies
// that both required v1 migrations are completed, no maintenance lock is active,
// and no unresolved issues exist.
func InspectCheckpoints(ctx context.Context, databasePath string) (CheckpointEvidence, error) {
	return inspectCheckpoints(ctx, databasePath, nil)
}

func inspectCheckpoints(ctx context.Context, databasePath string, managerLock *processlock.Lock) (CheckpointEvidence, error) {
	paths := sqlite.InspectionPaths{Database: databasePath}
	options := sqlite.InspectionOptions{SourceStopped: true, ManagerLock: managerLock}

	observed, err := sqlite.InspectLegacySource(ctx, paths, options)
	if err != nil {
		return CheckpointEvidence{}, errors.Join(ErrCheckpointFailed, err)
	}
	return checkpointsFromInspection(observed)
}

func checkpointsFromInspection(observed sqlite.LegacyInspection) (CheckpointEvidence, error) {
	if len(observed.Issues) != 0 {
		return CheckpointEvidence{}, ErrInspectionIssuesDetected
	}
	if observed.CacheAccountingV2 != "completed-observed" {
		return CheckpointEvidence{}, ErrCheckpointFailed
	}
	if observed.QuotaSnapshotLifecycleV1 != "completed-observed" {
		return CheckpointEvidence{}, ErrCheckpointFailed
	}
	if observed.History["maintenance"] != "none-observed" {
		return CheckpointEvidence{}, ErrMaintenanceLockActive
	}
	if observed.History["archives"] == "pending-observed" {
		return CheckpointEvidence{}, ErrPendingArchivesDetected
	}

	return CheckpointEvidence{
		CacheAccountingV2:        observed.CacheAccountingV2,
		QuotaSnapshotLifecycleV1: observed.QuotaSnapshotLifecycleV1,
		MaintenanceLock:          observed.History["maintenance"],
	}, nil
}
