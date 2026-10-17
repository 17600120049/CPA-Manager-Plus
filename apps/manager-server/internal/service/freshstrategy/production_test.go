package freshstrategy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestRuntimeDestinationImplementationPinMatchesExecutionChain(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", "..", ".."))
	paths := []string{
		"apps/manager-server/internal/model/runtime.go",
		"apps/manager-server/internal/model/runtime_destination.go",
		"apps/manager-server/internal/service/runtime/destination.go",
		"apps/manager-server/internal/service/runtime/embedded.go",
		"apps/runtime-supervisor/cmd/cpamp-runtime-supervisor/runtime.go",
		"apps/runtime-supervisor/internal/lifecycle/destination.go",
		"apps/runtime-supervisor/internal/lifecycle/start.go",
		"apps/runtime-supervisor/internal/lifecycle/restart.go",
		"apps/runtime-supervisor/internal/lifecycle/recovery.go",
		"apps/runtime-supervisor/internal/protocol/destination.go",
		"apps/runtime-supervisor/internal/protocol/handler.go",
	}
	hash := sha256.New()
	for _, name := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(hash, name+"\n")
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte("\n"))
	}
	pin, err := productionEvidence.ReadFile("qualification/runtime_destination_implementation.sha256")
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if strings.TrimSpace(string(pin)) != got {
		t.Fatalf("Runtime destination implementation pin=%s current=%s", strings.TrimSpace(string(pin)), got)
	}
}

func TestProductionRecordRemainsUnqualified(t *testing.T) {
	consumers, err := loadProductionQualification(productionEvidence)
	if err != nil || len(consumers) != 4 {
		t.Fatalf("compiled consumer record: %v", err)
	}
	for key, consumer := range consumers {
		if consumer.Qualification.ArtifactQualified || consumer.Qualification.RequiredProfileQualified || consumer.Qualification.StagingV8TransitionQualified || consumer.ArtifactID != "" {
			t.Fatalf("pending evidence promoted %v: %+v", key, consumer)
		}
	}
}

func TestProductionConstructorDiscardsInjectedQualification(t *testing.T) {
	var requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("unqualified production consumer touched source")
	}))
	defer source.Close()
	runtime := &managedRuntime{}
	service := NewProduction(Options{Runtime: runtime, Qualification: bootstrap.CPAQualification{
		ManagementGeneration: "v8", ArtifactQualified: true, RequiredProfileQualified: true, StagingV8TransitionQualified: true,
	}, QualifiedArtifactID: model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64)), AdoptionResources: acceptedKeys()})
	for _, item := range []struct {
		strategy bootstrap.CPAStrategy
		class    bootstrap.AdoptSourceClass
		blocker  string
	}{
		{bootstrap.NewManaged, "", "exact_v8_artifact_qualification_required"},
		{bootstrap.ConnectExternal, "", "required_v8_profile_qualification_required"},
		{bootstrap.AdoptExisting, bootstrap.AdoptNetworkExisting, "adoption_source_qualification_required"},
		{bootstrap.AdoptExisting, bootstrap.AdoptLegacyManaged, "adoption_source_qualification_required"},
	} {
		for attempt := 0; attempt < 2; attempt++ {
			result, err := service.Execute(context.Background(), operation(), Request{Strategy: item.strategy, AdoptSourceClass: item.class, BaseURL: source.URL, ManagementKey: "private-source-secret"})
			var blocker *Blocker
			if !errors.As(err, &blocker) || blocker.Code != item.blocker || result.Checkpoint.Strategy != "" {
				t.Fatalf("%s/%s: %+v %v", item.strategy, item.class, result, err)
			}
		}
	}
	if requests.Load() != 0 || len(runtime.starts) != 0 {
		t.Fatal("unqualified production strategy had effects")
	}
}

// This fixture is deliberately created only in test memory. Its synthetic
// review, report and trace are never embedded in a production binary.
func acceptedConsumerFixture(t *testing.T, consumerIndex int) (productionRecord, fstest.MapFS) {
	t.Helper()
	data, err := productionEvidence.ReadFile("qualification/consumer.json")
	if err != nil {
		t.Fatal(err)
	}
	var record productionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	record.ImplementationSHA256 = consumerImplementationSHA256()
	consumer := &record.Consumers[consumerIndex]
	consumer.Status = "accepted"
	consumer.AcceptanceSHA256 = storeSyntheticEvidence(files, []byte("Synthetic independent acceptance for loader tests only.\n"), ".md")
	consumer.Limitations = []string{"synthetic test only; no production acceptance"}
	for i := range consumer.Evidence {
		evidence := &consumer.Evidence[i]
		caseRef := "test-" + evidence.Profile
		evidence.CaseRefs = []string{caseRef}
		manifest := map[string]any{
			"schemaVersion": 1, "contractId": "cpamp-cpa-v8-qualification", "revision": "r-test",
			"candidate": map[string]any{"id": "candidate-test", "declaredSha256": record.ArchiveSHA256},
			"profiles":  []any{map[string]any{"id": evidence.Profile}},
			"cases":     []any{map[string]any{"id": caseRef, "profileRefs": []string{evidence.Profile}, "proofScope": "upstream-function"}},
		}
		evidence.ManifestSHA256 = storeSyntheticEvidence(files, encodeJSON(t, manifest), ".json")
		traceHash := storeSyntheticEvidence(files, encodeJSON(t, map[string]any{"schemaVersion": 1, "runId": "test-run", "caseRef": caseRef}), ".json")
		report := map[string]any{
			"schemaVersion": 1, "contractId": "cpamp-cpa-v8-qualification", "manifestRevision": "r-test", "manifestSha256": evidence.ManifestSHA256,
			"run":                 map[string]any{"id": "test-run", "runnerCommit": evidence.ImplementationHead},
			"artifactObservation": map[string]any{"candidateRef": "candidate-test", "provenance": "locally-verified", "observedArchiveSha256": record.ArchiveSHA256, "observedBinarySha256": record.BinarySHA256},
			"results":             []any{map[string]any{"caseRef": caseRef, "executionState": "completed", "assertionOutcome": "pass", "capability": "supported", "evidenceRefs": []string{"test-trace"}}},
			"evidence":            []any{map[string]any{"id": "test-trace", "kind": "sanitized-trace", "sha256": traceHash}},
		}
		evidence.ReportSHA256 = storeSyntheticEvidence(files, encodeJSON(t, report), ".json")
	}
	writeSyntheticRecord(t, files, record)
	return record, files
}

func encodeJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func writeSyntheticRecord(t *testing.T, files fstest.MapFS, record productionRecord) {
	t.Helper()
	files["qualification/consumer.json"] = &fstest.MapFile{Data: encodeJSON(t, record)}
}

func storeSyntheticEvidence(files fstest.MapFS, data []byte, extension string) string {
	digest := sha256.Sum256(data)
	id := hex.EncodeToString(digest[:])
	files["qualification/evidence/"+id+extension] = &fstest.MapFile{Data: data}
	return id
}

func TestProductionLoaderRejectsMalformedAuthority(t *testing.T) {
	base, err := fs.ReadFile(productionEvidence, "qualification/consumer.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"missing":       nil,
		"invalid":       []byte("{"),
		"unknown":       bytes.Replace(base, []byte(`"schemaVersion": 1,`), []byte(`"schemaVersion": 1, "unknown": true,`), 1),
		"duplicate":     bytes.Replace(base, []byte(`"schemaVersion": 1,`), []byte(`"schemaVersion": 1, "schemaVersion": 1,`), 1),
		"missing field": bytes.Replace(base, []byte("  \"managementGeneration\": \"v8\",\n"), nil, 1),
		"case folded":   bytes.Replace(base, []byte("schemaVersion"), []byte("SchemaVersion"), 1),
		"trailing":      append(append([]byte{}, base...), []byte("{}")...),
	} {
		t.Run(name, func(t *testing.T) {
			files := fstest.MapFS{}
			if data != nil {
				if name != "invalid" && name != "trailing" && bytes.Equal(base, data) {
					t.Fatal("malformed control did not change source")
				}
				files["qualification/consumer.json"] = &fstest.MapFile{Data: data}
			}
			if consumers, err := loadProductionQualification(files); err == nil || consumers != nil {
				t.Fatal("malformed authority granted qualification")
			}
		})
	}
}

func TestProductionLoaderBindsArtifactProfileEvidenceAndHead(t *testing.T) {
	for name, mutate := range map[string]func(*productionRecord, fstest.MapFS){
		"consumer implementation drift": func(r *productionRecord, _ fstest.MapFS) { r.ImplementationSHA256 = strings.Repeat("a", 64) },
		"artifact identity": func(r *productionRecord, _ fstest.MapFS) {
			r.ArtifactID = model.RuntimeArtifactID("sha256:" + strings.Repeat("a", 64))
		},
		"archive digest": func(r *productionRecord, _ fstest.MapFS) { r.ArchiveSHA256 = strings.Repeat("a", 64) },
		"binary digest": func(r *productionRecord, _ fstest.MapFS) {
			r.BinarySHA256 = strings.Repeat("a", 64)
			r.ArtifactID = model.RuntimeArtifactID("sha256:" + r.BinarySHA256)
		},
		"profile": func(r *productionRecord, _ fstest.MapFS) { r.Consumers[0].Evidence[0].Profile = "CPA8-STAGE-r1" },
		"execution head": func(r *productionRecord, _ fstest.MapFS) {
			r.Consumers[0].Evidence[0].ImplementationHead = strings.Repeat("a", 40)
		},
		"empty cases": func(r *productionRecord, _ fstest.MapFS) { r.Consumers[0].Evidence[0].CaseRefs = []string{} },
		"different case": func(r *productionRecord, _ fstest.MapFS) {
			r.Consumers[0].Evidence[0].CaseRefs = []string{"not-observed"}
		},
		"duplicate case": func(r *productionRecord, _ fstest.MapFS) {
			e := &r.Consumers[0].Evidence[0]
			e.CaseRefs = append(e.CaseRefs, e.CaseRefs[0])
		},
		"missing acceptance":     func(r *productionRecord, _ fstest.MapFS) { r.Consumers[0].AcceptanceSHA256 = "" },
		"missing limitations":    func(r *productionRecord, _ fstest.MapFS) { r.Consumers[0].Limitations = []string{} },
		"missing invalidation":   func(r *productionRecord, _ fstest.MapFS) { r.Consumers[0].InvalidatedBy = []string{} },
		"duplicate consumer":     func(r *productionRecord, _ fstest.MapFS) { r.Consumers[1] = r.Consumers[0] },
		"missing legacy profile": func(r *productionRecord, _ fstest.MapFS) { r.Consumers[3].Evidence = r.Consumers[3].Evidence[1:] },
		"missing original manifest": func(r *productionRecord, f fstest.MapFS) {
			delete(f, "qualification/evidence/"+r.Consumers[0].Evidence[0].ManifestSHA256+".json")
		},
		"report drift": func(r *productionRecord, f fstest.MapFS) {
			f["qualification/evidence/"+r.Consumers[0].Evidence[0].ReportSHA256+".json"].Data = []byte("{}")
		},
		"review drift": func(r *productionRecord, f fstest.MapFS) {
			f["qualification/evidence/"+r.Consumers[0].AcceptanceSHA256+".md"].Data = []byte("different review")
		},
		"trace missing": func(r *productionRecord, f fstest.MapFS) {
			var report struct{ Evidence []struct{ SHA256 string } }
			_ = json.Unmarshal(f["qualification/evidence/"+r.Consumers[0].Evidence[0].ReportSHA256+".json"].Data, &report)
			delete(f, "qualification/evidence/"+report.Evidence[0].SHA256+".json")
		},
	} {
		t.Run(name, func(t *testing.T) {
			record, files := acceptedConsumerFixture(t, 0)
			if consumers, err := loadProductionQualification(files); err != nil || !consumers[consumerKey{bootstrap.NewManaged, ""}].Qualification.ArtifactQualified {
				t.Fatalf("positive synthetic control: %v", err)
			}
			mutate(&record, files)
			writeSyntheticRecord(t, files, record)
			if consumers, err := loadProductionQualification(files); err == nil || consumers != nil {
				t.Fatal("mismatched evidence granted qualification")
			}
		})
	}
}

func TestProductionConsumerQualificationDoesNotCrossStrategiesOrSourceClasses(t *testing.T) {
	keys := []consumerKey{{bootstrap.NewManaged, ""}, {bootstrap.ConnectExternal, ""}, {bootstrap.AdoptExisting, bootstrap.AdoptNetworkExisting}, {bootstrap.AdoptExisting, bootstrap.AdoptLegacyManaged}}
	for i, key := range keys {
		t.Run(string(key.Strategy)+"/"+string(key.Class), func(t *testing.T) {
			record, files := acceptedConsumerFixture(t, i)
			consumers, err := loadProductionQualification(files)
			if err != nil {
				t.Fatal(err)
			}
			for _, other := range keys {
				consumer := consumers[other]
				accepted := consumer.Qualification.ValidateForStrategy(other.Strategy, other.Class) == nil
				if accepted != (other == key) {
					t.Fatalf("qualification crossed consumers: accepted %v from %v", other, key)
				}
				if other != key {
					service := &Service{options: Options{Qualification: qualified()}, production: true, consumers: consumers}
					result, err := service.Execute(context.Background(), operation(), Request{Strategy: other.Strategy, AdoptSourceClass: other.Class, BaseURL: "invalid-source"})
					var blocker *Blocker
					want := "adoption_source_qualification_required"
					if other.Strategy == bootstrap.NewManaged {
						want = "exact_v8_artifact_qualification_required"
					}
					if other.Strategy == bootstrap.ConnectExternal {
						want = "required_v8_profile_qualification_required"
					}
					if !errors.As(err, &blocker) || blocker.Code != want || result.Checkpoint.Strategy != "" {
						t.Fatalf("execution crossed consumers: %v from %v: %+v %v", other, key, result, err)
					}
				}
			}
			// An unchanged PASS report with acceptance withdrawn grants nothing.
			record.Consumers[i].Status = "pending"
			record.Consumers[i].AcceptanceSHA256 = ""
			writeSyntheticRecord(t, files, record)
			consumers, err = loadProductionQualification(files)
			if err != nil || consumers[key].Qualification.ValidateForStrategy(key.Strategy, key.Class) == nil {
				t.Fatalf("AQ PASS restored withdrawn acceptance: %v", err)
			}
		})
	}
}

func TestLoadedArtifactUsesExistingExactRuntimeFence(t *testing.T) {
	record, files := acceptedConsumerFixture(t, 0)
	consumers, err := loadProductionQualification(files)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &managedRuntime{status: model.RuntimeObservedStatus{Identity: "destination", Generation: 7, ProtocolVersion: "v1", State: model.RuntimeStateOffline,
		Capabilities: model.RuntimeCapabilities{model.RuntimeCapabilityStart}, ActiveGatewayArtifact: &model.ActiveGatewayArtifact{Engine: "cpa", ArtifactID: record.ArtifactID}}}
	service := &Service{options: Options{Runtime: runtime}, production: true, consumers: consumers}
	op := operation()
	request := Request{Strategy: bootstrap.NewManaged, ExpectedRuntimeIdentity: "destination", ExpectedRuntimeGeneration: 7, ExpectedArtifactID: record.ArtifactID}
	staged, err := service.Execute(context.Background(), op, request)
	if err != nil || staged.Checkpoint.Stage != "staged" || len(runtime.starts) != 0 {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	op.Strategy = staged.Checkpoint
	committed, err := service.Execute(context.Background(), op, request)
	if err != nil || committed.Checkpoint.Stage != "committed" || len(runtime.starts) != 1 {
		t.Fatalf("commit: %+v %v", committed, err)
	}
	runtime.status.ActiveGatewayArtifact.ArtifactID = model.RuntimeArtifactID("sha256:" + strings.Repeat("c", 64))
	before := op
	if result, err := service.Execute(context.Background(), op, request); err == nil || result.Checkpoint.Stage == "committed" || len(runtime.starts) != 1 || !reflect.DeepEqual(op, before) {
		t.Fatal("changed runtime artifact accepted")
	}
}
