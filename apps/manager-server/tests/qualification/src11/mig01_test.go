package src11

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const mig01CaseID = "MIG-01"
const mig01PlanCommit = "7eefdbf50d3596890a75e97a400dc1f5286e6498"
const mig01Profile = "mig-01-completed-revision2-v1"

// MIG-01 is a read-only qualification of its own real generated bundle. It
// never calls store.Open, migration discovery, workers or an Upgrade API.
func TestQualifyMIG01(t *testing.T) {
	root := os.Getenv("CPAMP_MIG01_SOURCE")
	if root == "" {
		t.Skip("opt-in sealed MIG-01 source qualification")
	}
	archive := envRequired(t, "CPAMP_SRC11_ARCHIVE")
	r := revalidateCase(t, root, archive, mig01CaseID, mig01PlanCommit, mig01Profile)
	out := envRequired(t, "CPAMP_MIG01_OUTPUT")
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatal("output must be absent")
	}
	must(t, os.MkdirAll(out, 0700))
	ctx := context.Background()
	sealed := inventory(t, filepath.Join(root, "bundle"))
	layout := maintenancebundle.BundleLayout(filepath.Join(root, "bundle"))
	db, err := sql.Open("sqlite", dbURI(layout.DatabasePath, true)+"&immutable=1&_pragma=query_only(1)")
	must(t, err)
	db.SetMaxOpenConns(1)
	defer db.Close()
	before := snapshot(t, db)
	var generated map[string][]map[string]any
	must(t, json.Unmarshal(read(t, filepath.Join(root, "source-generation/source-snapshot.json")), &generated))
	if digestBytes(mig01JSON(t, before)) != digestBytes(mig01JSON(t, generated)) {
		t.Fatal("source is not its generated authoritative snapshot")
	}
	costsBefore := read(t, filepath.Join(root, "source-generation/v1-analytics.json"))
	statesBefore := query(t, db, "select * from usage_data_migrations order by name")
	checks := sqliteChecks(t, db, false)
	if len(before["usage_events"]) != 4 || len(before["quota_cooldowns"]) == 0 {
		t.Fatal("controlled source is incomplete")
	}
	banked := query(t, db, "select * from account_quota_snapshots where reset_credits_available > 0 and reset_credits_json is not null")
	if len(banked) == 0 {
		t.Fatal("banked reset evidence is missing")
	}
	devin := query(t, db, "select * from usage_events where provider='devin'")
	if len(devin) != 1 || devin[0]["cache_input_mode"] != "read_included_creation_separate" || devin[0]["total_tokens"] != int64(1150) {
		t.Fatal("historical Devin revision 2 missing")
	}
	input := directCandidateInput{SourceRoot: root, ProducerArchive: archive,
		GenerationRecordSHA256: fileDigest(t, filepath.Join(root, "source-generation/record.json")), BundleSHA256: sealed.SourceRevision}
	ready, err := inspectDirectCandidateReadiness(ctx, input)
	must(t, err)
	if ready.Status != "ready" || ready.SourceRelease != "v1.14.4" || ready.SourceCommit != sourceCommit ||
		ready.UpgradeAuthorized || ready.TrustedHopReceipt || ready.Preflight.UpgradeAuthorized ||
		ready.Preflight.ExactSourceRelease.Status != "unknown" || ready.Preflight.BridgeReadiness.Status != "unknown" ||
		ready.Preflight.ForwardHopEvidence != nil {
		t.Fatal("readiness scope or authority separation invalid")
	}
	// Missing exact generation evidence is a narrow guard, not AUT or MIG-02.
	guard := input
	guard.GenerationRecordSHA256 = ""
	rejected, err := inspectDirectCandidateReadiness(ctx, guard)
	if err == nil || rejected.Status != "blocked" || rejected.UpgradeAuthorized || rejected.TrustedHopReceipt {
		t.Fatal("schema-only guard did not reject")
	}
	after := snapshot(t, db)
	statesAfter := query(t, db, "select * from usage_data_migrations order by name")
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(statesBefore, statesAfter) {
		t.Fatal("readiness consumed or rewrote authoritative source")
	}
	afterBundle := inventory(t, filepath.Join(root, "bundle"))
	sameInventory(t, sealed, afterBundle)
	if !reflect.DeepEqual(costsBefore, read(t, filepath.Join(root, "source-generation/v1-analytics.json"))) {
		t.Fatal("historical cost evidence changed")
	}
	beforeSHA := digestBytes(mig01JSON(t, before))
	afterSHA := digestBytes(mig01JSON(t, after))
	save(t, out, "input-bundle.json", sealed)
	save(t, out, "readiness-input.json", map[string]any{"sourceRelease": r.SourceRelease, "sourceCommit": r.SourceCommit, "sourceGenerationRecordSHA256": input.GenerationRecordSHA256, "inputBundleDigest": input.BundleSHA256, "producerArchiveSHA256": r.SourceProducerArchiveSHA256, "producerExecutableSHA256": r.SourceProducerExecutableSHA256, "readOnlySource": true})
	save(t, out, "readiness-result.json", ready)
	save(t, out, "migration-state-snapshot.json", map[string]any{"before": statesBefore, "after": statesAfter, "readinessInspection": ready.MigrationReadiness})
	save(t, out, "authoritative-digest.json", map[string]any{"before": beforeSHA, "after": afterSHA, "bundleBefore": sealed.SourceRevision, "bundleAfter": afterBundle.SourceRevision, "beforeSnapshot": before, "afterSnapshot": after})
	save(t, out, "semantic-assertions.json", map[string]any{
		"SEM-01":         map[string]any{"status": "runtime-pass", "allSourceFieldsEqual": true, "nullsPreserved": true, "historyBeforeSHA256": beforeSHA, "historyAfterSHA256": afterSHA, "historicalCosts": json.RawMessage(read(t, filepath.Join(root, "source-generation/v1-analytics.json"))), "noRepricing": true},
		"SEM-07":         map[string]any{"status": "runtime-pass", "quotaRowsEqual": true, "migrationRowsEqual": true, "bankedResetEvidence": banked, "cooldownEvidence": before["quota_cooldowns"]},
		"sourceMutation": map[string]any{"detected": false, "fullInventoryEqual": true, "sourceWritesAllowed": false, "staging": "existing matching validator private copies only"},
		"guard":          map[string]any{"missingExactGenerationEvidence": "rejected", "caseQualification": false},
	})
	save(t, out, "execution-record.json", map[string]any{"executionCommit": codeIdentity(t), "steps": []string{"verify-own-generation-producer-and-bundle", "readonly-source-snapshot", "production-compatible-readiness", "missing-exact-evidence-guard", "readonly-snapshot-and-inventory-recheck"}, "sqlite": checks, "readinessStatus": ready.Status, "sourceMutation": false, "applicableSEM": []string{"SEM-01", "SEM-07"}})
	save(t, out, "result.json", map[string]any{"caseID": mig01CaseID, "runtimeEvidence": "pass", "runtimeAssertions": "pass", "casePass": false, "independentAcceptance": "pending", "SourceRelease": r.SourceRelease, "SourceCommit": r.SourceCommit, "PlanCommit": mig01PlanCommit, "SourceGeneratorCodeCommit": r.GeneratorCodeCommit, "executionCommit": codeIdentity(t), "sourceGenerationRecordSHA256": input.GenerationRecordSHA256, "inputBundleDigest": sealed.SourceRevision, "beforeDigest": beforeSHA, "afterDigest": afterSHA, "sourceBeforeDigest": sealed.SourceRevision, "sourceAfterDigest": afterBundle.SourceRevision, "BridgeReadiness": "ready", "Cache": "completed", "Quota": "completed", "cacheRevision": 2, "applicableSEM": []string{"SEM-01", "SEM-07"}, "UpgradeAuthorized": false, "TrustedHopReceipt": false, "Phase4A07Complete": false, "Phase4AComplete": false, "acceptedCases": []string{"SRC-11", "RET-01", "AUT-03"}, "independentAcceptancePendingCases": 46})
}

func mig01JSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	must(t, err)
	return b
}
