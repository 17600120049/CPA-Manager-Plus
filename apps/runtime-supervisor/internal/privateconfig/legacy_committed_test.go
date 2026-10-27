package privateconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLegacyCommittedRetryMigratesIntoTheCommittedField covers an upgraded
// private installation whose last write was the pre-allowlist retry: it must
// still start, expose the committed value once, and keep starting after the same
// field is written through the allowlist.
func TestLegacyCommittedRetryMigratesIntoTheCommittedField(t *testing.T) {
	f := fresh(t)
	f.install()
	prior := f.begin("retry-legacy", "routing.retry.request-retry", 3)
	f.write(f.changedInt("3", "routing", "retry", "request-retry"))
	f.commit(prior)
	f.patchRecord(func(r map[string]json.RawMessage) {
		delete(r, "committedFields")
		r["committedRetry"] = legacyJSON(t, 3)
		r["operation"] = legacyJSON(t, legacyOperation(prior.OperationID, "request-retry", 3, 0, false, "persisted"))
	})
	f.rebuild()
	if err := f.a.CheckStartup(); err != nil {
		t.Fatal("an installation whose last write was a legacy retry cannot start:", err)
	}
	v := f.observe()
	if got := string(v.CommittedFields["routing.retry.request-retry"]); got != "3" {
		t.Fatalf("legacy committed retry is not the field value: %q", got)
	}
	if v.Operation.Path != "routing.retry.request-retry" || v.Operation.Value != 3 {
		t.Fatalf("legacy operation not resolved to its field: %+v", v.Operation)
	}
	next := f.begin("retry-new", "routing.retry.request-retry", 5)
	f.write(f.changedInt("5", "routing", "retry", "request-retry"))
	f.commit(next)
	f.rebuild()
	if err := f.a.CheckStartup(); err != nil {
		t.Fatal("the stale legacy committed retry blocked the next start:", err)
	}
	v = f.observe()
	if got := string(v.CommittedFields["routing.retry.request-retry"]); got != "5" {
		t.Fatalf("committed field is %q, want the newly written 5", got)
	}
	raw, err := os.ReadFile(filepath.Join(f.a.root, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var durable map[string]json.RawMessage
	if err = json.Unmarshal(raw, &durable); err != nil {
		t.Fatal(err)
	}
	if value, ok := durable["committedRetry"]; ok && string(value) != "null" {
		t.Fatalf("legacy committed retry survived the migration: %s", value)
	}
}
