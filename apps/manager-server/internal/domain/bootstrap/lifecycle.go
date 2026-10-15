package bootstrap

import (
	"errors"
	"fmt"
)

type FreshClaim struct {
	InstallationID           string
	GenerationID             string
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
	Domain      JournalDomain
	OperationID OperationID
	Result      JournalResult
	CommitRef   string
}

func JournalReplayAction(record JournalRecord, requested OperationID) (JournalAction, error) {
	switch record.Domain {
	case ProductJournal, CPAJournal, RoutingJournal:
	default:
		return "", fmt.Errorf("unknown journal domain %q", record.Domain)
	}
	if !validOperationID(record.OperationID) || !validOperationID(requested) {
		return "", errors.New("invalid journal operation id")
	}
	if requested != record.OperationID {
		return JournalRejectNewOperation, nil
	}
	switch record.Result {
	case JournalCommitted:
		if record.CommitRef == "" {
			return "", errors.New("committed journal requires commit reference")
		}
		return JournalReturnCommitted, nil
	case JournalUnknown, JournalInProgress:
		return JournalInspectFirst, nil
	case JournalRetryableFailure:
		return JournalResumeSameOperation, nil
	default:
		return "", fmt.Errorf("unknown journal result %q", record.Result)
	}
}
