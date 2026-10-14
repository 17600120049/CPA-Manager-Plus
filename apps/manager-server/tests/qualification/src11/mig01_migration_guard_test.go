package src11

import "testing"

// Unit guard only: these in-memory checkpoints are not historical evidence and
// do not qualify any additional MIG case.
func TestCompletedV1144RequiresPersistedEvidence(t *testing.T) {
	revision := "2"
	zero := int64(0)
	s := &requiredMigrationReadiness{CacheRevision: &revision, CacheStagingRows: &zero, QuotaUnattachedRows: &zero,
		Cache: []migrationCheckpoint{{Status: "completed"}}, Quota: []migrationCheckpoint{{Status: "completed"}}}
	if !s.completedV1144() {
		t.Fatal("legitimate empty completed checkpoint with NULL timestamp rejected")
	}
	s.Cache[0].TargetID = 1
	if s.completedV1144() {
		t.Fatal("incomplete checkpoint ignored")
	}
	s.Cache[0].LastID = 1
	if !s.completedV1144() {
		t.Fatal("complete checkpoint rejected")
	}
	revision = "1"
	if s.completedV1144() {
		t.Fatal("revision 2 requirement was downgraded")
	}
}
