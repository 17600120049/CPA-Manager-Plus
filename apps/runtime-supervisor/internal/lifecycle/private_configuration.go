package lifecycle

import (
	"context"

	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/artifact"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/cpaprocess"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/privateconfig"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/selection"
)

// PrivateConfigurationBoundary shares the lifecycle lock without changing
// ordinary lifecycle execution or its independently pinned qualification.
type PrivateConfigurationBoundary struct {
	executor  *Executor
	authority *privateconfig.Authority
}

// NewPrivateConfigurationBoundary supplies only the fixed bundled artifact
// observation needed by fenced Start. It installs no updater/adoption writer.
func NewPrivateConfigurationBoundary(e *Executor, a *privateconfig.Authority, observer *artifact.Observer, metadataPath string) (*PrivateConfigurationBoundary, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	selected := selection.Bundled(e.executable, metadataPath)
	if err := refreshSelectedDescriptor(observer, &selected); err != nil {
		return nil, err
	}
	e.active = selected
	e.artifact = observer
	return &PrivateConfigurationBoundary{executor: e, authority: a}, nil
}
func (b *PrivateConfigurationBoundary) PrivateConfiguration(ctx context.Context, q privateconfig.Request) (privateconfig.Result, error) {
	e := b.executor
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx.Err() != nil || e.closed.Load() || b.authority == nil {
		return privateconfig.Result{}, privateconfig.ErrStop
	}
	state := e.process.Observe().State
	if state == cpaprocess.StateUnknown {
		return privateconfig.Result{}, privateconfig.ErrStop
	}
	return b.authority.Do(q, state == cpaprocess.StateRunning)
}
