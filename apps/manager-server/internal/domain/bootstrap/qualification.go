package bootstrap

import "errors"

// CPAQualification freezes the evidence gates a concrete Bootstrap plan must
// satisfy before it may name a CPA strategy. It does not perform qualification.
type CPAQualification struct {
	ManagementGeneration         string
	ArtifactQualified            bool
	RequiredProfileQualified     bool
	StagingV8TransitionQualified bool
}

func (q CPAQualification) ValidateForStrategy(strategy CPAStrategy) error {
	if q.ManagementGeneration != CPAManagementGenerationV8 {
		return errors.New("CPA strategy requires v8 management generation")
	}
	switch strategy {
	case NewManaged:
		if !q.ArtifactQualified {
			return errors.New("new_managed requires a qualified exact v8 artifact")
		}
	case AdoptExisting:
		if !q.RequiredProfileQualified && !q.StagingV8TransitionQualified {
			return errors.New("adopt_existing requires a qualified v8 profile or staging v8 transition")
		}
	case ConnectExternal:
		if !q.RequiredProfileQualified {
			return errors.New("connect_external requires the required v8 management profile")
		}
	case MigrateLegacy:
		if !q.StagingV8TransitionQualified {
			return errors.New("migrate_legacy requires a qualified staging v8 transition")
		}
	default:
		return errors.New("unknown CPA strategy")
	}
	return nil
}
