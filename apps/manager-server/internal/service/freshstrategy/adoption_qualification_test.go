package freshstrategy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func resourceConsumerFixture(t *testing.T, index int) (productionRecord, fstest.MapFS) {
	t.Helper()
	record, files := acceptedConsumerFixture(t, index)
	consumer := &record.Consumers[index]
	evidence := &consumer.Evidence[1]
	refs := []string{"test-keys-read", "test-keys-replay", "test-keys-verify"}
	evidence.CaseRefs = refs
	consumer.AdoptionEvidence = &adoptionResourceEvidence{Profile: "CPA8-STAGE-r1", ReadCaseRef: refs[0], ReplayCaseRef: refs[1], VerifyCaseRef: refs[2]}
	cases, results, traces := []any{}, []any{}, []any{}
	for i, ref := range refs {
		traceID := ref + "-trace"
		traceHash := storeSyntheticEvidence(files, encodeJSON(t, map[string]any{"schemaVersion": 1, "runId": "test-resource-run", "caseRef": ref}), ".json")
		cases = append(cases, map[string]any{"id": ref, "profileRefs": []string{evidence.Profile}})
		results = append(results, map[string]any{"caseRef": ref, "executionState": "completed", "assertionOutcome": "pass", "capability": "limited", "evidenceRefs": []string{traceID}})
		traces = append(traces, map[string]any{"id": traceID, "kind": "sanitized-trace", "sha256": traceHash, "relativePath": string(rune('a'+i)) + ".json"})
	}
	manifest := map[string]any{"schemaVersion": 1, "contractId": "cpamp-cpa-v8-qualification", "revision": "test-resources", "candidate": map[string]any{"id": "candidate-test", "declaredSha256": record.ArchiveSHA256}, "profiles": []any{map[string]any{"id": evidence.Profile}}, "cases": cases}
	evidence.ManifestSHA256 = storeSyntheticEvidence(files, encodeJSON(t, manifest), ".json")
	report := map[string]any{"schemaVersion": 1, "contractId": "cpamp-cpa-v8-qualification", "manifestRevision": "test-resources", "manifestSha256": evidence.ManifestSHA256, "run": map[string]any{"id": "test-resource-run", "runnerCommit": evidence.ImplementationHead}, "artifactObservation": map[string]any{"candidateRef": "candidate-test", "provenance": "locally-verified", "observedArchiveSha256": record.ArchiveSHA256, "observedBinarySha256": record.BinarySHA256}, "results": results, "evidence": traces}
	evidence.ReportSHA256 = storeSyntheticEvidence(files, encodeJSON(t, report), ".json")
	files["qualification/consumer.json"] = &fstest.MapFile{Data: encodeJSON(t, record)}
	return record, files
}

func TestProductionAdoptionResourceAcceptanceCannotComeFromSafeRead(t *testing.T) {
	for _, index := range []int{2, 3} {
		record, files := resourceConsumerFixture(t, index)
		consumers, err := loadProductionQualification(files)
		if err != nil {
			t.Fatal(err)
		}
		key := consumerKey{record.Consumers[index].Strategy, record.Consumers[index].SourceClass}
		if len(consumers[key].AdoptionResources) != 1 || consumers[key].ArtifactID != record.ArtifactID {
			t.Fatal("accepted scoped resources unavailable")
		}
		for _, change := range []func(*consumerRecord){func(c *consumerRecord) { c.AdoptionEvidence.Profile = "CPA8-EXTERNAL-SAFETY-r1" }, func(c *consumerRecord) { c.AdoptionEvidence.ReadCaseRef = "unaccepted-read" }, func(c *consumerRecord) { c.AdoptionEvidence.ReplayCaseRef = c.AdoptionEvidence.ReadCaseRef }, func(c *consumerRecord) { c.AdoptionEvidence.VerifyCaseRef = "" }} {
			original := record.Consumers[index].AdoptionEvidence
			copy := *original
			record.Consumers[index].AdoptionEvidence = &copy
			change(&record.Consumers[index])
			files["qualification/consumer.json"] = &fstest.MapFile{Data: encodeJSON(t, record)}
			if _, err := loadProductionQualification(files); err == nil {
				t.Fatal("unaccepted resource evidence granted replay")
			}
			record.Consumers[index].AdoptionEvidence = original
		}
		// Source qualification alone grants no native resource or target ID.
		record.Consumers[index].AdoptionEvidence = nil
		files["qualification/consumer.json"] = &fstest.MapFile{Data: encodeJSON(t, record)}
		consumers, err = loadProductionQualification(files)
		if err != nil || len(consumers[key].AdoptionResources) != 0 || consumers[key].ArtifactID != "" {
			t.Fatal("source profile granted destination replay", err)
		}
	}
}

func TestUnacceptedAdoptionResourcesBlockBeforeDiscovery(t *testing.T) {
	reads := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads++; t.Error("unaccepted discovery touched source") }))
	defer source.Close()
	protector, _ := security.NewProtector([]byte("test-only-resource-key"))
	runtime := &adoptionRuntime{}
	for _, resources := range [][]bootstrap.AdoptionResourceDecision{nil, {{ResourceRef: "client-api-keys", Kind: bootstrap.APIKeys, Scope: bootstrap.ClientAPIKeys, Qualification: bootstrap.CapabilityPending, Disposition: bootstrap.Unsupported}}, {{ResourceRef: "client-api-keys", Kind: bootstrap.APIKeys, Scope: bootstrap.ClientAPIKeys, Qualification: bootstrap.CapabilityQualified, Disposition: bootstrap.Migrate}}} {
		options := Options{Runtime: runtime, Protector: protector, Qualification: qualified(), QualifiedArtifactID: model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64)), DestinationBaseURL: "http://destination:8317", AdoptionResources: resources}
		_, err := New(options).Execute(t.Context(), operation(), Request{Strategy: bootstrap.AdoptExisting, BaseURL: source.URL, AdoptSourceClass: bootstrap.AdoptNetworkExisting, ManagementKey: "secret"})
		var blocker *Blocker
		if !errors.As(err, &blocker) || blocker.Code != "adoption_resource_qualification_required" || reads != 0 || len(runtime.stages) != 0 || len(runtime.starts) != 0 {
			t.Fatal("safe-read promoted resources", err)
		}
	}
}
