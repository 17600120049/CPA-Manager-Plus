package freshkeyapply

import (
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

func TestConfigTargetNeverUsesIndependentKeyWriter(t *testing.T) {
	s, _, _, id := fixture(t)
	if _, e := s.Apply(t.Context(), id, 7); e != nil {
		t.Fatal(e)
	}
	if m, e := s.ConfigTarget(t.Context(), 7); e != nil || m == "" {
		t.Fatal("existing S1 target unavailable", e)
	}
	s.keyOperations = true
	if _, e := s.ConfigTarget(t.Context(), 7); !errors.Is(e, cpa.ErrWriterIsolation) {
		t.Fatal("K/U profile accepted", e)
	}
	t.Setenv("CPAMP_PRIVATE_CONFIG_AUTHORITY", "1")
	if s.KeyOperationsEnabled() {
		t.Fatal("independent K/U writer reachable in Config profile")
	}
	if _, e := s.ConfigTarget(t.Context(), 7); !errors.Is(e, cpa.ErrWriterIsolation) {
		t.Fatal("environment guard mistaken for isolation proof", e)
	}
}
