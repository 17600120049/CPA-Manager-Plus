// Package bootstrap defines the Phase4 bootstrap domain contract without
// detecting or executing it. The legacy persisted BootstrapState is separate.
package bootstrap

import (
	"errors"
	"fmt"
	"strings"
)

const (
	PlannedDirectRelease      = "v1.14.4"
	PlannedDirectSourceCommit = "166262365d506157b99968105c890f480cc7c07c"
	CPAManagementGenerationV8 = "v8"
)

type Mode string

const (
	Fresh              Mode = "fresh"
	Upgrade            Mode = "upgrade"
	UnsupportedUpgrade Mode = "unsupported_upgrade"
	Recovery           Mode = "recovery"
	Completed          Mode = "completed"
)

type CPAStrategy string

const (
	NewManaged      CPAStrategy = "new_managed"
	AdoptExisting   CPAStrategy = "adopt_existing"
	ConnectExternal CPAStrategy = "connect_external"
	MigrateLegacy   CPAStrategy = "migrate_legacy"
)

type CPAMPAction string

const (
	Initialize CPAMPAction = "initialize"
	MigrateV1  CPAMPAction = "migrate_v1"
)

type UsageAction string

const (
	NoUsageAction UsageAction = "none"
	PreserveUsage UsageAction = "preserve"
)

type RuntimeMode string

const (
	Embedded RuntimeMode = "embedded"
	External RuntimeMode = "external"
)

type SourceCPA string

const (
	ExistingCPA SourceCPA = "existing"
	LegacyCPA   SourceCPA = "legacy"
	ExternalCPA SourceCPA = "external"
)

type SourceShape string

const (
	LegacySchemaA SourceShape = "legacy_schema_a"
	LegacySchemaB SourceShape = "legacy_schema_b"
	LegacySchemaC SourceShape = "legacy_schema_c"
	UnknownShape  SourceShape = "unknown"
)

type SourceEvidenceStatus string

const (
	SourceUnverified            SourceEvidenceStatus = "unverified"
	ExactCandidateVerified      SourceEvidenceStatus = "exact_candidate_verified"
	ForwardHopVerified          SourceEvidenceStatus = "forward_hop_verified"
	UnknownOrNewerSource        SourceEvidenceStatus = "unknown_or_newer"
)

type BridgeReadiness string

const (
	BridgeUnknown         BridgeReadiness = "unknown"
	BridgeReady           BridgeReadiness = "ready"
	BridgeBlocked         BridgeReadiness = "blocked"
	BridgeRecovery        BridgeReadiness = "recovery"
	BridgeOfflineRequired BridgeReadiness = "offline_required"
)

type RecoveryReason string

const (
	OperationIncomplete    RecoveryReason = "operation_incomplete"
	RollbackRequired       RecoveryReason = "rollback_required"
	DamagedState           RecoveryReason = "damaged_state"
	MigrationIncomplete    RecoveryReason = "migration_incomplete"
	BackupInvalid          RecoveryReason = "backup_invalid"
	SourceChanged          RecoveryReason = "source_changed"
	OfflineActionRequired  RecoveryReason = "offline_action_required"
	AuthorityUnqualified   RecoveryReason = "authority_unqualified"
	ManualRecoveryRequired RecoveryReason = "manual_recovery_required"
)

type AdminCredentialState string

const (
	AdminCredentialUninitialized AdminCredentialState = "uninitialized"
	AdminCredentialInitialized   AdminCredentialState = "initialized"
	AdminCredentialUnverifiable  AdminCredentialState = "unverifiable"
)

type SessionState string

const (
	SessionMissing       SessionState = "missing"
	SessionAuthenticated SessionState = "authenticated"
	SessionExpired       SessionState = "expired"
)

type PlannedCandidate struct {
	Release      string
	SourceCommit string
}

func ExactV1144Candidate() PlannedCandidate {
	return PlannedCandidate{Release: PlannedDirectRelease, SourceCommit: PlannedDirectSourceCommit}
}

func (c PlannedCandidate) valid() bool {
	return c == ExactV1144Candidate()
}

type EvidenceRef string

func validEvidenceRef(ref EvidenceRef) bool {
	if ref == "" {
		return true
	}
	id, ok := strings.CutPrefix(string(ref), "evidence:")
	return ok && id != "" && strings.TrimSpace(id) == id
}

// UpgradeAdmission freezes six independent dimensions:
// shape -> planned candidate -> artifact evidence -> source/hop evidence ->
// bridge readiness -> writable grant. Nothing here issues a grant.
type UpgradeAdmission struct {
	Shape                 SourceShape
	PlannedCandidate      PlannedCandidate
	ArtifactEvidenceRef   EvidenceRef
	SourceProvenanceRef   EvidenceRef
	ForwardHopEvidenceRef EvidenceRef
	EvidenceStatus        SourceEvidenceStatus
	Readiness             BridgeReadiness
}

func validSourceShape(shape SourceShape) bool {
	switch shape {
	case LegacySchemaA, LegacySchemaB, LegacySchemaC, UnknownShape:
		return true
	default:
		return false
	}
}

func validReadiness(readiness BridgeReadiness) bool {
	switch readiness {
	case BridgeUnknown, BridgeReady, BridgeBlocked, BridgeRecovery, BridgeOfflineRequired:
		return true
	default:
		return false
	}
}

func (a UpgradeAdmission) Validate() error {
	if !validSourceShape(a.Shape) {
		return fmt.Errorf("unknown source shape %q", a.Shape)
	}
	if !a.PlannedCandidate.valid() {
		return errors.New("planned direct candidate must remain exact v1.14.4")
	}
	if !validReadiness(a.Readiness) {
		return fmt.Errorf("unknown bridge readiness %q", a.Readiness)
	}
	for _, ref := range []EvidenceRef{a.ArtifactEvidenceRef, a.SourceProvenanceRef, a.ForwardHopEvidenceRef} {
		if !validEvidenceRef(ref) {
			return fmt.Errorf("invalid evidence reference %q", ref)
		}
	}
	switch a.EvidenceStatus {
	case SourceUnverified, UnknownOrNewerSource:
		// Shape and planned target are observations/planning only. They cannot
		// upgrade an unverified source.
		return nil
	case ExactCandidateVerified:
		if a.Shape != LegacySchemaC {
			return errors.New("verified exact v1.14.4 candidate must match LegacySchemaC")
		}
		if a.ArtifactEvidenceRef == "" || a.SourceProvenanceRef == "" {
			return errors.New("exact candidate requires artifact and source provenance evidence")
		}
		if a.ForwardHopEvidenceRef != "" {
			return errors.New("direct exact candidate cannot claim forward-hop evidence")
		}
	case ForwardHopVerified:
		if a.Shape != LegacySchemaC {
			return errors.New("verified forward-hop output candidate must match LegacySchemaC")
		}
		if a.ArtifactEvidenceRef == "" || a.SourceProvenanceRef == "" || a.ForwardHopEvidenceRef == "" {
			return errors.New("forward-hop candidate requires artifact, source provenance and hop evidence")
		}
	default:
		return fmt.Errorf("unknown source evidence status %q", a.EvidenceStatus)
	}
	return nil
}

func (a UpgradeAdmission) trustedSource() bool {
	return a.EvidenceStatus == ExactCandidateVerified || a.EvidenceStatus == ForwardHopVerified
}

func validRecoveryReason(reason RecoveryReason) bool {
	switch reason {
	case OperationIncomplete, RollbackRequired, DamagedState, MigrationIncomplete,
		BackupInvalid, SourceChanged, OfflineActionRequired, AuthorityUnqualified,
		ManualRecoveryRequired:
		return true
	default:
		return false
	}
}

// ClassificationEvidence deliberately keeps administrator/session state
// orthogonal to product mode classification.
type ClassificationEvidence struct {
	HasV1ProductState bool
	Admission         *UpgradeAdmission
	RecoveryReason    RecoveryReason
	BootstrapComplete bool
	AdminCredential   AdminCredentialState
	Session           SessionState
}

func validAdminCredentialState(state AdminCredentialState) bool {
	switch state {
	case AdminCredentialUninitialized, AdminCredentialInitialized, AdminCredentialUnverifiable:
		return true
	default:
		return false
	}
}

func validSessionState(state SessionState) bool {
	switch state {
	case SessionMissing, SessionAuthenticated, SessionExpired:
		return true
	default:
		return false
	}
}

type FreshInitializationPreconditions struct {
	Mode               Mode
	AdminCredential    AdminCredentialState
	HostClaimQualified bool
}

// CanInitializeAdministrator is a pure contract predicate. Fresh
// classification alone is not initialization authority.
func CanInitializeAdministrator(p FreshInitializationPreconditions) bool {
	return p.Mode == Fresh &&
		p.AdminCredential == AdminCredentialUninitialized &&
		p.HostClaimQualified
}

// Classify never infers a release from SourceShape. Only separately verified
// source/hop evidence plus ready migration state can classify as Upgrade.
func Classify(e ClassificationEvidence) (Mode, error) {
	if !validAdminCredentialState(e.AdminCredential) || !validSessionState(e.Session) {
		return "", errors.New("invalid administrator credential or session state")
	}
	if e.RecoveryReason != "" {
		if !validRecoveryReason(e.RecoveryReason) {
			return "", fmt.Errorf("unknown recovery reason %q", e.RecoveryReason)
		}
		return Recovery, nil
	}
	if e.BootstrapComplete {
		return Completed, nil
	}
	if !e.HasV1ProductState {
		if e.Admission != nil {
			return "", errors.New("upgrade admission supplied without v1 product state")
		}
		return Fresh, nil
	}
	if e.Admission == nil {
		return UnsupportedUpgrade, nil
	}
	if err := e.Admission.Validate(); err != nil {
		return "", err
	}
	if !e.Admission.trustedSource() {
		return UnsupportedUpgrade, nil
	}
	if e.Admission.Readiness != BridgeReady {
		return Recovery, nil
	}
	return Upgrade, nil
}

// ValidateTransition prevents unresolved recovery from being relabeled as done.
func ValidateTransition(from, to Mode) error {
	if from == Recovery && to == Completed {
		return errors.New("recovery cannot transition directly to completed")
	}
	return nil
}

type MigrationStage string

const (
	ReadOnlyClassification   MigrationStage = "read_only_classification"
	CompleteVerifiedBackup   MigrationStage = "complete_verified_backup"
	SourceRevalidation       MigrationStage = "source_revalidation"
	WritableMigration        MigrationStage = "writable_migration"
)

var requiredUpgradeOrdering = []MigrationStage{
	ReadOnlyClassification,
	CompleteVerifiedBackup,
	SourceRevalidation,
	WritableMigration,
}

// ValidateUpgradeOrdering freezes the one-way first-write boundary.
func ValidateUpgradeOrdering(stages []MigrationStage) error {
	if len(stages) != len(requiredUpgradeOrdering) {
		return errors.New("upgrade ordering must contain exactly four stages")
	}
	for i := range requiredUpgradeOrdering {
		if stages[i] != requiredUpgradeOrdering[i] {
			return fmt.Errorf("upgrade stage %d must be %q", i, requiredUpgradeOrdering[i])
		}
	}
	return nil
}

// WritablePreconditions describes what a future Manager-owned production
// authority must prove. Phase4A-08 does not implement that authority.
type WritablePreconditions struct {
	Mode                       Mode
	Admission                  UpgradeAdmission
	BackupVerified             bool
	SourceRevalidated          bool
	RequiredCheckpoints        RequiredMigrationCheckpoints
	ManagerAuthorityQualified  bool
	TrustedForwardHopQualified bool
	Ordering                   []MigrationStage
}

// CanIssueWritableGrant is a pure contract predicate, not a grant issuer.
func CanIssueWritableGrant(p WritablePreconditions) bool {
	if p.Mode != Upgrade ||
		!p.BackupVerified ||
		!p.SourceRevalidated ||
		!p.RequiredCheckpoints.Complete() ||
		!p.ManagerAuthorityQualified {
		return false
	}
	if p.Admission.Validate() != nil || !p.Admission.trustedSource() || p.Admission.Readiness != BridgeReady {
		return false
	}
	if p.Admission.EvidenceStatus == ForwardHopVerified && !p.TrustedForwardHopQualified {
		return false
	}
	return ValidateUpgradeOrdering(p.Ordering) == nil
}

type CPACoreContract struct {
	ManagementGeneration      string
	CoreV0FallbackAllowed     bool
	ExternalManagementOwner   string
	ExternalLifecycleOwner    string
}

func (c CPACoreContract) Validate() error {
	if c.ManagementGeneration != CPAManagementGenerationV8 {
		return errors.New("CPA core management generation must be v8")
	}
	if c.CoreV0FallbackAllowed {
		return errors.New("CPA core v0 fallback is forbidden")
	}
	if c.ExternalManagementOwner != "cpamp_manager" || c.ExternalLifecycleOwner != "operator" {
		return errors.New("external management and lifecycle ownership must remain separated")
	}
	return nil
}

// Plan is a product-domain decision, not a Public API DTO or executable
// operation. It carries no trusted receipt and cannot authorize a write.
type Plan struct {
	Mode                            Mode
	CPAMPAction                     CPAMPAction
	CPAStrategy                     CPAStrategy
	SourceAdmission                 *UpgradeAdmission
	SourceCPA                       SourceCPA
	UsageAction                     UsageAction
	BackupRequired                  bool
	RuntimeMode                     RuntimeMode
	InitializeAdminCredential       bool
	PreserveExistingAdminCredential bool
	AuthenticationRequired          bool
	Mutations                       []string
	Preserved                       []string
	Unsupported                     []string
	DiscoveredResourceRefs          []ResourceRef
	AdoptionDecisions               []AdoptionResourceDecision
	ControlBasePath                 string
	EstimatedSteps                  []string
	MigrationOrdering               []MigrationStage
}

func RuntimeForStrategy(strategy CPAStrategy) (RuntimeMode, error) {
	switch strategy {
	case NewManaged, AdoptExisting, MigrateLegacy:
		return Embedded, nil
	case ConnectExternal:
		return External, nil
	default:
		return "", fmt.Errorf("unknown CPA strategy %q", strategy)
	}
}

func (p Plan) Validate() error {
	var expectedAction CPAMPAction
	var expectedUsage UsageAction
	var expectedBackup bool

	switch p.Mode {
	case Fresh:
		expectedAction, expectedUsage = Initialize, NoUsageAction
		if p.CPAStrategy != NewManaged && p.CPAStrategy != AdoptExisting && p.CPAStrategy != ConnectExternal {
			return errors.New("fresh requires new_managed, adopt_existing or connect_external")
		}
		if p.SourceAdmission != nil {
			return errors.New("fresh cannot have upgrade admission")
		}
		if !p.InitializeAdminCredential || p.PreserveExistingAdminCredential || p.AuthenticationRequired {
			return errors.New("fresh must initialize, not preserve, the administrator credential")
		}
		if len(p.MigrationOrdering) != 0 {
			return errors.New("fresh cannot carry legacy migration ordering")
		}
	case Upgrade:
		expectedAction, expectedUsage, expectedBackup = MigrateV1, PreserveUsage, true
		if p.CPAStrategy != MigrateLegacy && p.CPAStrategy != ConnectExternal {
			return errors.New("upgrade requires migrate_legacy or connect_external")
		}
		if p.SourceAdmission == nil || p.SourceAdmission.Validate() != nil || !p.SourceAdmission.trustedSource() {
			return errors.New("upgrade requires separately verified source admission")
		}
		if p.SourceAdmission.Readiness != BridgeReady {
			return errors.New("upgrade plan requires ready bridge state")
		}
		if p.InitializeAdminCredential || !p.PreserveExistingAdminCredential || !p.AuthenticationRequired {
			return errors.New("upgrade must preserve and authenticate the existing administrator credential")
		}
		if err := ValidateUpgradeOrdering(p.MigrationOrdering); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mode %q cannot produce a user path plan", p.Mode)
	}

	if p.CPAMPAction != expectedAction || p.UsageAction != expectedUsage || p.BackupRequired != expectedBackup {
		return errors.New("CPAMP action, usage action or backup requirement conflicts with mode")
	}
	runtime, err := RuntimeForStrategy(p.CPAStrategy)
	if err != nil || p.RuntimeMode != runtime {
		return errors.New("runtime mode must derive from CPA strategy")
	}
	var expectedSource SourceCPA
	switch p.CPAStrategy {
	case AdoptExisting:
		expectedSource = ExistingCPA
	case MigrateLegacy:
		expectedSource = LegacyCPA
	case ConnectExternal:
		expectedSource = ExternalCPA
	}
	if p.SourceCPA != expectedSource {
		return errors.New("source CPA conflicts with strategy")
	}
	if strings.TrimSpace(p.ControlBasePath) == "" || len(p.EstimatedSteps) == 0 || len(p.Mutations) == 0 {
		return errors.New("plan requires controlBasePath, mutations and estimated steps")
	}
	if err := validateLists(p.Mutations, p.Preserved, p.Unsupported, p.EstimatedSteps); err != nil {
		return err
	}

	if p.Mode == Upgrade {
		for _, required := range []string{"authoritative_usage_history", "archive_retained_evidence_when_required"} {
			if !has(p.Preserved, required) {
				return fmt.Errorf("upgrade must preserve %s", required)
			}
		}
		if !has(p.Unsupported, "full_history_export_transform_reimport") {
			return errors.New("upgrade must forbid replacing authoritative history by export-transform-reimport")
		}
	}

	if p.CPAStrategy == AdoptExisting {
		for _, value := range []string{
			"historical_usage_import", "request_history_import", "cpa_log_import",
			"historical_analytics_import", "plugin_runtime_implicit_import",
			"source_cpa_stop", "source_cpa_delete", "source_cpa_overwrite",
		} {
			if !has(p.Unsupported, value) {
				return fmt.Errorf("adoption must disallow %s", value)
			}
		}
		if !has(p.Preserved, "source_cpa_recoverable_until_success") ||
			!has(p.Preserved, "source_cpa_available_until_commit") {
			return errors.New("adoption must keep source CPA available and recoverable")
		}
		if err := validateAdoptionDecisions(p.DiscoveredResourceRefs, p.AdoptionDecisions, p.Mutations, p.Unsupported); err != nil {
			return err
		}
	} else {
		if len(p.DiscoveredResourceRefs) != 0 || len(p.AdoptionDecisions) != 0 {
			return errors.New("adoption resources require adopt_existing strategy")
		}
		for _, group := range [][]string{p.Mutations, p.Unsupported} {
			for _, effect := range group {
				if strings.HasPrefix(effect, "adoption:") {
					return errors.New("adoption effect requires adopt_existing strategy")
				}
			}
		}
	}

	if p.CPAStrategy == ConnectExternal {
		for _, value := range []string{"source_cpa_stop", "source_cpa_delete", "source_cpa_takeover"} {
			if !has(p.Unsupported, value) {
				return fmt.Errorf("external connection must disallow %s", value)
			}
		}
		if !has(p.Preserved, "source_cpa_user_owned") {
			return errors.New("external CPA must remain user-owned")
		}
	}
	if p.Mode == Fresh && p.CPAStrategy != NewManaged && !has(p.Unsupported, "historical_usage_import") {
		return errors.New("fresh source CPA cannot import historical usage")
	}
	return nil
}

func validateLists(groups ...[]string) error {
	for _, group := range groups {
		if group == nil {
			return errors.New("plan lists must be explicit")
		}
		seen := map[string]bool{}
		for _, value := range group {
			if value == "" || seen[value] {
				return errors.New("plan lists contain empty or duplicate value")
			}
			seen[value] = true
		}
	}
	return nil
}

func has(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
