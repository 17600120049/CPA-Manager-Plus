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

type OperationConflictDecision string

const (
	ConflictBlocked         OperationConflictDecision = "blocked"
	ConflictInspectExisting OperationConflictDecision = "inspect_existing"
)

// EvaluateOperationConflict freezes the conservative ownership rule for
// state-mutating Bootstrap operations. A same-ID replay may only inspect the
// exact same operation class; every different operation remains blocked.
func EvaluateOperationConflict(active, requested OperationClass, sameOperationID bool) (OperationConflictDecision, error) {
	if !validOperationClass(active) || !validOperationClass(requested) {
		return "", fmt.Errorf("unknown operation class %q/%q", active, requested)
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
