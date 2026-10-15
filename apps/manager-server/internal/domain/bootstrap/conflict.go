package bootstrap

import "fmt"

type OperationClass string

const (
	OperationInstall     OperationClass = "install"
	OperationRestore     OperationClass = "restore"
	OperationMigration   OperationClass = "migration"
	OperationMaintenance OperationClass = "maintenance"
	OperationCPAUpdate   OperationClass = "cpa_update"
	OperationCutover     OperationClass = "cutover"
	OperationPrefixApply OperationClass = "prefix_apply"
)

func validOperationClass(value OperationClass) bool {
	switch value {
	case OperationInstall, OperationRestore, OperationMigration, OperationMaintenance,
		OperationCPAUpdate, OperationCutover, OperationPrefixApply:
		return true
	default:
		return false
	}
}

type ConflictScope string

const (
	ConflictManagerStore       ConflictScope = "manager_store"
	ConflictRuntimeTarget      ConflictScope = "runtime_target"
	ConflictRoutingDeployment  ConflictScope = "routing_deployment"
	ConflictIndependentResource ConflictScope = "independent_resource"
)

func validConflictScope(scope ConflictScope) bool {
	switch scope {
	case ConflictManagerStore, ConflictRuntimeTarget, ConflictRoutingDeployment, ConflictIndependentResource:
		return true
	default:
		return false
	}
}

type OperationConflictDecision string

const (
	ConflictBlocked         OperationConflictDecision = "blocked"
	ConflictInspectExisting OperationConflictDecision = "inspect_existing"
	ConflictNoContractBlock OperationConflictDecision = "no_contract_block"
)

// EvaluateOperationConflict freezes only the known shared-resource boundary.
// It intentionally does not define Phase4-04 lock ordering or create a global
// mutex across unrelated mutation domains.
func EvaluateOperationConflict(active, requested OperationClass, scope ConflictScope, sameOperationID bool) (OperationConflictDecision, error) {
	if !validOperationClass(active) || !validOperationClass(requested) {
		return "", fmt.Errorf("unknown operation class %q/%q", active, requested)
	}
	if !validConflictScope(scope) {
		return "", fmt.Errorf("unknown conflict scope %q", scope)
	}
	if sameOperationID {
		if active != requested {
			return "", fmt.Errorf("same operation id cannot change class from %q to %q", active, requested)
		}
		return ConflictInspectExisting, nil
	}
	if scope == ConflictIndependentResource {
		return ConflictNoContractBlock, nil
	}
	return ConflictBlocked, nil
}

type CollectorCondition string

const (
	CollectorBackupQuiescence          CollectorCondition = "backup_quiescence"
	CollectorVerifiedQueueWindow       CollectorCondition = "verified_queue_window"
	CollectorCoverageUnprovenOrOutside CollectorCondition = "coverage_unproven_or_outside_window"
)

type CollectorOutcome string

const (
	CollectorPaused       CollectorOutcome = "paused"
	CollectorQueueWindow  CollectorOutcome = "queue_window"
	CollectorGapOrUnknown CollectorOutcome = "gap_or_unknown"
)

func CollectorOutcomeFor(condition CollectorCondition) (CollectorOutcome, error) {
	switch condition {
	case CollectorBackupQuiescence:
		return CollectorPaused, nil
	case CollectorVerifiedQueueWindow:
		return CollectorQueueWindow, nil
	case CollectorCoverageUnprovenOrOutside:
		return CollectorGapOrUnknown, nil
	default:
		return "", fmt.Errorf("unknown Collector condition %q", condition)
	}
}
