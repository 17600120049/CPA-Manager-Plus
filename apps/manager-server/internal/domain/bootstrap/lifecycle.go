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
	ExpectedRevision         uint64
	CurrentRevision          uint64
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
		claim.ExpectedRevision > 0 && claim.ExpectedRevision == claim.CurrentRevision &&
		claim.ExpiresAtUnixMilli > claim.NowUnixMilli && !claim.Revoked && !claim.Consumed &&
		claim.PersistedAcrossRestart && claim.CSRFVerified && claim.HostAuthenticityVerified
}

type FreshInitializationState struct {
	Claim              FreshClaim
	AdminCredential    AdminCredentialState
	ProductInitialized bool
}

// ValidateFreshInitializationCommit freezes the single-winner CAS boundary:
// claim consumption, administrator initialization and initialized state move
// together while the durable claim revision advances exactly once.
func ValidateFreshInitializationCommit(before, after FreshInitializationState) error {
	if !CanConsumeFreshClaim(Fresh, before.AdminCredential, before.Claim) || before.ProductInitialized {
		return errors.New("fresh initialization pre-state is not eligible")
	}
	if before.Claim.InstallationID != after.Claim.InstallationID ||
		before.Claim.GenerationID != after.Claim.GenerationID ||
		before.Claim.CurrentInstallationID != after.Claim.CurrentInstallationID ||
		before.Claim.CurrentGenerationID != after.Claim.CurrentGenerationID ||
		before.Claim.ExpectedRevision != after.Claim.ExpectedRevision ||
		before.Claim.ExpiresAtUnixMilli != after.Claim.ExpiresAtUnixMilli {
		return errors.New("claim identity or expected revision changed during initialization")
	}
	if after.Claim.CurrentRevision != before.Claim.CurrentRevision+1 {
		return errors.New("claim consumption must advance durable revision exactly once")
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
	ObservedProductRevision uint64
	ObservedGenerationID    string
	ObservedRoutingRevision uint64
	AcceptedReplayQualified bool
}

type JournalObservation struct {
	CurrentProductRevision uint64
	CurrentGenerationID    string
	CurrentRoutingRevision uint64
}

func validJournalDomain(domain JournalDomain) bool {
	switch domain {
	case ProductJournal, CPAJournal, RoutingJournal:
		return true
	default:
		return false
	}
}

// journalFenceMatches keeps each failure domain tied to its own authority:
// Manager product/checkpoint revision, Supervisor RuntimeGeneration, or Manager
// routing revision. RuntimeGeneration is never reused as product/routing revision.
func journalFenceMatches(record JournalRecord, observed JournalObservation) (bool, error) {
	switch record.Domain {
	case ProductJournal:
		if record.ObservedProductRevision == 0 || observed.CurrentProductRevision == 0 {
			return false, errors.New("product journal requires product revision observation")
		}
		return record.ObservedProductRevision == observed.CurrentProductRevision, nil
	case CPAJournal:
		if record.ObservedGenerationID == "" || observed.CurrentGenerationID == "" {
			return false, errors.New("CPA journal requires RuntimeGeneration observation")
		}
		return record.ObservedGenerationID == observed.CurrentGenerationID, nil
	case RoutingJournal:
		if record.ObservedRoutingRevision == 0 || observed.CurrentRoutingRevision == 0 {
			return false, errors.New("routing journal requires routing revision observation")
		}
		return record.ObservedRoutingRevision == observed.CurrentRoutingRevision, nil
	default:
		return false, fmt.Errorf("unknown journal domain %q", record.Domain)
	}
}

// JournalReplayAction never invents a generic rerun. A retryable failure may
// resume only for the same operation, with an accepted replay path, while that
// journal domain's own authority fence is unchanged.
func JournalReplayAction(record JournalRecord, requested OperationID, observed JournalObservation) (JournalAction, error) {
	if !validJournalDomain(record.Domain) {
		return "", fmt.Errorf("unknown journal domain %q", record.Domain)
	}
	if !validOperationID(record.OperationID) || !validOperationID(requested) {
		return "", errors.New("invalid journal operation id")
	}
	if requested != record.OperationID {
		return JournalRejectNewOperation, nil
	}
	fenceMatches, err := journalFenceMatches(record, observed)
	if err != nil {
		return "", err
	}
	if !fenceMatches {
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
