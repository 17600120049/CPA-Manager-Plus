package freshstrategy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

type adoptionSnapshot struct {
	SourceManagementKey string
	DestinationURL      string
	Destination         model.RuntimeDestinationRequest
}

func connectionIdentity(base string) string {
	digest := sha256.Sum256([]byte(base))
	return "external:sha256:" + hex.EncodeToString(digest[:])
}

func (s *Service) adopt(ctx context.Context, op bootstrap.FreshOperation, r Request) (Result, error) {
	result := Result{RuntimeMode: model.RuntimeModeEmbedded, GatewayPending: true, SourcePreserved: true}
	block := func(code string) (Result, error) { return result, &Blocker{Code: code} }
	runtime, ok := s.options.Runtime.(runtimeservice.DestinationClient)
	if !ok || s.options.Protector == nil || s.options.DestinationBaseURL == "" {
		return result, &Blocker{Code: "adoption_destination_staging_required", Unsupported: []string{"cpa_native_destination_staging_required", "credentials_require_reauthentication", "provider_runtime_state_not_migrated", "historical_usage_excluded"}}
	}
	// A source safe-read profile does not grant any resource write capability.
	if len(s.options.AdoptionResources) != 1 {
		return block("adoption_resource_qualification_required")
	}
	decision := s.options.AdoptionResources[0]
	disposition, err := bootstrap.DecideAdoptionResource(decision)
	if err != nil || decision.ResourceRef != "client-api-keys" || decision.Kind != bootstrap.APIKeys || decision.Scope != bootstrap.ClientAPIKeys || disposition != bootstrap.Migrate || decision.Disposition != disposition {
		return block("adoption_resource_qualification_required")
	}
	if !s.options.QualifiedArtifactID.IsValid() {
		return block("exact_v8_artifact_qualification_required")
	}
	source, err := cpa.NormalizeV8BaseURL(r.BaseURL)
	if err != nil {
		return block("management_connection_invalid")
	}
	target, err := cpa.NormalizeV8BaseURL(s.options.DestinationBaseURL)
	if err != nil || source == target {
		return block("adoption_destination_identity_conflict")
	}
	status, err := s.options.Runtime.Status(ctx)
	if err != nil || status.Validate() != nil || status.ProtocolVersion != "v1" || status.ActiveGatewayArtifact == nil {
		return block("managed_target_observation_unavailable")
	}
	if status.Identity != r.ExpectedRuntimeIdentity || status.Generation != r.ExpectedRuntimeGeneration || status.ActiveGatewayArtifact.ArtifactID != r.ExpectedArtifactID || r.ExpectedArtifactID != s.options.QualifiedArtifactID {
		return block("managed_target_identity_mismatch")
	}
	if !status.Capabilities.Supports(model.RuntimeCapabilityStageDestination) || !status.Capabilities.Supports(model.RuntimeCapabilityStart) {
		return block("adoption_destination_staging_required")
	}
	checkpoint := bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.AdoptExisting, TargetIdentity: string(status.Identity), TargetGeneration: uint64(status.Generation), TargetArtifactID: string(r.ExpectedArtifactID), Stage: "staged", SourceIdentity: connectionIdentity(source), SourceClass: r.AdoptSourceClass, DestinationConnectionIdentity: connectionIdentity(target), AdoptionDecisions: []bootstrap.AdoptionResourceDecision{decision}}
	var snapshot adoptionSnapshot
	if op.Strategy.Strategy == "" {
		if r.AdoptionCiphertext != "" {
			return block("fresh_strategy_owner_conflict")
		}
		keys, err := cpa.ReadV8AdoptionKeys(ctx, source, r.ManagementKey)
		if err != nil {
			return block("adoption_source_read_unavailable")
		}
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return block("management_secret_storage_unavailable")
		}
		snapshot = adoptionSnapshot{SourceManagementKey: r.ManagementKey, DestinationURL: target, Destination: model.RuntimeDestinationRequest{
			OperationID: string(op.Identity.OperationID) + ":adopt-destination", ExpectedRuntimeIdentity: status.Identity, ExpectedRuntimeGeneration: status.Generation, ExpectedActiveArtifactID: r.ExpectedArtifactID,
			SourceIdentity: checkpoint.SourceIdentity, SourceClass: string(r.AdoptSourceClass), APIKeys: keys, ManagementKey: hex.EncodeToString(secret)}}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return block("management_secret_storage_unavailable")
		}
		result.AdoptionCiphertext, err = s.options.Protector.ProtectString(string(data))
		if err != nil {
			return block("management_secret_storage_unavailable")
		}
		result.Checkpoint = checkpoint
		// This intent and encrypted resource snapshot must be CAS-persisted
		// before any destination write or lifecycle operation.
		return result, nil
	}
	checkpoint.Stage = op.Strategy.Stage
	if !reflect.DeepEqual(checkpoint, op.Strategy) {
		return block("fresh_strategy_owner_conflict")
	}
	if !security.IsValidProtectedEnvelope(r.AdoptionCiphertext) {
		return block("adoption_snapshot_unavailable")
	}
	plain, err := s.options.Protector.UnprotectString(r.AdoptionCiphertext)
	if err != nil || json.Unmarshal([]byte(plain), &snapshot) != nil || snapshot.Destination.Validate() != nil {
		return block("adoption_snapshot_unavailable")
	}
	destination := snapshot.Destination
	if !security.EqualHMAC(snapshot.SourceManagementKey, r.ManagementKey) || snapshot.DestinationURL != target || destination.OperationID != string(op.Identity.OperationID)+":adopt-destination" || destination.ExpectedRuntimeIdentity != status.Identity || destination.ExpectedRuntimeGeneration != status.Generation || destination.ExpectedActiveArtifactID != r.ExpectedArtifactID || destination.SourceIdentity != checkpoint.SourceIdentity || destination.SourceClass != string(r.AdoptSourceClass) {
		return block("fresh_strategy_owner_conflict")
	}
	result.Checkpoint = op.Strategy
	if op.Strategy.Stage != "committed" {
		staged, err := runtime.StageDestination(ctx, destination)
		if err != nil || !adoptionOperationSucceeded(staged, destination, model.RuntimeOperationStageDestination, destination.OperationID) {
			return block("adoption_destination_stage_unresolved")
		}
		started, err := s.options.Runtime.Start(ctx, model.RuntimeMutationRequest{OperationID: destination.OperationID + ":start", ExpectedRuntimeIdentity: status.Identity, ExpectedRuntimeGeneration: status.Generation, ExpectedActiveArtifactID: r.ExpectedArtifactID})
		if err != nil || !adoptionOperationSucceeded(started, destination, model.RuntimeOperationStart, destination.OperationID+":start") {
			return block("adoption_destination_start_unresolved")
		}
	}
	err = cpa.VerifyV8Adoption(ctx, target, destination.ManagementKey, destination.APIKeys)
	if err != nil {
		return block("adoption_destination_verification_required")
	}
	// Re-observe after readback: an in-flight replacement/generation cannot
	// commit evidence from the previous target. Gateway remains separately shut.
	after, err := s.options.Runtime.Status(ctx)
	if err != nil || after.Validate() != nil || after.State != model.RuntimeStateReady || after.Identity != status.Identity || after.Generation != status.Generation || after.ActiveGatewayArtifact == nil || after.ActiveGatewayArtifact.ArtifactID != r.ExpectedArtifactID {
		return block("managed_target_identity_mismatch")
	}
	ciphertext, err := s.options.Protector.ProtectString(destination.ManagementKey)
	if err != nil {
		return block("management_secret_storage_unavailable")
	}
	result.Connection = &ProtectedConnection{BaseURL: target, ManagementKeyCiphertext: ciphertext}
	result.Checkpoint.Stage = "committed"
	return result, nil
}

func adoptionOperationSucceeded(result model.RuntimeOperationResult, r model.RuntimeDestinationRequest, kind model.RuntimeOperationType, id string) bool {
	return result.Validate() == nil && result.OperationID == id && result.OperationType == kind && result.RuntimeIdentity == r.ExpectedRuntimeIdentity && result.RuntimeGeneration == r.ExpectedRuntimeGeneration && result.State == model.RuntimeOperationSucceeded
}
