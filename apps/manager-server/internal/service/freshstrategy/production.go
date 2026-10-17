package freshstrategy

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// No installation path, environment variable or HTTP input selects this record.
// Original AQ manifests remain evidence, never an alternative authority.
//
//go:embed qualification
var productionEvidence embed.FS

// These are the consumer implementation bytes, separate from the original
// evidence runner HEADs. A semantic source change invalidates an old acceptance
// without requiring a self-referential commit SHA inside that same commit.
//
//go:embed production.go service.go
var consumerImplementation embed.FS

var errProductionQualification = errors.New("production qualification unavailable")

type consumerKey struct {
	Strategy bootstrap.CPAStrategy
	Class    bootstrap.AdoptSourceClass
}

type qualifiedConsumer struct {
	Qualification bootstrap.CPAQualification
	ArtifactID    model.RuntimeArtifactID
}

type productionRecord struct {
	SchemaVersion        int                     `json:"schemaVersion"`
	ManagementGeneration string                  `json:"managementGeneration"`
	ImplementationSHA256 string                  `json:"implementationSha256"`
	ArchiveSHA256        string                  `json:"archiveSha256"`
	BinarySHA256         string                  `json:"binarySha256"`
	ArtifactID           model.RuntimeArtifactID `json:"artifactId"`
	Consumers            []consumerRecord        `json:"consumers"`
}

type consumerRecord struct {
	Strategy         bootstrap.CPAStrategy      `json:"strategy"`
	SourceClass      bootstrap.AdoptSourceClass `json:"sourceClass"`
	Status           string                     `json:"status"`
	Evidence         []profileEvidence          `json:"evidence"`
	AcceptanceSHA256 string                     `json:"acceptanceSha256"`
	Limitations      []string                   `json:"limitations"`
	InvalidatedBy    []string                   `json:"invalidatedBy"`
}

type profileEvidence struct {
	Profile            string   `json:"profile"`
	ManifestSHA256     string   `json:"manifestSha256"`
	ReportSHA256       string   `json:"reportSha256"`
	ImplementationHead string   `json:"implementationHead"`
	CaseRefs           []string `json:"caseRefs"`
}

// NewProduction replaces injectable qualification with the single compiled
// consumer record. New remains the explicit server-side injection seam for
// synthetic tests; production wiring must use this constructor.
func NewProduction(options Options) *Service {
	options.Qualification = bootstrap.CPAQualification{}
	options.QualifiedArtifactID = ""
	consumers, err := loadProductionQualification(productionEvidence)
	if err != nil {
		consumers = nil // Invalid or unavailable evidence grants nothing.
	}
	return &Service{options: options, production: true, consumers: consumers}
}

func loadProductionQualification(files fs.FS) (map[consumerKey]qualifiedConsumer, error) {
	data, err := fs.ReadFile(files, "qualification/consumer.json")
	if err != nil {
		return nil, errProductionQualification
	}
	var record productionRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil {
		return nil, errProductionQualification
	}
	// This one checked-in record has canonical JSON bytes. Round-trip equality
	// also rejects duplicate, missing, case-folded and trailing JSON fields.
	canonical, err := json.MarshalIndent(record, "", "  ")
	if err != nil || !bytes.Equal(data, append(canonical, '\n')) || record.SchemaVersion != 1 ||
		record.ManagementGeneration != bootstrap.CPAManagementGenerationV8 ||
		!hexDigest(record.ArchiveSHA256, 64) || !hexDigest(record.BinarySHA256, 64) ||
		string(record.ArtifactID) != "sha256:"+record.BinarySHA256 || len(record.Consumers) != 4 {
		return nil, errProductionQualification
	}
	consumers := make(map[consumerKey]qualifiedConsumer)
	for _, consumer := range record.Consumers {
		key := consumerKey{consumer.Strategy, consumer.SourceClass}
		profiles := requiredConsumerProfiles(key)
		if len(profiles) == 0 || len(consumer.Evidence) != len(profiles) || consumer.Limitations == nil ||
			!slices.Equal(consumer.InvalidatedBy, []string{"artifact", "manifest", "report", "implementation", "profile", "config", "mode"}) {
			return nil, errProductionQualification
		}
		if _, duplicate := consumers[key]; duplicate {
			return nil, errProductionQualification
		}
		for i, evidence := range consumer.Evidence {
			if evidence.Profile != profiles[i] || !hexDigest(evidence.ManifestSHA256, 64) ||
				!hexDigest(evidence.ReportSHA256, 64) || !hexDigest(evidence.ImplementationHead, 40) || evidence.CaseRefs == nil {
				return nil, errProductionQualification
			}
		}
		qualified := qualifiedConsumer{Qualification: bootstrap.CPAQualification{ManagementGeneration: record.ManagementGeneration}}
		switch consumer.Status {
		case "pending":
			if consumer.AcceptanceSHA256 != "" {
				return nil, errProductionQualification
			}
		case "accepted":
			// Acceptance is explicit and separate from an AQ PASS. The pinned
			// original independent review must be included in this build.
			if _, err := pinnedEvidence(files, consumer.AcceptanceSHA256, ".md"); err != nil || len(consumer.Limitations) == 0 ||
				record.ImplementationSHA256 != consumerImplementationSHA256() {
				return nil, errProductionQualification
			}
			for _, limitation := range consumer.Limitations {
				if strings.TrimSpace(limitation) == "" {
					return nil, errProductionQualification
				}
			}
			for _, evidence := range consumer.Evidence {
				if err := verifyProfileEvidence(files, record, evidence); err != nil {
					return nil, err
				}
			}
			qualified.Qualification.ArtifactQualified = key.Strategy == bootstrap.NewManaged
			qualified.Qualification.RequiredProfileQualified = key.Strategy != bootstrap.NewManaged
			qualified.Qualification.StagingV8TransitionQualified = key.Class == bootstrap.AdoptLegacyManaged
			if qualified.Qualification.ArtifactQualified {
				qualified.ArtifactID = record.ArtifactID
			}
		default:
			return nil, errProductionQualification
		}
		consumers[key] = qualified
	}
	return consumers, nil
}

func consumerImplementationSHA256() string {
	hash := sha256.New()
	for _, name := range []string{"production.go", "service.go"} {
		data, _ := consumerImplementation.ReadFile(name) // Required at compile time.
		_, _ = hash.Write(data)
	}
	_, _ = hash.Write([]byte(cpa.V8SafeReadImplementationSHA256()))
	return hex.EncodeToString(hash.Sum(nil))
}

func requiredConsumerProfiles(key consumerKey) []string {
	switch key {
	case consumerKey{bootstrap.NewManaged, bootstrap.AdoptSourceNone}:
		return []string{"CPA8-MGMT-MIN-r1"}
	case consumerKey{bootstrap.ConnectExternal, bootstrap.AdoptSourceNone}, consumerKey{bootstrap.AdoptExisting, bootstrap.AdoptNetworkExisting}:
		return []string{"CPA8-EXTERNAL-SAFETY-r1"}
	case consumerKey{bootstrap.AdoptExisting, bootstrap.AdoptLegacyManaged}:
		return []string{"CPA8-EXTERNAL-SAFETY-r1", "CPA8-STAGE-r1"}
	}
	return nil
}

func hexDigest(value string, length int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(value) == length && hex.EncodeToString(decoded) == value
}

func pinnedEvidence(files fs.FS, digest, extension string) ([]byte, error) {
	if !hexDigest(digest, 64) {
		return nil, errProductionQualification
	}
	data, err := fs.ReadFile(files, "qualification/evidence/"+digest+extension)
	actual := sha256.Sum256(data)
	if err != nil || len(data) == 0 || hex.EncodeToString(actual[:]) != digest {
		return nil, errProductionQualification
	}
	return data, nil
}

// Hashes bind the original immutable bytes, including fields outside these
// projections. Independent acceptance still owns coverage, limitations and
// applicability; this loader does not run or reinterpret AQ qualification.
func verifyProfileEvidence(files fs.FS, record productionRecord, evidence profileEvidence) error {
	manifestBytes, err := pinnedEvidence(files, evidence.ManifestSHA256, ".json")
	if err != nil {
		return err
	}
	reportBytes, err := pinnedEvidence(files, evidence.ReportSHA256, ".json")
	if err != nil {
		return err
	}
	var manifest struct {
		SchemaVersion int
		ContractID    string
		Revision      string
		Candidate     struct{ ID, DeclaredSHA256 string }
		Profiles      []struct{ ID string }
		Cases         []struct {
			ID, ProofScope string
			ProfileRefs    []string
		}
	}
	var report struct {
		SchemaVersion                                int
		ContractID, ManifestRevision, ManifestSHA256 string
		Run                                          struct{ ID, RunnerCommit string }
		ArtifactObservation                          struct{ CandidateRef, Provenance, ObservedArchiveSHA256, ObservedBinarySHA256 string }
		Results                                      []struct {
			CaseRef, ExecutionState, AssertionOutcome, Capability string
			EvidenceRefs                                          []string
		}
		Evidence []struct{ ID, Kind, SHA256 string }
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || json.Unmarshal(reportBytes, &report) != nil ||
		manifest.SchemaVersion != 1 || report.SchemaVersion != 1 || manifest.ContractID != "cpamp-cpa-v8-qualification" ||
		report.ContractID != manifest.ContractID || report.ManifestRevision != manifest.Revision ||
		report.ManifestSHA256 != evidence.ManifestSHA256 || report.Run.RunnerCommit != evidence.ImplementationHead ||
		report.ArtifactObservation.CandidateRef != manifest.Candidate.ID ||
		report.ArtifactObservation.Provenance != "locally-verified" ||
		manifest.Candidate.DeclaredSHA256 != record.ArchiveSHA256 ||
		report.ArtifactObservation.ObservedArchiveSHA256 != record.ArchiveSHA256 ||
		report.ArtifactObservation.ObservedBinarySHA256 != record.BinarySHA256 || len(evidence.CaseRefs) == 0 {
		return errProductionQualification
	}
	if !slices.ContainsFunc(manifest.Profiles, func(p struct{ ID string }) bool { return p.ID == evidence.Profile }) {
		return errProductionQualification
	}
	seen := make(map[string]bool)
	for _, caseRef := range evidence.CaseRefs {
		if seen[caseRef] || caseRef == "" {
			return errProductionQualification
		}
		seen[caseRef] = true
		index := slices.IndexFunc(manifest.Cases, func(c struct {
			ID, ProofScope string
			ProfileRefs    []string
		}) bool {
			return c.ID == caseRef
		})
		if index < 0 || !slices.Contains(manifest.Cases[index].ProfileRefs, evidence.Profile) {
			return errProductionQualification
		}
		resultIndex := slices.IndexFunc(report.Results, func(r struct {
			CaseRef, ExecutionState, AssertionOutcome, Capability string
			EvidenceRefs                                          []string
		}) bool {
			return r.CaseRef == caseRef
		})
		if resultIndex < 0 {
			return errProductionQualification
		}
		result := report.Results[resultIndex]
		if result.ExecutionState != "completed" || result.AssertionOutcome != "pass" ||
			(result.Capability != "supported" && result.Capability != "limited") || len(result.EvidenceRefs) == 0 {
			return errProductionQualification
		}
		for _, ref := range result.EvidenceRefs {
			i := slices.IndexFunc(report.Evidence, func(e struct{ ID, Kind, SHA256 string }) bool { return e.ID == ref })
			if i < 0 || report.Evidence[i].Kind != "sanitized-trace" {
				return errProductionQualification
			}
			trace, err := pinnedEvidence(files, report.Evidence[i].SHA256, ".json")
			var binding struct {
				SchemaVersion  int
				RunID, CaseRef string
			}
			if err != nil || json.Unmarshal(trace, &binding) != nil || binding.SchemaVersion != 1 || binding.RunID != report.Run.ID || binding.CaseRef != caseRef {
				return errProductionQualification
			}
		}
	}
	return nil
}
