package freshkeyapply

import (
	"context"
	"os"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// ConfigTarget consumes existing S1/S2 target/permit checks. It grants no Key
// mutation. The serial Host must exclude every K/U writer for the source lease.
func (s *Service) ConfigTarget(ctx context.Context, generation uint64) (string, error) {
	if s == nil {
		return "", ErrUnavailable
	}
	if s.keyOperations {
		return "", cpa.ErrWriterIsolation
	}
	// A reachable per-step permit is incompatible with the config-only profile,
	// even when its environment switch is off. Host must never issue one here.
	if _, e := os.Lstat(KeyPermitPath); !os.IsNotExist(e) {
		return "", cpa.ErrWriterIsolation
	}
	if o, e := s.repo.LatestOperation(ctx); e != nil || o != nil {
		return "", cpa.ErrSourceConflict
	}
	p, e := s.operationTarget(ctx, generation)
	if e != nil {
		return "", e
	}
	return p.ManagementKey, nil
}
