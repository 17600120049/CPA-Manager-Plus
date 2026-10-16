package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
)

const phase401FixtureDir = "../../../../../tests/fixtures/phase4-bootstrap"

type phase401RouteJSON struct {
	ID                  string
	ContractVersion     string
	MethodClass         bootstrap.RouteMethodClass
	Path                *string
	PathBinding         string
	Namespace           string
	Lifecycle           bootstrap.RouteLifecycle
	PublicOwner         bootstrap.RouteOwnerClass
	InternalOwner       bootstrap.RouteOwnerClass
	AuthClass           bootstrap.RouteAuthClass
	CredentialAuthority bootstrap.CredentialAuthority
	FallbackPolicy      bootstrap.RouteFallbackPolicy
	TerminalBehavior    bootstrap.RouteTerminalBehavior
	CollisionClass      bootstrap.RouteCollisionClass
}

func (r phase401RouteJSON) domain() bootstrap.RouteOwnerEntry {
	return bootstrap.RouteOwnerEntry{
		ID: r.ID, ContractVersion: r.ContractVersion, MethodClass: r.MethodClass,
		Path: valueOrEmpty(r.Path), PathBinding: r.PathBinding, Namespace: r.Namespace,
		Lifecycle: r.Lifecycle, PublicOwner: r.PublicOwner, InternalOwner: r.InternalOwner,
		AuthClass: r.AuthClass, CredentialAuthority: r.CredentialAuthority,
		FallbackPolicy: r.FallbackPolicy, TerminalBehavior: r.TerminalBehavior,
		CollisionClass: r.CollisionClass,
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type phase401CandidateJSON struct {
	Release      string
	SourceCommit string
}

type phase401ExpectedJSON struct {
	Accepted                bool
	Mode                    bootstrap.Mode
	Reason                  bootstrap.SourceAdmissionReason
	Qualification           bootstrap.QualificationState
	SourceIdentityPreserved bool
}

type phase401AdmissionJSON struct {
	ID                   string
	Case                 bootstrap.SourceAdmissionCase
	Shape                bootstrap.SourceShape
	Provenance           bootstrap.SourceEvidenceStatus
	RequestedTarget      phase401CandidateJSON
	ObservedSourceCommit string
	Expected             phase401ExpectedJSON
}

type phase401Fixture struct {
	Schema                         string `json:"$schema"`
	ContractVersion                string
	ProductBaseline                string
	ProductionUpgradeEnabled       bool
	FixedBootstrapDiscoveryPath    string
	RouteOwnerLedger               []phase401RouteJSON
	CompletedDiscoveryTerminal     string
	UnauthenticatedDiscoveryFields []string
	ForbiddenDiscoveryFields       []string
	AdmissionCases                 []phase401AdmissionJSON
	IntegrationRequirements        []string
}

func readPhase401Fixture(t *testing.T) phase401Fixture {
	t.Helper()
	fixturePath := filepath.Join(phase401FixtureDir, "phase4-01-contract.json")
	schemaPath := filepath.Join(phase401FixtureDir, "phase4-01-contract.schema.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := decodePhase401Fixture(data, schemaData)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func decodePhase401Fixture(data, schemaData []byte) (phase401Fixture, error) {
	var fixture phase401Fixture
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return fixture, err
	}
	if err := rejectDuplicateJSONKeys(schemaData); err != nil {
		return fixture, err
	}
	var schema map[string]any
	var raw any
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return fixture, err
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fixture, err
	}
	if err := validateSchemaValue(raw, schema, schema); err != nil {
		return fixture, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return fixture, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fixture, io.ErrUnexpectedEOF
		}
		return fixture, err
	}
	return fixture, nil
}

func TestPhase401FixtureIsStrictlyReadFromRepository(t *testing.T) {
	fixture := readPhase401Fixture(t)
	if fixture.Schema != "./phase4-01-contract.schema.json" ||
		fixture.ContractVersion != "cpamp.phase4-01.contract.v1" ||
		fixture.ProductBaseline != "c0a4418d02a18247630cec03365637cce0eaf639" {
		t.Fatal("phase4-01 fixture header drifted")
	}
	if fixture.ProductionUpgradeEnabled {
		t.Fatal("fixture enabled production Upgrade")
	}
	entries := make([]bootstrap.RouteOwnerEntry, len(fixture.RouteOwnerLedger))
	for i := range fixture.RouteOwnerLedger {
		entries[i] = fixture.RouteOwnerLedger[i].domain()
	}
	ledger := bootstrap.RouteOwnerLedger{
		ContractVersion:             bootstrap.RouteContractVersion,
		ProductionUpgradeEnabled:    fixture.ProductionUpgradeEnabled,
		FixedBootstrapDiscoveryPath: fixture.FixedBootstrapDiscoveryPath,
		Entries:                     entries,
	}
	if err := ledger.Validate(); err != nil {
		t.Fatalf("route owner ledger rejected: %v", err)
	}

	owners := map[bootstrap.RouteOwnerClass]bool{}
	for _, entry := range entries {
		owners[entry.PublicOwner] = true
	}
	for _, owner := range []bootstrap.RouteOwnerClass{
		bootstrap.OwnerBootstrapDiscovery, bootstrap.OwnerPublicAPIV2,
		bootstrap.OwnerManagerControl, bootstrap.OwnerGatewayDataPlane,
		bootstrap.OwnerFullWebStatic, bootstrap.OwnerPanelLite,
		bootstrap.OwnerFixedTransport, bootstrap.OwnerRetiredControlDeny,
	} {
		if !owners[owner] {
			t.Fatalf("route owner class %q missing from repository fixture", owner)
		}
	}
	if fixture.CompletedDiscoveryTerminal != "generic_404" {
		t.Fatal("completed discovery terminal behavior drifted")
	}
	for _, field := range []string{"controlBasePath", "sourceCPAUrl", "cpaManagementKey", "adminKey", "dbPath", "archivePath", "backupReceipt", "resourceRefs", "mutations", "internalPorts", "filesystemPaths", "rawVerifierError"} {
		if !contains(fixture.ForbiddenDiscoveryFields, field) {
			t.Fatalf("sensitive discovery field %q is not forbidden by fixture", field)
		}
	}
}

func TestPhase401StrictRunnerRejectsUnknownAndDuplicateFixtureFields(t *testing.T) {
	fixturePath := filepath.Join(phase401FixtureDir, "phase4-01-contract.json")
	schemaPath := filepath.Join(phase401FixtureDir, "phase4-01-contract.schema.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(data, []byte(`"contractVersion": "cpamp.phase4-01.contract.v1",`), []byte(`"contractVersion": "cpamp.phase4-01.contract.v1", "unexpected": true,`), 1)
	if _, err := decodePhase401Fixture(unknown, schema); err == nil {
		t.Fatal("new fixture runner accepted an unknown field")
	}
	duplicate := bytes.Replace(data, []byte(`"contractVersion": "cpamp.phase4-01.contract.v1",`), []byte(`"contractVersion": "cpamp.phase4-01.contract.v1", "contractVersion": "cpamp.phase4-01.contract.v1",`), 1)
	if _, err := decodePhase401Fixture(duplicate, schema); err == nil {
		t.Fatal("new fixture runner accepted a duplicate field")
	}
	unknownEnum := bytes.Replace(data, []byte(`"methodClass": "all"`), []byte(`"methodClass": "future_method"`), 1)
	if _, err := decodePhase401Fixture(unknownEnum, schema); err == nil {
		t.Fatal("new fixture runner ignored an unknown enum")
	}
}

func TestRouteOwnerLedgerRejectsDuplicateAndConflictingOwnership(t *testing.T) {
	fixture := readPhase401Fixture(t)
	entries := make([]bootstrap.RouteOwnerEntry, len(fixture.RouteOwnerLedger))
	for i := range fixture.RouteOwnerLedger {
		entries[i] = fixture.RouteOwnerLedger[i].domain()
	}
	base := bootstrap.RouteOwnerLedger{
		ContractVersion:             bootstrap.RouteContractVersion,
		FixedBootstrapDiscoveryPath: fixture.FixedBootstrapDiscoveryPath,
		Entries:                     entries,
	}

	duplicate := append([]bootstrap.RouteOwnerEntry(nil), entries...)
	duplicate = append(duplicate, entries[0])
	base.Entries = duplicate
	if err := base.Validate(); err == nil {
		t.Fatal("duplicate route owner entry accepted")
	}
	duplicateRoute := entries[1]
	duplicateRoute.ID = "duplicate-public-api-route"
	base.Entries = append(append([]bootstrap.RouteOwnerEntry(nil), entries...), duplicateRoute)
	if err := base.Validate(); err == nil {
		t.Fatal("duplicate route ownership accepted under a different entry id")
	}
	duplicateRoute = entries[1]
	duplicateRoute.ID = "duplicate-public-api-namespace"
	duplicateRoute.Namespace = "future-namespace-alias"
	base.Entries = append(append([]bootstrap.RouteOwnerEntry(nil), entries...), duplicateRoute)
	if err := base.Validate(); err == nil {
		t.Fatal("duplicate route path accepted under a different namespace")
	}

	unknownOwner := entries[1]
	unknownOwner.PublicOwner = bootstrap.RouteOwnerClass("future_owner")
	if err := unknownOwner.Validate(); err == nil {
		t.Fatal("unknown route owner accepted")
	}

	managerToGateway := entries[1]
	managerToGateway.FallbackPolicy = bootstrap.FallbackGateway
	if err := managerToGateway.Validate(); err == nil {
		t.Fatal("Manager route was allowed to fall back to Gateway")
	}
	managerToGateway = entries[1]
	managerToGateway.TerminalBehavior = bootstrap.TerminalGatewayForward
	if err := managerToGateway.Validate(); err == nil {
		t.Fatal("Manager route was allowed to forward to Gateway")
	}

	panelFallback := entries[5]
	panelFallback.FallbackPolicy = bootstrap.FallbackNoGateway
	if err := panelFallback.Validate(); err == nil {
		t.Fatal("Panel Lite route accepted a non-panel fallback")
	}

	conflict := entries[1]
	conflict.InternalOwner = bootstrap.OwnerGatewayDataPlane
	if err := conflict.Validate(); err == nil {
		t.Fatal("conflicting public/internal route ownership accepted")
	}
	conflict = entries[3]
	conflict.InternalOwner = bootstrap.OwnerManagerControl
	if err := conflict.Validate(); err == nil {
		t.Fatal("Gateway/public route ownership conflict accepted")
	}

	invalidMethod := entries[1]
	invalidMethod.MethodClass = bootstrap.RouteMethodClass("post")
	if err := invalidMethod.Validate(); err == nil {
		t.Fatal("unknown method class accepted")
	}
}

func TestPublicDiscoveryAndStatusAxesFailClosed(t *testing.T) {
	mode := bootstrap.Fresh
	valid := []bootstrap.PublicDiscovery{
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicFreshClaimRequired, Mode: &mode, AuthenticationRequired: true, AuthEntry: bootstrap.AuthEntryFreshClaim, Reason: bootstrap.ReasonFreshClaimRequired},
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicUnavailable, AuthEntry: bootstrap.AuthEntryUnavailable, Reason: bootstrap.ReasonObservationUnavailable},
	}
	for _, value := range valid {
		if err := value.Validate(); err != nil {
			t.Fatalf("valid public discovery rejected: %v", err)
		}
	}
	for _, value := range []bootstrap.PublicDiscovery{
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicUnsupportedUpgrade, Mode: modePtr(bootstrap.Upgrade), AuthEntry: bootstrap.AuthEntryAdminSession, Reason: bootstrap.ReasonQualificationUnavailable},
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicUnsupportedUpgrade, Mode: modePtr(bootstrap.UnsupportedUpgrade), AuthEntry: bootstrap.AuthEntryAdminSession, Reason: bootstrap.ReasonQualificationUnavailable},
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicRecoveryRequired, Mode: modePtr(bootstrap.Completed), AuthEntry: bootstrap.AuthEntryHostIntervention, Reason: bootstrap.ReasonRecoveryHostIntervention},
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicUnavailable, Mode: &mode, AuthEntry: bootstrap.AuthEntryUnavailable, Reason: bootstrap.ReasonObservationUnavailable},
		{ContractVersion: bootstrap.PublicDiscoveryContractVersion, Status: bootstrap.PublicStatus("future_status"), AuthEntry: bootstrap.AuthEntryUnavailable, Reason: bootstrap.ReasonObservationUnavailable},
	} {
		if value.Status == bootstrap.PublicUnsupportedUpgrade {
			value.AuthenticationRequired = false
		}
		if err := value.Validate(); err == nil {
			t.Fatal("invalid public discovery accepted")
		}
	}

	for _, value := range []bootstrap.StatusSnapshot{
		{Mode: bootstrap.Fresh, AdminCredential: bootstrap.AdminCredentialUninitialized, Session: bootstrap.SessionMissing},
		{Mode: bootstrap.Upgrade, AdminCredential: bootstrap.AdminCredentialInitialized, Session: bootstrap.SessionMissing},
		{Mode: bootstrap.Recovery, AdminCredential: bootstrap.AdminCredentialUnverifiable, Session: bootstrap.SessionMissing},
		{Mode: bootstrap.Completed, AdminCredential: bootstrap.AdminCredentialInitialized, Session: bootstrap.SessionMissing},
		{Mode: bootstrap.Upgrade, AdminCredential: bootstrap.AdminCredentialInitialized, Session: bootstrap.SessionExpired},
	} {
		if err := value.Validate(); err != nil {
			t.Fatalf("orthogonal status rejected: %+v: %v", value, err)
		}
	}
	if err := (bootstrap.StatusSnapshot{Mode: bootstrap.Fresh, AdminCredential: bootstrap.AdminCredentialInitialized, Session: bootstrap.SessionMissing}).Validate(); err == nil {
		t.Fatal("Fresh with initialized admin credential accepted")
	}
	if err := (bootstrap.StatusSnapshot{Mode: bootstrap.Mode("future"), AdminCredential: bootstrap.AdminCredentialInitialized, Session: bootstrap.SessionMissing}).Validate(); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestLegacyBootstrapStatusIsEvidenceOnly(t *testing.T) {
	freshEvidence := bootstrap.ClassificationEvidence{
		HasV1ProductState: false,
		AdminCredential:   bootstrap.AdminCredentialUninitialized,
		Session:           bootstrap.SessionMissing,
	}
	for _, legacyStatus := range []string{"fresh", "ready"} {
		mode, err := bootstrap.ClassifyLegacyEvidence(bootstrap.LegacyBootstrapEvidence{LegacyStatus: legacyStatus, Classification: freshEvidence})
		if err != nil || mode != bootstrap.Fresh {
			t.Fatalf("legacy status %q changed evidence classification: mode=%s err=%v", legacyStatus, mode, err)
		}
	}
	legacyStatus := bootstrap.LegacyBootstrapEvidence{
		LegacyStatus:   "fresh",
		Classification: baseClassification(nil),
	}
	mode, err := bootstrap.ClassifyLegacyEvidence(legacyStatus)
	if err != nil || mode != bootstrap.UnsupportedUpgrade {
		t.Fatalf("legacy status fresh overrode legacy source evidence: mode=%s err=%v", mode, err)
	}
	legacyStatus.LegacyStatus = "ready"
	mode, err = bootstrap.ClassifyLegacyEvidence(legacyStatus)
	if err != nil || mode == bootstrap.Completed {
		t.Fatalf("legacy ready was treated as completed authority: mode=%s err=%v", mode, err)
	}
}

func TestAUT09AndAUT10RemainPendingAndFailClosed(t *testing.T) {
	fixture := readPhase401Fixture(t)
	for _, item := range fixture.AdmissionCases {
		attempt := bootstrap.SourceAdmissionAttempt{
			Case: item.Case, Shape: item.Shape, Provenance: item.Provenance,
			RequestedTarget:      bootstrap.PlannedCandidate{Release: item.RequestedTarget.Release, SourceCommit: item.RequestedTarget.SourceCommit},
			ObservedSourceCommit: item.ObservedSourceCommit,
		}
		decision, err := attempt.Evaluate()
		if err != nil {
			t.Fatalf("%s evaluation failed: %v", item.ID, err)
		}
		if err := decision.Validate(); err != nil {
			t.Fatalf("%s decision invalid: %v", item.ID, err)
		}
		if decision.Accepted || decision.Qualification != bootstrap.QualificationPending ||
			decision.Mode == bootstrap.Fresh || decision.SourceIdentityPreserved == false {
			t.Fatalf("%s was promoted or rewritten: %+v", item.ID, decision)
		}
		if decision.Reason != item.Expected.Reason || decision.Mode != item.Expected.Mode ||
			decision.Qualification != item.Expected.Qualification || decision.SourceIdentityPreserved != item.Expected.SourceIdentityPreserved {
			t.Fatalf("%s decision=%+v expected=%+v", item.ID, decision, item.Expected)
		}
	}
	unknown := bootstrap.SourceAdmissionAttempt{Case: bootstrap.SourceAdmissionCase("future"), Shape: bootstrap.LegacySchemaC, Provenance: bootstrap.UnknownOrNewerSource, RequestedTarget: bootstrap.ExactV1144Candidate(), ObservedSourceCommit: "unknown"}
	if _, err := unknown.Evaluate(); err == nil {
		t.Fatal("unknown admission case accepted")
	}
	for name, malformed := range map[string]bootstrap.SourceAdmissionAttempt{
		"missing observed evidence": {
			Case: bootstrap.AdmissionUnknownNewerSameSchema, Shape: bootstrap.LegacySchemaC,
			Provenance: bootstrap.UnknownOrNewerSource, RequestedTarget: bootstrap.ExactV1144Candidate(),
		},
		"unknown shape": {
			Case: bootstrap.AdmissionUnknownNewerSameSchema, Shape: bootstrap.SourceShape("future_shape"),
			Provenance: bootstrap.UnknownOrNewerSource, RequestedTarget: bootstrap.ExactV1144Candidate(), ObservedSourceCommit: "unknown",
		},
		"trusted provenance": {
			Case: bootstrap.AdmissionUnknownNewerSameSchema, Shape: bootstrap.LegacySchemaC,
			Provenance: bootstrap.ExactCandidateVerified, RequestedTarget: bootstrap.ExactV1144Candidate(), ObservedSourceCommit: "unknown",
		},
		"compatibility range guess": {
			Case: bootstrap.AdmissionUnknownNewerSameSchema, Shape: bootstrap.LegacySchemaB,
			Provenance: bootstrap.UnknownOrNewerSource, RequestedTarget: bootstrap.ExactV1144Candidate(), ObservedSourceCommit: "unknown",
		},
		"target drift": {
			Case: bootstrap.AdmissionUnknownNewerSameSchema, Shape: bootstrap.LegacySchemaC,
			Provenance:      bootstrap.UnknownOrNewerSource,
			RequestedTarget: bootstrap.PlannedCandidate{Release: "v1.15.0", SourceCommit: "new-target"}, ObservedSourceCommit: "unknown",
		},
	} {
		if _, err := malformed.Evaluate(); err == nil {
			t.Fatalf("malformed %s evidence accepted", name)
		}
	}
	for name, stale := range map[string]bootstrap.SourceAdmissionDecision{
		"promoted authority": {
			Accepted: true, Mode: bootstrap.Upgrade, Reason: bootstrap.ReasonUnknownNewerSameSchema,
			Qualification: bootstrap.QualificationState("qualified"), SourceIdentityPreserved: true,
		},
		"rewritten identity": {
			Accepted: false, Mode: bootstrap.UnsupportedUpgrade, Reason: bootstrap.ReasonUnknownNewerSameSchema,
			Qualification: bootstrap.QualificationPending, SourceIdentityPreserved: false,
		},
		"unknown reason": {
			Accepted: false, Mode: bootstrap.UnsupportedUpgrade, Reason: bootstrap.SourceAdmissionReason("future_reason"),
			Qualification: bootstrap.QualificationPending, SourceIdentityPreserved: true,
		},
	} {
		if err := stale.Validate(); err == nil {
			t.Fatalf("stale %s authority accepted", name)
		}
	}
}

func modePtr(value bootstrap.Mode) *bootstrap.Mode { return &value }

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
