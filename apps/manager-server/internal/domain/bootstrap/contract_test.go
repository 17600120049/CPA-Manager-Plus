package bootstrap_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
)

const fixtureDir = "../../../../../tests/fixtures/phase4-bootstrap"

type candidateJSON struct {
	Release      string
	SourceCommit string
}

func (c candidateJSON) domain() bootstrap.PlannedCandidate {
	return bootstrap.PlannedCandidate{Release: c.Release, SourceCommit: c.SourceCommit}
}

type admissionJSON struct {
	Shape                 bootstrap.SourceShape
	PlannedCandidate      candidateJSON
	ArtifactEvidenceRef   bootstrap.EvidenceRef
	SourceProvenanceRef   bootstrap.EvidenceRef
	ForwardHopEvidenceRef bootstrap.EvidenceRef
	EvidenceStatus        bootstrap.SourceEvidenceStatus
	Readiness             bootstrap.BridgeReadiness
}

func (a admissionJSON) domain() bootstrap.UpgradeAdmission {
	return bootstrap.UpgradeAdmission{
		Shape:                 a.Shape,
		PlannedCandidate:      a.PlannedCandidate.domain(),
		ArtifactEvidenceRef:   a.ArtifactEvidenceRef,
		SourceProvenanceRef:   a.SourceProvenanceRef,
		ForwardHopEvidenceRef: a.ForwardHopEvidenceRef,
		EvidenceStatus:        a.EvidenceStatus,
		Readiness:             a.Readiness,
	}
}

type adoptionJSON struct {
	ResourceRef       bootstrap.ResourceRef
	Kind              bootstrap.AdoptionResourceKind
	Scope             bootstrap.AdoptionScope
	ReadEvidenceRef   bootstrap.CapabilityEvidenceRef
	ReplayEvidenceRef bootstrap.CapabilityEvidenceRef
	VerifyEvidenceRef bootstrap.CapabilityEvidenceRef
	ManualAction      bootstrap.AdoptionManualAction
	Disposition       bootstrap.AdoptionDisposition
}

func (a adoptionJSON) domain() bootstrap.AdoptionResourceDecision {
	return bootstrap.AdoptionResourceDecision{
		ResourceRef:       a.ResourceRef,
		Kind:              a.Kind,
		Scope:             a.Scope,
		ReadEvidenceRef:   a.ReadEvidenceRef,
		ReplayEvidenceRef: a.ReplayEvidenceRef,
		VerifyEvidenceRef: a.VerifyEvidenceRef,
		ManualAction:      a.ManualAction,
		Disposition:       a.Disposition,
	}
}

type pathJSON struct {
	ID                              string
	Mode                            bootstrap.Mode
	CPAMPAction                     bootstrap.CPAMPAction
	CPAStrategy                     bootstrap.CPAStrategy
	SourceAdmission                 *admissionJSON
	SourceCPA                       bootstrap.SourceCPA
	UsageAction                     bootstrap.UsageAction
	BackupRequired                  bool
	RuntimeMode                     bootstrap.RuntimeMode
	InitializeAdminCredential       bool
	PreserveExistingAdminCredential bool
	AuthenticationRequired          bool
	Mutations                       []string
	Preserved                       []string
	Unsupported                     []string
	DiscoveredResourceRefs          []bootstrap.ResourceRef
	AdoptionDecisions               []adoptionJSON
	ControlBasePath                 string
	EstimatedSteps                  []string
	MigrationOrdering               []bootstrap.MigrationStage
}

func (p pathJSON) domain() bootstrap.Plan {
	var admission *bootstrap.UpgradeAdmission
	if p.SourceAdmission != nil {
		value := p.SourceAdmission.domain()
		admission = &value
	}
	decisions := make([]bootstrap.AdoptionResourceDecision, len(p.AdoptionDecisions))
	for i := range p.AdoptionDecisions {
		decisions[i] = p.AdoptionDecisions[i].domain()
	}
	return bootstrap.Plan{
		Mode:                            p.Mode,
		CPAMPAction:                     p.CPAMPAction,
		CPAStrategy:                     p.CPAStrategy,
		SourceAdmission:                 admission,
		SourceCPA:                       p.SourceCPA,
		UsageAction:                     p.UsageAction,
		BackupRequired:                  p.BackupRequired,
		RuntimeMode:                     p.RuntimeMode,
		InitializeAdminCredential:       p.InitializeAdminCredential,
		PreserveExistingAdminCredential: p.PreserveExistingAdminCredential,
		AuthenticationRequired:          p.AuthenticationRequired,
		Mutations:                       p.Mutations,
		Preserved:                       p.Preserved,
		Unsupported:                     p.Unsupported,
		DiscoveredResourceRefs:          p.DiscoveredResourceRefs,
		AdoptionDecisions:               decisions,
		ControlBasePath:                 p.ControlBasePath,
		EstimatedSteps:                  p.EstimatedSteps,
		MigrationOrdering:               p.MigrationOrdering,
	}
}

type classificationJSON struct {
	ID                string
	HasV1ProductState bool
	Admission         *admissionJSON
	RecoveryReason    bootstrap.RecoveryReason
	BootstrapComplete bool
	AdminCredential   bootstrap.AdminCredentialState
	Session           bootstrap.SessionState
	ExpectedMode      bootstrap.Mode
}

type coreJSON struct {
	ManagementGeneration    string
	CoreV0FallbackAllowed   bool
	ExternalManagementOwner string
	ExternalLifecycleOwner  string
}

func (c coreJSON) domain() bootstrap.CPACoreContract {
	return bootstrap.CPACoreContract{
		ManagementGeneration:    c.ManagementGeneration,
		CoreV0FallbackAllowed:   c.CoreV0FallbackAllowed,
		ExternalManagementOwner: c.ExternalManagementOwner,
		ExternalLifecycleOwner:  c.ExternalLifecycleOwner,
	}
}

type fixture struct {
	Schema                   string
	SchemaVersion            int
	ContractID               string
	ExecutionBaseline        string
	PlannedCandidate         candidateJSON
	CPACore                  coreJSON
	UpgradeOrdering          []bootstrap.MigrationStage
	BootstrapModes           []bootstrap.Mode
	CPAStrategies            []bootstrap.CPAStrategy
	Paths                    []pathJSON
	ClassificationAssertions []classificationJSON
	GlobalAssertions         []string
}

func readFixture(t *testing.T) fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var result fixture
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw["$schema"], &result.Schema); err != nil {
		t.Fatal(err)
	}
	return result
}

func setEqual[T ~string](got []T, want []T) bool {
	if len(got) != len(want) {
		return false
	}
	a := make([]string, len(got))
	b := make([]string, len(want))
	for i := range got {
		a[i] = string(got[i])
	}
	for i := range want {
		b[i] = string(want[i])
	}
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

func TestCheckedInContract(t *testing.T) {
	f := readFixture(t)
	if f.Schema != "./contract.schema.json" || f.SchemaVersion != 2 ||
		f.ContractID != "cpamp-v2-phase4-00-bootstrap-contract-v2" ||
		f.ExecutionBaseline != "4d2e2ec72b6b65d84fd1d24ed0022ea65a8dcea0" {
		t.Fatal("contract identity drifted")
	}
	if f.PlannedCandidate.domain() != bootstrap.ExactV1144Candidate() {
		t.Fatal("planned direct candidate drifted")
	}
	if err := f.CPACore.domain().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.ValidateUpgradeOrdering(f.UpgradeOrdering); err != nil {
		t.Fatal(err)
	}
	if !setEqual(f.BootstrapModes, []bootstrap.Mode{
		bootstrap.Fresh, bootstrap.Upgrade, bootstrap.UnsupportedUpgrade, bootstrap.Recovery, bootstrap.Completed,
	}) {
		t.Fatal("bootstrap modes drifted")
	}
	if !setEqual(f.CPAStrategies, []bootstrap.CPAStrategy{
		bootstrap.NewManaged, bootstrap.AdoptExisting, bootstrap.ConnectExternal, bootstrap.MigrateLegacy,
	}) {
		t.Fatal("CPA strategies drifted")
	}
	if len(f.Paths) != 5 {
		t.Fatalf("got %d paths, want 5", len(f.Paths))
	}
	seenPaths := map[string]bool{}
	for _, path := range f.Paths {
		if seenPaths[path.ID] {
			t.Fatalf("duplicate path %s", path.ID)
		}
		seenPaths[path.ID] = true
		if err := path.domain().Validate(); err != nil {
			t.Fatalf("%s: %v", path.ID, err)
		}
	}
	for _, id := range []string{"P4-BOOT-01", "P4-BOOT-02", "P4-BOOT-03", "P4-BOOT-04", "P4-BOOT-05"} {
		if !seenPaths[id] {
			t.Fatalf("missing path %s", id)
		}
	}
	for _, assertion := range f.ClassificationAssertions {
		var admission *bootstrap.UpgradeAdmission
		if assertion.Admission != nil {
			value := assertion.Admission.domain()
			admission = &value
		}
		got, err := bootstrap.Classify(bootstrap.ClassificationEvidence{
			HasV1ProductState: assertion.HasV1ProductState,
			Admission:         admission,
			RecoveryReason:    assertion.RecoveryReason,
			BootstrapComplete: assertion.BootstrapComplete,
			AdminCredential:   assertion.AdminCredential,
			Session:           assertion.Session,
		})
		if err != nil || got != assertion.ExpectedMode {
			t.Fatalf("%s: got %q err=%v want %q", assertion.ID, got, err, assertion.ExpectedMode)
		}
	}
	wantGlobal := []string{
		"shape_not_release_identity",
		"planned_candidate_not_artifact_authority",
		"artifact_not_source_or_hop_authority",
		"source_or_hop_evidence_not_readiness",
		"readiness_not_writable_grant",
		"read_only_classification_before_backup",
		"complete_verified_backup_before_write",
		"source_revalidation_before_write",
		"bootstrap_mode_admin_session_orthogonal",
		"fresh_mode_not_admin_initialization_authority",
		"upgrade_preserves_existing_admin_credential",
		"authoritative_history_includes_archive_retained_evidence",
		"cpa_core_v8_only_no_v0_fallback",
		"external_management_not_lifecycle_authority",
		"contract_fixture_never_grants_production_authority",
		"deferred_qualification_remains_pending",
	}
	if !setEqual(f.GlobalAssertions, wantGlobal) {
		t.Fatal("global contract assertions drifted")
	}
}

func exactAdmission() bootstrap.UpgradeAdmission {
	return bootstrap.UpgradeAdmission{
		Shape:               bootstrap.LegacySchemaC,
		PlannedCandidate:    bootstrap.ExactV1144Candidate(),
		ArtifactEvidenceRef: "evidence:artifact",
		SourceProvenanceRef: "evidence:source",
		EvidenceStatus:      bootstrap.ExactCandidateVerified,
		Readiness:           bootstrap.BridgeReady,
	}
}

func baseClassification(admission *bootstrap.UpgradeAdmission) bootstrap.ClassificationEvidence {
	return bootstrap.ClassificationEvidence{
		HasV1ProductState: true,
		Admission:         admission,
		AdminCredential:   bootstrap.AdminCredentialInitialized,
		Session:           bootstrap.SessionMissing,
	}
}

func TestShapeNeverProvesExactRelease(t *testing.T) {
	admission := exactAdmission()
	admission.ArtifactEvidenceRef = ""
	admission.SourceProvenanceRef = ""
	admission.EvidenceStatus = bootstrap.SourceUnverified
	got, err := bootstrap.Classify(baseClassification(&admission))
	if err != nil {
		t.Fatal(err)
	}
	if got != bootstrap.UnsupportedUpgrade {
		t.Fatalf("LegacySchemaC without trusted source evidence classified %q", got)
	}
	admission.EvidenceStatus = bootstrap.UnknownOrNewerSource
	admission.Readiness = bootstrap.BridgeReady
	got, err = bootstrap.Classify(baseClassification(&admission))
	if err != nil {
		t.Fatal(err)
	}
	if got != bootstrap.UnsupportedUpgrade {
		t.Fatalf("unknown/newer SchemaC classified %q", got)
	}
}

func TestVerifiedCandidateRequiresCurrentSchemaC(t *testing.T) {
	admission := exactAdmission()
	admission.Shape = bootstrap.LegacySchemaB
	if err := admission.Validate(); err == nil {
		t.Fatal("verified exact candidate accepted with non-SchemaC current shape")
	}

	admission = exactAdmission()
	admission.EvidenceStatus = bootstrap.ForwardHopVerified
	admission.ForwardHopEvidenceRef = "evidence:hop"
	admission.Shape = bootstrap.LegacySchemaA
	if err := admission.Validate(); err == nil {
		t.Fatal("verified forward-hop output accepted with non-SchemaC current shape")
	}
}

func TestFreshModeAloneCannotInitializeAdministrator(t *testing.T) {
	base := bootstrap.FreshInitializationPreconditions{
		Mode:               bootstrap.Fresh,
		AdminCredential:    bootstrap.AdminCredentialUninitialized,
		HostClaimQualified: true,
	}
	if !bootstrap.CanInitializeAdministrator(base) {
		t.Fatal("qualified fresh initialization was rejected")
	}

	cases := []bootstrap.FreshInitializationPreconditions{
		{Mode: bootstrap.Upgrade, AdminCredential: bootstrap.AdminCredentialUninitialized, HostClaimQualified: true},
		{Mode: bootstrap.Fresh, AdminCredential: bootstrap.AdminCredentialInitialized, HostClaimQualified: true},
		{Mode: bootstrap.Fresh, AdminCredential: bootstrap.AdminCredentialUnverifiable, HostClaimQualified: true},
		{Mode: bootstrap.Fresh, AdminCredential: bootstrap.AdminCredentialUninitialized, HostClaimQualified: false},
	}
	for _, tc := range cases {
		if bootstrap.CanInitializeAdministrator(tc) {
			t.Fatalf("unsafe fresh initialization predicate returned true: %+v", tc)
		}
	}
}

func TestEvidenceReadinessAndGrantRemainIndependent(t *testing.T) {
	admission := exactAdmission()
	admission.Readiness = bootstrap.BridgeBlocked
	got, err := bootstrap.Classify(baseClassification(&admission))
	if err != nil {
		t.Fatal(err)
	}
	if got != bootstrap.Recovery {
		t.Fatalf("verified but blocked source classified %q", got)
	}

	admission = exactAdmission()
	ordering := []bootstrap.MigrationStage{
		bootstrap.ReadOnlyClassification,
		bootstrap.CompleteVerifiedBackup,
		bootstrap.SourceRevalidation,
		bootstrap.WritableMigration,
	}
	base := bootstrap.WritablePreconditions{
		Mode:                      bootstrap.Upgrade,
		Admission:                 admission,
		BackupVerified:            true,
		SourceRevalidated:         true,
		ManagerAuthorityQualified: true,
		Ordering:                  ordering,
	}
	if !bootstrap.CanIssueWritableGrant(base) {
		t.Fatal("complete future preconditions should satisfy the pure grant predicate")
	}
	tests := []struct {
		name string
		edit func(*bootstrap.WritablePreconditions)
	}{
		{"backup missing", func(p *bootstrap.WritablePreconditions) { p.BackupVerified = false }},
		{"source not revalidated", func(p *bootstrap.WritablePreconditions) { p.SourceRevalidated = false }},
		{"authority unqualified", func(p *bootstrap.WritablePreconditions) { p.ManagerAuthorityQualified = false }},
		{"readiness blocked", func(p *bootstrap.WritablePreconditions) { p.Admission.Readiness = bootstrap.BridgeBlocked }},
		{"source unverified", func(p *bootstrap.WritablePreconditions) { p.Admission.EvidenceStatus = bootstrap.SourceUnverified }},
		{"ordering broken", func(p *bootstrap.WritablePreconditions) {
			p.Ordering = []bootstrap.MigrationStage{
				bootstrap.ReadOnlyClassification,
				bootstrap.SourceRevalidation,
				bootstrap.CompleteVerifiedBackup,
				bootstrap.WritableMigration,
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value := base
			value.Ordering = append([]bootstrap.MigrationStage(nil), base.Ordering...)
			tc.edit(&value)
			if bootstrap.CanIssueWritableGrant(value) {
				t.Fatal("unsafe writable grant predicate returned true")
			}
		})
	}
}

func TestBootstrapModeDoesNotDependOnSessionState(t *testing.T) {
	admission := exactAdmission()
	for _, session := range []bootstrap.SessionState{
		bootstrap.SessionMissing, bootstrap.SessionAuthenticated, bootstrap.SessionExpired,
	} {
		for _, admin := range []bootstrap.AdminCredentialState{
			bootstrap.AdminCredentialInitialized, bootstrap.AdminCredentialUnverifiable,
		} {
			input := baseClassification(&admission)
			input.Session = session
			input.AdminCredential = admin
			got, err := bootstrap.Classify(input)
			if err != nil || got != bootstrap.Upgrade {
				t.Fatalf("session=%s admin=%s got=%s err=%v", session, admin, got, err)
			}
		}
	}
}

func TestUpgradeOrderingRejectsEarlyWrite(t *testing.T) {
	if err := bootstrap.ValidateUpgradeOrdering([]bootstrap.MigrationStage{
		bootstrap.ReadOnlyClassification,
		bootstrap.CompleteVerifiedBackup,
		bootstrap.SourceRevalidation,
		bootstrap.WritableMigration,
	}); err != nil {
		t.Fatal(err)
	}
	for _, stages := range [][]bootstrap.MigrationStage{
		{bootstrap.ReadOnlyClassification, bootstrap.WritableMigration},
		{bootstrap.ReadOnlyClassification, bootstrap.SourceRevalidation, bootstrap.CompleteVerifiedBackup, bootstrap.WritableMigration},
		{bootstrap.CompleteVerifiedBackup, bootstrap.ReadOnlyClassification, bootstrap.SourceRevalidation, bootstrap.WritableMigration},
	} {
		if err := bootstrap.ValidateUpgradeOrdering(stages); err == nil {
			t.Fatalf("unsafe ordering accepted: %v", stages)
		}
	}
}

func TestUpgradePlanCannotReinitializeAdminOrReplaceHistory(t *testing.T) {
	f := readFixture(t)
	var upgrade bootstrap.Plan
	for _, path := range f.Paths {
		if path.ID == "P4-BOOT-04" {
			upgrade = path.domain()
			break
		}
	}
	if err := upgrade.Validate(); err != nil {
		t.Fatal(err)
	}
	upgrade.InitializeAdminCredential = true
	if err := upgrade.Validate(); err == nil {
		t.Fatal("upgrade reinitialized administrator credential")
	}

	for _, path := range f.Paths {
		if path.ID == "P4-BOOT-04" {
			upgrade = path.domain()
			break
		}
	}
	upgrade.Preserved = []string{"authoritative_usage_history"}
	if err := upgrade.Validate(); err == nil {
		t.Fatal("upgrade dropped archive/retained evidence preservation")
	}
}

func TestCPACoreContractIsV8OnlyAndKeepsExternalLifecycleUserOwned(t *testing.T) {
	valid := bootstrap.CPACoreContract{
		ManagementGeneration:    bootstrap.CPAManagementGenerationV8,
		CoreV0FallbackAllowed:   false,
		ExternalManagementOwner: "cpamp_manager",
		ExternalLifecycleOwner:  "operator",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := []bootstrap.CPACoreContract{
		{ManagementGeneration: "v0", ExternalManagementOwner: "cpamp_manager", ExternalLifecycleOwner: "operator"},
		{ManagementGeneration: "v8", CoreV0FallbackAllowed: true, ExternalManagementOwner: "cpamp_manager", ExternalLifecycleOwner: "operator"},
		{ManagementGeneration: "v8", ExternalManagementOwner: "cpamp_manager", ExternalLifecycleOwner: "cpamp_manager"},
	}
	for _, value := range invalid {
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid core contract accepted: %+v", value)
		}
	}
}

func TestRecoveryCannotBecomeCompletedDirectly(t *testing.T) {
	if err := bootstrap.ValidateTransition(bootstrap.Recovery, bootstrap.Completed); err == nil {
		t.Fatal("recovery transitioned directly to completed")
	}
	if err := bootstrap.ValidateTransition(bootstrap.Upgrade, bootstrap.Completed); err != nil {
		t.Fatal(err)
	}
}

func TestContractSchemaIsClosedV2(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtureDir, "contract.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatal("schema must be draft 2020-12 and reject top-level additional properties")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema properties missing")
	}
	version, ok := properties["schemaVersion"].(map[string]any)
	if !ok || version["const"] != float64(2) {
		t.Fatal("schemaVersion must remain 2")
	}
}
