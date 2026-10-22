package main

import (
	"context"
	"errors"
	"log"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/adapters/cpaidentityinventory"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/application/credentialdeletemutation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/application/identitymutation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/application/identityreconcile"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	ports "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/ports/identitystore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
)

type freshIdentityWiring struct {
	apiKeys          *identitymutation.Service
	credentialDelete *credentialdeletemutation.Service
	observer         identityreconcile.RuntimeObserver
	processInstance  string
	timeSource       func() int64
}

// wireFreshIdentityMutations reuses the admitted Store and the normal
// Embedded mutation journal/fence contract. It performs no Runtime calls and
// opens no connection; every actual mutation obtains its own live fence.
func wireFreshIdentityMutations(ctx *app.Context, observer identitymutation.RuntimeObserver) (freshIdentityWiring, error) {
	if !ctx.Config.EmbeddedRuntimeConfigured() {
		return freshIdentityWiring{}, nil
	}
	if observer == nil {
		return freshIdentityWiring{}, errors.New("Embedded Runtime observer is required")
	}
	mutationRepo, ok := ctx.Store.Identities.(ports.MutationRepository)
	if !ok {
		return freshIdentityWiring{}, errors.New("identity store does not support API-key mutation intents")
	}
	deleteRepo, ok := ctx.Store.Identities.(ports.CredentialDeleteRepository)
	if !ok {
		return freshIdentityWiring{}, errors.New("identity store does not support credential delete intents")
	}
	instance, err := identity.NewAPIKeyID()
	if err != nil {
		return freshIdentityWiring{}, err
	}
	clock := identitymutation.NewMonotonicMillis(nil)
	keys, err := identitymutation.NewService(identitymutation.Config{
		RuntimeObserver: observer, InventoryClient: cpaidentityinventory.New(nil, nil),
		Repository: mutationRepo, ProcessInstanceID: string(instance), TimeSource: clock,
	})
	if err != nil {
		return freshIdentityWiring{}, err
	}
	deletes, err := credentialdeletemutation.NewService(credentialdeletemutation.Config{
		RuntimeObserver: observer, Repository: deleteRepo,
		ProcessInstanceID: string(instance), TimeSource: clock,
	})
	if err != nil {
		return freshIdentityWiring{}, err
	}
	ctx.ProxyService.SetAPIKeyMutationService(keys)
	ctx.ProxyService.SetCredentialDeleteService(deletes)
	return freshIdentityWiring{apiKeys: keys, credentialDelete: deletes, observer: observer, processInstance: string(instance), timeSource: clock}, nil
}

// newFreshIdentityReconcileWorker keeps the normal passive inventory and
// forward-completion recovery alive when Fresh owns the process bootstrap.
// Without it, an already-running Embedded install can never establish the
// canonical identity binding required by the legacy Accounts/Key mutations.
func newFreshIdentityReconcileWorker(ctx *app.Context, wiring freshIdentityWiring) (*identityreconcile.Worker, error) {
	if wiring.apiKeys == nil || wiring.credentialDelete == nil || wiring.processInstance == "" {
		return nil, nil
	}
	service, err := identityreconcile.NewService(identityreconcile.Config{
		RuntimeObserver: wiring.observer,
		ConnectionResolver: func(resolveCtx context.Context) (string, string, error) {
			setup, ok, err := ctx.ManagerConfigService.ResolveSetup(resolveCtx)
			if err != nil {
				return "", "", err
			}
			if !ok {
				return "", "", errors.New("CPA connection setup is not available")
			}
			return setup.CPAUpstreamURL, setup.ManagementKey, nil
		},
		InventoryClient:   cpaidentityinventory.New(nil, nil),
		IdentityRepo:      ctx.Store.Identities,
		ProcessInstanceID: wiring.processInstance,
		TimeSource:        wiring.timeSource,
		BeforeCapture: func(captureCtx context.Context) error {
			return retryIdentityForwardCompletions(captureCtx,
				wiring.apiKeys.RetryForwardCompletions,
				wiring.credentialDelete.RetryForwardCompletions)
		},
		CredentialDeletePhysicalObserver: func(captureCtx context.Context, baseURL, managementKey, runtimeIdentity string) (map[string]ports.PhysicalSourceEvidence, error) {
			pending, err := wiring.credentialDelete.PendingPhysicalSources(captureCtx, runtimeIdentity)
			if err != nil {
				return nil, err
			}
			evidence := make(map[string]ports.PhysicalSourceEvidence, len(pending))
			client := cpaauthfiles.New(nil)
			for _, item := range pending {
				present, probeErr := client.PhysicalFileExists(captureCtx, baseURL, managementKey, item.PhysicalName)
				if probeErr != nil {
					continue
				}
				if present {
					evidence[item.IntentID] = ports.PhysicalSourcePresent
				} else {
					evidence[item.IntentID] = ports.PhysicalSourceAbsent
				}
			}
			return evidence, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return identityreconcile.NewWorker(service, log.Printf), nil
}
