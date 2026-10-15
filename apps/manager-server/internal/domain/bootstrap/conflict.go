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
	ConflictManagerStore      ConflictScope = "manager_store"
	ConflictRuntimeTarget     ConflictScope = "runtime_target"
	ConflictRoutingDeployment ConflictScope = "routing_deployment"
)

type OperationConflictDecision string

const (
	ConflictBlocked         OperationConflictDecision = "blocked"
	ConflictInspectExisting OperationConflictDecision = "inspect_existing"
)

func operationPair(a, b OperationClass) string {
	if a < b {
		return string(a) + "|" + string(b)
	}
	return string(b) + "|" + string(a)
}

// ClassifyOperationConflictScope is the server-owned classifier for only the
// conflict pairs that Phase4A-08 is authorized to freeze. Unknown pairs are
// deliberately left undecided for Phase4-04 rather than globally blocked or
// prematurely declared independent.
func ClassifyOperationConflictScope(active, requested OperationClass) (ConflictScope, error) {
	if !validOperationClass(active) || !validOperationClass(requested) {
		return "", fmt.Errorf("unknown operation class %q/%q", active, requested)
	}
	if active == requested {
		switch active {
		case OperationMigration, OperationMaintenance:
			return ConflictManagerStore, nil
		case OperationCPAUpdate:
			return ConflictRuntimeTarget, nil
		case OperationInstall, OperationRestore, OperationCutover, OperationPrefixApply:
			return ConflictRoutingDeployment, nil
		}
	}
	switch operationPair(active, requested) {
	case "install|restore":
		return ConflictRoutingDeployment, nil
	case "maintenance|migration":
		return ConflictManagerStore, nil
	case "cpa_update|cutover":
		return ConflictRuntimeTarget, nil
	case "cutover|prefix_apply":
		return ConflictRoutingDeployment, nil
	default:
		return "", fmt.Errorf("operation conflict scope is deferred for %q/%q", active, requested)
	}
}

func EvaluateOperationConflict(active, requested OperationClass, sameOperationID bool) (OperationConflictDecision, error) {
	_, err := ClassifyOperationConflictScope(active, requested)
	if err != nil {
		return "", err
	}
	if sameOperationID {
		if active != requested {
			return "", fmt.Errorf("same operation id cannot change class from %q to %q", active, requested)
		}
		return ConflictInspectExisting, nil
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
