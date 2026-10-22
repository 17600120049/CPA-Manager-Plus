package freshconfig

import (
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

func TestIntervalH1AdmissionBindsActualTargetGeneration(t *testing.T) {
	s, _, q := retryFixture(t)
	r, err := s.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source := cpa.RetrySource{
		SourceID: q.Expected.SourceID, Generation: q.Expected.Generation,
		Revision: q.Expected.Revision, SHA256: q.Expected.SHA256,
		H1: true, HandoffValid: true, Installed: true, State: "persisted",
	}
	if !intervalSourceMatchesTarget(r, source, q) {
		t.Fatal("valid H1 source rejected")
	}
	// A matching client expected object must never substitute for the Fresh
	// installation's committed target generation when deciding to claim H1.
	source.Generation = "8"
	q.Expected.Generation = "8"
	if intervalSourceMatchesTarget(r, source, q) {
		t.Fatal("foreign Runtime generation admitted before H1 claim")
	}
}
