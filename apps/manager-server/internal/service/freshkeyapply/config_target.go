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
	if s.managed {
		p, err := s.operationTarget(ctx, generation)
		if err != nil {
			return "", err
		}
		return p.ManagementKey, nil
	}
	if s.h1 != nil {
		p, e := s.operationTarget(ctx, generation)
		if e != nil {
			return "", e
		}
		t, ok := s.wire.(*cpa.Transport)
		if !ok {
			return "", ErrUnavailable
		}
		if _, e = s.h1.Ready(ctx, t); e != nil {
			return "", e
		}
		return p.ManagementKey, nil
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

// DefaultInstallCPAAuthorityProven is the Manager-internal admission predicate
// for lane G: the committed NewManaged callback must yield a valid credential
// and authenticated Runtime status must match its identity/generation/artifact.
// It intentionally exposes only a boolean and never returns the credential.
func (s *Service) DefaultInstallCPAAuthorityProven(ctx context.Context) bool {
	if s == nil || !s.managed {
		return false
	}
	p, err := s.permit()
	if err != nil {
		return false
	}
	_, err = s.target(ctx, p)
	return err == nil
}
