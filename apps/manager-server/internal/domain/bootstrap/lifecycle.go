package bootstrap

import (
	"errors"
	"fmt"
)

type FreshClaim struct {
	InstallationID           string
	GenerationID             string
	CurrentInstallationID    string
	CurrentGenerationID      string
	ExpiresAtUnixMilli       int64
	NowUnixMilli             int64
	Revoked                  bool
	Consumed                 bool
	PersistedAcrossRestart   bool
	CSRFVerified             bool
	HostAuthenticityVerified bool
}

func CanConsumeFreshClaim(mode Mode, admin AdminCredentialState, claim FreshClaim) bool {
	return mode == Fresh && admin == AdminCredentialUninitialized &&
		claim.InstallationID != "" && claim.GenerationID != "" &&
		claim.InstallationID == claim.CurrentInstallationID &&
		claim.GenerationID == claim.CurrentGenerationID &&
		claim.ExpiresAtUnixMilli > claim.NowUnixMilli && !claim.Revoked && !claim.Consumed &&
		claim.PersistedAcrossRestart && claim.CSRFVerified && claim.HostAuthenticityVerified
}

type FreshInitializationState struct {
	Claim              FreshClaim
	AdminCredential    AdminCredentialState
	ProductInitialized bool
}

// ValidateFreshInitializationCommit freezes the atomic consume-once boundary:
// claim consumption, administrator initialization and initialized state move
// together. Persistence/locking belongs to Phase4 implementation.
func ValidateFreshInitializationCommit(before, after FreshInitializationState) error {
	if !CanConsumeFreshClaim(Fresh, before.AdminCredential, before.Claim) || before.ProductInitialized {
		return errors.New("fresh initialization pre-state is not eligible")
	}
	if before.Claim.InstallationID != after.Claim.InstallationID ||
		before.Claim.GenerationID != after.Claim.GenerationID ||
		before.Claim.CurrentInstallationID != after.Claim.CurrentInstallationID ||
		before.Claim.CurrentGenerationID != after.Claim.CurrentGenerationID ||
		before.Claim.ExpiresAtUnixMilli != after.Claim.ExpiresAtUnixMilli {
		return errors.New("claim identity changed during initialization")
	}
	if !after.Claim.Consumed || after.AdminCredential != AdminCredentialInitialized || !after.ProductInitialized {
		return errors.New("claim consumption, administrator initialization and product initialization must commit together")
	}
	return nil
}

type GatewayActivationPreconditions struct {
	ProductInitialized               bool
	ManagementProfileQualified       bool
	ClientAuthenticationQualified    bool
	RequiredEffectiveConfigQualified bool
}

func CanActivatePublicGateway(p GatewayActivationPreconditions) bool {
	return p.ProductInitialized && p.ManagementProfileQualified &&
		p.ClientAuthenticationQualified && p.RequiredEffectiveConfigQualified
}

type JournalDomain string

const (
	ProductJournal JournalDomain = "product"
	CPAJournal     JournalDomain = "cpa"
	RoutingJournal JournalDomain = "routing"
)

type JournalResult string

const (
	JournalInProgress       JournalResult = "in_progress"
	JournalCommitted        JournalResult = "committed"
	JournalUnknown          JournalResult = "unknown"
	JournalRetryableFailure JournalResult = "retryable_failure"
)

type JournalAction string

const (
	JournalInspectFirst        JournalAction = "inspect_first"
	JournalReturnCommitted     JournalAction = "return_committed"
	JournalResumeSameOperation JournalAction = "resume_same_operation"
	JournalRejectNewOperation  JournalAction = "reject_new_operation"
)

type JournalRecord struct {
	Domain                  JournalDomain
	OperationID             OperationID
	Result                  JournalResult
	CommitRef               string
	ObservedGenerationID    string
	AcceptedReplayQualified bool
}

func validJournalDomain(domain JournalDomain) bool {
	switch domain {
	case ProductJournal, CPAJournal, RoutingJournal:
		return true
	default:
		return false
	}
}

// JournalReplayAction never invents a generic rerun. A retryable failure may
// resume only when the same operation has an explicitly accepted replay path
// and the currently observed Supervisor generation is unchanged.
func JournalReplayAction(record JournalRecord, requested OperationID, currentGenerationID string) (JournalAction, error) {
	if !validJournalDomain(record.Domain) {
		return "", fmt.Errorf("unknown journal domain %q", record.Domain)
	}
	if !validOperationID(record.OperationID) || !validOperationID(requested) {
		return "", errors.New("invalid journal operation id")
	}
	if record.ObservedGenerationID == "" || currentGenerationID == "" {
		return "", errors.New("journal replay requires observed and current generation identity")
	}
	if requested != record.OperationID {
		return JournalRejectNewOperation, nil
	}
	if record.ObservedGenerationID != currentGenerationID {
		return JournalInspectFirst, nil
	}
	if record.Result == JournalCommitted {
		if record.CommitRef == "" {
			return "", errors.New("committed journal requires commit reference")
		}
		return JournalReturnCommitted, nil
	}
	switch record.Result {
	case JournalUnknown, JournalInProgress:
		return JournalInspectFirst, nil
	case JournalRetryableFailure:
		if !record.AcceptedReplayQualified {
			return JournalInspectFirst, nil
		}
		return JournalResumeSameOperation, nil
	default:
		return "", fmt.Errorf("unknown journal result %q", record.Result)
	}
}
