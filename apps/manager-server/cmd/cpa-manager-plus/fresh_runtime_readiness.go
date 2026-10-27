package main

import (
	"context"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

const (
	// A Supervisor Start result means OS spawn and ownership publication, not
	// Runtime readiness (apps/runtime-supervisor/internal/lifecycle/start.go).
	// The install consumer verifies its staged destination by reading CPA right
	// after Start, so it needs the Supervisor's own readiness observation — the
	// owned child running and answering on its expected listener — before that
	// read can succeed. This bound covers a CPA child starting inside an already
	// running Supervisor; an unavailable Supervisor or a failed spawn is reported
	// by Start itself and never waits here.
	freshRuntimeReadinessBudget = 20 * time.Second
	// The Supervisor samples the same readiness with this cadence.
	freshRuntimeReadinessInterval = 200 * time.Millisecond
	// One observation is a local Supervisor read whose own probe of CPA is
	// bounded by one second. The whole wait stays bounded by the budget.
	freshRuntimeReadinessProbe = 2 * time.Second
)

// freshReadyStartRuntime decorates the install consumer's Runtime client so a
// successful Start reports that exact Supervisor result only once the Runtime
// also observes the started instance ready. It grants no lifecycle authority
// and never turns a readiness timeout into a failed Start: the spawn did happen,
// so the later verification keeps its own typed, resumable blocker.
func freshReadyStartRuntime(client runtimeservice.RuntimeClient) runtimeservice.RuntimeClient {
	destination, ok := client.(runtimeservice.DestinationClient)
	if !ok {
		// An External observation client stages nothing and keeps its exact
		// behaviour, including a nil client.
		return client
	}
	return &freshReadyStartClient{
		RuntimeClient: client,
		destination:   destination,
		budget:        freshRuntimeReadinessBudget,
		interval:      freshRuntimeReadinessInterval,
	}
}

type freshReadyStartClient struct {
	runtimeservice.RuntimeClient
	destination runtimeservice.DestinationClient
	budget      time.Duration
	interval    time.Duration
}

func (c *freshReadyStartClient) StageDestination(ctx context.Context, request model.RuntimeDestinationRequest) (model.RuntimeOperationResult, error) {
	return c.destination.StageDestination(ctx, request)
}

func (c *freshReadyStartClient) Start(ctx context.Context, request model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	result, err := c.RuntimeClient.Start(ctx, request)
	if err != nil || result.State != model.RuntimeOperationSucceeded {
		return result, err
	}
	c.awaitReady(ctx, request.ExpectedRuntimeIdentity, request.ExpectedRuntimeGeneration)
	return result, nil
}

// awaitReady polls the Supervisor's readiness observation until the instance
// this Start spawned reports ready or the budget expires. Only an observation
// decides: a transport failure or an incomplete observation is not readiness
// yet, and the profile of a different Runtime instance never is.
func (c *freshReadyStartClient) awaitReady(ctx context.Context, identity model.RuntimeIdentity, generation model.RuntimeGeneration) {
	deadline := time.Now().Add(c.budget)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 || ctx.Err() != nil {
			return
		}
		probe, cancel := context.WithTimeout(ctx, min(remaining, freshRuntimeReadinessProbe))
		observed, err := c.Status(probe)
		cancel()
		if err == nil && observed.Validate() == nil && observed.State == model.RuntimeStateReady &&
			(identity == "" || observed.Identity == identity && observed.Generation == generation) {
			return
		}
		timer := time.NewTimer(min(remaining, c.interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
