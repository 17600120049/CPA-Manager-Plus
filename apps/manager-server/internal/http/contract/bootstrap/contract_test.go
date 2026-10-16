package bootstrapcontract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	domain "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
)

func TestDiscoveryDTOIsMinimalAndStrict(t *testing.T) {
	mode := domain.Fresh
	dto, err := DiscoveryFromDomain(domain.PublicDiscovery{
		ContractVersion: domain.PublicDiscoveryContractVersion,
		Status:          domain.PublicFreshClaimRequired, Mode: &mode,
		AuthenticationRequired: true, AuthEntry: domain.AuthEntryFreshClaim,
		Reason: domain.ReasonFreshClaimRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"contract_version": true, "status": true, "mode": true,
		"authentication_required": true, "auth_entry": true, "reason": true,
	}
	for field := range fields {
		if !allowed[field] {
			t.Fatalf("discovery serialized unexpected field %q", field)
		}
	}
	for _, forbidden := range []string{
		"controlBasePath", "sourceCPAUrl", "CPA Management Key", "adminKey",
		"/private/manager.sqlite", "backupReceipt", "resourceRefs", "rawVerifierError",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("discovery serialized sensitive value %q: %s", forbidden, encoded)
		}
	}

	var decoded DiscoveryDTO
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	completed := dto
	completedMode := domain.Completed
	completed.Mode = &completedMode
	if _, err := json.Marshal(completed); err == nil {
		t.Fatal("completed discovery fingerprint was serializable")
	}
	for _, payload := range []string{
		`{"contract_version":"cpamp.phase4-01.discovery.v1","status":"fresh_claim_required","mode":"fresh","authentication_required":true,"auth_entry":"fresh_claim","reason":"fresh_claim_required","controlBasePath":"/secret"}`,
		`{"contract_version":"cpamp.phase4-01.discovery.v1","status":"fresh_claim_required","mode":"completed","authentication_required":true,"auth_entry":"fresh_claim","reason":"fresh_claim_required"}`,
		`{"contract_version":"cpamp.phase4-01.discovery.v1","status":"unsupported_upgrade","mode":"upgrade","authentication_required":true,"auth_entry":"admin_session","reason":"qualification_unavailable"}`,
		`{"contract_version":"cpamp.phase4-01.discovery.v1","status":"fresh_claim_required","status":"fresh_claim_required","mode":"fresh","authentication_required":true,"auth_entry":"fresh_claim","reason":"fresh_claim_required"}`,
	} {
		if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
			t.Fatalf("unsafe discovery payload accepted: %s", payload)
		}
	}
}

func TestBootstrapStatusDTOKeepsModeCredentialAndSessionOrthogonal(t *testing.T) {
	valid := []domain.StatusSnapshot{
		{Mode: domain.Fresh, AdminCredential: domain.AdminCredentialUninitialized, Session: domain.SessionMissing},
		{Mode: domain.Upgrade, AdminCredential: domain.AdminCredentialInitialized, Session: domain.SessionExpired},
		{Mode: domain.Recovery, AdminCredential: domain.AdminCredentialUnverifiable, Session: domain.SessionMissing},
		{Mode: domain.Completed, AdminCredential: domain.AdminCredentialInitialized, Session: domain.SessionMissing},
	}
	for _, snapshot := range valid {
		dto, err := StatusFromDomain(snapshot)
		if err != nil {
			t.Fatalf("snapshot rejected: %+v: %v", snapshot, err)
		}
		encoded, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		var decoded BootstrapStatusDTO
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("status round trip failed: %v", err)
		}
		if decoded.Mode != snapshot.Mode || decoded.AdminCredential != snapshot.AdminCredential || decoded.Session != snapshot.Session {
			t.Fatalf("status axes changed during round trip: %+v -> %+v", snapshot, decoded)
		}
	}
	if _, err := StatusFromDomain(domain.StatusSnapshot{Mode: domain.Fresh, AdminCredential: domain.AdminCredentialInitialized, Session: domain.SessionMissing}); err == nil {
		t.Fatal("Fresh/admin conflict accepted by status DTO")
	}
}

func TestBootstrapPlanDTOIsReadOnlyAndKeepsQualificationUnknown(t *testing.T) {
	dto := BootstrapPlanDTO{
		ContractVersion: PlanContractVersion,
		Mode:            domain.Upgrade, CPAMPAction: domain.MigrateV1, CPAStrategy: domain.MigrateLegacy,
		UsageAction: domain.PreserveUsage, BackupRequired: true, AuthenticationRequired: true,
		Mutations:      []string{"manager_checkpoint"},
		Preserved:      []string{"authoritative_usage_history"},
		Unsupported:    []string{"full_history_export_transform_reimport"},
		EstimatedSteps: []string{"inspect_source", "verify_backup"},
		Qualification:  QualificationUnknown,
	}
	if err := dto.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthenticatedPlanAccess(false, dto); err == nil {
		t.Fatal("unauthenticated BootstrapPlan access accepted")
	}
	if err := ValidateAuthenticatedPlanAccess(true, dto); err != nil {
		t.Fatalf("authenticated read-only BootstrapPlan rejected: %v", err)
	}
	for name, invalid := range map[string]BootstrapPlanDTO{
		"unknown CPAMP action": func() BootstrapPlanDTO {
			value := dto
			value.CPAMPAction = domain.CPAMPAction("future_action")
			return value
		}(),
		"unknown CPA strategy": func() BootstrapPlanDTO {
			value := dto
			value.CPAStrategy = domain.CPAStrategy("future_strategy")
			return value
		}(),
		"unknown usage action": func() BootstrapPlanDTO {
			value := dto
			value.UsageAction = domain.UsageAction("future_usage")
			return value
		}(),
		"unauthenticated plan": func() BootstrapPlanDTO {
			value := dto
			value.AuthenticationRequired = false
			return value
		}(),
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("unsafe %s accepted", name)
		}
		if _, err := json.Marshal(invalid); err == nil {
			t.Fatalf("unsafe %s serialized", name)
		}
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"migration_succeeded", "backup_complete", "source_authorized", "cutover_complete",
		"adminKey", "CPA Management Key", "operation_id",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("plan serialized forbidden placeholder or secret field %q: %s", forbidden, encoded)
		}
	}
	var decoded BootstrapPlanDTO
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"contract_version":"cpamp.phase4-01.plan.v1","mode":"upgrade","cpamp_action":"migrate_v1","cpa_strategy":"migrate_legacy","usage_action":"preserve","backup_required":true,"authentication_required":true,"mutations":["x"],"preserved":["y"],"unsupported":["z"],"estimated_steps":["inspect"],"qualification":"pending","migration_succeeded":true}`,
		`{"contract_version":"cpamp.phase4-01.plan.v1","mode":"upgrade","cpamp_action":"migrate_v1","cpa_strategy":"migrate_legacy","usage_action":"preserve","backup_required":true,"authentication_required":true,"mutations":["x"],"preserved":["x"],"unsupported":["z"],"estimated_steps":["inspect"],"qualification":"pending"}`,
	} {
		if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
			t.Fatalf("unsafe plan payload accepted: %s", payload)
		}
	}
}

func TestDTORejectsUnknownEnums(t *testing.T) {
	var discovery DiscoveryDTO
	if err := json.Unmarshal([]byte(`{"contract_version":"cpamp.phase4-01.discovery.v1","status":"future","authentication_required":false,"auth_entry":"unavailable","reason":"future"}`), &discovery); err == nil {
		t.Fatal("unknown public status/reason accepted")
	}
	var status BootstrapStatusDTO
	if err := json.Unmarshal([]byte(`{"contract_version":"cpamp.phase4-01.status.v1","mode":"fresh","admin_credential_state":"future","session_state":"missing"}`), &status); err == nil {
		t.Fatal("unknown administrator state accepted")
	}
}

func TestContractDTOsHaveNoUntypedObjectOrStringerEscapeHatch(t *testing.T) {
	for _, value := range []any{DiscoveryDTO{}, BootstrapStatusDTO{}, BootstrapPlanDTO{}} {
		typeOfValue := reflect.TypeOf(value)
		for index := 0; index < typeOfValue.NumField(); index++ {
			field := typeOfValue.Field(index)
			if field.Type.Kind() == reflect.Interface || field.Type.Kind() == reflect.Map {
				t.Fatalf("DTO field %s.%s has an untyped serialization escape hatch", typeOfValue.Name(), field.Name)
			}
		}
	}
}
