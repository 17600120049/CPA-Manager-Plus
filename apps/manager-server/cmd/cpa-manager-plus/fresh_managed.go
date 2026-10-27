package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
)

// managedInstallSource is the only default-install authority. It re-reads the
// committed Fresh checkpoint and the protected Manager connection on every call
// and never caches the management credential. Any mismatch between the
// checkpoint, the destination identity and the configured Manager connection
// fails closed instead of rebinding to another CPA.
func managedInstallSource(store freshstore.Store, protector *security.Protector, endpoint string) freshkeyapply.ManagedSource {
	baseURL, endpointErr := cpa.NormalizeV8BaseURL(endpoint)
	return func(ctx context.Context) (freshkeyapply.ManagedInstall, error) {
		if store == nil || protector == nil || endpointErr != nil {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		record, err := store.Load(ctx)
		if err != nil {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		checkpoint := record.Operation.Strategy
		if checkpoint.Strategy != bootstrap.NewManaged || checkpoint.Stage != "committed" ||
			!strings.HasPrefix(checkpoint.DestinationConnectionIdentity, "managed:sha256:") ||
			record.Connection == nil {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		// The Manager connection is the CPA the destination was staged for. A
		// changed address is a different installation, never a silent rebind.
		if cpa.NormalizeBaseURL(record.Connection.CPABaseURL) != baseURL ||
			checkpoint.DestinationConnectionIdentity != freshstrategy.ManagedDestinationIdentity(baseURL) {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		key, err := protector.UnprotectString(record.Connection.ManagementKey)
		if err != nil || len(key) != 64 {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		return freshkeyapply.ManagedInstall{
			RunID:         "managed:" + string(record.Operation.Identity.OperationID),
			Generation:    strconv.FormatUint(checkpoint.TargetGeneration, 10),
			Identity:      checkpoint.TargetIdentity,
			ArtifactID:    checkpoint.TargetArtifactID,
			BaseURL:       baseURL,
			ManagementKey: key,
		}, nil
	}
}
