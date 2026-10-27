package main

import (
	"context"
	"reflect"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
)

// migrateManagedDestination upgrades the one legacy shape produced by the old
// start-only New Managed path. The protected destination snapshot is the first
// CAS, then the Supervisor's durable stage/start journal and authenticated read
// are reconciled, and only the final CAS publishes the connection. A crash at
// any point resumes the same destination operation; a generation or checkpoint
// change is a typed conflict rather than a silent rebind.
func (a *freshApplication) migrateManagedDestination(ctx context.Context) error {
	if a == nil || a.strategy == nil {
		return nil
	}
	record, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	if !record.Operation.ProductComplete || record.Operation.Strategy.Strategy != bootstrap.NewManaged {
		return nil
	}
	strategy := record.Operation.Strategy
	if record.Connection != nil {
		// A legacy Config connection is operator-owned and cannot be silently
		// rebound to a fresh Supervisor child.
		return nil
	}
	if strategy.DestinationConnectionIdentity != "" {
		return &freshstrategy.Blocker{Code: "managed_migration_shape_conflict"}
	}
	if strategy.Stage != "committed" {
		return &freshstrategy.Blocker{Code: "managed_migration_shape_conflict"}
	}
	request := freshstrategy.Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: model.RuntimeIdentity(strategy.TargetIdentity), ExpectedRuntimeGeneration: model.RuntimeGeneration(strategy.TargetGeneration), ExpectedArtifactID: model.RuntimeArtifactID(strategy.TargetArtifactID)}
	if record.AdoptionCiphertext == "" {
		prepared, prepareErr := a.strategy.PrepareManagedMigration(ctx, record.Operation, request)
		if prepareErr != nil {
			return prepareErr
		}
		if prepared.AdoptionCiphertext == "" || prepared.Checkpoint.Stage != "committed" || prepared.Checkpoint.DestinationConnectionIdentity != "" {
			return &freshstrategy.Blocker{Code: "managed_migration_snapshot_unavailable"}
		}
		before := record
		record, err = a.store.Update(ctx, before.Operation.Identity, before.Operation.Revision, func(current *freshstore.Record) error {
			if !reflect.DeepEqual(current.Operation.Strategy, before.Operation.Strategy) || current.AdoptionCiphertext != before.AdoptionCiphertext || current.Connection != nil {
				return freshstore.ErrConflict
			}
			current.Operation.Strategy = prepared.Checkpoint
			current.AdoptionCiphertext = prepared.AdoptionCiphertext
			return nil
		})
		if err != nil {
			return err
		}
	}
	if record.AdoptionCiphertext == "" {
		return &freshstrategy.Blocker{Code: "managed_migration_snapshot_unavailable"}
	}
	request.AdoptionCiphertext = record.AdoptionCiphertext
	result, executeErr := a.strategy.Execute(ctx, record.Operation, request)
	if executeErr != nil {
		return executeErr
	}
	_, err = a.store.Update(ctx, record.Operation.Identity, record.Operation.Revision, func(current *freshstore.Record) error {
		if !reflect.DeepEqual(current.Operation.Strategy, record.Operation.Strategy) || current.AdoptionCiphertext != record.AdoptionCiphertext || current.Connection != nil {
			return freshstore.ErrConflict
		}
		current.Operation.Strategy = result.Checkpoint
		if result.Connection != nil {
			current.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: result.Connection.BaseURL, ManagementKey: result.Connection.ManagementKeyCiphertext}
		}
		return nil
	})
	return err
}
