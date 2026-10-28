package main

import (
	"context"
	"errors"
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

// externalInstallSource is the only authority of a committed Connect External
// installation. It re-reads the checkpoint and the protected Manager connection
// on every call, decrypts the credential the user supplied, and observes the
// user's own CPA to derive the configuration digest the generation binds to.
// Nothing here is cached, and a 401 stays the typed blocker it is on the wire.
func externalInstallSource(store freshstore.Store, protector *security.Protector) freshkeyapply.ExternalSource {
	return func(ctx context.Context) (freshkeyapply.ExternalInstall, error) {
		if store == nil || protector == nil {
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		record, err := store.Load(ctx)
		if err != nil {
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		checkpoint := record.Operation.Strategy
		// External is the one profile whose completion this Manager cannot infer:
		// no Runtime is staged for it, so the durable product checkpoint is the
		// only proof the installation finished.
		if checkpoint.Strategy != bootstrap.ConnectExternal || checkpoint.Stage != "committed" ||
			!record.Operation.ProductComplete || checkpoint.TargetGeneration == 0 || record.Connection == nil {
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		baseURL, err := cpa.NormalizeV8BaseURL(record.Connection.CPABaseURL)
		if err != nil || baseURL != strings.TrimRight(record.Connection.CPABaseURL, "/") {
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		// The committed target must still be the connection this Manager holds.
		// Comparing the address digests is the only comparison available: the
		// credential is a protected envelope, and a connection the user edited to
		// another CPA is a rebind, which this path must never perform silently.
		if checkpoint.TargetIdentity != freshkeyapply.ExternalIdentity(baseURL) {
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		key, err := protector.UnprotectString(record.Connection.ManagementKey)
		if err != nil || len(key) != 64 {
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		// The generation binds this connection to the configuration the user's CPA
		// presents now, so one authenticated read is part of resolving the target.
		snapshot, err := cpa.NewTransportForEndpoint(baseURL).ExternalKeySnapshot(ctx, key)
		if err != nil {
			if errors.Is(err, cpa.ErrManagementRejected) {
				return freshkeyapply.ExternalInstall{}, cpa.ErrManagementRejected
			}
			return freshkeyapply.ExternalInstall{}, freshkeyapply.ErrUnavailable
		}
		return freshkeyapply.ExternalInstall{
			Identity:      freshkeyapply.ExternalIdentity(baseURL),
			ConfigSHA256:  snapshot.SourceSHA256,
			Checkpoint:    checkpoint.TargetGeneration,
			BaseURL:       baseURL,
			ManagementKey: key,
		}, nil
	}
}
